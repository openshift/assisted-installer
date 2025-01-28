package assisted_installer_controller

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thoas/go-funk"

	"github.com/hashicorp/go-version"
	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
)

const (
	// We retry 10 times in 30sec interval meaning that we tolerate the operator to be in failed
	// state for 5minutes.
	failedOperatorRetry   = 10
	generalWaitTimeoutInt = 30
	// zero means -> bring all logs
	controllerLogsSecondsAgo  = 0
	consoleOperatorName       = "console"
	ingressConfigMapName      = "default-ingress-cert"
	ingressConfigMapNamespace = "openshift-config-managed"
	dnsServiceName            = "dns-default"
	dnsServiceNamespace       = "openshift-dns"
	dnsOperatorNamespace      = "openshift-dns-operator"
	maxDeletionAttempts       = 5
	maxDNSServiceIPAttempts   = 45
	KeepWaiting               = false
	ExitWaiting               = true
	customManifestsFile       = "custom_manifests.json"

	consoleCapabilityName           = configv1.ClusterVersionCapability("Console")
	clusterOperatorReportKey string = "CLUSTER_OPERATORS_REPORT"
	workerMCPName                   = "worker"
	roleLabel                       = "node-role.kubernetes.io"
)

var (
	GeneralWaitInterval      = generalWaitTimeoutInt * time.Second
	GeneralProgressUpdateInt = 60 * time.Second
	LogsUploadPeriod         = 5 * time.Minute
	SummaryLogsPeriod        = 30 * time.Second
	WaitTimeout              = 70 * time.Minute
	DNSAddressRetryInterval  = 20 * time.Second
	DeletionRetryInterval    = 10 * time.Second
	FetchRetryInterval       = 10 * time.Second
	CVOMaxTimeout            = 10 * time.Hour
	dnsValidationTimeout     = 10 * time.Second
)

// assisted installer controller is added to control installation process after  bootstrap pivot
// assisted installer will deploy it on installation process
// as a first step it will wait till nodes are added to cluster and update their status to Done

type ControllerConfig struct {
	ClusterID               string `envconfig:"CLUSTER_ID" required:"true"`
	URL                     string `envconfig:"INVENTORY_URL" required:"true"`
	PullSecretToken         string `envconfig:"PULL_SECRET_TOKEN" required:"true" secret:"true"`
	SkipCertVerification    bool   `envconfig:"SKIP_CERT_VERIFICATION" required:"false" default:"false"`
	CACertPath              string `envconfig:"CA_CERT_PATH" required:"false" default:""`
	Namespace               string `envconfig:"NAMESPACE" required:"false" default:"assisted-installer"`
	OpenshiftVersion        string `envconfig:"OPENSHIFT_VERSION" required:"true"`
	HighAvailabilityMode    string `envconfig:"HIGH_AVAILABILITY_MODE" required:"false" default:"Full"`
	ControlPlainCount       int
	WaitForClusterVersion   bool   `envconfig:"CHECK_CLUSTER_VERSION" required:"false" default:"false"`
	MustGatherImage         string `envconfig:"MUST_GATHER_IMAGE" required:"false" default:""`
	DryRunEnabled           bool   `envconfig:"DRY_ENABLE" required:"false" default:"false"`
	DryFakeRebootMarkerPath string `envconfig:"DRY_FAKE_REBOOT_MARKER_PATH" required:"false" default:""`
	DryRunClusterHostsPath  string `envconfig:"DRY_CLUSTER_HOSTS_PATH"`
	NotifyNumReboots        bool   `envconfig:"NOTIFY_NUM_REBOOTS" default:"false"`
	// DryRunClusterHostsPath gets read parsed into ParsedClusterHosts by DryParseClusterHosts
	ParsedClusterHosts config.DryClusterHosts
}

type ControllerStatus struct {
	errCounter uint32
	components map[string]bool
	lock       sync.Mutex
}

//go:generate mockgen -source=assisted_installer_controller.go -package=assisted_installer_controller -destination=mock_controller.go
type Controller interface {
	WaitAndUpdateNodesStatus(ctx context.Context, wg *sync.WaitGroup, removeUninitializedTaint bool)
	HackDNSAddressConflict(wg *sync.WaitGroup)
	PostInstallConfigs(ctx context.Context, wg *sync.WaitGroup)
	UpdateNodeLabels(ctx context.Context, wg *sync.WaitGroup)
	UpdateBMHs(ctx context.Context, wg *sync.WaitGroup)
	UploadLogs(ctx context.Context, wg *sync.WaitGroup, invoker string)
	SetReadyState(waitTimeout time.Duration) *models.Cluster
	GetStatus() *ControllerStatus
}

type controller struct {
	ControllerConfig
	status          *ControllerStatus
	log             *logrus.Logger
	ops             ops.Ops
	ic              inventory_client.InventoryClient
	kc              k8s_client.K8SClient
	rebootsNotifier RebootsNotifier
}

// manifest store the operator manifest used by assisted-installer to create CRs of the OLM:
type manifest struct {
	// name of the operator the CR manifest we want create
	Name string
	// content of the manifest of the opreator
	Content string
}

func newController(log *logrus.Logger, cfg ControllerConfig, ops ops.Ops, ic inventory_client.InventoryClient, kc k8s_client.K8SClient, rebootsNotifier RebootsNotifier) *controller {
	return &controller{
		log:              log,
		ControllerConfig: cfg,
		ops:              ops,
		ic:               ic,
		kc:               kc,
		status:           NewControllerStatus(),
		rebootsNotifier:  rebootsNotifier,
	}
}

func NewController(log *logrus.Logger, cfg ControllerConfig, ops ops.Ops, ic inventory_client.InventoryClient, kc k8s_client.K8SClient, rebootsNotifier RebootsNotifier) Controller {
	return newController(log, cfg, ops, ic, kc, rebootsNotifier)
}

func NewControllerStatus() *ControllerStatus {
	return &ControllerStatus{
		components: make(map[string]bool),
	}
}

func (status *ControllerStatus) Error() {
	atomic.AddUint32(&status.errCounter, 1)
}

func (status *ControllerStatus) HasError() bool {
	return atomic.LoadUint32(&status.errCounter) > 0
}

func (status *ControllerStatus) OperatorError(component string) {
	status.lock.Lock()
	defer status.lock.Unlock()
	status.components[component] = true
}

func (status *ControllerStatus) HasOperatorError() bool {
	status.lock.Lock()
	defer status.lock.Unlock()
	return len(status.components) > 0
}

func (status *ControllerStatus) GetOperatorsInError() []string {
	result := make([]string, 0)
	status.lock.Lock()
	defer status.lock.Unlock()
	for op := range status.components {
		result = append(result, op)
	}
	return result
}

func logHostsStatus(log logrus.FieldLogger, hosts map[string]inventory_client.HostData) {
	hostsStatus := make(map[string][]string)
	for hostname, hostData := range hosts {
		hostsStatus[hostname] = []string{
			*hostData.Host.Status,
			string(hostData.Host.Progress.CurrentStage),
			hostData.Host.Progress.ProgressInfo}
	}
	log.Infof("Hosts status: %v", hostsStatus)
}

// WaitAndUpdateNodesStatus waits till all nodes joins the cluster and become ready
// it will update joined/done status
// approve csr will run as routine and cancelled whenever all nodes are ready and joined
// this will allow to run it and end only when it is needed
// removeUninitializedTaint parameter is removing unitialized taint on platforms with
// external cloud provider, as assisted-installer is using fake credentials
func (c *controller) WaitAndUpdateNodesStatus(ctx context.Context, wg *sync.WaitGroup, removeUninitializedTaint bool) {
	approveCtx, approveCancel := context.WithCancel(ctx)
	defer func() {
		approveCancel()
		c.log.Infof("WaitAndUpdateNodesStatus finished")
		wg.Done()
	}()
	// starting approve csrs
	go c.ApproveCsrs(approveCtx)

	c.log.Infof("Waiting till all nodes will join and update status to assisted installer")
	_ = utils.WaitForeverForPredicate(ctx, GeneralWaitInterval, utils.ToPredicate(c.waitAndUpdateNodesStatus, removeUninitializedTaint))
}

func (c *controller) waitAndUpdateNodesStatus(removeUninitializedTaint bool) bool {
	ignoreStatuses := []string{models.HostStatusDisabled}
	var hostsInError int
	ctxReq := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctxReq, c.log)

	assistedNodesMap, err := c.ic.GetHosts(ctxReq, log, ignoreStatuses)
	knownIpAddresses := common.BuildHostsMapIPAddressBased(assistedNodesMap)
	if err != nil {
		log.WithError(err).Error("Failed to get node map from the assisted service")
		return KeepWaiting
	}

	logHostsStatus(log, assistedNodesMap)

	hostsInProgressMap := common.GetHostsInStatus(assistedNodesMap, []string{models.HostStatusInstalled}, false)
	errNodesMap := common.GetHostsInStatus(hostsInProgressMap, []string{models.HostStatusError}, true)
	hostsInError = len(errNodesMap)

	//if all hosts are in error, mark the failure and finish
	if hostsInError > 0 && hostsInError == len(hostsInProgressMap) {
		c.log.Infof("Done waiting for all the nodes. Nodes in error status: %d\n", hostsInError)
		return ExitWaiting
	}
	//if all hosts are successfully installed, finish
	if len(hostsInProgressMap) == 0 {
		c.log.Infof("All nodes were successfully installed")
		return ExitWaiting
	}
	//otherwise, update the progress status and keep waiting
	log.Infof("Checking if cluster nodes are ready. %d nodes remaining", len(hostsInProgressMap))
	nodes, err := c.kc.ListNodes()
	if err != nil {
		log.WithError(err).Error("Failed to get list of nodes from k8s client")
		return KeepWaiting
	}
	for _, node := range nodes.Items {
		host, ok := common.HostMatchByNameOrIPAddress(node, hostsInProgressMap, knownIpAddresses)
		if !ok {
			log.Warnf("Node %s is not in inventory hosts", strings.ToLower(node.Name))
			continue
		}
		common.LogIfHostIpChanged(c.log, node, knownIpAddresses)

		if funk.Contains([]models.HostStage{models.HostStageConfiguring, models.HostStageRebooting}, host.Host.Progress.CurrentStage) {
			log.Infof("Found new joined node %s with inventory id %s, kubernetes id %s, updating its status to %s",
				node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageJoined)
			if err := c.ic.UpdateHostInstallProgress(ctxReq, host.Host.InfraEnvID.String(), host.Host.ID.String(), models.HostStageJoined, ""); err != nil {
				log.WithError(err).Errorf("Failed to update node %s installation status", node.Name)
				continue
			}
		}

		ready := common.IsK8sNodeIsReady(node)
		if !ready && removeUninitializedTaint {
			if err := c.kc.UntaintNode(node.Name); err != nil {
				log.WithError(err).Errorf("Failed to remove uninitialized taint from node %s", node.Name)
				continue
			}
		}

		if ready && host.Host.Progress.CurrentStage != models.HostStageDone {
			log.Infof("Found new ready node %s with inventory id %s, kubernetes id %s, updating its status to %s",
				node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageDone)

			if err := c.ic.UpdateHostInstallProgress(ctxReq, host.Host.InfraEnvID.String(), host.Host.ID.String(), models.HostStageDone, ""); err != nil {
				log.WithError(err).Errorf("Failed to update node %s installation status", node.Name)
				continue
			}
			c.rebootsNotifier.Start(ctxReq, node.Name, host.Host.ID, &host.Host.InfraEnvID, host.Host.ClusterID)
		}
	}

	// Since the host statuses may have changed due to the above loop,
	// we need to get the updated list of hosts again so we don't operate
	// on stale data.
	assistedNodesMapUpdated, err2 := c.ic.GetHosts(ctxReq, log, ignoreStatuses)
	if err2 != nil {
		log.WithError(err2).Error("Failed to get node map from the assisted service")
		return KeepWaiting
	}

	c.updateConfiguringStatusIfNeeded(assistedNodesMapUpdated)
	return KeepWaiting
}

func (c *controller) HackDNSAddressConflict(wg *sync.WaitGroup) {
	c.log.Infof("Making sure service %s can reserve the .10 address", dnsServiceName)

	defer func() {
		c.log.Infof("HackDNSAddressConflict finished")
		wg.Done()
	}()
	networks, err := c.kc.GetServiceNetworks()
	if err != nil || len(networks) == 0 {
		c.log.Errorf("Failed to get service networks: %s", err)
		return
	}

	netIp, _, _ := net.ParseCIDR(networks[0])
	ip := netIp.To16()
	if ip == nil {
		c.log.Infof("Failed to parse service network cidr %s, skipping", networks[0])
		return
	}

	ip[len(ip)-1] = 10 // .10 or :a is the conflicting address

	for i := 0; i < maxDNSServiceIPAttempts; i++ {
		svs, err := c.kc.ListServices("")
		if err != nil {
			c.log.WithError(err).Warnf("Failed to list running services, attempt %d/%d", i+1, maxDNSServiceIPAttempts)
			time.Sleep(DNSAddressRetryInterval)
			continue
		}
		s := c.findServiceByIP(ip.String(), &svs.Items)
		if s == nil {
			c.log.Infof("No service found with IP %s, attempt %d/%d", ip, i+1, maxDNSServiceIPAttempts)
			time.Sleep(DNSAddressRetryInterval)
			continue
		}
		if s.Name == dnsServiceName && s.Namespace == dnsServiceNamespace {
			c.log.Infof("Service %s has successfully taken IP %s", dnsServiceName, ip)
			break
		}
		c.log.Warnf("Deleting service %s in namespace %s whose IP %s conflicts with %s", s.Name, s.Namespace, ip, dnsServiceName)
		if err := c.killConflictingService(s); err != nil {
			c.log.WithError(err).Warnf("Failed to delete service %s in namespace %s", s.Name, s.Namespace)
			continue
		}
		if err := c.deleteDNSOperatorPods(); err != nil {
			c.log.WithError(err).Warn("Failed to delete DNS operator pods")
		}
	}
}

func (c *controller) findServiceByIP(ip string, services *[]v1.Service) *v1.Service {
	for _, s := range *services {
		if s.Spec.ClusterIP == ip {
			return &s
		}
	}
	return nil
}

func (c *controller) killConflictingService(s *v1.Service) error {
	return utils.Retry(maxDeletionAttempts, DeletionRetryInterval, c.log, func() error {
		return c.kc.DeleteService(s.Name, s.Namespace)
	})
}

func (c *controller) deleteDNSOperatorPods() error {
	return utils.Retry(maxDeletionAttempts, DeletionRetryInterval, c.log, func() error {
		return c.kc.DeletePods(dnsOperatorNamespace)
	})
}

func (c *controller) getMCSLogs() (string, error) {
	logs := ""
	namespace := "openshift-machine-config-operator"
	pods, err := c.kc.GetPods(namespace, map[string]string{"k8s-app": "machine-config-server"}, "")
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get mcs pods")
		return "", nil
	}
	for _, pod := range pods {
		podLogs, err := c.kc.GetPodLogs(namespace, pod.Name, generalWaitTimeoutInt*10)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to get logs of pod %s", pod.Name)
			return "", nil
		}
		logs += podLogs
	}
	return logs, nil
}

func (c *controller) updateConfiguringStatusIfNeeded(hosts map[string]inventory_client.HostData) {
	logs, err := c.getMCSLogs()
	if err != nil {
		return
	}
	common.SetConfiguringStatusForHosts(c.ic, hosts, logs, false, c.log)
}

func (c *controller) ApproveCsrs(ctx context.Context) {
	c.log.Infof("Start approving CSRs")
	ticker := time.NewTicker(GeneralWaitInterval)
	for {
		select {
		case <-ctx.Done():
			c.log.Infof("Finish approving CSRs")
			return
		case <-ticker.C:
			csrs, err := c.kc.ListCsrs()
			if err != nil {
				continue
			}
			c.approveCsrs(csrs)
		}
	}
}

func (c *controller) approveCsrs(csrs *certificatesv1.CertificateSigningRequestList) {
	for i := range csrs.Items {
		csr := csrs.Items[i]
		if !isCsrApproved(&csr) {
			c.log.Infof("Approving CSR %s", csr.Name)
			// We can fail and it is ok, we will retry on the next time
			_ = c.kc.ApproveCsr(&csr)
		}
	}
}

func isCsrApproved(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certificatesv1.CertificateApproved {
			return true
		}
	}
	return false
}

func (c *controller) updateFinalizingProgress(ctx context.Context, stage models.FinalizingStage) {
	_ = utils.RetryWithContext(ctx, math.MaxInt, GeneralWaitInterval, c.log, func() error {
		return c.ic.UpdateFinalizingProgress(ctx, c.ClusterID, stage)
	})
}

func (c *controller) waitOnStage(ctx context.Context, stage models.FinalizingStage, interval time.Duration, predicate func() bool) error {
	c.updateFinalizingProgress(ctx, stage)
	return utils.WaitForeverForPredicate(ctx, interval, predicate)
}

func (c *controller) PostInstallConfigs(ctx context.Context, wg *sync.WaitGroup) {
	defer func() {
		c.log.Infof("Finished PostInstallConfigs")
		wg.Done()
	}()
	err := utils.WaitForeverForPredicate(ctx, GeneralWaitInterval, func() bool {
		ctxReq := utils.GenerateRequestContext()
		cluster, err := c.ic.GetCluster(ctx, false)
		if err != nil {
			utils.RequestIDLogger(ctxReq, c.log).WithError(err).Errorf("Failed to get cluster %s from assisted-service", c.ClusterID)
			return false
		}
		return *cluster.Status == models.ClusterStatusFinalizing
	})
	if err != nil {
		return
	}

	errMessage := ""
	err, data := c.postInstallConfigs(ctx)
	// Even if the context was cancelled, the error is sent to the assisted service.
	// The timeouts will be handled by the service
	if err != nil {
		c.log.Error(err)
		errMessage = err.Error()
		c.status.Error()
	}
	success := err == nil
	if success {
		c.updateFinalizingProgress(ctx, models.FinalizingStageDone)
	}
	c.sendCompleteInstallation(ctx, success, errMessage, data)
}

func (c *controller) postInstallConfigs(ctx context.Context) (error, map[string]interface{}) {
	var err error
	var data map[string]interface{}

	c.log.Info("Start post install configs")
	c.updateFinalizingProgress(ctx, models.FinalizingStageWaitingForClusterOperators)
	err = c.waitingForClusterOperators(ctx)
	// compiles a report about cluster operators and prints it to the logs
	if report := c.operatorReport(); report != nil {
		data = map[string]interface{}{clusterOperatorReportKey: report}
	}
	if err != nil {
		return errors.Wrapf(err, "Stopped waiting for cluster operators to be available"), data
	}

	err = c.waitOnStage(ctx, models.FinalizingStageAddingRouterCa, failedOperatorRetry*time.Second, c.addRouterCAToClusterCA)
	if err != nil {
		return errors.Wrapf(err, "Stopped waiting for router ca data"), data
	}

	// Wait for OLM operators
	if err = c.waitForOLMOperators(ctx); err != nil {
		// no need to send error in case olm failed as service should move to degraded in that case
		c.log.WithError(err).Warn("Error while initializing OLM operators")
	}

	return nil, nil
}

func (c *controller) isStageTimedOut(stage models.FinalizingStage) func() bool {
	return func() bool {
		cluster, err := c.ic.GetCluster(context.TODO(), false)
		if err != nil {
			c.log.WithError(err).Error("failed to get cluster")
			return false
		}
		return cluster != nil && cluster.Progress != nil && cluster.Progress.FinalizingStage == stage && cluster.Progress.FinalizingStageTimedOut
	}
}

func (c *controller) waitForOLMOperators(ctx context.Context) error {
	var operators []models.MonitoredOperator
	var err, retryErr error
	var errs []error

	// In case the timeout occur, we have to update the pending OLM operators to failed state,
	// so the assisted-service can update the cluster state to complete.
	defer func() {
		if errs == nil {
			return
		}
		updateFunc := func() error {
			return c.updatePendingOLMOperators(context.TODO())
		}
		err = utils.RetryWithContext(ctx, failedOperatorRetry, GeneralWaitInterval, c.log, updateFunc)
		if err != nil {
			c.log.WithError(err).Error("Timeout while waiting for some of the operators and not able to update its state")
		}
	}()

	// Get the monitored operators:
	c.updateFinalizingProgress(ctx, models.FinalizingStageWaitingForOlmOperatorsCsvInitialization)
	retryErr = utils.WaitForeverForPredicateWithCancel(ctx, FetchRetryInterval, func() bool {
		var err2 error
		// We use the cache here to retrieve results from the assisted-service, as this function is executed repeatedly.
		// Over time, this approach ensures that we ultimately get the correct result.
		operators, err2 = c.ic.GetClusterMonitoredOLMOperators(context.TODO(), c.ClusterID, c.OpenshiftVersion, true)
		if err2 != nil {
			c.log.WithError(err2).Errorf("Error fetching the monitored operators from assisted-service")
			return false
		}
		return true
	}, c.isStageTimedOut(models.FinalizingStageWaitingForOlmOperatorsCsvInitialization))
	if retryErr != nil {
		return errors.Wrapf(retryErr, "Failed to fetch monitored operators")
	}

	if len(operators) == 0 {
		c.log.Info("No OLM operators found.")
		return nil
	}

	// Wait for the CSV state of the OLM operators, before applying OLM CRs
	err = utils.WaitForeverForPredicateWithCancel(ctx, GeneralWaitInterval, utils.ToPredicate(c.waitForCSVBeCreated, operators),
		c.isStageTimedOut(models.FinalizingStageWaitingForOlmOperatorsCsvInitialization))
	if err != nil {
		err = errors.Wrap(err, "Failed to wait for some of the OLM operators to be initialized")
		c.log.WithError(err).Warning("OLM CSV initialization")

		// We continue in case of failure, because we want to try to apply manifest at least for operators which are ready.
		errs = append(errs, err)
	}

	// Apply post install manifests
	c.updateFinalizingProgress(ctx, models.FinalizingStageApplyingOlmManifests)
	err = utils.WaitForeverForPredicateWithCancel(ctx, GeneralWaitInterval, utils.ToPredicate(c.applyPostInstallManifests, operators),
		c.isStageTimedOut(models.FinalizingStageApplyingOlmManifests))
	if err != nil {
		err = errors.Wrap(err, "Stopped applying post manifests")
		c.log.WithError(err).Warning("Applying manifests")

		errs = append(errs, err)
	}

	c.updateFinalizingProgress(ctx, models.FinalizingStageWaitingForOlmOperatorsCsv)
	err = c.waitForCSV(ctx)
	if err != nil {
		err = errors.Wrap(err, "Stopped waiting for OLM operators be installed")
		c.log.WithError(err).Warning("Installing OLM operators")

		errs = append(errs, err)
	}
	return stderrors.Join(errs...)
}

func (c *controller) getReadyOperators(operators []models.MonitoredOperator) ([]string, []models.MonitoredOperator, error) {
	var readyOperators []string
	for index := range operators {
		handler := NewClusterServiceVersionHandler(c.log, c.kc, &operators[index], c.status)
		c.log.Infof("Checking if %s operator is initialized", operators[index].Name)
		if handler.IsInitialized() {
			c.log.Infof("%s operator is initialized", operators[index].Name)
			readyOperators = append(readyOperators, handler.GetName())
		}
	}
	return readyOperators, operators, nil
}

func (c *controller) waitForCSVBeCreated(operators []models.MonitoredOperator) bool {
	c.log.Infof("Waiting for csv to be created")
	readyOperators, operators, err := c.getReadyOperators(operators)
	if err != nil {
		c.log.WithError(err).Warn("Error while fetch the operators state.")
		return false
	}
	if len(operators) == len(readyOperators) {
		return true
	}

	return false
}

func (c *controller) applyPostInstallManifests(operators []models.MonitoredOperator) bool {
	ctx := utils.GenerateRequestContext()
	tempDir, err := os.MkdirTemp("", "controller-custom-manifests-")
	if err != nil {
		c.log.WithError(err).Error("Failed to create temporary directory to create custom manifests.")
		return false
	}
	c.log.Infof("Created temporary directory %s to store custom manifest content.", tempDir)
	defer os.RemoveAll(tempDir)

	customManifestPath := path.Join(tempDir, customManifestsFile)
	if err = c.ic.DownloadFile(ctx, customManifestsFile, customManifestPath); err != nil {
		return false
	}

	kubeconfigName, err := common.DownloadKubeconfigNoingress(ctx, tempDir, c.ic, c.log)
	if err != nil {
		return false
	}

	// Unmarshall the content of the operators manifests:
	var manifests []manifest
	data, err := os.ReadFile(customManifestPath)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to read the custom manifests file.")
		return false
	}
	if err = json.Unmarshal(data, &manifests); err != nil {
		c.log.WithError(err).Errorf("Failed to unmarshall custom manifest file content %s.", data)
		return false
	}

	// Create the manifests of the operators, which are properly initialized:
	readyOperators, _, err := c.getReadyOperators(operators)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to fetch operators from assisted-service")
		return false
	}

	c.log.Infof("Ready operators to be applied: %v", readyOperators)
	applyErrOccured := false
	ocpVer, err := version.NewVersion(c.OpenshiftVersion)
	if err != nil {
		return false
	}
	constraints, err := version.NewConstraint(">= 4.8, < 4.9")
	if err != nil {
		return false
	}

	for _, manifest := range manifests {
		// renaming odf if ocs as manifest is applied based on the ready operators list
		if manifest.Name == "odf" && constraints.Check(ocpVer) {
			manifest.Name = "ocs"
		}
		c.log.Infof("Applying manifest %s: %s", manifest.Name, manifest.Content)

		// Check if the operator is properly initialized by CSV:
		if !func() bool {
			for _, readyOperator := range readyOperators {
				if readyOperator == manifest.Name {
					return true
				}
			}
			return false
		}() {
			continue
		}

		content, err := base64.StdEncoding.DecodeString(manifest.Content)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to decode content of operator CR %s.", manifest.Name)
			applyErrOccured = true
			continue
		}

		err = c.ops.CreateManifests(kubeconfigName, content)
		if err != nil {
			c.log.WithError(err).Error("Failed to apply manifest file.")
			applyErrOccured = true
			continue
		}

		c.log.Infof("Manifest %s applied.", manifest.Name)
	}

	return !applyErrOccured
}

func (c *controller) UpdateBMHs(ctx context.Context, wg *sync.WaitGroup) {
	defer func() {
		c.log.Infof("Finished UpdateBMHs")
		wg.Done()
	}()
	_ = utils.WaitForeverForPredicate(ctx, GeneralWaitInterval, func() bool {
		bmhs, err := c.kc.ListBMHs()
		if err != nil {
			c.log.WithError(err).Errorf("Failed to list BMH hosts")
			return KeepWaiting
		}

		c.log.Infof("Number of BMHs is %d", len(bmhs.Items))

		machines, err := c.unallocatedMachines(bmhs)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to find unallocated machines")
			return KeepWaiting
		}

		c.log.Infof("Number of unallocated Machines is %d", len(machines.Items))

		allUpdated := c.updateBMHs(&bmhs, machines)
		if allUpdated {
			c.log.Infof("Updated all the BMH CRs, finished successfully")
			return ExitWaiting
		}
		return KeepWaiting
	})
}

func (c *controller) unallocatedMachines(bmhList metal3v1alpha1.BareMetalHostList) (*mapiv1beta1.MachineList, error) {
	machineList, err := c.kc.ListMachines()
	if err != nil {
		return nil, err
	}

	unallocatedList := &mapiv1beta1.MachineList{Items: machineList.Items[:0]}

	for _, machine := range machineList.Items {
		role, ok := machine.Labels["machine.openshift.io/cluster-api-machine-role"]
		if ok && role == "worker" {
			c.log.Infof("Found worker machine %s", machine.Name)
		}
		unallocated := ok && role == "worker"
		for _, bmh := range bmhList.Items {
			if bmh.Spec.ConsumerRef != nil && bmh.Spec.ConsumerRef.Name == machine.Name {
				unallocated = false
			}
		}
		if unallocated {
			unallocatedList.Items = append(unallocatedList.Items, machine)
		}
	}
	return unallocatedList, nil
}

func (c *controller) copyBMHAnnotationsToStatus(bmh *metal3v1alpha1.BareMetalHost) (bool, error) {
	annotations := bmh.GetAnnotations()
	content := []byte(annotations[metal3v1alpha1.StatusAnnotation])

	if annotations[metal3v1alpha1.StatusAnnotation] == "" {
		c.log.Infof("Skipping setting status of BMH host %s, status annotation not present", bmh.Name)
		return false, nil
	}
	objStatus, err := c.unmarshalStatusAnnotation(content)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to unmarshal status annotation of %s", bmh.Name)
		return false, err
	}
	bmh.Status = *objStatus
	if bmh.Status.LastUpdated.IsZero() {
		// Ensure the LastUpdated timestamp in set to avoid
		// infinite loops if the annotation only contained
		// part of the status information.
		t := metav1.Now()
		bmh.Status.LastUpdated = &t
	}
	err = c.kc.UpdateBMHStatus(bmh)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to update status of BMH %s", bmh.Name)
		return false, err
	}
	return true, nil
}

func (c *controller) unmarshalStatusAnnotation(content []byte) (*metal3v1alpha1.BareMetalHostStatus, error) {
	bmhStatus := &metal3v1alpha1.BareMetalHostStatus{}
	err := json.Unmarshal(content, bmhStatus)
	if err != nil {
		return nil, err
	}
	return bmhStatus, nil
}

// updateBMHWithProvisioning() If we are in None or Unmanaged state:
// - set ExternallyProvisioned
// - Removing pausedAnnotation from BMH master hosts
// - set the consumer ref
func (c *controller) updateBMHWithProvisioning(bmh *metal3v1alpha1.BareMetalHost, machineList *mapiv1beta1.MachineList) error {
	if bmh.Status.Provisioning.State != metal3v1alpha1.StateNone && bmh.Status.Provisioning.State != metal3v1alpha1.StateUnmanaged {
		c.log.Infof("bmh %s Provisioning.State=%s - ignoring", bmh.Name, bmh.Status.Provisioning.State)
		return nil
	}
	var err error
	needsUpdate := !bmh.Spec.ExternallyProvisioned
	bmh.Spec.ExternallyProvisioned = true
	// when baremetal operator is enabled, we need to remove the pausedAnnotation
	// to indicated that the statusAnnotation is available. This is only on
	// the master nodes.
	annotations := bmh.GetAnnotations()
	if _, ok := annotations[metal3v1alpha1.PausedAnnotation]; ok {
		c.log.Infof("Removing pausedAnnotation from BMH host %s", bmh.Name)
		delete(annotations, metal3v1alpha1.PausedAnnotation)
		needsUpdate = true
	}
	if bmh.Spec.ConsumerRef == nil {
		err = c.updateConsumerRef(bmh, machineList)
		if err != nil {
			return err
		}
		needsUpdate = true
	}
	if needsUpdate {
		c.log.Infof("Updating bmh %s", bmh.Name)
		err = c.kc.UpdateBMH(bmh)
		if err != nil {
			return errors.Wrapf(err, "Failed to update BMH %s", bmh.Name)
		}
	}
	return nil
}

// updateBMHWithNOProvisioning()
// - move the BMH status annotation to the status sub resource
// - set the consumer ref
func (c *controller) updateBMHWithNOProvisioning(bmh *metal3v1alpha1.BareMetalHost, machineList *mapiv1beta1.MachineList) error {
	statusUpdated, err := c.copyBMHAnnotationsToStatus(bmh)
	if err != nil {
		return errors.Wrapf(err, "Failed to copy BMH Annotations to status %s", bmh.Name)
	}
	if statusUpdated {
		// refresh the BMH after the StatusUpdate to get the new generation
		bmh, err = c.kc.GetBMH(bmh.Name)
		if err != nil {
			return errors.Wrapf(err, "Failed to refresh the BMH %s", bmh.Name)
		}
	}
	needsUpdate := false
	annotations := bmh.GetAnnotations()
	if _, ok := annotations[metal3v1alpha1.StatusAnnotation]; ok {
		delete(annotations, metal3v1alpha1.StatusAnnotation)
		needsUpdate = true
	}

	if bmh.Spec.ConsumerRef == nil {
		err = c.updateConsumerRef(bmh, machineList)
		if err != nil {
			return err
		}
		needsUpdate = true
	}
	if needsUpdate {
		c.log.Infof("Updating bmh %s", bmh.Name)
		err = c.kc.UpdateBMH(bmh)
		if err != nil {
			return errors.Wrapf(err, "Failed to update BMH %s", bmh.Name)
		}
	}
	return nil
}

func (c *controller) updateConsumerRef(bmh *metal3v1alpha1.BareMetalHost, machineList *mapiv1beta1.MachineList) error {
	//	update consumer ref for workers only in case machineset controller has already
	//	created an unassigned Machine (base on machineset replica count)
	if len(machineList.Items) == 0 {
		return fmt.Errorf("no available machine for bmh %s, need to wait for machineset controller to create one", bmh.Name)
	}
	c.log.Infof("Updating consumer ref for bmh %s", bmh.Name)
	machine := &machineList.Items[0]
	machineList.Items = machineList.Items[1:]
	bmh.Spec.ConsumerRef = &v1.ObjectReference{
		APIVersion: machine.APIVersion,
		Kind:       machine.Kind,
		Namespace:  machine.Namespace,
		Name:       machine.Name,
	}
	return nil
}

func (c *controller) updateBMHs(bmhList *metal3v1alpha1.BareMetalHostList, machineList *mapiv1beta1.MachineList) bool {
	provisioningExists, err := c.kc.IsMetalProvisioningExists()
	if err != nil {
		c.log.WithError(err).Errorf("Failed get IsMetalProvisioningExists")
		return false
	}

	allUpdated := true
	for i := range bmhList.Items {
		bmh := bmhList.Items[i]
		c.log.Infof("Checking bmh %s", bmh.Name)

		if provisioningExists {
			err = c.updateBMHWithProvisioning(&bmh, machineList)
		} else {
			err = c.updateBMHWithNOProvisioning(&bmh, machineList)
		}
		if err != nil {
			c.log.WithError(err).Errorf("Failed to update BMH %s", bmh.Name)
			allUpdated = false
			continue
		}
	}
	return allUpdated
}

// AddRouterCAToClusterCA adds router CA to cluster CA in kubeconfig
func (c *controller) addRouterCAToClusterCA() bool {
	ctx := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctx, c.log)
	log.Infof("Start adding ingress ca to cluster")
	caConfigMap, err := c.kc.GetConfigMap(ingressConfigMapNamespace, ingressConfigMapName)
	if err != nil {
		log.WithError(err).Warnf("fetching %s configmap from %s namespace", ingressConfigMapName, ingressConfigMapNamespace)
		return KeepWaiting
	}
	log.Infof("Sending ingress certificate to inventory service. Certificate data %s", caConfigMap.Data["ca-bundle.crt"])
	err = c.ic.UploadIngressCa(ctx, caConfigMap.Data["ca-bundle.crt"], c.ClusterID)
	if err != nil {
		log.WithError(err).Errorf("Failed to upload ingress ca to assisted-service")
		return KeepWaiting
	}
	log.Infof("Ingress ca successfully sent to inventory")
	return ExitWaiting

}

func (c *controller) getProgressingOLMOperators(useCache bool) ([]*models.MonitoredOperator, error) {
	ret := make([]*models.MonitoredOperator, 0)
	operators, err := c.ic.GetClusterMonitoredOLMOperators(context.TODO(), c.ClusterID, c.OpenshiftVersion, useCache)
	if err != nil {
		c.log.WithError(err).Warningf("Failed to connect to assisted service")
		return ret, err
	}
	for index := range operators {
		if operators[index].Status != models.OperatorStatusAvailable && operators[index].Status != models.OperatorStatusFailed {
			ret = append(ret, &operators[index])
		}
	}
	return ret, nil
}

func (c *controller) updatePendingOLMOperators(ctx context.Context) error {
	c.log.Infof("Updating pending OLM operators")

	// This call is executed only once during the application's graceful shutdown.
	// Therefore, it is essential to retrieve up-to-date results on which operators are
	// still progressing from the assisted-service. To ensure accuracy, we avoid using the cache in this case.
	operators, err := c.getProgressingOLMOperators(false)
	if err != nil {
		return err
	}

	for _, operator := range operators {
		c.log.Infof("Updating operator %s status to 'failed'", operator.Name)
		c.status.OperatorError(operator.Name)
		err := c.ic.UpdateClusterOperator(ctx, c.ClusterID, operator.Name, operator.Version, models.OperatorStatusFailed, "Waiting for operator timed out")
		if err != nil {
			c.log.WithError(err).Warnf("Failed to update olm %s status", operator.Name)
			return err
		}
	}

	return nil
}

// waitForCSV wait until all OLM monitored operators are available or failed.
func (c *controller) waitForCSV(ctx context.Context) error {
	// This function is executed only once during the wait for OLM operators to become ready.
	// As such, we need up-to-date information on which operators are still progressing.
	// While no operators should yet be in a 'failing' or 'available' status at this point, avoiding the cache helps mitigate potential edge cases.
	operators, err := c.getProgressingOLMOperators(false)
	if err != nil {
		return err
	}
	if len(operators) == 0 {
		return nil
	}

	handlers := make(map[string]*ClusterServiceVersionHandler)

	for index := range operators {
		handlers[operators[index].Name] = NewClusterServiceVersionHandler(c.log, c.kc, operators[index], c.status)
	}

	areOLMOperatorsAvailable := func() bool {
		for index := range handlers {
			// We use the cache here to retrieve results from the assisted-service, as this function is executed repeatedly.
			// Over time, this approach ensures that we ultimately get the correct result.
			if c.checkAndUpdateOperatorAvailability(handlers[index], true) {
				delete(handlers, index)
			}
		}

		return len(handlers) == 0
	}

	return utils.WaitForeverForPredicateWithCancel(ctx, GeneralWaitInterval, areOLMOperatorsAvailable,
		c.isStageTimedOut(models.FinalizingStageWaitingForOlmOperatorsCsv))
}

// waitingForClusterOperators checks Console operator and the Cluster Version Operator availability in the
// new OCP cluster in parallel.
// A success would be announced only when the service acknowledges the operators availability,
// in order to avoid unsycned scenarios.
func (c *controller) waitingForClusterOperators(ctx context.Context) error {
	// In case cvo changes it message we will update timer but we want to have maximum timeout
	// for this context with timeout is used
	c.log.Info("Waiting for cluster operators")
	ctxWithTimeout, cancel := context.WithTimeout(ctx, CVOMaxTimeout)
	defer cancel()

	isClusterVersionAvailable := func() bool {
		clusterOperatorHandler := NewClusterOperatorHandler(c.kc, consoleOperatorName, c.log)
		clusterVersionHandler := NewClusterVersionHandler(c.log, c.kc)
		isConsoleEnabled, err := c.kc.IsClusterCapabilityEnabled(consoleCapabilityName)
		if err != nil {
			c.log.WithError(err).Error("Failed to check if console is enabled")
			return false
		}
		var result bool
		if isConsoleEnabled {
			c.log.Info("Console is enabled, will wait for the console operator to be available")
			// We use the cache here to retrieve results from the assisted-service, as this function is executed repeatedly.
			// Over time, this approach ensures that we ultimately get the correct result.
			result = c.checkAndUpdateOperatorAvailability(clusterOperatorHandler, true)
		} else {
			c.log.Info("Console is disabled, will not wait for the console operator to be available")
			result = true
		}
		if c.WaitForClusterVersion {
			// We use the cache here to retrieve results from the assisted-service, as this function is executed repeatedly.
			// Over time, this approach ensures that we ultimately get the correct result.
			result = c.checkAndUpdateOperatorAvailability(clusterVersionHandler, true) && result
		}
		return result
	}
	return utils.WaitForeverForPredicate(ctxWithTimeout, GeneralProgressUpdateInt, isClusterVersionAvailable)
}

func (c *controller) UpdateNodeLabels(ctx context.Context, wg *sync.WaitGroup) {
	defer func() {
		c.log.Infof("Finished UpdateNodeLabels")
		wg.Done()
	}()

	c.log.Infof("Updating host labels if required")
	err := utils.WaitForeverForPredicate(ctx, GeneralWaitInterval, c.setNodesLabels)
	if err != nil {
		c.log.Warn("Failed to label the nodes")
	}
}

func areNodeLabelsUpdated(node v1.Node, nodeLabels string) bool {
	nodeLabelsPresent := node.Labels
	nodeLabelsRequired := make(map[string]string)
	_ = json.Unmarshal([]byte(nodeLabels), &nodeLabelsRequired)

	for key, value := range nodeLabelsRequired {
		if val, found := nodeLabelsPresent[key]; !found || value != val {
			return false
		}
	}

	return true
}

func (c *controller) getAIHostsWithLabels() (map[string]inventory_client.HostData, error) {
	ignoreStatuses := []string{models.HostStatusDisabled, models.HostStatusError}
	ctxReq := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctxReq, c.log)
	assistedNodesMap, err := c.ic.GetHosts(ctxReq, log, ignoreStatuses)
	if err != nil {
		log.WithError(err).Error("Failed to get node map from the assisted service")
		return nil, err
	}

	hostsWithLabels := make(map[string]inventory_client.HostData)
	for hostname, hostData := range assistedNodesMap {
		if len(hostData.Host.NodeLabels) == 0 {
			continue
		}
		hostsWithLabels[hostname] = hostData
	}
	return hostsWithLabels, nil
}

func (c *controller) patchWorkerMCPPaused(pause bool) error {
	err := c.kc.PatchMachineConfigPoolPaused(pause, workerMCPName)
	if err != nil {
		c.log.WithError(err).Warnf("Failed to unpause pool %s", workerMCPName)
	}
	return err
}

func (c *controller) shouldPauseWorkerMCP(hostsWithLabels map[string]inventory_client.HostData) bool {
	for _, hostData := range hostsWithLabels {
		if hostData.Host.Role == models.HostRoleWorker && hostData.Host.MachineConfigPoolName != "" {
			return true
		}
	}
	return false
}

// Set labels to hosts
// If one of the labels was role pause mcp and set this role
// after setting all labels unpause in case it was paused
func (c *controller) setNodesLabels() bool {
	c.log.Infof("Setting node labels if required")
	hostsWithLabels, err := c.getAIHostsWithLabels()
	if err != nil {
		return KeepWaiting
	}
	if len(hostsWithLabels) == 0 {
		c.log.Infof("No hosts with labels, skipping update node labels")
		return ExitWaiting
	}
	pauseWorkerMCP := c.shouldPauseWorkerMCP(hostsWithLabels)
	// if it was paused already we will skip in PauseUnpauseMachineConfigPool
	if pauseWorkerMCP {
		if err := c.patchWorkerMCPPaused(true); err != nil {
			return KeepWaiting
		}
	}

	if len(hostsWithLabels) > c.patchNodesLabels(c.log, hostsWithLabels) {
		c.log.Infof("Not all host labels where added yet, keep trying")
		return KeepWaiting
	}

	if pauseWorkerMCP {
		if err := c.patchWorkerMCPPaused(false); err != nil {
			return KeepWaiting
		}
	}

	return ExitWaiting
}

func (c *controller) patchNodesLabels(log logrus.FieldLogger, hostsWithLabels map[string]inventory_client.HostData) int {
	patchedNumber := 0
	knownIpAddresses := common.BuildHostsMapIPAddressBased(hostsWithLabels)
	nodes, err := c.kc.ListNodes()
	if err != nil {
		log.WithError(err).Error("Failed to get list of nodes from k8s client")
		return patchedNumber
	}

	for _, node := range nodes.Items {
		if !common.IsK8sNodeIsReady(node) {
			continue
		}
		host, ok := common.HostMatchByNameOrIPAddress(node, hostsWithLabels, knownIpAddresses)
		if !ok {
			continue
		}
		if areNodeLabelsUpdated(node, host.Host.NodeLabels) {
			patchedNumber++
			continue
		}

		err = c.kc.PatchNodeLabels(node.Name, host.Host.NodeLabels)
		if err != nil {
			log.WithError(err).Errorf("Failed to patch node %s with node labels %s", node.Name, host.Host.NodeLabels)
			continue
		}
		log.Infof("Successfully patched label for host %s", node.Name)
		patchedNumber++
	}
	return patchedNumber
}

func (c *controller) sendCompleteInstallation(ctx context.Context, isSuccess bool, errorInfo string, data map[string]interface{}) {
	c.log.Infof("Start complete installation step, with params success: %t, error info: %s", isSuccess, errorInfo)
	_ = utils.WaitForeverForPredicate(ctx, GeneralProgressUpdateInt, func() bool {
		ctxReq := utils.GenerateRequestContext()
		if err := c.ic.CompleteInstallation(ctxReq, c.ClusterID, isSuccess, errorInfo, data); err != nil {
			utils.RequestIDLogger(ctxReq, c.log).Error(err)
			return false
		}
		return true
	})
	c.log.Infof("Done complete installation step")
}

// Gather information about cluster operators. This information is required for
// debugging purposes so it is printed into the controller's log file (always)
// and sent to the service to be formatted in an event to the user (in case of
// a failure)
func (c *controller) operatorReport() []models.OperatorMonitorReport {
	operators, err := c.kc.ListClusterOperators()
	if err != nil {
		c.log.WithError(err).Warning("Failed to list cluster operators")
		return nil
	}

	report := make([]models.OperatorMonitorReport, 0)
	for _, operator := range operators.Items {
		// Note: always keep this log so we have all the information
		// in the controller's log
		c.log.Infof("Operator %s, statuses: %v", operator.Name, operator.Status.Conditions)
		status, message := utils.MonitoredOperatorStatus(operator.Status.Conditions)
		report = append(report, models.OperatorMonitorReport{
			Name:       operator.Name,
			Status:     status,
			StatusInfo: message,
		})
	}
	return report
}

func (c *controller) logHostResolvConf() {
	for _, filePath := range []string{"/etc/resolv.conf", "/tmp/var-run-resolv.conf", "/tmp/host-resolv.conf"} {
		content, err := c.ops.ReadFile(filePath)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to read %s", filePath)
			continue
		}
		c.log.Infof("Content of %s is %s", filePath, string(content))
	}
}

// This is temporary function, till https://bugzilla.redhat.com/show_bug.cgi?id=2097041 will not be resolved,
// that should validate router state only in case of failure
// It will patch router to add access logs
// It will run http router health check to see if router is healthy on host network
func (c *controller) logRouterStatus() {
	if !c.status.HasError() || c.HighAvailabilityMode != models.ClusterHighAvailabilityModeNone || c.ControlPlainCount > 1 {
		return
	}
	c.log.Infof("Start checking router status")
	var cl *models.Cluster
	var err error
	//nolint:gosec // need insecure TLS option for testing and development
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr, Timeout: dnsValidationTimeout}
	localIp, _ := utils.GetOutboundIP()
	if localIp == "" {
		localIp = "localhost"
	}

	var consoleUrl string
	// run every 30 seconds router check and log the result
	go func() {
		_ = utils.WaitForPredicate(WaitTimeout, SummaryLogsPeriod, func() bool {
			if consoleUrl == "" {
				cl, err = c.ic.GetCluster(context.Background(), false)
				if err != nil {
					c.log.WithError(err).Errorf("Failed to get cluster %s from assisted-service", c.ClusterID)
					return false
				}
			}

			consoleUrl = fmt.Sprintf("https://canary-openshift-ingress-canary.apps.%s.%s/health", cl.Name, cl.BaseDNSDomain)
			r, err := client.Get(consoleUrl)
			if err != nil {
				c.log.WithError(err).Warning("Failed to reach console")
			} else {
				defer r.Body.Close()
				response, errR := io.ReadAll(r.Body)
				if errR != nil {
					response = []byte("Failed to read response")
				}
				c.log.Infof("canary url status %s, response %s", r.Status, string(response))
			}

			url := fmt.Sprintf("http://%s/_______internal_router_healthz", localIp)
			r, err = client.Get(url)
			if err != nil {
				c.log.WithError(err).Warning("Failed to reach internal router health")
			} else {
				c.log.Infof("route internal health status %s", r.Status)
			}
			return false
		})
	}()
}

/**
 * This function upload the following logs at once to the service at the end of the installation process
 * It takes a lenient approach so if some logs are not available it ignores them and moves on
 * currently the bundled logs are:
 * - controller logs
 * - oc must-gather logs
 **/
func (c *controller) uploadSummaryLogs(podName string, namespace string, sinceSeconds int64) error {
	var tarentries = make([]utils.TarEntry, 0)
	var ok bool = true
	ctx := utils.GenerateRequestContext()
	if c.status.HasError() || c.status.HasOperatorError() {
		c.logHostResolvConf()
		c.log.Infof("Uploading cluster operator status logs before must-gather")
		err := common.UploadPodLogs(c.kc, c.ic, c.ClusterID, podName, c.Namespace, controllerLogsSecondsAgo, c.log)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to upload controller logs")
		}
		c.log.Infof("Uploading oc must-gather logs")
		images := c.parseMustGatherImages()
		if tarfile, err := c.collectMustGatherLogs(ctx, images...); err == nil {
			if entry, tarerr := utils.NewTarEntryFromFile(tarfile); tarerr == nil {
				tarentries = append(tarentries, *entry)
			}
		} else {
			ok = false
		}
	}

	c.log.Infof("Uploading logs for %s in %s", podName, namespace)
	podLogs := common.GetControllerPodLogs(c.kc, podName, namespace, sinceSeconds, c.log)
	tarentries = append(tarentries,
		*utils.NewTarEntry(podLogs, nil, int64(podLogs.Len()), common.ControllerLogFileName))

	//write the combined input of the summary sources into a pipe and offload it
	//to the UploadLogs request to the assisted-service
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		defer pw.Close()
		err := utils.WriteToTarGz(pw, tarentries, c.log)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to create tar.gz body of the log uplaod request")
		}
	}()
	// if error will occur in goroutine above, the writer will be closed
	// and as a result upload will fail
	err := c.ic.UploadLogs(ctx, c.ClusterID, models.LogsTypeController, pr)
	if err != nil {
		utils.RequestIDLogger(ctx, c.log).WithError(err).Error("Failed to upload logs")
		return err
	}

	if !ok {
		msg := "Some Logs were not collected in summary"
		c.log.Error(msg)
		return errors.New(msg)
	}

	return nil
}

func (c *controller) parseMustGatherImages() []string {
	images := make([]string, 0)
	if c.MustGatherImage == "" {
		c.log.Infof("collecting must-gather logs into using image from release")
		return images
	}

	c.log.Infof("collecting must-gather logs using this image configuration %s", c.MustGatherImage)
	var imageMap map[string]string
	err := json.Unmarshal([]byte(c.MustGatherImage), &imageMap)
	if err != nil {
		//MustGatherImage is not a JSON. Pass it as is
		images = append(images, c.MustGatherImage)
		return images
	}

	//Use the parsed MustGatherImage to find the images needed for collecting
	//the information
	if c.status.HasError() {
		//general error - collect all data from the cluster using the standard image
		images = append(images, imageMap["ocp"])
	}

	for _, op := range c.status.GetOperatorsInError() {
		if imageMap[op] != "" {
			//per failed operator - add feature image for collecting more
			//information about failed olm operators
			images = append(images, imageMap[op])
		}
	}
	c.log.Infof("collecting must-gather logs with images: %v", images)
	return images
}

func (c *controller) collectMustGatherLogs(ctx context.Context, images ...string) (string, error) {
	mustGatherDir := "/tmp/must-gather-logs"
	// We should not create must-gather logs if they already were created and upload failed
	if _, err := os.Stat(path.Join(mustGatherDir, ops.MustGatherFileName)); err == nil {
		return path.Join(mustGatherDir, ops.MustGatherFileName), nil
	}

	if err := utils.RecreateFolder(mustGatherDir); err != nil {
		c.log.Errorf("Failed to create dir for must gather logs")
		return "", err
	}

	kubeconfigPath, err := common.DownloadKubeconfigNoingress(ctx, mustGatherDir, c.ic, c.log)
	if err != nil {
		return "", err
	}

	//collect must gather logs
	logtar, err := c.ops.GetMustGatherLogs(mustGatherDir, kubeconfigPath, images...)
	if err != nil {
		c.log.Errorf("Failed to collect must-gather logs %v\n", err)
		return "", err
	}

	return logtar, nil
}

// Uploading logs every 5 minutes
// We will take logs of assisted controller and upload them to assisted-service
// by creating tar gz of them.
func (c *controller) UploadLogs(ctx context.Context, wg *sync.WaitGroup, invoker string) {
	podName := ""
	ticker := time.NewTicker(LogsUploadPeriod)
	progressCtx := utils.GenerateRequestContext()

	defer func() {
		c.log.Infof("Finished UploadLogs")
		wg.Done()
	}()
	c.log.Infof("Start sending logs")
	for {
		select {
		case <-ctx.Done():
			if invoker == common.InvokerAgent {
				// In the agent installer, assisted-service will not be available after
				// the bootstrap node reboots. Attempting to upload logs to assisted-service
				// will fail in a continuous loop. To avoid this situation, we skip the final
				// log uploads.
				c.log.Infof("assisted-service is offline in agent installer mode. final log uploads skipped.")
				return
			}
			c.log.Infof("Upload final controller and cluster logs before exit")
			c.ic.ClusterLogProgressReport(progressCtx, c.ClusterID, models.LogsStateRequested)
			c.logRouterStatus()
			_ = utils.WaitForPredicate(WaitTimeout, SummaryLogsPeriod, func() bool {
				err := c.uploadSummaryLogs(podName, c.Namespace, controllerLogsSecondsAgo)
				if err != nil {
					c.log.Infof("retry uploading logs in 30 seconds...")
				}
				return err == nil
			})
			c.ic.ClusterLogProgressReport(progressCtx, c.ClusterID, models.LogsStateCompleted)
			return
		case <-ticker.C:
			if podName == "" {
				pods, err := c.kc.GetPods(c.Namespace, map[string]string{"job-name": "assisted-installer-controller"},
					fmt.Sprintf("status.phase=%s", v1.PodRunning))
				if err != nil || len(pods) < 1 {
					c.log.WithError(err).Warnf("Failed to get controller pod name")
				} else {
					podName = pods[0].Name
				}
			}

			//on normal flow, keep updating the controller log output every 5 minutes
			c.log.Infof("Start uploading controller logs (intermediate snapshot)")
			err := common.UploadPodLogs(c.kc, c.ic, c.ClusterID, podName, c.Namespace, controllerLogsSecondsAgo, c.log)
			if err != nil {
				c.log.WithError(err).Warnf("Failed to upload controller logs")
				continue
			}
		}
	}
}

func (c controller) SetReadyState(waitTimeout time.Duration) *models.Cluster {
	c.log.Infof("Start waiting to be ready")
	var cluster *models.Cluster
	var err error
	_ = utils.WaitForPredicate(waitTimeout, 1*time.Second, func() bool {
		cluster, err = c.ic.GetCluster(context.TODO(), false)
		if err != nil {
			c.log.WithError(err).Warningf("Failed to connect to assisted service")
			return false
		}
		c.log.Infof("assisted-service is available")

		_, err = c.kc.ListNodes()
		if err != nil {
			c.log.WithError(err).Warningf("Failed to connect to ocp cluster")
			return false
		}
		c.log.Infof("kube-apiserver is available")

		c.log.Infof("Sending ready event")
		if _, err := c.kc.CreateEvent(c.Namespace, common.AssistedControllerIsReadyEvent,
			"Assisted controller managed to connect to assisted service and kube-apiserver and is ready to start",
			common.AssistedControllerPrefix); err != nil && !apierrors.IsAlreadyExists(err) {
			c.log.WithError(err).Errorf("Failed to spawn event")
			return false
		}

		return true
	})
	return cluster
}

func (c *controller) GetStatus() *ControllerStatus {
	return c.status
}

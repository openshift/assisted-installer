package assisted_installer_controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	mapiv1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
)

const (
	// We retry 10 times in 30sec interval meaning that we tolerate the operator to be in failed
	// state for 5minutes.
	failedOperatorRetry       = 10
	generalWaitTimeoutInt     = 30
	controllerLogsSecondsAgo  = 120 * 60
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
	customManifestsFile       = "custom_manifests.yaml"
	kubeconfigFileName        = "kubeconfig-noingress"
)

var (
	retryPostManifestTimeout = 10 * time.Minute
	GeneralWaitInterval      = generalWaitTimeoutInt * time.Second
	GeneralProgressUpdateInt = 60 * time.Second
	LogsUploadPeriod         = 5 * time.Minute
	WaitTimeout              = 70 * time.Minute
	CompleteTimeout          = 30 * time.Minute
	DNSAddressRetryInterval  = 20 * time.Second
	DeletionRetryInterval    = 10 * time.Second
	LongWaitTimeout          = 10 * time.Hour
	CVOMaxTimeout            = 3 * time.Hour
)

// assisted installer controller is added to control installation process after  bootstrap pivot
// assisted installer will deploy it on installation process
// as a first step it will wait till nodes are added to cluster and update their status to Done

type ControllerConfig struct {
	ClusterID             string `envconfig:"CLUSTER_ID" required:"true" `
	URL                   string `envconfig:"INVENTORY_URL" required:"true"`
	PullSecretToken       string `envconfig:"PULL_SECRET_TOKEN" required:"true"`
	SkipCertVerification  bool   `envconfig:"SKIP_CERT_VERIFICATION" required:"false" default:"false"`
	CACertPath            string `envconfig:"CA_CERT_PATH" required:"false" default:""`
	Namespace             string `envconfig:"NAMESPACE" required:"false" default:"assisted-installer"`
	OpenshiftVersion      string `envconfig:"OPENSHIFT_VERSION" required:"true"`
	HighAvailabilityMode  string `envconfig:"HIGH_AVAILABILITY_MODE" required:"false" default:"Full"`
	WaitForClusterVersion bool   `envconfig:"CHECK_CLUSTER_VERSION" required:"false" default:"false"`
	MustGatherImage       string `envconfig:"MUST_GATHER_IMAGE" required:"false" default:""`
}
type Controller interface {
	WaitAndUpdateNodesStatus(status *ControllerStatus)
}

type ControllerStatus struct {
	errCounter uint32
	components map[string]bool
	lock       sync.Mutex
}

type controller struct {
	ControllerConfig
	Status *ControllerStatus
	log    *logrus.Logger
	ops    ops.Ops
	ic     inventory_client.InventoryClient
	kc     k8s_client.K8SClient
}

func NewController(log *logrus.Logger, cfg ControllerConfig, ops ops.Ops, ic inventory_client.InventoryClient, kc k8s_client.K8SClient) *controller {
	return &controller{
		log:              log,
		ControllerConfig: cfg,
		ops:              ops,
		ic:               ic,
		kc:               kc,
		Status:           NewControllerStatus(),
	}
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
func (c *controller) WaitAndUpdateNodesStatus(ctx context.Context, wg *sync.WaitGroup) {
	approveCtx, approveCancel := context.WithCancel(ctx)
	defer func() {
		approveCancel()
		c.log.Infof("WaitAndUpdateNodesStatus finished")
		wg.Done()
	}()
	// starting approve csrs
	go c.ApproveCsrs(approveCtx)

	c.log.Infof("Waiting till all nodes will join and update status to assisted installer")
	_ = utils.WaitForPredicateWithContext(ctx, LongWaitTimeout, GeneralWaitInterval, c.waitAndUpdateNodesStatus)
}

func (c *controller) waitAndUpdateNodesStatus() bool {
	ignoreStatuses := []string{models.HostStatusDisabled}
	var hostsInError int
	ctxReq := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctxReq, c.log)

	assistedNodesMap, err := c.ic.GetHosts(ctxReq, log, ignoreStatuses)
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
		host, ok := hostsInProgressMap[strings.ToLower(node.Name)]
		if !ok {
			if _, ok := assistedNodesMap[strings.ToLower(node.Name)]; !ok {
				log.Warnf("Node %s is not in inventory hosts", strings.ToLower(node.Name))
			}

			continue
		}
		if common.IsK8sNodeIsReady(node) {
			log.Infof("Found new ready node %s with inventory id %s, kubernetes id %s, updating its status to %s",
				node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageDone)
			if err := c.ic.UpdateHostInstallProgress(ctxReq, host.Host.ID.String(), models.HostStageDone, ""); err != nil {
				log.WithError(err).Errorf("Failed to update node %s installation status", node.Name)
				continue
			}
		} else if host.Host.Progress.CurrentStage == models.HostStageConfiguring {
			log.Infof("Found new joined node %s with inventory id %s, kubernetes id %s, updating its status to %s",
				node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageJoined)
			if err := c.ic.UpdateHostInstallProgress(ctxReq, host.Host.ID.String(), models.HostStageJoined, ""); err != nil {
				log.WithError(err).Errorf("Failed to update node %s installation status", node.Name)
				continue
			}
		}
	}
	c.updateConfiguringStatusIfNeeded(assistedNodesMap)
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

	ip, _, _ := net.ParseCIDR(networks[0])
	ip4 := ip.To4()
	if ip4 == nil {
		c.log.Infof("Service network is IPv6: %s, skipping the .10 address hack", ip)
		return
	}
	ip4[3] = 10 // .10 is the conflicting address

	for i := 0; i < maxDNSServiceIPAttempts; i++ {
		svs, err := c.kc.ListServices("")
		if err != nil {
			c.log.WithError(err).Warnf("Failed to list running services, attempt %d/%d", i+1, maxDNSServiceIPAttempts)
			time.Sleep(DNSAddressRetryInterval)
			continue
		}
		s := c.findServiceByIP(ip4.String(), &svs.Items)
		if s == nil {
			c.log.Infof("No service found with IP %s, attempt %d/%d", ip4, i+1, maxDNSServiceIPAttempts)
			time.Sleep(DNSAddressRetryInterval)
			continue
		}
		if s.Name == dnsServiceName && s.Namespace == dnsServiceNamespace {
			c.log.Infof("Service %s has successfully taken IP %s", dnsServiceName, ip4)
			break
		}
		c.log.Warnf("Deleting service %s in namespace %s whose IP %s conflicts with %s", s.Name, s.Namespace, ip4, dnsServiceName)
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

func (c controller) approveCsrs(csrs *certificatesv1.CertificateSigningRequestList) {
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

func (c controller) PostInstallConfigs(ctx context.Context, wg *sync.WaitGroup) {
	defer func() {
		c.log.Infof("Finished PostInstallConfigs")
		wg.Done()
	}()
	err := utils.WaitForPredicateWithContext(ctx, LongWaitTimeout, GeneralWaitInterval, func() bool {
		ctxReq := utils.GenerateRequestContext()
		cluster, err := c.ic.GetCluster(ctx)
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
	err = c.postInstallConfigs(ctx)
	// context was cancelled, requires usage of WaitForPredicateWithContext
	// no reason to set error
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		c.log.Error(err)
		errMessage = err.Error()
		c.Status.Error()
	}
	success := err == nil
	c.sendCompleteInstallation(ctx, success, errMessage)
}

func (c controller) postInstallConfigs(ctx context.Context) error {
	var err error

	c.log.Infof("Waiting for cluster version operator: %t", c.WaitForClusterVersion)

	if c.WaitForClusterVersion {
		if err = c.waitingForClusterVersion(ctx); err != nil {
			return errors.Wrapf(err, "Timeout while waiting for cluster version to be available")
		}
	}

	// Unlabel run-level from assisted-installer namespace after the installation.
	// Keeping the `run-level` label represents a security risk as it overwrites the SecurityContext configurations
	// used for applications deployed in this namespace.
	data := []byte(`{"metadata":{"labels":{"$patch": "delete", "openshift.io/run-level":"0"}}}`)
	c.log.Infof("Removing run-level label from %s namespace", c.ControllerConfig.Namespace)
	err = c.kc.PatchNamespace(c.ControllerConfig.Namespace, data)
	if err != nil {
		// It is a conscious decision not to fail an installation if for any reason patching the namespace
		// in order to remove the `run-level` label has failed. This will be redesigned in the next release
		// so that the `run-level` label is not created in the first place.
		c.log.Warn("Failed to unlabel AI namespace after the installation.")
	}

	err = utils.WaitForPredicateWithContext(ctx, WaitTimeout, GeneralWaitInterval, c.addRouterCAToClusterCA)
	if err != nil {
		return errors.Wrapf(err, "Timeout while waiting router ca data")
	}

	unpatch, err := utils.EtcdPatchRequired(c.ControllerConfig.OpenshiftVersion)
	if err != nil {
		return errors.Wrapf(err, "Failed to patch etcd")
	}
	if unpatch && c.HighAvailabilityMode != models.ClusterHighAvailabilityModeNone {
		if err = utils.WaitForPredicateWithContext(ctx, WaitTimeout, GeneralWaitInterval, c.unpatchEtcd); err != nil {
			return errors.Wrapf(err, "Timeout while trying to unpatch etcd")
		}
	} else {
		c.log.Infof("Skipping etcd unpatch for cluster version %s", c.ControllerConfig.OpenshiftVersion)
	}

	if err = utils.WaitForPredicateWithContext(ctx, WaitTimeout, GeneralWaitInterval, c.validateConsoleAvailability); err != nil {
		return errors.Wrapf(err, "Timeout while waiting for console to become available")
	}

	// Apply post install manifests
	err = utils.WaitForPredicateWithContext(ctx, retryPostManifestTimeout, GeneralWaitInterval, c.applyPostInstallManifests)
	if err != nil {
		return errors.Wrapf(err, "Failed to apply post manifests")
	}

	if err != c.waitForOLMOperators(ctx) {
		// In case the timeout occur, we have to update the pending OLM operators to failed state,
		// so the assisted-service can update the cluster state to completed.
		if err = c.updatePendingOLMOperators(); err != nil {
			return errors.Errorf("Timeout while waiting for some of the operators and not able to update its state")
		}
		return errors.Wrapf(err, "Timeout while waiting for OLM operators be installed")
	}

	return nil
}

func (c controller) applyPostInstallManifests() bool {
	ctx := utils.GenerateRequestContext()
	tempDir, err := ioutil.TempDir("", "controller-custom-manifests-")
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

	kubeconfigName, err := c.downloadKubeconfigNoingress(ctx, tempDir)
	if err != nil {
		return false
	}

	err = c.ops.CreateManifests(kubeconfigName, customManifestPath)
	if err != nil {
		c.log.WithError(err).Error("Failed to apply manifest file.")
		return false
	}

	return true
}

func (c controller) UpdateBMHs(ctx context.Context, wg *sync.WaitGroup) {
	defer func() {
		c.log.Infof("Finished UpdateBMHs")
		wg.Done()
	}()
	_ = utils.WaitForPredicateWithContext(ctx, time.Duration(1<<63-1), GeneralWaitInterval, func() bool {
		bmhs, err := c.kc.ListBMHs()
		if err != nil {
			c.log.WithError(err).Errorf("Failed to list BMH hosts")
			return false
		}

		c.log.Infof("Number of BMHs is %d", len(bmhs.Items))

		machines, err := c.unallocatedMachines(bmhs)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to find unallocated machines")
			return false
		}

		c.log.Infof("Number of unallocated Machines is %d", len(machines.Items))

		allUpdated := c.updateBMHs(&bmhs, machines)
		if allUpdated {
			c.log.Infof("Updated all the BMH CRs, finished successfully")
			return true
		}
		return false
	})
}

func (c controller) unallocatedMachines(bmhList metal3v1alpha1.BareMetalHostList) (*mapiv1beta1.MachineList, error) {
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

func (c controller) copyBMHAnnotationsToStatus(bmh *metal3v1alpha1.BareMetalHost) (bool, error) {
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

func (c controller) unmarshalStatusAnnotation(content []byte) (*metal3v1alpha1.BareMetalHostStatus, error) {
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
func (c controller) updateBMHWithProvisioning(bmh *metal3v1alpha1.BareMetalHost, machineList *mapiv1beta1.MachineList) error {
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
// - move the BMH status annotation to the Status sub resource
// - set the consumer ref
func (c controller) updateBMHWithNOProvisioning(bmh *metal3v1alpha1.BareMetalHost, machineList *mapiv1beta1.MachineList) error {
	statusUpdated, err := c.copyBMHAnnotationsToStatus(bmh)
	if err != nil {
		return errors.Wrapf(err, "Failed to copy BMH Annotations to Status %s", bmh.Name)
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

func (c controller) updateConsumerRef(bmh *metal3v1alpha1.BareMetalHost, machineList *mapiv1beta1.MachineList) error {
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

func (c controller) updateBMHs(bmhList *metal3v1alpha1.BareMetalHostList, machineList *mapiv1beta1.MachineList) bool {
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

func (c controller) unpatchEtcd() bool {
	c.log.Infof("Unpatching etcd")
	if err := c.kc.UnPatchEtcd(); err != nil {
		c.log.Error(err)
		return false
	}
	return true
}

// AddRouterCAToClusterCA adds router CA to cluster CA in kubeconfig
func (c controller) addRouterCAToClusterCA() bool {
	ctx := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctx, c.log)
	log.Infof("Start adding ingress ca to cluster")
	caConfigMap, err := c.kc.GetConfigMap(ingressConfigMapNamespace, ingressConfigMapName)

	if err != nil {
		log.WithError(err).Errorf("fetching %s configmap from %s namespace", ingressConfigMapName, ingressConfigMapNamespace)
		return false
	}
	log.Infof("Sending ingress certificate to inventory service. Certificate data %s", caConfigMap.Data["ca-bundle.crt"])
	err = c.ic.UploadIngressCa(ctx, caConfigMap.Data["ca-bundle.crt"], c.ClusterID)
	if err != nil {
		log.WithError(err).Errorf("Failed to upload ingress ca to assisted-service")
		return false
	}
	log.Infof("Ingress ca successfully sent to inventory")
	return true

}

func (c controller) getMaximumOLMTimeout(operators []*models.MonitoredOperator) time.Duration {
	timeout := WaitTimeout.Seconds()
	for _, operator := range operators {
		timeout = math.Max(float64(operator.TimeoutSeconds), timeout)
	}

	return time.Duration(timeout * float64(time.Second))
}

func (c controller) getProgressingOLMOperators() ([]*models.MonitoredOperator, error) {
	ret := make([]*models.MonitoredOperator, 0)
	operators, err := c.ic.GetClusterMonitoredOLMOperators(context.TODO(), c.ClusterID)
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

func (c controller) updatePendingOLMOperators() error {
	c.log.Infof("Updating pending OLM operators")
	ctx := utils.GenerateRequestContext()
	operators, err := c.getProgressingOLMOperators()
	if err != nil {
		return err
	}
	for _, operator := range operators {
		c.Status.OperatorError(operator.Name)
		err := c.ic.UpdateClusterOperator(ctx, c.ClusterID, operator.Name, models.OperatorStatusFailed, "Waiting for operator timed out")
		if err != nil {
			c.log.WithError(err).Warnf("Failed to update olm %s status", operator.Name)
			return err
		}
	}
	return nil
}

// waitForOLMOperators wait until all OLM monitored operators are available or failed.
func (c controller) waitForOLMOperators(ctx context.Context) error {
	operators, err := c.getProgressingOLMOperators()
	if err != nil {
		return err
	}
	if len(operators) == 0 {
		return nil
	}

	handlers := make(map[string]*ClusterServiceVersionHandler)

	for index := range operators {
		handlers[operators[index].Name] = NewClusterServiceVersionHandler(c.kc, operators[index], c.Status)
	}

	areOLMOperatorsAvailable := func() bool {
		if len(handlers) == 0 {
			return true
		}

		for index := range handlers {
			if c.isOperatorAvailable(handlers[index]) {
				delete(handlers, index)
			}
		}
		return false
	}

	waitTimeout := c.getMaximumOLMTimeout(operators)
	c.log.Infof("Waiting for OLM operators for %v", waitTimeout)
	return utils.WaitForPredicateWithContext(ctx, waitTimeout, GeneralWaitInterval, areOLMOperatorsAvailable)
}

// validateConsoleAvailability checks if the console operator is available
func (c controller) validateConsoleAvailability() bool {
	return c.isOperatorAvailable(NewClusterOperatorHandler(c.kc, consoleOperatorName))
}

// waitingForClusterVersion checks the Cluster Version Operator availability in the
// new OCP cluster. A success would be announced only when the service acknowledges
// the CVO availability, in order to avoid unsycned scenarios.
// In case cvo changes it message we will update timer but we want to have maximum timeout
// for this context with timeout is used
func (c controller) waitingForClusterVersion(ctx context.Context) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, CVOMaxTimeout)
	defer cancel()
	isClusterVersionAvailable := func(timer *time.Timer) bool {
		return c.isOperatorAvailable(NewClusterVersionHandler(c.kc, timer))
	}
	return utils.WaitForPredicateWithTimer(ctxWithTimeout, WaitTimeout, GeneralProgressUpdateInt, isClusterVersionAvailable)
}

func (c controller) sendCompleteInstallation(ctx context.Context, isSuccess bool, errorInfo string) {
	c.log.Infof("Start complete installation step, with params success: %t, error info: %s", isSuccess, errorInfo)
	_ = utils.WaitForPredicateWithContext(ctx, CompleteTimeout, GeneralProgressUpdateInt, func() bool {
		ctxReq := utils.GenerateRequestContext()
		if err := c.ic.CompleteInstallation(ctxReq, c.ClusterID, isSuccess, errorInfo); err != nil {
			utils.RequestIDLogger(ctxReq, c.log).Error(err)
			return false
		}
		return true
	})
	c.log.Infof("Done complete installation step")
}

// logClusterOperatorsStatus logging cluster operators status
func (c controller) logClusterOperatorsStatus() {
	operators, err := c.kc.ListClusterOperators()
	if err != nil {
		c.log.WithError(err).Warning("Failed to list cluster operators")
		return
	}

	for _, operator := range operators.Items {
		c.log.Infof("Operator %s, statuses: %v", operator.Name, operator.Status.Conditions)
	}
}

/**
 * This function upload the following logs at once to the service at the end of the installation process
 * It takes a lenient approach so if some logs are not available it ignores them and moves on
 * currently the bundled logs are:
 * - controller logs
 * - oc must-gather logs
 **/
func (c controller) uploadSummaryLogs(podName string, namespace string, sinceSeconds int64) error {
	var tarentries = make([]utils.TarEntry, 0)
	var ok bool = true
	ctx := utils.GenerateRequestContext()

	c.logClusterOperatorsStatus()
	if c.Status.HasError() || c.Status.HasOperatorError() {
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
	if podLogs, err := c.kc.GetPodLogsAsBuffer(namespace, podName, sinceSeconds); err == nil {
		tarentries = append(tarentries,
			*utils.NewTarEntry(podLogs, nil, int64(podLogs.Len()), fmt.Sprintf("%s.logs", podName)))
	} else {
		ok = false
	}

	if len(tarentries) == 0 {
		return errors.New("No logs are available for sending summary logs")
	}

	//write the combined input of the summary sources into a pipe and offload it
	//to the UploadLogs request to the assisted-service
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		defer pw.Close()
		err := utils.WriteToTarGz(pw, tarentries)
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
		c.log.Errorf(msg)
		return errors.New(msg)
	}

	return nil
}

func (c controller) parseMustGatherImages() []string {
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
	if c.Status.HasError() {
		//general error - collect all data from the cluster using the standard image
		images = append(images, imageMap["ocp"])
	}

	for _, op := range c.Status.GetOperatorsInError() {
		if imageMap[op] != "" {
			//per failed operator - add feature image for collecting more
			//information about failed olm operators
			images = append(images, imageMap[op])
		}
	}
	c.log.Infof("collecting must-gather logs with images: %v", images)
	return images
}

func (c controller) downloadKubeconfigNoingress(ctx context.Context, dir string) (string, error) {
	// Download kubeconfig file
	kubeconfigPath := path.Join(dir, kubeconfigFileName)
	err := c.ic.DownloadFile(ctx, kubeconfigFileName, kubeconfigPath)
	if err != nil {
		c.log.Errorf("Failed to download noingress kubeconfig %v\n", err)
		return "", err
	}
	c.log.Infof("Downloaded %s to %s.", kubeconfigFileName, kubeconfigPath)

	return kubeconfigPath, nil
}

func (c controller) collectMustGatherLogs(ctx context.Context, images ...string) (string, error) {
	tempDir, ferr := ioutil.TempDir("", "controller-must-gather-logs-")
	if ferr != nil {
		c.log.Errorf("Failed to create temp directory for must-gather-logs %v\n", ferr)
		return "", ferr
	}

	kubeconfigPath, err := c.downloadKubeconfigNoingress(ctx, tempDir)
	if err != nil {
		return "", err
	}

	//collect must gather logs
	logtar, err := c.ops.GetMustGatherLogs(tempDir, kubeconfigPath, images...)
	if err != nil {
		c.log.Errorf("Failed to collect must-gather logs %v\n", err)
		return "", err
	}

	return logtar, nil
}

// Uploading logs every 5 minutes
// We will take logs of assisted controller and upload them to assisted-service
// by creating tar gz of them.
func (c *controller) UploadLogs(ctx context.Context, wg *sync.WaitGroup) {
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
			if podName != "" {
				c.log.Infof("Upload final controller and cluster logs before exit")
				c.ic.ClusterLogProgressReport(progressCtx, c.ClusterID, models.LogsStateRequested)
				_ = utils.WaitForPredicate(WaitTimeout, LogsUploadPeriod, func() bool {
					err := c.uploadSummaryLogs(podName, c.Namespace, controllerLogsSecondsAgo)
					if err != nil {
						c.log.Infof("retry uploading logs in 5 minutes...")
					}
					return err == nil
				})
			}
			c.ic.ClusterLogProgressReport(progressCtx, c.ClusterID, models.LogsStateCompleted)
			return
		case <-ticker.C:
			if podName == "" {
				pods, err := c.kc.GetPods(c.Namespace, map[string]string{"job-name": "assisted-installer-controller"},
					fmt.Sprintf("status.phase=%s", v1.PodRunning))
				if err != nil {
					c.log.WithError(err).Warnf("Failed to get controller pod name")
					continue
				}
				if len(pods) < 1 {
					c.log.Infof("Didn't find myself, something strange had happened")
					continue
				}
				podName = pods[0].Name
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

func (c controller) SetReadyState() {
	c.log.Infof("Start waiting to be ready")
	_ = utils.WaitForPredicate(WaitTimeout, 1*time.Second, func() bool {
		_, err := c.ic.GetCluster(context.TODO())
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
}

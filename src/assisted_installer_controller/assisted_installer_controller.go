package assisted_installer_controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/api/certificates/v1beta1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
)

const (
	generalWaitTimeoutInt    = 30
	controllerLogsSecondsAgo = 120 * 60
)

var GeneralWaitInterval = generalWaitTimeoutInt * time.Second
var generalProgressUpdateInt = 60 * time.Second
var LogsUploadPeriod = 5 * time.Minute
var WaitTimeout = 70 * time.Minute
var NumRetrySendingLogs = 6

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
}
type Controller interface {
	WaitAndUpdateNodesStatus(status *ControllerStatus)
}

type ControllerStatus struct {
	errCounter uint32
}

type controller struct {
	ControllerConfig
	log *logrus.Logger
	ops ops.Ops
	ic  inventory_client.InventoryClient
	kc  k8s_client.K8SClient
}

func NewController(log *logrus.Logger, cfg ControllerConfig, ops ops.Ops, ic inventory_client.InventoryClient, kc k8s_client.K8SClient) *controller {
	return &controller{
		log:              log,
		ControllerConfig: cfg,
		ops:              ops,
		ic:               ic,
		kc:               kc,
	}
}

func (status *ControllerStatus) Error() {
	atomic.AddUint32(&status.errCounter, 1)
}

func (status *ControllerStatus) HasError() bool {
	return atomic.LoadUint32(&status.errCounter) > 0
}

func (c *controller) WaitAndUpdateNodesStatus(status *ControllerStatus) {
	c.log.Infof("Waiting till all nodes will join and update status to assisted installer")
	ignoreStatuses := []string{models.HostStatusDisabled, models.HostStatusInstalled}
	var hostsInError int
	for {
		time.Sleep(GeneralWaitInterval)
		ctx := utils.GenerateRequestContext()
		log := utils.RequestIDLogger(ctx, c.log)

		assistedInstallerNodesMap, err := c.ic.GetHosts(ctx, log, ignoreStatuses)
		if err != nil {
			log.WithError(err).Error("Failed to get node map from inventory")
			continue
		}
		errNodesMap := common.FilterHostsByStatus(assistedInstallerNodesMap, []string{models.HostStatusError})
		hostsInError = len(errNodesMap)

		//if all hosts are in error, mark the failure and finish
		if hostsInError > 0 && hostsInError == len(assistedInstallerNodesMap) {
			status.Error()
			break
		}
		//if all hosts are successfully installed, finish
		if len(assistedInstallerNodesMap) == 0 {
			break
		}
		//otherwise, update the progress status and keep waiting
		log.Infof("Searching for host to change status, number to find %d", len(assistedInstallerNodesMap))
		nodes, err := c.kc.ListNodes()
		if err != nil {
			continue
		}
		for _, node := range nodes.Items {
			host, ok := assistedInstallerNodesMap[strings.ToLower(node.Name)]
			if !ok {
				continue
			}

			ctx := utils.GenerateRequestContext()
			log := utils.RequestIDLogger(ctx, c.log)
			log.Infof("Found new joined node %s with inventory id %s, kubernetes id %s, updating its status to %s",
				node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageDone)
			if err := c.ic.UpdateHostInstallProgress(ctx, host.Host.ID.String(), models.HostStageDone, ""); err != nil {
				log.Errorf("Failed to update node %s installation status, %s", node.Name, err)
				continue
			}
		}
		c.updateConfiguringStatusIfNeeded(assistedInstallerNodesMap)
	}
	c.log.Infof("Done waiting for all the nodes. Nodes in error status: %d\n", hostsInError)
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

func (c *controller) ApproveCsrs(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
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

func (c controller) approveCsrs(csrs *v1beta1.CertificateSigningRequestList) {
	for i := range csrs.Items {
		csr := csrs.Items[i]
		if !isCsrApproved(&csr) {
			c.log.Infof("Approving CSR %s", csr.Name)
			// We can fail and it is ok, we will retry on the next time
			_ = c.kc.ApproveCsr(&csr)
		}
	}
}

func isCsrApproved(csr *certificatesv1beta1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certificatesv1beta1.CertificateApproved {
			return true
		}
	}
	return false
}

func (c controller) PostInstallConfigs(wg *sync.WaitGroup, status *ControllerStatus) {
	defer wg.Done()
	for {
		time.Sleep(GeneralWaitInterval)
		ctx := utils.GenerateRequestContext()
		cluster, err := c.ic.GetCluster(ctx)
		if err != nil {
			utils.RequestIDLogger(ctx, c.log).WithError(err).Errorf("Failed to get cluster %s from assisted-service", c.ClusterID)
			continue
		}
		// waiting till cluster will be installed(3 masters must be installed)
		if *cluster.Status != models.ClusterStatusFinalizing {
			continue
		}
		break
	}

	errMessage := ""
	err := c.postInstallConfigs()
	if err != nil {
		errMessage = err.Error()
		status.Error()
	}
	success := err == nil
	c.sendCompleteInstallation(success, errMessage)
}

func (c controller) postInstallConfigs() error {
	var err error

	if c.WaitForClusterVersion {
		err = c.waitingForClusterVersion()
		if err != nil {
			return err
		}
	}

	err = utils.WaitForPredicate(WaitTimeout, GeneralWaitInterval, c.addRouterCAToClusterCA)
	if err != nil {
		return errors.Errorf("Timeout while waiting router ca data")
	}

	unpatch, err := utils.EtcdPatchRequired(c.ControllerConfig.OpenshiftVersion)
	if err != nil {
		return err
	}
	if unpatch && c.HighAvailabilityMode != models.ClusterHighAvailabilityModeNone {
		err = utils.WaitForPredicate(WaitTimeout, GeneralWaitInterval, c.unpatchEtcd)
		if err != nil {
			return errors.Errorf("Timeout while trying to unpatch etcd")
		}
	} else {
		c.log.Infof("Skipping etcd unpatch for cluster version %s", c.ControllerConfig.OpenshiftVersion)
	}

	err = utils.WaitForPredicate(WaitTimeout, GeneralWaitInterval, c.validateConsolePod)
	if err != nil {
		return errors.Errorf("Timeout while waiting for console pod to be running")
	}

	return nil
}

func (c controller) UpdateBMHs(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		time.Sleep(GeneralWaitInterval)
		exists, err := c.kc.IsMetalProvisioningExists()
		if err != nil {
			continue
		}
		if err == nil && exists {
			c.log.Infof("Provisioning CR exists, no need to update BMHs")
			return
		}

		bmhs, err := c.kc.ListBMHs()
		if err != nil {
			c.log.WithError(err).Errorf("Failed to BMH hosts")
			continue
		}

		machines, err := c.unallocatedMachines(bmhs)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to get machines")
			continue
		}

		allUpdated := c.updateBMHs(bmhs, machines)
		if allUpdated {
			c.log.Infof("Updated all the BMH CRs, finished successfully")
			return
		}
	}
}

func (c controller) unallocatedMachines(bmhList metal3v1alpha1.BareMetalHostList) (*mapiv1beta1.MachineList, error) {
	machineList, err := c.kc.ListMachines()
	if err != nil {
		return nil, err
	}

	unallocatedList := &mapiv1beta1.MachineList{Items: machineList.Items[:0]}

	for _, machine := range machineList.Items {
		role, ok := machine.Labels["machine.openshift.io/cluster-api-machine-role"]
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

func (c controller) updateBMHs(bmhList metal3v1alpha1.BareMetalHostList, machineList *mapiv1beta1.MachineList) bool {
	allUpdated := true

	for i := range bmhList.Items {
		bmh := bmhList.Items[i]
		c.log.Infof("Checking bmh %s", bmh.Name)
		annotations := bmh.GetAnnotations()
		content := []byte(annotations[metal3v1alpha1.StatusAnnotation])

		if annotations[metal3v1alpha1.StatusAnnotation] == "" && bmh.Spec.ConsumerRef != nil {
			c.log.Infof("Skipping setting status of BMH host %s, status annotation not present", bmh.Name)
			continue
		}
		allUpdated = false
		if annotations[metal3v1alpha1.StatusAnnotation] != "" {
			objStatus, err := c.unmarshalStatusAnnotation(content)
			if err != nil {
				c.log.WithError(err).Errorf("Failed to unmarshal status annotation of %s", bmh.Name)
				continue
			}
			bmh.Status = *objStatus
			if bmh.Status.LastUpdated.IsZero() {
				// Ensure the LastUpdated timestamp in set to avoid
				// infinite loops if the annotation only contained
				// part of the status information.
				t := metav1.Now()
				bmh.Status.LastUpdated = &t
			}
			err = c.kc.UpdateBMHStatus(&bmh)
			if err != nil {
				c.log.WithError(err).Errorf("Failed to update status of BMH %s", bmh.Name)
				continue
			}
			delete(annotations, metal3v1alpha1.StatusAnnotation)
		}
		if bmh.Spec.ConsumerRef == nil && len(machineList.Items) > 0 {
			machine := machineList.Items[0]
			machineList.Items = machineList.Items[1:]
			bmh.Spec.ConsumerRef = &v1.ObjectReference{
				APIVersion: machine.APIVersion,
				Kind:       machine.Kind,
				Namespace:  machine.Namespace,
				Name:       machine.Name,
			}
		}

		err := c.kc.UpdateBMH(&bmh)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to update BMH %s", bmh.Name)
		}
	}
	return allUpdated
}

func (c controller) unmarshalStatusAnnotation(content []byte) (*metal3v1alpha1.BareMetalHostStatus, error) {
	bmhStatus := &metal3v1alpha1.BareMetalHostStatus{}
	err := json.Unmarshal(content, bmhStatus)
	if err != nil {
		return nil, err
	}
	return bmhStatus, nil
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
	cmName := "default-ingress-cert"
	cmNamespace := "openshift-config-managed"
	ctx := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctx, c.log)
	log.Infof("Start adding ingress ca to cluster")
	caConfigMap, err := c.kc.GetConfigMap(cmNamespace, cmName)

	if err != nil {
		log.WithError(err).Errorf("fetching %s configmap from %s namespace", cmName, cmNamespace)
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

func (c controller) validateConsolePod() bool {
	c.log.Infof("Checking if console pod is running")
	pods, err := c.kc.GetPods("openshift-console", map[string]string{"app": "console", "component": "ui"}, "")
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get console pods")
		return false
	}
	for _, pod := range pods {
		if pod.Status.Phase == "Running" {
			c.log.Infof("Found running console pod")
			return true
		}
		c.log.Infof("Console pod is in status %s. Continue waiting for it", pod.Status.Phase)
	}
	return false
}

func (c controller) waitingForClusterVersion() error {
	isClusterVersionAvailable := func() bool {
		available, msg := c.validateClusterVersion()
		status := fmt.Sprintf("Cluster version is available: %t , message: %s", available, msg)
		c.log.Infof(status)
		if err := c.ic.UpdateClusterInstallProgress(utils.GenerateRequestContext(), c.ClusterID, status); err != nil {
			c.log.Errorf("Failed to update cluster %s installation progress status: %s", c.ClusterID, err)
		}
		return available
	}

	err := utils.WaitForPredicate(WaitTimeout, generalProgressUpdateInt, isClusterVersionAvailable)
	if err != nil {
		return errors.Errorf("Timeout while waiting for cluster version to be available")
	}
	return nil
}

func (c controller) validateClusterVersion() (bool, string) {
	c.log.Infof("Waiting for cluster version to be available")
	cv, err := c.kc.GetClusterVersion("version")
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get cluster version")
		return false, ""
	}
	conditionsByType := map[configv1.ClusterStatusConditionType]configv1.ClusterOperatorStatusCondition{}
	// condition type to dict
	for _, condition := range cv.Status.Conditions {
		conditionsByType[condition.Type] = condition
	}
	// check if it cluster version is available
	condition, ok := conditionsByType[configv1.OperatorAvailable]
	if ok && condition.Status == configv1.ConditionTrue {
		return true, condition.Message
	}
	// if not available return message from progressing
	condition, ok = conditionsByType[configv1.OperatorProgressing]
	if ok {
		return false, condition.Message
	}

	return false, ""
}

func (c controller) sendCompleteInstallation(isSuccess bool, errorInfo string) {
	c.log.Infof("Start complete installation step, with params success:%t, error info %s", isSuccess, errorInfo)
	for {
		ctx := utils.GenerateRequestContext()
		if err := c.ic.CompleteInstallation(ctx, c.ClusterID, isSuccess, errorInfo); err != nil {
			utils.RequestIDLogger(ctx, c.log).Error(err)
			continue
		}
		break
	}
	c.log.Infof("Done complete installation step")
}

/**
 * This function upload the following logs at once to the service at the end of the installation process
 * It takes a linient approach so if some logs are not available it ignores them and moves on
 * currently the bundled logs are:
 * - controller logs
 * - oc must-gather logs
 **/
func (c controller) uploadSummaryLogs(podName string, namespace string, sinceSeconds int64, isMustGatherEnabled bool) error {
	var tarentries = make([]utils.TarEntry, 0)
	var ok bool = true
	ctx := utils.GenerateRequestContext()

	if isMustGatherEnabled {
		c.log.Infof("Uploading oc must-gather logs")
		if tarfile, err := c.collectMustGatherLogs(ctx); err == nil {
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

func (c controller) collectMustGatherLogs(ctx context.Context) (string, error) {
	tempDir, ferr := ioutil.TempDir("", "controller-must-gather-logs-")
	if ferr != nil {
		c.log.Errorf("Failed to create temp directory for must-gather-logs %v\n", ferr)
		return "", ferr
	}

	//donwload kubeconfig file
	kubeconfig_file_name := "kubeconfig-noingress"
	kubeconfigPath := path.Join(tempDir, kubeconfig_file_name)
	err := c.ic.DownloadFile(ctx, kubeconfig_file_name, kubeconfigPath)
	if err != nil {
		c.log.Errorf("Failed to download noingress kubeconfig %v\n", err)
		return "", err
	}

	//collect must gather logs
	logtar, err := c.ops.GetMustGatherLogs(tempDir, kubeconfigPath)
	if err != nil {
		c.log.Errorf("Failed to collect must-gather logs %v\n", err)
		return "", err
	}

	return logtar, nil
}

// Uploading logs every 5 minutes
// We will take logs of assisted controller and upload them to assisted-service
// by creating tar gz of them.
func (c *controller) UploadLogs(ctx context.Context, cancellog context.CancelFunc, wg *sync.WaitGroup, status *ControllerStatus) {
	defer wg.Done()
	c.log.Infof("Start sending logs")
	podName := ""
	ticker := time.NewTicker(LogsUploadPeriod)
	for {
		select {
		case <-ctx.Done():
			if podName != "" {
				c.log.Infof("Upload final controller and cluster logs before exit")
				_ = utils.WaitForPredicate(WaitTimeout, LogsUploadPeriod, func() bool {
					err := c.uploadSummaryLogs(podName, c.Namespace, controllerLogsSecondsAgo, status.HasError())
					if err != nil {
						c.log.Infof("retry uploading logs in 5 minutes...")
					}
					return err == nil
				})
			}
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

			if status.HasError() {
				c.log.Infof("Error detected. Closing logs and aborting...")
				cancellog()
				continue
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

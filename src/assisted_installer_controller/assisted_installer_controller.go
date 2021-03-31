package assisted_installer_controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
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

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	mapiv1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
)

const (
	generalWaitTimeoutInt    = 30
	controllerLogsSecondsAgo = 120 * 60
)

var GeneralWaitInterval = generalWaitTimeoutInt * time.Second
var GeneralProgressUpdateInt = 60 * time.Second
var LogsUploadPeriod = 5 * time.Minute
var WaitTimeout = 70 * time.Minute

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
}

type controller struct {
	ControllerConfig
	log       *logrus.Logger
	ops       ops.Ops
	ic        inventory_client.InventoryClient
	kc        k8s_client.K8SClient
	cvoStatus OperatorStatus
}

type OperatorStatus struct {
	isAvailable bool
	message     string
}

func NewController(log *logrus.Logger, cfg ControllerConfig, ops ops.Ops, ic inventory_client.InventoryClient, kc k8s_client.K8SClient) *controller {
	return &controller{
		log:              log,
		ControllerConfig: cfg,
		ops:              ops,
		ic:               ic,
		kc:               kc,
		cvoStatus:        OperatorStatus{isAvailable: false, message: ""},
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

		hostsStatus := make(map[string]string)
		for hostname, hostData := range assistedInstallerNodesMap {
			hostsStatus[hostname] = *hostData.Host.Status
		}
		log.Infof("Host status: %v", hostsStatus)

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
			if common.IsK8sNodeIsReady(node) {
				log.Infof("Found new ready node %s with inventory id %s, kubernetes id %s, updating its status to %s",
					node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageDone)
				if err := c.ic.UpdateHostInstallProgress(ctx, host.Host.ID.String(), models.HostStageDone, ""); err != nil {
					log.WithError(err).Errorf("Failed to update node %s installation status", node.Name)
					continue
				}
			} else if host.Host.Progress.CurrentStage == models.HostStageConfiguring {
				log.Infof("Found new joined node %s with inventory id %s, kubernetes id %s, updating its status to %s",
					node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageJoined)
				if err := c.ic.UpdateHostInstallProgress(ctx, host.Host.ID.String(), models.HostStageJoined, ""); err != nil {
					log.WithError(err).Errorf("Failed to update node %s installation status", node.Name)
					continue
				}
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
		c.log.Infof("Waiting for cluster version operator")
		err = c.waitingForClusterVersion()
		if err != nil {
			return err
		}
	} else {
		c.log.Infof("Skipping waiting for cluster version operator")
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

	err = utils.WaitForPredicate(WaitTimeout, GeneralWaitInterval, c.validateConsoleAvailability)
	if err != nil {
		return errors.Errorf("Timeout while waiting for console to become available")
	}

	waitTimeout := c.getMaximumOLMTimeout()
	err = utils.WaitForPredicate(waitTimeout, GeneralWaitInterval, c.waitForOLMOperators)
	if err != nil {
		c.log.Warnf("Timeout while waiting for OLM operators be installed")
	}

	return nil
}

func (c controller) UpdateBMHs(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		time.Sleep(GeneralWaitInterval)
		bmhs, err := c.kc.ListBMHs()
		if err != nil {
			c.log.WithError(err).Errorf("Failed to list BMH hosts")
			continue
		}

		c.log.Infof("Number of BMHs is %d", len(bmhs.Items))

		machines, err := c.unallocatedMachines(bmhs)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to find unallocated machines")
			continue
		}

		c.log.Infof("Number of unallocated Machines is %d", len(machines.Items))

		allUpdated := c.updateBMHs(&bmhs, machines)
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

func (c controller) getMaximumOLMTimeout() time.Duration {

	operators, err := c.ic.GetClusterMonitoredOLMOperators(context.TODO(), c.ClusterID)
	if err != nil {
		c.log.WithError(err).Warningf("Failed to connect to assisted service")
		return WaitTimeout
	}

	timeout := WaitTimeout.Seconds()
	for _, operator := range operators {
		timeout = math.Max(float64(operator.TimeoutSeconds), timeout)
	}

	return time.Duration(timeout * float64(time.Second))
}

func (c controller) getProgressingOLMOperators() ([]models.MonitoredOperator, error) {
	ret := make([]models.MonitoredOperator, 0)
	operators, err := c.ic.GetClusterMonitoredOLMOperators(context.TODO(), c.ClusterID)
	if err != nil {
		c.log.WithError(err).Warningf("Failed to connect to assisted service")
		return ret, err
	}
	for _, operator := range operators {
		if operator.Status != models.OperatorStatusAvailable && operator.Status != models.OperatorStatusFailed {
			ret = append(ret, operator)
		}
	}
	return ret, nil
}

// waitForOLMOperators wait until all OLM monitored operators are available or failed.
func (c controller) waitForOLMOperators() bool {
	c.log.Infof("Checking OLM operators")
	operators, _ := c.getProgressingOLMOperators()
	if len(operators) == 0 {
		return true
	}
	for _, operator := range operators {
		csvName, err := c.kc.GetCSVFromSubscription(operator.Namespace, operator.SubscriptionName)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to get subscription of operator %s", operator.Name)
			continue
		}

		csv, err := c.kc.GetCSV(operator.Namespace, csvName)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to get %s", operator.Name)
			continue
		}

		operatorStatus := utils.CsvStatusToOperatorStatus(string(csv.Status.Phase))
		err = c.ic.UpdateClusterOperator(context.TODO(), c.ClusterID, operator.Name, operatorStatus, csv.Status.Message)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to update olm %s status", operator.Name)
			continue
		}

		c.log.Infof("CSV %s is in status %s, message %s.", operator.Name, csv.Status.Phase, csv.Status.Message)
	}
	return false
}

// validateConsoleAvailability checks if the console operator is available
func (c controller) validateConsoleAvailability() bool {
	c.log.Infof("Checking if console is available")
	co, err := c.kc.GetClusterOperator("console")
	if err != nil {
		c.log.WithError(err).Warn("Failed to get console operator")
		return false
	}

	operatorStatus, operatorMessage := utils.ClusterOperatorConditionsToMonitoredOperatorStatus(co.Status.Conditions)
	err = c.ic.UpdateClusterOperator(context.TODO(), c.ClusterID, "console", operatorStatus, operatorMessage)
	if err != nil {
		c.log.WithError(err).Warn("Failed to update console operator status")
		return false
	}

	if !c.checkOperatorStatusCondition(co, configv1.OperatorAvailable, configv1.ConditionTrue) ||
		!c.checkOperatorStatusCondition(co, configv1.OperatorDegraded, configv1.ConditionFalse) {
		return false
	}

	c.log.Info("Console operator is available")
	return true
}

func (c controller) waitingForClusterVersion() error {
	isClusterVersionAvailable := func() bool {
		newCvoStatus, conditions := c.validateClusterVersion()
		if c.cvoStatus.isAvailable != newCvoStatus.isAvailable || (c.cvoStatus.message != newCvoStatus.message && newCvoStatus.message != "") {
			status := fmt.Sprintf("Cluster version is available: %t , message: %s", newCvoStatus.isAvailable, newCvoStatus.message)
			c.log.Infof(status)

			// Update built-in monitored operator cluster version status
			operatorStatus, operatorMessage := utils.ClusterOperatorConditionsToMonitoredOperatorStatus(conditions)
			if err := c.ic.UpdateClusterOperator(utils.GenerateRequestContext(), c.ClusterID, "cvo", operatorStatus, operatorMessage); err != nil {
				c.log.WithError(err).Errorf("Failed to update cluster %s cvo status", c.ClusterID)
			}

			if err := c.ic.UpdateClusterInstallProgress(utils.GenerateRequestContext(), c.ClusterID, status); err != nil {
				c.log.WithError(err).Errorf("Failed to update cluster %s installation progress status", c.ClusterID)
			}
		}

		c.cvoStatus = newCvoStatus
		return newCvoStatus.isAvailable
	}

	err := utils.WaitForPredicate(WaitTimeout, GeneralProgressUpdateInt, isClusterVersionAvailable)
	if err != nil {
		return errors.Errorf("Timeout while waiting for cluster version to be available")
	}
	return nil
}

func (c controller) validateClusterVersion() (OperatorStatus, []configv1.ClusterOperatorStatusCondition) {
	c.log.Infof("Waiting for cluster version to be available")
	cv, err := c.kc.GetClusterVersion("version")
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get cluster version")
		return OperatorStatus{isAvailable: false, message: ""}, []configv1.ClusterOperatorStatusCondition{}
	}
	conditionsByType := map[configv1.ClusterStatusConditionType]configv1.ClusterOperatorStatusCondition{}
	// condition type to dict
	for _, condition := range cv.Status.Conditions {
		conditionsByType[condition.Type] = condition
	}
	// check if it cluster version is available
	condition, ok := conditionsByType[configv1.OperatorAvailable]
	if ok && condition.Status == configv1.ConditionTrue {
		return OperatorStatus{isAvailable: true, message: condition.Message}, cv.Status.Conditions
	}
	// if not available return message from progressing
	condition, ok = conditionsByType[configv1.OperatorProgressing]
	if ok {
		return OperatorStatus{isAvailable: false, message: condition.Message}, cv.Status.Conditions
	}

	return OperatorStatus{isAvailable: false, message: ""}, cv.Status.Conditions
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
 * It takes a linient approach so if some logs are not available it ignores them and moves on
 * currently the bundled logs are:
 * - controller logs
 * - oc must-gather logs
 **/
func (c controller) uploadSummaryLogs(podName string, namespace string, sinceSeconds int64, isMustGatherEnabled bool, mustGatherImg string) error {
	var tarentries = make([]utils.TarEntry, 0)
	var ok bool = true
	ctx := utils.GenerateRequestContext()

	c.logClusterOperatorsStatus()

	if isMustGatherEnabled {
		c.log.Infof("Uploading oc must-gather logs")
		if tarfile, err := c.collectMustGatherLogs(ctx, mustGatherImg); err == nil {
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

func (c controller) collectMustGatherLogs(ctx context.Context, mustGatherImg string) (string, error) {
	tempDir, ferr := ioutil.TempDir("", "controller-must-gather-logs-")
	if ferr != nil {
		c.log.Errorf("Failed to create temp directory for must-gather-logs %v\n", ferr)
		return "", ferr
	}

	//download kubeconfig file
	kubeconfigFileName := "kubeconfig-noingress"
	kubeconfigPath := path.Join(tempDir, kubeconfigFileName)
	err := c.ic.DownloadFile(ctx, kubeconfigFileName, kubeconfigPath)
	if err != nil {
		c.log.Errorf("Failed to download noingress kubeconfig %v\n", err)
		return "", err
	}

	//collect must gather logs
	logtar, err := c.ops.GetMustGatherLogs(tempDir, kubeconfigPath, mustGatherImg)
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
	podName := ""
	ticker := time.NewTicker(LogsUploadPeriod)
	progress_ctx := utils.GenerateRequestContext()
	c.log.Infof("Start sending logs")
	for {
		select {
		case <-ctx.Done():
			if podName != "" {
				c.log.Infof("Upload final controller and cluster logs before exit")
				c.ic.ClusterLogProgressReport(progress_ctx, c.ClusterID, models.LogsStateRequested)
				_ = utils.WaitForPredicate(WaitTimeout, LogsUploadPeriod, func() bool {
					err := c.uploadSummaryLogs(podName, c.Namespace, controllerLogsSecondsAgo, status.HasError(), c.MustGatherImage)
					if err != nil {
						c.log.Infof("retry uploading logs in 5 minutes...")
					}
					return err == nil
				})
			}
			c.ic.ClusterLogProgressReport(progress_ctx, c.ClusterID, models.LogsStateCompleted)
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

// checkOperatorStatusCondition checks if given operator has a condition with an expected status.
func (c controller) checkOperatorStatusCondition(co *configv1.ClusterOperator,
	conditionType configv1.ClusterStatusConditionType,
	status configv1.ConditionStatus) bool {
	for _, condition := range co.Status.Conditions {
		if condition.Type == conditionType {
			if condition.Status == status {
				return true
			}
			c.log.Warnf("Operator %s condition '%s' is not met due to '%s': %s",
				co.Name, conditionType, condition.Reason, condition.Message)
			return false
		}
	}
	c.log.Warnf("Operator %s condition '%s' does not exist", co.Name, conditionType)
	return false
}

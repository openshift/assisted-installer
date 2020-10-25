package assisted_installer_controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"

	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-service/models"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/api/certificates/v1beta1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	generalWaitTimeoutInt    = 30
	controllerLogsSecondsAgo = 120 * 60
)

var GeneralWaitInterval = generalWaitTimeoutInt * time.Second
var LogsUploadPeriod = 5 * time.Minute
var WaitTimeout = 2 * time.Hour

// assisted installer controller is added to control installation process after  bootstrap pivot
// assisted installer will deploy it on installation process
// as a first step it will wait till nodes are added to cluster and update their status to Done

type ControllerConfig struct {
	ClusterID            string `envconfig:"CLUSTER_ID" required:"true" `
	URL                  string `envconfig:"INVENTORY_URL" required:"true"`
	PullSecretToken      string `envconfig:"PULL_SECRET_TOKEN" required:"true"`
	SkipCertVerification bool   `envconfig:"SKIP_CERT_VERIFICATION" required:"false" default:"false"`
	CACertPath           string `envconfig:"CA_CERT_PATH" required:"false" default:""`
	Namespace            string `enconfig:"NAMESPACE" required:"false" default:"assisted-installer"`
}

type Controller interface {
	WaitAndUpdateNodesStatus()
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

func (c *controller) WaitAndUpdateNodesStatus() {
	c.log.Infof("Waiting till all nodes will join and update status to assisted installer")
	ignoreStatuses := []string{models.HostStatusDisabled,
		models.HostStatusError, models.HostStatusInstalled}
	for {
		time.Sleep(GeneralWaitInterval)
		assistedInstallerNodesMap, err := c.ic.GetHosts(ignoreStatuses)
		if err != nil {
			c.log.WithError(err).Error("Failed to get node map from inventory")
			continue
		}
		if len(assistedInstallerNodesMap) == 0 {
			break
		}
		c.log.Infof("Searching for host to change status, number to find %d", len(assistedInstallerNodesMap))
		nodes, err := c.kc.ListNodes()
		if err != nil {
			continue
		}
		for _, node := range nodes.Items {
			host, ok := assistedInstallerNodesMap[strings.ToLower(node.Name)]
			if !ok {
				c.log.Warnf("Node %s is not in inventory hosts", node.Name)
				continue
			}

			c.log.Infof("Found new joined node %s with inventory id %s, kubernetes id %s, updating its status to %s",
				node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageDone)
			if err := c.ic.UpdateHostInstallProgress(host.Host.ID.String(), models.HostStageDone, ""); err != nil {
				c.log.Errorf("Failed to update node %s installation status, %s", node.Name, err)
				continue
			}
		}
		c.updateConfiguringStatusIfNeeded(assistedInstallerNodesMap)

	}
	c.log.Infof("All nodes were found. WaitAndUpdateNodesStatus - Done")
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

func (c controller) PostInstallConfigs(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		time.Sleep(GeneralWaitInterval)
		cluster, err := c.ic.GetCluster()
		if err != nil {
			c.log.WithError(err).Errorf("Failed to get cluster %s from assisted-service", c.ClusterID)
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
	}
	success := err == nil
	c.sendCompleteInstallation(success, errMessage)
}

func (c controller) postInstallConfigs() error {

	err := utils.WaitForPredicate(WaitTimeout, GeneralWaitInterval, c.addRouterCAToClusterCA)
	if err != nil {
		return errors.Errorf("Timeout while waiting router ca data")
	}

	err = utils.WaitForPredicate(WaitTimeout, GeneralWaitInterval, c.unpatchEtcd)
	if err != nil {
		return errors.Errorf("Timeout while trying to unpatch etcd")
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

		allUpdated := c.updateBMHStatus(bmhs)
		if allUpdated {
			c.log.Infof("Updated all the BMH CRs, finished successfully")
			return
		}
	}
}

func (c controller) updateBMHStatus(bmhList metal3v1alpha1.BareMetalHostList) bool {
	allUpdated := true
	for i := range bmhList.Items {
		bmh := bmhList.Items[i]
		c.log.Infof("Checking bmh %s", bmh.Name)
		annotations := bmh.GetAnnotations()
		content := []byte(annotations[metal3v1alpha1.StatusAnnotation])
		if annotations[metal3v1alpha1.StatusAnnotation] == "" {
			c.log.Infof("Skipping setting status of BMH host %s, status annotation not present", bmh.Name)
			continue
		}
		allUpdated = false
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
		err = c.kc.UpdateBMH(&bmh)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to remove status annotation from BMH %s", bmh.Name)
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
	c.log.Infof("Start adding ingress ca to cluster")
	caConfigMap, err := c.kc.GetConfigMap(cmNamespace, cmName)

	if err != nil {
		c.log.WithError(err).Errorf("fetching %s configmap from %s namespace", cmName, cmNamespace)
		return false
	}

	c.log.Infof("Sending ingress certificate to inventory service. Certificate data %s", caConfigMap.Data["ca-bundle.crt"])
	err = c.ic.UploadIngressCa(caConfigMap.Data["ca-bundle.crt"], c.ClusterID)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to upload ingress ca to assisted-service")
		return false
	}
	c.log.Infof("Ingress ca successfully sent to inventory")
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

func (c controller) sendCompleteInstallation(isSuccess bool, errorInfo string) {
	c.log.Infof("Start complete installation step, with params success:%t, error info %s", isSuccess, errorInfo)
	for {
		if err := c.ic.CompleteInstallation(c.ClusterID, isSuccess, errorInfo); err != nil {
			c.log.Error(err)
			continue
		}
		break
	}
	c.log.Infof("Done complete installation step")
}

// get pod logs,
// write tar.gz to pipe in a routine
// upload tar.gz from pipe to assisted service.
// close read and write pipes
func (c controller) uploadPodLogs(podName string, namespace string, sinceSeconds int64) error {
	c.log.Infof("Uploading logs for %s in %s", podName, namespace)
	podLogs, err := c.kc.GetPodLogsAsBuffer(namespace, podName, sinceSeconds)
	if err != nil {
		return errors.Wrapf(err, "Failed to get logs of pod %s", podName)
	}
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		defer pw.Close()
		err = utils.WriteToTarGz(pw, podLogs, int64(podLogs.Len()), fmt.Sprintf("%s.logs", podName))
		if err != nil {
			c.log.WithError(err).Warnf("Failed to create tar.gz")
		}
	}()
	// if error will occur in goroutine above
	//it will close writer and upload will fail
	err = c.ic.UploadLogs(c.ClusterID, models.LogsTypeController, pr)
	if err != nil {
		return errors.Wrapf(err, "Failed to upload logs")
	}
	return nil
}

// Uploading logs every 5 minutes
// We will take logs of assisted controller and upload them to assisted-service
// by creating tar gz of them.
func (c *controller) UploadControllerLogs(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	c.log.Infof("Start sending logs")
	podName := ""
	ticker := time.NewTicker(LogsUploadPeriod)
	for {
		select {
		case <-ctx.Done():
			if podName != "" {
				c.log.Infof("Upload logs before exit")
				_ = c.uploadPodLogs(podName, c.Namespace, controllerLogsSecondsAgo)

			}
			c.log.Infof("Done uploading logs")
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
			err := common.UploadPodLogs(c.kc, c.ic, c.ClusterID, podName, c.Namespace, controllerLogsSecondsAgo, c.log)
			if err != nil {
				c.log.WithError(err).Warnf("Failed to upload controller logs")
				continue
			}
			c.log.Infof("Successfully uploaded controller logs")
		}
	}
}

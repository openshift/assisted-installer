package assisted_installer_controller

import (
	"sync"
	"time"

	"github.com/eranco74/assisted-installer/src/common"

	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/k8s_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/filanov/bm-inventory/models"

	"github.com/sirupsen/logrus"
	"k8s.io/api/certificates/v1beta1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
)

const (
	generalWaitTimeoutInt = 30
)

var GeneralWaitTimeout = generalWaitTimeoutInt * time.Second

// assisted installer controller is added to control installation process after  bootstrap pivot
// assisted installer will deploy it on installation process
// as a first step it will wait till nodes are added to cluster and update their status to Done

type ControllerConfig struct {
	ClusterID string `envconfig:"CLUSTER_ID" required:"true" `
	Host      string `envconfig:"INVENTORY_HOST" required:"true"`
	Port      int    `envconfig:"INVENTORY_PORT" required:"true"`
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
	assistedInstallerNodesMap := c.getInventoryNodesMap()
	for len(assistedInstallerNodesMap) > 0 {
		time.Sleep(GeneralWaitTimeout)
		c.log.Infof("Searching for host to change status")
		nodes, err := c.kc.ListNodes()
		if err != nil {
			continue
		}
		for _, node := range nodes.Items {
			host, ok := assistedInstallerNodesMap[node.Name]
			if !ok {
				continue
			}

			c.log.Infof("Found new joined node %s with inventory id %s, kuberentes id %s, updating its status to %s",
				node.Name, host.Host.ID.String(), node.Status.NodeInfo.SystemUUID, models.HostStageDone)
			if err := c.ic.UpdateHostInstallProgress(host.Host.ID.String(), models.HostStageDone, ""); err != nil {
				c.log.Errorf("Failed to update node %s installation status, %s", node.Name, err)
				continue
			}
			delete(assistedInstallerNodesMap, node.Name)
		}
		c.updateConfiguringStatusIfNeeded(assistedInstallerNodesMap)

	}
	c.log.Infof("All nodes were found. WaitAndUpdateNodesStatus - Done")
}

func (c *controller) getMCSLogs() (string, error) {
	logs := ""
	namespace := "openshift-machine-config-operator"
	pods, err := c.kc.GetPods(namespace, map[string]string{"k8s-app": "machine-config-server"})
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

func (c *controller) updateConfiguringStatusIfNeeded(hosts map[string]inventory_client.EnabledHostData) {
	logs, err := c.getMCSLogs()
	if err != nil {
		return
	}
	common.SetConfiguringStatusForHosts(c.ic, hosts, logs, true, c.log)
}

func (c *controller) getInventoryNodesMap() map[string]inventory_client.EnabledHostData {
	c.log.Infof("Getting map of inventory nodes")
	var assistedInstallerNodesMap map[string]inventory_client.EnabledHostData
	var err error
	for {
		assistedInstallerNodesMap, err = c.ic.GetEnabledHostsNamesHosts()
		if err != nil {
			c.log.Errorf("Failed to get node map from inventory, will retry in 30 seconds, %s", err)
			time.Sleep(GeneralWaitTimeout)
			continue
		}
		break
	}
	c.log.Infof("Got map of host from inventory, number of nodes %d", len(assistedInstallerNodesMap))
	return assistedInstallerNodesMap
}

func (c *controller) ApproveCsrs(done <-chan bool, wg *sync.WaitGroup) {
	defer wg.Done()
	c.log.Infof("Start approving csrs")
	ticker := time.NewTicker(GeneralWaitTimeout)
	for {
		select {
		case <-done:
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
	for _, csr := range csrs.Items {
		if !isCsrApproved(&csr) {
			c.log.Infof("Approving csr %s", csr.Name)
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
		time.Sleep(GeneralWaitTimeout)
		cluster, err := c.ic.GetCluster()
		if err != nil {
			c.log.WithError(err).Errorf("Failed to get cluster %s from bm-inventory", c.ClusterID)
			continue
		}
		// waiting till cluster will be installed(3 masters must be installed)
		if *cluster.Status != "installed" {
			continue
		}
		break
	}
	c.addRouterCAToClusterCA()
	c.unpatchEtcd()
}

func (c controller) unpatchEtcd() {
	c.log.Infof("Unpatching etcd")
	for {
		if err := c.kc.UnPatchEtcd(); err != nil {
			c.log.Error(err)
			continue
		}
		break
	}

}

// AddRouterCAToClusterCA adds router CA to cluster CA in kubeconfig
func (c controller) addRouterCAToClusterCA() {
	cmName := "default-ingress-cert"
	cmNamespace := "openshift-config-managed"
	c.log.Infof("Start adding ingress ca to cluster")
	for {
		caConfigMap, err := c.kc.GetConfigMap(cmNamespace, cmName)

		if err != nil {
			c.log.WithError(err).Errorf("fetching %s configmap from %s namespace", cmName, cmNamespace)
			continue
		}

		c.log.Infof("Sending ingress certificate to inventory service. Certificate data %s", caConfigMap.Data["ca-bundle.crt"])
		err = c.ic.UploadIngressCa(caConfigMap.Data["ca-bundle.crt"], c.ClusterID)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to upload ingress ca to bm-inventory")
			continue
		}
		c.log.Infof("Ingress ca successfully sent to inventory")
		return
	}
}

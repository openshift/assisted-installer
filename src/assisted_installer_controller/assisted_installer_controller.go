package assisted_installer_controller

import (
	"sync"
	"time"

	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/k8s_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
	"k8s.io/api/certificates/v1beta1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
)

const (
	done = "Done"
)

var GeneralWaitTimeout = 30 * time.Second

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
	assistedInstallerNodes := c.getInventoryNodes()
	numberOfNodesToWaitFor := len(assistedInstallerNodes)
	nodeUUIDAndName := make(map[string]string)
out:
	for {
		nodes, err := c.kc.ListNodes()
		if err != nil {
			continue
		}
		for _, node := range nodes.Items {
			if _, ok := nodeUUIDAndName[node.Status.NodeInfo.SystemUUID]; ok {
				continue
			}
			c.log.Infof("Found new joined node %s with id %s, updating its status to %s", node.Name, node.Status.NodeInfo.SystemUUID, done)
			if err := c.ic.UpdateHostStatus(done, node.Status.NodeInfo.SystemUUID); err != nil {
				c.log.Errorf("Failed to update node installation status, %s", err)
				continue out
			}
			nodeUUIDAndName[node.Status.NodeInfo.SystemUUID] = node.Name
			assistedInstallerNodes = utils.FindAndRemoveElementFromStringList(assistedInstallerNodes, node.Status.NodeInfo.SystemUUID)
		}
		c.log.Infof("Found %d nodes: %+v", len(nodes.Items), nodeUUIDAndName)
		if len(nodeUUIDAndName) >= numberOfNodesToWaitFor {
			c.log.Infof("All nodes were found. WaitAndUpdateNodesStatus - Done")
			break
		}
		c.log.Infof("Still waiting for %d nodes %v", len(assistedInstallerNodes), assistedInstallerNodes)
		time.Sleep(GeneralWaitTimeout)
	}
}

func (c *controller) getInventoryNodes() []string {
	c.log.Infof("Getting list of inventory nodes")
	var assistedInstallerNodes []string
	var err error
	for {
		assistedInstallerNodes, err = c.ic.GetHostsIds()
		if err != nil {
			c.log.Errorf("Failed to get node list from inventory, will retry in 30 seconds, %s", err)
			time.Sleep(GeneralWaitTimeout)
			continue
		}
		break
	}
	c.log.Infof("Got list of host from inventory %s", assistedInstallerNodes)
	return assistedInstallerNodes
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

// AddRouterCAToClusterCA adds router CA to cluster CA in kubeconfig
func (c controller) AddRouterCAToClusterCA(wg *sync.WaitGroup) {
	defer wg.Done()
	cmName := "default-ingress-cert"
	cmNamespace := "openshift-config-managed"
	c.log.Infof("Start adding ingress ca to cluster")
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
		break
	}
}

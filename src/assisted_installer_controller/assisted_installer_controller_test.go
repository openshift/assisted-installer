package assisted_installer_controller

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	"github.com/filanov/bm-inventory/models"

	"k8s.io/api/certificates/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/core/v1"

	"github.com/eranco74/assisted-installer/src/k8s_client"

	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

var _ = Describe("installer HostRoleMaster role", func() {
	var (
		l                 = logrus.New()
		ctrl              *gomock.Controller
		mockops           *ops.MockOps
		mockbmclient      *inventory_client.MockInventoryClient
		mockk8sclient     *k8s_client.MockK8SClient
		c                 *controller
		inventoryNamesIds map[string]string
		kubeNamesIds      map[string]string
		wg                sync.WaitGroup
	)
	inventoryNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
		"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238",
		"node2": "b898d516-3e16-49d0-86a5-0ad5bd04e3ed"}
	kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
		"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
		"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
	//hostIds = []string{"7916fa89-ea7a-443e-a862-b3e930309f65", "eb82821f-bf21-4614-9a3b-ecb07929f238", "b898d516-3e16-49d0-86a5-0ad5bd04e3ed"}
	l.SetOutput(ioutil.Discard)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
		inventoryNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
			"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238",
			"node2": "b898d516-3e16-49d0-86a5-0ad5bd04e3ed"}
		kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
			"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
			"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
		//hostIds = []string{"7916fa89-ea7a-443e-a862-b3e930309f65", "eb82821f-bf21-4614-9a3b-ecb07929f238", "b898d516-3e16-49d0-86a5-0ad5bd04e3ed"}
		GeneralWaitTimeout = 100 * time.Millisecond
	})

	getInventoryNodes := func() map[string]string {
		mockbmclient.EXPECT().GetEnabledHostsNamesIds().Return(inventoryNamesIds, nil).Times(1)
		return inventoryNamesIds
	}

	updateStatusSuccess := func(statuses []string, inventoryNamesIds map[string]string) {
		var hostIds []string
		for _, id := range inventoryNamesIds {
			hostIds = append(hostIds, id)
		}

		for i, status := range statuses {
			mockbmclient.EXPECT().UpdateHostStatus(status, hostIds[i]).Return(nil).Times(1)
		}
	}

	listNodes := func() {
		mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
	}

	Context("Waiting for 3 nodes", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			Host:      "https://bm-inventory.com",
			Port:      80,
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
		})
		It("WaitAndUpdateNodesStatus happy flow", func() {
			updateStatusSuccess([]string{done, done, done}, inventoryNamesIds)
			getInventoryNodes()
			listNodes()
			c.WaitAndUpdateNodesStatus()

		})
		AfterEach(func() {
			ctrl.Finish()
		})
	})
	Context("Waiting for 3 nodes, will appear one by one", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			Host:      "https://bm-inventory.com",
			Port:      80,
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			updateStatusSuccess = func(statuses []string, inventoryNamesIds map[string]string) {
				var hostIds []string
				for _, id := range inventoryNamesIds {
					hostIds = append(hostIds, id)
				}
				for i, status := range statuses {
					mockbmclient.EXPECT().UpdateHostStatus(status, hostIds[i]).Return(nil).Times(1)
				}
			}
			inventoryNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
				"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238",
				"node2": "b898d516-3e16-49d0-86a5-0ad5bd04e3ed"}
			kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
				"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
				"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
		})
		It("WaitAndUpdateNodesStatus one by one", func() {
			listNodes := func() {
				kubeNameIdsToReturn := make(map[string]string)
				for name, id := range kubeNamesIds {
					kubeNameIdsToReturn[name] = id
					mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNameIdsToReturn), nil).Times(1)
				}
			}

			updateStatusSuccess([]string{done, done, done}, inventoryNamesIds)
			getInventoryNodes()
			listNodes()
			c.WaitAndUpdateNodesStatus()

		})
		AfterEach(func() {
			ctrl.Finish()
		})
	})
	Context("UpdateStatusFails and then succeeds", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			Host:      "https://bm-inventory.com",
			Port:      80,
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
		})
		It("UpdateStatus fails and then succeeds", func() {
			updateStatusSuccessFailureTest := func(statuses []string, inventoryNamesIds map[string]string) {
				var hostIds []string
				for _, id := range inventoryNamesIds {
					hostIds = append(hostIds, id)
				}
				for i, status := range statuses {
					mockbmclient.EXPECT().UpdateHostStatus(status, hostIds[i]).Return(fmt.Errorf("dummy")).Times(1)
					mockbmclient.EXPECT().UpdateHostStatus(status, hostIds[i]).Return(nil).Times(1)
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(2)
			updateStatusSuccessFailureTest([]string{done, done, done}, inventoryNamesIds)
			getInventoryNodes()
			c.WaitAndUpdateNodesStatus()

		})
		AfterEach(func() {
			ctrl.Finish()
		})
	})
	Context("ListNodes fails and then succeeds", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			Host:      "https://bm-inventory.com",
			Port:      80,
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
		})
		It("ListNodes fails and then succeeds", func() {
			listNodes := func() {
				mockk8sclient.EXPECT().ListNodes().Return(nil, fmt.Errorf("dummy")).Times(1)
				mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			}
			updateStatusSuccess([]string{done, done, done}, inventoryNamesIds)
			getInventoryNodes()
			listNodes()
			c.WaitAndUpdateNodesStatus()

		})
		AfterEach(func() {
			ctrl.Finish()
		})
	})
	Context("validating getInventoryNodes", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			Host:      "https://bm-inventory.com",
			Port:      80,
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			GeneralWaitTimeout = 1 * time.Second
		})
		It("inventory client fails and return result only on second run", func() {
			mockbmclient.EXPECT().GetEnabledHostsNamesIds().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetEnabledHostsNamesIds().Return(inventoryNamesIds, nil).Times(1)
			nodesNamesIds := c.getInventoryNodesMap()
			Expect(nodesNamesIds).Should(Equal(inventoryNamesIds))
		})
		AfterEach(func() {
			ctrl.Finish()
		})
	})

	Context("validating ApproveCsrs", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			Host:      "https://bm-inventory.com",
			Port:      80,
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			GeneralWaitTimeout = 1 * time.Second
		})
		It("Run ApproveCsrs and validate it exists on channel set", func() {
			testList := v1beta1.CertificateSigningRequestList{}
			mockk8sclient.EXPECT().ListCsrs().Return(&testList, nil).MinTimes(2).MaxTimes(5)
			done := make(chan bool)
			wg.Add(1)
			go c.ApproveCsrs(done, &wg)
			time.Sleep(3 * time.Second)
			done <- true
			wg.Wait()
		})
		It("Run ApproveCsrs when list returns error", func() {
			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(5)
			done := make(chan bool)
			wg.Add(1)
			go c.ApproveCsrs(done, &wg)
			time.Sleep(3 * time.Second)
			done <- true
			wg.Wait()
		})
		It("Run ApproveCsrs with csrs list", func() {
			csr := v1beta1.CertificateSigningRequest{}
			csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1beta1.CertificateSigningRequestCondition{
				Type:           certificatesv1beta1.CertificateDenied,
				Reason:         "dummy",
				Message:        "dummy",
				LastUpdateTime: metav1.Now(),
			})
			csrApproved := v1beta1.CertificateSigningRequest{}
			csrApproved.Status.Conditions = append(csrApproved.Status.Conditions, certificatesv1beta1.CertificateSigningRequestCondition{
				Type:           certificatesv1beta1.CertificateApproved,
				Reason:         "dummy",
				Message:        "dummy",
				LastUpdateTime: metav1.Now(),
			})
			testList := v1beta1.CertificateSigningRequestList{}
			testList.Items = []v1beta1.CertificateSigningRequest{csr, csrApproved}
			mockk8sclient.EXPECT().ListCsrs().Return(&testList, nil).MinTimes(1)
			mockk8sclient.EXPECT().ApproveCsr(&csr).Return(nil).MinTimes(1)
			mockk8sclient.EXPECT().ApproveCsr(&csrApproved).Return(nil).Times(0)
			done := make(chan bool)
			wg.Add(1)
			go c.ApproveCsrs(done, &wg)
			time.Sleep(2 * time.Second)
			done <- true
		})

		AfterEach(func() {
			ctrl.Finish()
		})
	})

	Context("validating AddRouterCAToClusterCA", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			Host:      "https://bm-inventory.com",
			Port:      80,
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			GeneralWaitTimeout = 1 * time.Second
		})
		It("Run AddRouterCAToClusterCA", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			installed := "installed"
			cluster := models.Cluster{Status: &installed}
			mockbmclient.EXPECT().GetCluster().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster().Return(&cluster, nil).AnyTimes()
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(2)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(nil).Times(1)
			wg.Add(1)
			go c.AddRouterCAToClusterCA(&wg)
			wg.Wait()
		})

		AfterEach(func() {
			ctrl.Finish()
		})
	})
})

func GetKubeNodes(kubeNamesIds map[string]string) *v1.NodeList {
	file, _ := ioutil.ReadFile("../../test_files/node.json")
	var node v1.Node
	_ = json.Unmarshal(file, &node)
	nodeList := &v1.NodeList{}
	for name, id := range kubeNamesIds {
		node.Status.NodeInfo.SystemUUID = id
		node.Name = name
		nodeList.Items = append(nodeList.Items, node)
	}
	return nodeList
}

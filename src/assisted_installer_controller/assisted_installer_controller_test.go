package assisted_installer_controller

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"

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
		inventoryNamesIds map[string]inventory_client.EnabledHostData
		kubeNamesIds      map[string]string
		wg                sync.WaitGroup
		defaultStages     []models.HostStage
	)
	kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
		"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
		"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
	l.SetOutput(ioutil.Discard)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
		node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
		node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
		node2Id := strfmt.UUID("b898d516-3e16-49d0-86a5-0ad5bd04e3ed")
		currentState := models.HostProgress{CurrentStage: models.HostStageConfiguring}
		inventoryNamesIds = map[string]inventory_client.EnabledHostData{"node0": {Host: &models.Host{ID: &node0Id, Progress: &currentState}},
			"node1": {Host: &models.Host{ID: &node1Id, Progress: &currentState}},
			"node2": {Host: &models.Host{ID: &node2Id, Progress: &currentState}}}
		kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
			"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
			"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
		GeneralWaitTimeout = 100 * time.Millisecond

		defaultStages = []models.HostStage{models.HostStageDone,
			models.HostStageDone,
			models.HostStageDone}
	})

	getInventoryNodes := func() map[string]inventory_client.EnabledHostData {
		mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(inventoryNamesIds, nil).Times(1)
		return inventoryNamesIds
	}
	configuringSuccess := func() {
		mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any()).Return([]v1.Pod{}, nil).AnyTimes()
		mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	}

	updateProgressSuccess := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.EnabledHostData) {
		var hostIds []string
		for _, host := range inventoryNamesIds {
			hostIds = append(hostIds, host.Host.ID.String())
		}

		for i, stage := range stages {
			mockbmclient.EXPECT().UpdateHostInstallProgress(hostIds[i], stage, "").Return(nil).Times(1)
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
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			getInventoryNodes()
			configuringSuccess()
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

			updateProgressSuccess = func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.EnabledHostData) {
				var hostIds []string
				for _, host := range inventoryNamesIds {
					hostIds = append(hostIds, host.Host.ID.String())
				}
				for i, stage := range stages {
					mockbmclient.EXPECT().UpdateHostInstallProgress(hostIds[i], stage, "").Return(nil).Times(1)
				}
			}
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

			updateProgressSuccess(defaultStages, inventoryNamesIds)
			getInventoryNodes()
			listNodes()
			configuringSuccess()
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
			updateProgressSuccessFailureTest := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.EnabledHostData) {
				var hostIds []string
				for _, host := range inventoryNamesIds {
					hostIds = append(hostIds, host.Host.ID.String())
				}
				for i, stage := range stages {
					mockbmclient.EXPECT().UpdateHostInstallProgress(hostIds[i], stage, "").Return(fmt.Errorf("dummy")).Times(1)
					mockbmclient.EXPECT().UpdateHostInstallProgress(hostIds[i], stage, "").Return(nil).Times(1)
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(2)
			updateProgressSuccessFailureTest(defaultStages, inventoryNamesIds)
			getInventoryNodes()
			configuringSuccess()
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
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			getInventoryNodes()
			listNodes()
			configuringSuccess()
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
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(inventoryNamesIds, nil).Times(1)
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
		It("Run addRouterCAToClusterCA", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(2)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(nil).Times(1)
			c.addRouterCAToClusterCA()
		})
		It("Run PostInstallConfigs", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			consoleNamespace := "openshift-console"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			finalizing := models.ClusterStatusFinalizing
			installing := models.ClusterStatusInstalling
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster().Return(&models.Cluster{Status: &installing}, nil).Times(1)
			mockbmclient.EXPECT().GetCluster().Return(&cluster, nil).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(nil).Times(1)
			mockk8sclient.EXPECT().UnPatchEtcd().Return(fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().UnPatchEtcd().Return(nil).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any()).Return([]v1.Pod{{Status: v1.PodStatus{Phase: "Pending"}}}, nil).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any()).Return([]v1.Pod{{Status: v1.PodStatus{Phase: "Running"}}}, nil).Times(1)
			mockbmclient.EXPECT().CompleteInstallation("cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().CompleteInstallation("cluster-id", true, "").Return(nil).Times(1)

			wg.Add(1)
			go c.PostInstallConfigs(&wg)
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

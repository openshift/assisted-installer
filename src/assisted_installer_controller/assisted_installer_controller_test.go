package assisted_installer_controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/go-openapi/strfmt"

	"github.com/openshift/assisted-service/models"

	"k8s.io/api/certificates/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/core/v1"

	"github.com/openshift/assisted-installer/src/k8s_client"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/ops"
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
		inventoryNamesIds map[string]inventory_client.HostData
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
		currentState := models.HostProgressInfo{CurrentStage: models.HostStageConfiguring}
		inventoryNamesIds = map[string]inventory_client.HostData{"node0": {Host: &models.Host{ID: &node0Id, Progress: &currentState}},
			"node1": {Host: &models.Host{ID: &node1Id, Progress: &currentState}},
			"node2": {Host: &models.Host{ID: &node2Id, Progress: &currentState}}}
		kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
			"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
			"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
		GeneralWaitInterval = 100 * time.Millisecond

		defaultStages = []models.HostStage{models.HostStageDone,
			models.HostStageDone,
			models.HostStageDone}
	})
	AfterEach(func() {
		ctrl.Finish()
	})

	getInventoryNodes := func(numOfFullListReturn int) map[string]inventory_client.HostData {
		for i := 0; i < numOfFullListReturn; i++ {
			mockbmclient.EXPECT().GetHosts([]string{models.HostStatusDisabled,
				models.HostStatusError, models.HostStatusInstalled}).Return(inventoryNamesIds, nil).Times(1)
		}
		mockbmclient.EXPECT().GetHosts([]string{models.HostStatusDisabled,
			models.HostStatusError, models.HostStatusInstalled}).Return(map[string]inventory_client.HostData{}, nil).Times(1)
		return inventoryNamesIds
	}
	configuringSuccess := func() {
		mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()
		mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	}

	updateProgressSuccess := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
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
			URL:       "https://assisted-service.com:80",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
		})
		It("WaitAndUpdateNodesStatus happy flow", func() {
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			getInventoryNodes(1)
			configuringSuccess()
			listNodes()
			c.WaitAndUpdateNodesStatus()
		})

		It("WaitAndUpdateNodesStatus getHost failure once", func() {
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			configuringSuccess()
			listNodes()

			mockbmclient.EXPECT().GetHosts([]string{models.HostStatusDisabled,
				models.HostStatusError, models.HostStatusInstalled}).Return(map[string]inventory_client.HostData{}, fmt.Errorf("dummy")).Times(1)
			getInventoryNodes(1)

			c.WaitAndUpdateNodesStatus()
		})
	})
	Context("Waiting for 3 nodes, will appear one by one", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			URL:       "https://assisted-service.com:80",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)

			updateProgressSuccess = func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
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
					targetMap := make(map[string]inventory_client.HostData)
					// Copy from the original map to the target map
					for key, value := range inventoryNamesIds {
						targetMap[key] = value
					}
					mockbmclient.EXPECT().GetHosts([]string{models.HostStatusDisabled,
						models.HostStatusError, models.HostStatusInstalled}).Return(targetMap, nil).Times(1)
					delete(inventoryNamesIds, name)
				}
				mockbmclient.EXPECT().GetHosts([]string{models.HostStatusDisabled,
					models.HostStatusError, models.HostStatusInstalled}).Return(inventoryNamesIds, nil).Times(1)
			}

			updateProgressSuccess(defaultStages, inventoryNamesIds)
			listNodes()
			configuringSuccess()
			c.WaitAndUpdateNodesStatus()

		})
	})
	Context("UpdateStatusFails and then succeeds", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			URL:       "https://assisted-service.com:80",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
		})
		It("UpdateStatus fails and then succeeds", func() {
			updateProgressSuccessFailureTest := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
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
			getInventoryNodes(2)
			configuringSuccess()
			c.WaitAndUpdateNodesStatus()

		})
	})
	Context("ListNodes fails and then succeeds", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			URL:       "https://assisted-service.com:80",
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
			getInventoryNodes(2)
			listNodes()
			configuringSuccess()
			c.WaitAndUpdateNodesStatus()

		})
	})
	Context("validating ApproveCsrs", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			URL:       "https://assisted-service.com:80",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			GeneralWaitInterval = 1 * time.Second
		})
		It("Run ApproveCsrs and validate it exists on channel set", func() {
			testList := v1beta1.CertificateSigningRequestList{}
			mockk8sclient.EXPECT().ListCsrs().Return(&testList, nil).MinTimes(2).MaxTimes(5)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go c.ApproveCsrs(ctx, &wg)
			time.Sleep(3 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Run ApproveCsrs when list returns error", func() {
			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(5)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go c.ApproveCsrs(ctx, &wg)
			time.Sleep(3 * time.Second)
			cancel()
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
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go c.ApproveCsrs(ctx, &wg)
			time.Sleep(2 * time.Second)
			cancel()
		})
	})

	Context("validating AddRouterCAToClusterCA", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			URL:       "https://assisted-service.com:80",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			GeneralWaitInterval = 1 * time.Second
		})
		It("Run addRouterCAToClusterCA happy flow", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(nil).Times(1)
			res := c.addRouterCAToClusterCA()
			Expect(res).Should(Equal(true))
		})
		It("Run addRouterCAToClusterCA Config map failed", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(nil, fmt.Errorf("dummy")).Times(1)
			res := c.addRouterCAToClusterCA()
			Expect(res).Should(Equal(false))
		})
		It("Run addRouterCAToClusterCA UploadIngressCa failed", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(fmt.Errorf("dummy")).Times(1)
			res := c.addRouterCAToClusterCA()
			Expect(res).Should(Equal(false))
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
			badClusterVersion := &configv1.ClusterVersion{}
			badClusterVersion.Status.Conditions = []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status: configv1.ConditionFalse}}
			goodClusterVersion := &configv1.ClusterVersion{}
			goodClusterVersion.Status.Conditions = []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status: configv1.ConditionTrue}}
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster().Return(&models.Cluster{Status: &installing}, nil).Times(1)
			mockbmclient.EXPECT().GetCluster().Return(&cluster, nil).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(data["ca-bundle.crt"], c.ClusterID).Return(nil).Times(1)
			mockk8sclient.EXPECT().UnPatchEtcd().Return(fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().UnPatchEtcd().Return(nil).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any(), "").Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any(), "").Return([]v1.Pod{{Status: v1.PodStatus{Phase: "Pending"}}}, nil).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any(), "").Return([]v1.Pod{{Status: v1.PodStatus{Phase: "Running"}}}, nil).Times(1)

			mockbmclient.EXPECT().CompleteInstallation("cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().CompleteInstallation("cluster-id", true, "").Return(nil).Times(1)

			wg.Add(1)
			go c.PostInstallConfigs(&wg)
			wg.Wait()
		})
		It("Run PostInstallConfigs failed", func() {
			WaitTimeout = 2 * time.Second
			finalizing := models.ClusterStatusFinalizing
			badClusterVersion := &configv1.ClusterVersion{}
			badClusterVersion.Status.Conditions = []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status: configv1.ConditionFalse}}
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster().Return(&cluster, nil).Times(1)

			mockk8sclient.EXPECT().GetConfigMap(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("aaa")).MinTimes(1)
			mockbmclient.EXPECT().CompleteInstallation("cluster-id", false,
				"Timeout while waiting router ca data").Return(nil).Times(1)

			wg.Add(1)
			go c.PostInstallConfigs(&wg)
			wg.Wait()
		})
	})
	Context("Upload logs", func() {
		conf := ControllerConfig{
			ClusterID: "cluster-id",
			URL:       "https://assisted-service.com:80",
			Namespace: "assisted-installer",
		}
		var pod v1.Pod
		BeforeEach(func() {
			LogsUploadPeriod = 100 * time.Millisecond
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			pod = v1.Pod{TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: v1.PodSpec{}, Status: v1.PodStatus{Phase: "Pending"}}
		})
		It("Validate upload logs, get pod fails", func() {
			mockk8sclient.EXPECT().GetPods(conf.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(10)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go c.UploadControllerLogs(ctx, &wg)
			time.Sleep(1 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs, Get pods logs failed", func() {
			mockk8sclient.EXPECT().GetPods(conf.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).MinTimes(1)
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(conf.Namespace, "test", gomock.Any()).Return(nil, fmt.Errorf("dummy")).MinTimes(1)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go c.UploadControllerLogs(ctx, &wg)
			time.Sleep(500 * time.Millisecond)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs, Upload failed", func() {
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(conf.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(conf.ClusterID, models.LogsTypeController, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			err := c.uploadPodLogs("test", conf.Namespace, controllerLogsSecondsAgo)
			Expect(err).To(HaveOccurred())
		})
		It("Validate upload logs happy flow", func() {
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(conf.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(conf.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).Times(1)
			err := c.uploadPodLogs("test", conf.Namespace, controllerLogsSecondsAgo)
			Expect(err).NotTo(HaveOccurred())
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

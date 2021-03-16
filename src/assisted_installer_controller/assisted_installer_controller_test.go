package assisted_installer_controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	"github.com/openshift/assisted-installer/src/common"

	"github.com/go-openapi/strfmt"
	"github.com/golang/mock/gomock"
	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-service/models"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

var (
	defaultTestControllerConf = ControllerConfig{
		ClusterID:             "cluster-id",
		URL:                   "https://assisted-service.com:80",
		OpenshiftVersion:      "4.7",
		WaitForClusterVersion: false,
		Namespace:             "assisted-installer",
		MustGatherImage:       "quay.io/test-must-gather:latest",
	}

	badClusterVersion = &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status: configv1.ConditionFalse}},
		},
	}

	goodClusterVersion = &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status: configv1.ConditionTrue}},
		},
	}

	validConsoleOperator = getClusterOperatorWithConditionsStatus(configv1.ConditionTrue, configv1.ConditionFalse)
)

var _ = Describe("installer HostRoleMaster role", func() {
	var (
		l                  = logrus.New()
		ctrl               *gomock.Controller
		mockops            *ops.MockOps
		mockbmclient       *inventory_client.MockInventoryClient
		mockk8sclient      *k8s_client.MockK8SClient
		assistedController *controller
		inventoryNamesIds  map[string]inventory_client.HostData
		kubeNamesIds       map[string]string
		wg                 sync.WaitGroup
		status             *ControllerStatus
		defaultStages      []models.HostStage
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
		currentStatus := models.HostStatusInstallingInProgress
		inventoryNamesIds = map[string]inventory_client.HostData{
			"node0": {Host: &models.Host{ID: &node0Id, Progress: &currentState, Status: &currentStatus}},
			"node1": {Host: &models.Host{ID: &node1Id, Progress: &currentState, Status: &currentStatus}},
			"node2": {Host: &models.Host{ID: &node2Id, Progress: &currentState, Status: &currentStatus}}}
		kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
			"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
			"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
		GeneralWaitInterval = 100 * time.Millisecond
		GeneralProgressUpdateInt = 100 * time.Millisecond

		defaultStages = []models.HostStage{models.HostStageDone,
			models.HostStageDone,
			models.HostStageDone}

		assistedController = NewController(l, defaultTestControllerConf, mockops, mockbmclient, mockk8sclient)
		status = &ControllerStatus{}
	})
	AfterEach(func() {
		ctrl.Finish()
	})

	getInventoryNodes := func(numOfFullListReturn int) map[string]inventory_client.HostData {
		for i := 0; i < numOfFullListReturn; i++ {
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
				models.HostStatusInstalled}).Return(inventoryNamesIds, nil).Times(1)
		}
		mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
			models.HostStatusInstalled}).Return(map[string]inventory_client.HostData{}, nil).Times(1)
		return inventoryNamesIds
	}
	configuringSuccess := func() {
		mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()
		mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), gomock.Any(), models.HostStageConfiguring, gomock.Any()).AnyTimes()
	}

	updateProgressSuccess := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
		var hostIds []string
		for _, host := range inventoryNamesIds {
			hostIds = append(hostIds, host.Host.ID.String())
		}

		for i, stage := range stages {
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), hostIds[i], stage, "").Return(nil).Times(1)
		}
	}

	listNodes := func() {
		mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
	}

	logClusterOperatorsSuccess := func() {
		operators := configv1.ClusterOperatorList{}
		operators.Items = []configv1.ClusterOperator{{Status: configv1.ClusterOperatorStatus{Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
			Status: configv1.ConditionFalse}}}}}
		mockk8sclient.EXPECT().ListClusterOperators().Return(&operators, nil).AnyTimes()
	}

	reportLogProgressSuccess := func() {
		mockbmclient.EXPECT().ClusterLogProgressReport(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	}

	Context("Waiting for 3 nodes", func() {
		It("Set ready event", func() {
			// fail to connect to assisted and then succeed
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(nil, nil).Times(3)

			// fail to connect to ocp and then succeed
			mockk8sclient.EXPECT().ListNodes().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().ListNodes().Return(nil, nil).Times(2)

			// fail to create event and then succeed
			mockk8sclient.EXPECT().CreateEvent(assistedController.Namespace, common.AssistedControllerIsReadyEvent, gomock.Any(), common.AssistedControllerPrefix).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().CreateEvent(assistedController.Namespace, common.AssistedControllerIsReadyEvent, gomock.Any(), common.AssistedControllerPrefix).Return(nil, nil).Times(1)

			assistedController.SetReadyState()
			Expect(status.HasError()).Should(Equal(false))
		})

		It("WaitAndUpdateNodesStatus happy flow", func() {
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			getInventoryNodes(1)
			configuringSuccess()
			listNodes()
			assistedController.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})

		It("WaitAndUpdateNodesStatus including joined state", func() {
			joined := []models.HostStage{models.HostStageJoined,
				models.HostStageJoined,
				models.HostStageJoined}

			getInventoryNodes(2)
			//updateProgressSuccess(defaultStages, inventoryNamesIds)
			// not ready nodes
			nodes := GetKubeNodes(kubeNamesIds)
			for _, node := range nodes.Items {
				for i, cond := range node.Status.Conditions {
					if cond.Type == v1.NodeReady {
						node.Status.Conditions[i].Status = v1.ConditionFalse
					}
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(nodes, nil).Times(1)
			updateProgressSuccess(joined, inventoryNamesIds)
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			configuringSuccess()
			listNodes()

			assistedController.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})

		It("WaitAndUpdateNodesStatus getHost failure once", func() {
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			configuringSuccess()
			listNodes()

			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
				models.HostStatusInstalled}).Return(map[string]inventory_client.HostData{}, fmt.Errorf("dummy")).Times(1)
			getInventoryNodes(1)

			assistedController.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})

		It("All hosts move to error state", func() {
			getInventoryNodesInError := func() {
				errorStatus := models.HostStatusError
				for _, host := range inventoryNamesIds {
					host.Host.Status = &errorStatus
				}
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
					models.HostStatusInstalled}).Return(inventoryNamesIds, nil).Times(1)
			}
			getInventoryNodesInError()
			assistedController.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(true))
		})
	})
	Context("Waiting for 3 nodes, will appear one by one", func() {
		BeforeEach(func() {
			updateProgressSuccess = func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
				var hostIds []string
				for _, host := range inventoryNamesIds {
					hostIds = append(hostIds, host.Host.ID.String())
				}
				for i, stage := range stages {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), hostIds[i], stage, "").Return(nil).Times(1)
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
					mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
						models.HostStatusInstalled}).Return(targetMap, nil).Times(1)
					delete(inventoryNamesIds, name)
				}
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
					models.HostStatusInstalled}).Return(inventoryNamesIds, nil).Times(1)
			}

			updateProgressSuccess(defaultStages, inventoryNamesIds)
			listNodes()
			configuringSuccess()
			assistedController.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})
	})
	Context("UpdateStatusFails and then succeeds", func() {
		It("UpdateStatus fails and then succeeds", func() {
			updateProgressSuccessFailureTest := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
				var hostIds []string
				for _, host := range inventoryNamesIds {
					hostIds = append(hostIds, host.Host.ID.String())
				}
				for i, stage := range stages {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), hostIds[i], stage, "").Return(fmt.Errorf("dummy")).Times(1)
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), hostIds[i], stage, "").Return(nil).Times(1)
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(2)
			updateProgressSuccessFailureTest(defaultStages, inventoryNamesIds)
			getInventoryNodes(2)
			configuringSuccess()
			assistedController.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})
	})
	Context("ListNodes fails and then succeeds", func() {
		It("ListNodes fails and then succeeds", func() {
			listNodes := func() {
				mockk8sclient.EXPECT().ListNodes().Return(nil, fmt.Errorf("dummy")).Times(1)
				mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			}
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			getInventoryNodes(2)
			listNodes()
			configuringSuccess()
			assistedController.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})
	})
	Context("validating ApproveCsrs", func() {
		BeforeEach(func() {
			GeneralWaitInterval = 1 * time.Second
		})
		It("Run ApproveCsrs and validate it exists on channel set", func() {
			testList := certificatesv1.CertificateSigningRequestList{}
			mockk8sclient.EXPECT().ListCsrs().Return(&testList, nil).MinTimes(2).MaxTimes(5)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.ApproveCsrs(ctx, &wg)
			time.Sleep(3 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Run ApproveCsrs when list returns error", func() {
			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(5)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.ApproveCsrs(ctx, &wg)
			time.Sleep(3 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Run ApproveCsrs with csrs list", func() {
			csr := certificatesv1.CertificateSigningRequest{}
			csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
				Type:           certificatesv1.CertificateDenied,
				Reason:         "dummy",
				Message:        "dummy",
				LastUpdateTime: metav1.Now(),
			})
			csrApproved := certificatesv1.CertificateSigningRequest{}
			csrApproved.Status.Conditions = append(csrApproved.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
				Type:           certificatesv1.CertificateApproved,
				Reason:         "dummy",
				Message:        "dummy",
				LastUpdateTime: metav1.Now(),
			})
			testList := certificatesv1.CertificateSigningRequestList{}
			testList.Items = []certificatesv1.CertificateSigningRequest{csr, csrApproved}
			mockk8sclient.EXPECT().ListCsrs().Return(&testList, nil).MinTimes(1)
			mockk8sclient.EXPECT().ApproveCsr(&csr).Return(nil).MinTimes(1)
			mockk8sclient.EXPECT().ApproveCsr(&csrApproved).Return(nil).Times(0)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.ApproveCsrs(ctx, &wg)
			time.Sleep(2 * time.Second)
			cancel()
		})
	})

	Context("validating AddRouterCAToClusterCA", func() {
		BeforeEach(func() {
			assistedController.WaitForClusterVersion = true
			GeneralWaitInterval = 1 * time.Second
		})
		It("Run addRouterCAToClusterCA happy flow", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], assistedController.ClusterID).Return(nil).Times(1)
			res := assistedController.addRouterCAToClusterCA()
			Expect(res).Should(Equal(true))
		})
		It("Run addRouterCAToClusterCA Config map failed", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(nil, fmt.Errorf("dummy")).Times(1)
			res := assistedController.addRouterCAToClusterCA()
			Expect(res).Should(Equal(false))
		})
		It("Run addRouterCAToClusterCA UploadIngressCa failed", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], assistedController.ClusterID).Return(fmt.Errorf("dummy")).Times(1)
			res := assistedController.addRouterCAToClusterCA()
			Expect(res).Should(Equal(false))
		})
		It("Run PostInstallConfigs - wait for cluster version", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			consoleOperatorName := "console"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			finalizing := models.ClusterStatusFinalizing
			installing := models.ClusterStatusInstalling
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&models.Cluster{Status: &installing}, nil).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&cluster, nil).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], assistedController.ClusterID).Return(nil).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(nil, fmt.Errorf("no-operator")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				&configv1.ClusterOperator{
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{},
					},
				}, fmt.Errorf("no-conditions")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				getClusterOperatorWithCondition(configv1.OperatorDegraded, configv1.ConditionFalse),
				fmt.Errorf("false-degraded-condition")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				getClusterOperatorWithCondition(configv1.OperatorAvailable, configv1.ConditionTrue),
				fmt.Errorf("missing-degraded-condition")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				getClusterOperatorWithCondition(configv1.OperatorAvailable, configv1.ConditionFalse),
				fmt.Errorf("false-available-condition")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				getClusterOperatorWithCondition(configv1.OperatorAvailable, configv1.ConditionTrue),
				fmt.Errorf("true-degraded-condition")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				&configv1.ClusterOperator{
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
						},
					},
				}, fmt.Errorf("missing-conditions")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				getClusterOperatorWithConditionsStatus(configv1.ConditionTrue, configv1.ConditionTrue),
				fmt.Errorf("bad-conditions-status")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				getClusterOperatorWithConditionsStatus(configv1.ConditionFalse, configv1.ConditionTrue),
				fmt.Errorf("bad-conditions-status")).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
				getClusterOperatorWithConditionsStatus(configv1.ConditionFalse, configv1.ConditionFalse),
				fmt.Errorf("bad-conditions-status")).Times(1)
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(validConsoleOperator, nil).Times(1)
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return([]models.MonitoredOperator{}, nil).Times(2)
			mockk8sclient.EXPECT().GetClusterVersion("version").Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().GetClusterVersion("version").Return(badClusterVersion, nil).Times(1)
			mockk8sclient.EXPECT().GetClusterVersion("version").Return(goodClusterVersion, nil).Times(1)
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateClusterInstallProgress(gomock.Any(), gomock.Any(), gomock.Any()).MinTimes(1)

			wg.Add(1)
			go assistedController.PostInstallConfigs(&wg, status)
			wg.Wait()

			Expect(status.HasError()).Should(Equal(false))
		})
		It("Run PostInstallConfigs failed - wait for cluster version", func() {
			WaitTimeout = 2 * time.Second
			GeneralProgressUpdateInt = 3 * time.Second
			finalizing := models.ClusterStatusFinalizing
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&cluster, nil).Times(1)

			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", false, "Timeout while waiting for cluster "+
				"version to be available").Return(nil).Times(1)

			wg.Add(1)
			go assistedController.PostInstallConfigs(&wg, status)
			wg.Wait()
			Expect(status.HasError()).Should(Equal(true))
		})
	})

	Context("PostInstallConfigs - not waiting for cluster version ", func() {
		BeforeEach(func() {
			assistedController.WaitForClusterVersion = false
			GeneralWaitInterval = 1 * time.Second
		})
		It("Run PostInstallConfigs - not waiting for cluster version", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			consoleOperatorName := "console"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			finalizing := models.ClusterStatusFinalizing
			installing := models.ClusterStatusInstalling
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&models.Cluster{Status: &installing}, nil).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&cluster, nil).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], assistedController.ClusterID).Return(nil).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(validConsoleOperator, nil).Times(1)
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return([]models.MonitoredOperator{}, nil).AnyTimes()
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)

			wg.Add(1)
			assistedController.PostInstallConfigs(&wg, status)
			wg.Wait()
			Expect(status.HasError()).Should(Equal(false))
		})
		It("Run PostInstallConfigs failed - not waiting for cluster version", func() {
			WaitTimeout = 2 * time.Second
			finalizing := models.ClusterStatusFinalizing
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&cluster, nil).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("aaa")).MinTimes(1)
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", false,
				"Timeout while waiting router ca data").Return(nil).Times(1)

			wg.Add(1)
			go assistedController.PostInstallConfigs(&wg, status)
			wg.Wait()
			Expect(status.HasError()).Should(Equal(true))
		})
		It("Run PostInstallConfigs - waiting for single OLM operator", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			consoleOperatorName := "console"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			finalizing := models.ClusterStatusFinalizing
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&cluster, nil).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], assistedController.ClusterID).Return(nil).Times(1)
			mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(validConsoleOperator, nil).Times(1)
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: "", TimeoutSeconds: 120 * 60}}, nil,
			).Times(1)
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusProgressing, TimeoutSeconds: 120 * 60}}, nil,
			).Times(1)
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusAvailable, TimeoutSeconds: 120 * 60}}, nil,
			).Times(1)
			mockk8sclient.EXPECT().GetCSVFromSubscription("openshift-local-storage", "local-storage-operator").Return("lso-1.1", nil).Times(1)
			mockk8sclient.EXPECT().GetCSV("openshift-local-storage", "lso-1.1").Return(&olmv1alpha1.ClusterServiceVersion{}, nil).Times(1)
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)

			wg.Add(1)
			assistedController.PostInstallConfigs(&wg, status)
			wg.Wait()
			Expect(status.HasError()).Should(Equal(false))
		})
	})

	Context("update BMHs", func() {
		t := metav1.Unix(98754, 0)
		bmhStatus := metal3v1alpha1.BareMetalHostStatus{
			LastUpdated: &t,
			HardwareDetails: &metal3v1alpha1.HardwareDetails{
				Hostname: "openshift-worker-0",
			},
		}
		annBytes, _ := json.Marshal(&bmhStatus)

		bmhList := metal3v1alpha1.BareMetalHostList{
			Items: []metal3v1alpha1.BareMetalHost{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "openshift-worker-0",
						Annotations: map[string]string{
							metal3v1alpha1.StatusAnnotation: string(annBytes),
						},
					},
				},
			},
		}
		machineList := machinev1beta1.MachineList{
			Items: []machinev1beta1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "xyz-assisted-instal-8p7km-worker-0-25rnh",
						Namespace: "openshift-machine-api",
						Labels: map[string]string{
							"machine.openshift.io/cluster-api-machine-role": "worker",
						},
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       "Machine",
						APIVersion: "metal3.io/v1alpha1",
					},
				},
			},
		}
		BeforeEach(func() {
			GeneralWaitInterval = 1 * time.Second
		})
		It("worker machine does not exists", func() {
			emptyMachineList := &machinev1beta1.MachineList{Items: machineList.Items[:0]}
			expect1 := &metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-worker-0",
					Annotations: map[string]string{
						metal3v1alpha1.StatusAnnotation: string(annBytes),
					},
				},
				Status: bmhStatus,
			}
			mockk8sclient.EXPECT().IsMetalProvisioningExists().Return(false, nil)
			mockk8sclient.EXPECT().UpdateBMHStatus(expect1).Return(nil)
			bmhListTemp := &metal3v1alpha1.BareMetalHostList{
				Items: []metal3v1alpha1.BareMetalHost{
					*(bmhList.Items[0].DeepCopy()),
				},
			}
			mockk8sclient.EXPECT().GetBMH(expect1.Name).Return(expect1, nil)
			assistedController.updateBMHs(bmhListTemp, emptyMachineList)
		})
		It("no MetalProvisioning", func() {
			expect1 := &metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-worker-0",
					Annotations: map[string]string{
						metal3v1alpha1.StatusAnnotation: string(annBytes),
					},
				},
				Status: bmhStatus,
			}

			mockk8sclient.EXPECT().IsMetalProvisioningExists().Return(false, nil)
			mockk8sclient.EXPECT().UpdateBMHStatus(expect1).Return(nil)
			mockk8sclient.EXPECT().GetBMH(expect1.Name).Return(expect1, nil)

			expect2 := expect1.DeepCopy()
			expect2.Spec = metal3v1alpha1.BareMetalHostSpec{
				ConsumerRef: &v1.ObjectReference{
					APIVersion: "metal3.io/v1alpha1",
					Kind:       "Machine",
					Namespace:  "openshift-machine-api",
					Name:       "xyz-assisted-instal-8p7km-worker-0-25rnh",
				},
			}
			expect2.ObjectMeta.Annotations = map[string]string{}
			mockk8sclient.EXPECT().UpdateBMH(expect2).Return(nil)
			assistedController.updateBMHs(bmhList.DeepCopy(), machineList.DeepCopy())
		})
		It("has MetalProvisioning", func() {
			bmhListWithPause := bmhList.DeepCopy()
			bmhListWithPause.Items[0].Annotations[metal3v1alpha1.PausedAnnotation] = ""
			expect1 := &metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-worker-0",
					Annotations: map[string]string{
						metal3v1alpha1.StatusAnnotation: string(annBytes),
					},
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					ExternallyProvisioned: true,
					ConsumerRef: &v1.ObjectReference{
						APIVersion: "metal3.io/v1alpha1",
						Kind:       "Machine",
						Namespace:  "openshift-machine-api",
						Name:       "xyz-assisted-instal-8p7km-worker-0-25rnh",
					},
				},
			}

			mockk8sclient.EXPECT().IsMetalProvisioningExists().Return(true, nil)
			mockk8sclient.EXPECT().UpdateBMH(expect1).Return(nil)
			assistedController.updateBMHs(bmhListWithPause, machineList.DeepCopy())
		})
	})

	Context("Upload logs", func() {
		var pod v1.Pod
		BeforeEach(func() {
			LogsUploadPeriod = 100 * time.Millisecond
			pod = v1.Pod{TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: v1.PodSpec{}, Status: v1.PodStatus{Phase: "Pending"}}
		})
		It("Validate upload logs, get pod fails", func() {
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(10)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.UploadLogs(ctx, cancel, &wg, status)
			time.Sleep(1 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs, Get pods logs failed", func() {
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).MinTimes(1)
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(nil, fmt.Errorf("dummy")).MinTimes(1)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.UploadLogs(ctx, cancel, &wg, status)
			time.Sleep(500 * time.Millisecond)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs (controllers logs only), Upload failed", func() {
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			err := assistedController.uploadSummaryLogs("test", assistedController.Namespace, controllerLogsSecondsAgo, false, "")
			Expect(err).To(HaveOccurred())
		})
		It("Validate upload logs happy flow (controllers logs only)", func() {
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).Times(1)
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			err := assistedController.uploadSummaryLogs("test", assistedController.Namespace, controllerLogsSecondsAgo, false, "")
			Expect(err).NotTo(HaveOccurred())
		})

		It("Validateupload logs happy flow (controllers logs only) and list operators failed ", func() {
			reportLogProgressSuccess()
			mockk8sclient.EXPECT().ListClusterOperators().Return(nil, fmt.Errorf("dummy"))
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).Times(1)
			err := assistedController.uploadSummaryLogs("test", assistedController.Namespace, controllerLogsSecondsAgo, false, "")
			Expect(err).NotTo(HaveOccurred())
		})

	})

	Context("Upload logs with oc must-gather", func() {
		var pod v1.Pod
		var ctx context.Context
		var cancel context.CancelFunc

		callUploadLogs := func(waitTime time.Duration) {
			wg.Add(1)
			go assistedController.UploadLogs(ctx, cancel, &wg, status)
			time.Sleep(waitTime)
			if !status.HasError() {
				cancel()
			}
			wg.Wait()
		}

		BeforeEach(func() {
			LogsUploadPeriod = 100 * time.Millisecond
			pod = v1.Pod{TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: v1.PodSpec{}, Status: v1.PodStatus{Phase: "Pending"}}

			ctx, cancel = context.WithCancel(context.Background())
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).AnyTimes()
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(r, nil).AnyTimes()
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).DoAndReturn(
				func(ctx context.Context, clusterId string, logsType models.LogsType, reader io.Reader) error {
					_, _ = new(bytes.Buffer).ReadFrom(reader)
					return nil
				}).AnyTimes()
			reportLogProgressSuccess()
		})
		It("Validate upload logs (with must-gather logs)", func() {
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), assistedController.MustGatherImage).Return("../../test_files/tartest.tar.gz", nil).Times(1)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			status.Error()
			callUploadLogs(150 * time.Millisecond)
		})

		It("Validate must-gather logs are not collected with no error", func() {
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			callUploadLogs(150 * time.Millisecond)
		})

		It("Validate must-gather logs are retried on error", func() {
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Return("", fmt.Errorf("failed"))
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Return("../../test_files/tartest.tar.gz", nil)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
			status.Error()
			callUploadLogs(250 * time.Millisecond)
		})
	})

	Context("getMaximumOLMTimeout", func() {
		It("Return general timeout if no OLM's present", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return([]models.MonitoredOperator{}, nil).Times(1)
			Expect(assistedController.getMaximumOLMTimeout()).To(Equal(WaitTimeout))
		})

		It("Return general timeout if assisted service is not reacheble", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("error")).Times(1)
			Expect(assistedController.getMaximumOLMTimeout()).To(Equal(WaitTimeout))
		})

		It("Return general timeout if OLM's timeout is lower", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return([]models.MonitoredOperator{}, nil).Times(1)
			Expect(assistedController.getMaximumOLMTimeout()).To(Equal(WaitTimeout))
		})

		It("Return maximum from multiple OLM's", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{
					{OperatorType: models.OperatorTypeOlm, TimeoutSeconds: 120 * 60},
					{OperatorType: models.OperatorTypeOlm, TimeoutSeconds: 130 * 60},
				}, nil,
			).Times(1)
			Expect(assistedController.getMaximumOLMTimeout()).To(Equal(130 * 60 * time.Second))
		})
	})

	Context("waitForOLMOperators", func() {
		It("Don't wait if OLM operators list is empty", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{}, nil,
			).Times(1)
			Expect(assistedController.waitForOLMOperators()).To(Equal(true))
		})
		It("Don't wait if OLM operator available", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{Status: models.OperatorStatusAvailable, OperatorType: models.OperatorTypeOlm}}, nil,
			).Times(1)
			Expect(assistedController.waitForOLMOperators()).To(Equal(true))
		})
		It("Don't wait if OLM operator failed", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{Status: models.OperatorStatusFailed, OperatorType: models.OperatorTypeOlm}}, nil,
			).Times(1)
			Expect(assistedController.waitForOLMOperators()).To(Equal(true))
		})
		It("Wait if OLM operator progressing and k8s unavailable", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{Name: "lso", Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm}}, nil,
			).Times(1)
			mockk8sclient.EXPECT().GetCSVFromSubscription(gomock.Any(), gomock.Any()).Return("", fmt.Errorf("Error")).Times(1)
			Expect(assistedController.waitForOLMOperators()).To(Equal(false))
		})
		It("Wait if OLM operator progressing", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", Name: "lso", Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm}}, nil,
			).Times(1)
			mockk8sclient.EXPECT().GetCSVFromSubscription("openshift-local-storage", "local-storage-operator").Return("lso-1.1", nil).Times(1)
			mockk8sclient.EXPECT().GetCSV("openshift-local-storage", "lso-1.1").Return(&olmv1alpha1.ClusterServiceVersion{}, nil).Times(1)
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			Expect(assistedController.waitForOLMOperators()).To(Equal(false))
		})
	})

	Context("waitingForClusterVersion", func() {
		tests := []struct {
			name             string
			currentCVOStatus OperatorStatus
			newCVOStatus     OperatorStatus
			shouldSendUpdate bool
		}{
			{
				name:             "(false, no message) -> (false, no message)",
				currentCVOStatus: OperatorStatus{isAvailable: false, message: ""},
				newCVOStatus:     OperatorStatus{isAvailable: false, message: ""},
				shouldSendUpdate: false,
			},
			{
				name:             "(false, no message) -> (false, with message)",
				currentCVOStatus: OperatorStatus{isAvailable: false, message: ""},
				newCVOStatus:     OperatorStatus{isAvailable: false, message: "message"},
				shouldSendUpdate: true,
			},
			{
				name:             "(false, with message) -> (false, same message)",
				currentCVOStatus: OperatorStatus{isAvailable: false, message: "message"},
				newCVOStatus:     OperatorStatus{isAvailable: false, message: "message"},
				shouldSendUpdate: false,
			},
			{
				name:             "(false, with message) -> (false, new message)",
				currentCVOStatus: OperatorStatus{isAvailable: false, message: "message"},
				newCVOStatus:     OperatorStatus{isAvailable: false, message: "new"},
				shouldSendUpdate: true,
			},
			{
				name:             "(false, with message) -> (false, no message)",
				currentCVOStatus: OperatorStatus{isAvailable: false, message: "message"},
				newCVOStatus:     OperatorStatus{isAvailable: false, message: ""},
				shouldSendUpdate: false,
			},
			{
				name:             "(false, no message) -> (true, no message)",
				currentCVOStatus: OperatorStatus{isAvailable: false, message: ""},
				newCVOStatus:     OperatorStatus{isAvailable: true, message: ""},
				shouldSendUpdate: true,
			},
			{
				name:             "(false, no message) -> (true, with message)",
				currentCVOStatus: OperatorStatus{isAvailable: false, message: ""},
				newCVOStatus:     OperatorStatus{isAvailable: true, message: "message"},
				shouldSendUpdate: true,
			},
		}

		BeforeEach(func() {
			GeneralProgressUpdateInt = 100 * time.Millisecond
			WaitTimeout = 150 * time.Millisecond
		})

		for i := range tests {
			t := tests[i]
			It(t.name, func() {
				assistedController.cvoStatus = t.currentCVOStatus
				clusterVersionReport := &configv1.ClusterVersion{
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{{Message: t.newCVOStatus.message}},
					},
				}

				if t.newCVOStatus.isAvailable {
					clusterVersionReport.Status.Conditions[0].Type = configv1.OperatorAvailable
					clusterVersionReport.Status.Conditions[0].Status = configv1.ConditionTrue
				} else {
					clusterVersionReport.Status.Conditions[0].Type = configv1.OperatorProgressing
					clusterVersionReport.Status.Conditions[0].Status = configv1.ConditionFalse
				}

				mockk8sclient.EXPECT().GetClusterVersion("version").Return(clusterVersionReport, nil).Times(1)

				if t.shouldSendUpdate {
					mockbmclient.EXPECT().UpdateClusterInstallProgress(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
				}

				if t.newCVOStatus.isAvailable {
					Expect(assistedController.waitingForClusterVersion()).ShouldNot(HaveOccurred())
				} else {
					Expect(assistedController.waitingForClusterVersion()).Should(HaveOccurred())
				}
			})
		}
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

func getClusterOperatorWithCondition(condition configv1.ClusterStatusConditionType, status configv1.ConditionStatus) *configv1.ClusterOperator {
	return &configv1.ClusterOperator{
		Status: configv1.ClusterOperatorStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: condition, Status: status},
			},
		},
	}
}

func getClusterOperatorWithConditionsStatus(availableStatus, degradedStatus configv1.ConditionStatus) *configv1.ClusterOperator {
	return &configv1.ClusterOperator{
		Status: configv1.ClusterOperatorStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: availableStatus},
				{Type: configv1.OperatorDegraded, Status: degradedStatus},
			},
		},
	}
}

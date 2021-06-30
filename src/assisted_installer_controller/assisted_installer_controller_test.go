package assisted_installer_controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"
	"github.com/openshift/assisted-installer/src/common"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	certificatesv1 "k8s.io/api/certificates/v1"

	"github.com/go-openapi/strfmt"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-service/models"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "controller_test")
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

	aiNamespaceRunlevelPatch = []byte(`{"metadata":{"labels":{"$patch": "delete", "openshift.io/run-level":"0"}}}`)

	progressClusterVersionCondition = &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Message: "progress"}},
		},
	}

	availableClusterVersionCondition = &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status:  configv1.ConditionTrue,
				Message: "done"}},
		},
	}

	validConsoleOperator = getClusterOperatorWithConditionsStatus(configv1.ConditionTrue, configv1.ConditionFalse)

	testIngressConfigMap = map[string]string{
		"ca-bundle.crt": "CA",
	}
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
		GeneralWaitInterval = 10 * time.Millisecond
		GeneralProgressUpdateInt = 10 * time.Millisecond

		defaultStages = []models.HostStage{models.HostStageDone,
			models.HostStageDone,
			models.HostStageDone}

		assistedController = NewController(l, defaultTestControllerConf, mockops, mockbmclient, mockk8sclient)
		status = &ControllerStatus{}
	})
	AfterEach(func() {
		ctrl.Finish()
	})

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

	setConsoleAsAvailable := func(clusterID string) {
		mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(validConsoleOperator, nil).Times(1)
		mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), clusterID, consoleOperatorName, models.OperatorStatusAvailable, gomock.Any()).Return(nil).Times(1)
		mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), clusterID, consoleOperatorName).Return(&models.MonitoredOperator{Status: models.OperatorStatusAvailable}, nil).Times(1)
	}

	setClusterAsFinalizing := func() {
		finalizing := models.ClusterStatusFinalizing
		mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&models.Cluster{Status: &finalizing}, nil).Times(1)
	}

	uploadIngressCert := func(clusterID string) {
		cm := v1.ConfigMap{Data: testIngressConfigMap}
		mockk8sclient.EXPECT().GetConfigMap(ingressConfigMapNamespace, ingressConfigMapName).Return(&cm, nil).Times(1)
		mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), testIngressConfigMap["ca-bundle.crt"], clusterID).Return(nil).Times(1)
	}

	setControllerWaitForOLMOperators := func(clusterID string) {
		WaitTimeout = 100 * time.Millisecond

		setClusterAsFinalizing()
		uploadIngressCert(clusterID)
		setConsoleAsAvailable(clusterID)
	}

	returnServiceWithDot10Address := func(name, namespace string) *gomock.Call {
		return mockk8sclient.EXPECT().ListServices("").Return(&v1.ServiceList{
			Items: []v1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: v1.ServiceSpec{
						ClusterIP: "10.56.20.10",
					},
				},
			},
		}, nil)
	}

	returnServiceNetwork := func() {
		mockk8sclient.EXPECT().GetServiceNetworks().Return([]string{"10.56.20.0/24"}, nil)
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

		It("waitAndUpdateNodesStatus happy flow - all nodes installing", func() {
			updateProgressSuccess(defaultStages, inventoryNamesIds)

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageConfiguring)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)
			configuringSuccess()
			listNodes()

			exit := assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(false))
		})

		It("waitAndUpdateNodesStatus happy flow - all nodes installed", func() {

			hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)
			exit := assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(true))
		})

		It("WaitAndUpdateNodesStatus including joined state", func() {
			joined := []models.HostStage{models.HostStageJoined,
				models.HostStageJoined,
				models.HostStageJoined}

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageConfiguring)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)
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
			configuringSuccess()

			exit := assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(false))
		})

		It("waitAndUpdateNodesStatus set installed", func() {
			done := []models.HostStage{models.HostStageDone,
				models.HostStageDone,
				models.HostStageDone}

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageJoined)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)
			nodes := GetKubeNodes(kubeNamesIds)
			mockk8sclient.EXPECT().ListNodes().Return(nodes, nil).Times(1)
			updateProgressSuccess(done, inventoryNamesIds)
			configuringSuccess()

			exit := assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(false))
		})

		It("2aitAndUpdateNodesStatus getHost failure", func() {
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(map[string]inventory_client.HostData{}, fmt.Errorf("dummy")).Times(1)

			exit := assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(false))
		})

		It("All hosts move to error state - exit true", func() {
			hosts := create3Hosts(models.HostStatusError, models.HostStageJoined)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)
			exit := assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(true))
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
			listNodesOneByOne := func() {
				kubeNameIdsToReturn := make(map[string]string)
				for name, id := range kubeNamesIds {
					kubeNameIdsToReturn[name] = id
					mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNameIdsToReturn), nil).Times(1)
					targetMap := make(map[string]inventory_client.HostData)
					// Copy from the original map to the target map
					for key, value := range inventoryNamesIds {
						targetMap[key] = value
					}
					mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
						Return(targetMap, nil).Times(1)
					delete(inventoryNamesIds, name)
				}
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
					Return(inventoryNamesIds, nil).Times(1)
			}

			updateProgressSuccess(defaultStages, inventoryNamesIds)
			listNodesOneByOne()
			configuringSuccess()

			// first host set to installed
			exit := assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(false))
			// second host set to installed
			exit = assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(false))
			// third host set to installed
			exit = assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(false))
			// all hosts were installed
			exit = assistedController.waitAndUpdateNodesStatus()
			Expect(exit).Should(Equal(true))
		})
	})

	Context("UpdateStatusFails and then succeeds", func() {
		It("UpdateStatus fails and then succeeds, list nodes failed ", func() {
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
			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageJoined)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			updateProgressSuccessFailureTest(defaultStages, hosts)
			hosts = create3Hosts(models.HostStatusInstalled, models.HostStageDone)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)

			configuringSuccess()

			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("no matter what")).AnyTimes()
			go assistedController.WaitAndUpdateNodesStatus(context.TODO(), &wg)
			wg.Add(1)
			wg.Wait()
		})
	})

	Context("ListNodes fails and then succeeds", func() {
		It("ListNodes fails and then succeeds", func() {
			listNodesOneFailure := func() {
				mockk8sclient.EXPECT().ListNodes().Return(nil, fmt.Errorf("dummy")).Times(1)
				mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			}
			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageJoined)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			updateProgressSuccess(defaultStages, hosts)
			hosts = create3Hosts(models.HostStatusInstalled, models.HostStageDone)
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)

			listNodesOneFailure()
			configuringSuccess()

			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("no matter what")).AnyTimes()
			go assistedController.WaitAndUpdateNodesStatus(context.TODO(), &wg)
			wg.Add(1)
			wg.Wait()

		})
	})

	Context("validating ApproveCsrs", func() {
		BeforeEach(func() {
			GeneralWaitInterval = 10 * time.Millisecond
		})
		It("Run ApproveCsrs and validate it exists on channel set", func() {
			testList := certificatesv1.CertificateSigningRequestList{}
			mockk8sclient.EXPECT().ListCsrs().Return(&testList, nil).MinTimes(2).MaxTimes(5)
			ctx, cancel := context.WithCancel(context.Background())
			go assistedController.ApproveCsrs(ctx)
			time.Sleep(30 * time.Millisecond)
			cancel()
			time.Sleep(30 * time.Millisecond)
		})
		It("Run ApproveCsrs when list returns error", func() {
			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(5)
			ctx, cancel := context.WithCancel(context.Background())
			go assistedController.ApproveCsrs(ctx)
			time.Sleep(30 * time.Millisecond)
			cancel()
			time.Sleep(30 * time.Millisecond)
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
			go assistedController.ApproveCsrs(ctx)
			time.Sleep(20 * time.Millisecond)
			cancel()
		})
	})

	Context("validating AddRouterCAToClusterCA", func() {
		BeforeEach(func() {
			assistedController.WaitForClusterVersion = true
			GeneralWaitInterval = 1 * time.Millisecond
		})
		It("happy flow", func() {
			uploadIngressCert(assistedController.ClusterID)
			res := assistedController.addRouterCAToClusterCA()
			Expect(res).Should(Equal(true))
		})
		It("Get Config map failed", func() {
			mockk8sclient.EXPECT().GetConfigMap(ingressConfigMapNamespace, ingressConfigMapName).Return(nil, fmt.Errorf("dummy")).Times(1)
			res := assistedController.addRouterCAToClusterCA()
			Expect(res).Should(Equal(false))
		})
		It("UploadIngressCa failed", func() {
			cm := v1.ConfigMap{Data: testIngressConfigMap}
			mockk8sclient.EXPECT().GetConfigMap(ingressConfigMapNamespace, ingressConfigMapName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), testIngressConfigMap["ca-bundle.crt"], assistedController.ClusterID).Return(fmt.Errorf("dummy")).Times(1)
			res := assistedController.addRouterCAToClusterCA()
			Expect(res).Should(Equal(false))
		})
	})

	Context("PostInstallConfigs", func() {
		Context("waiting for cluster version", func() {
			BeforeEach(func() {
				assistedController.WaitForClusterVersion = true
				GeneralWaitInterval = 1 * time.Millisecond
			})

			It("success", func() {
				installing := models.ClusterStatusInstalling
				mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&models.Cluster{Status: &installing}, nil).Times(1)
				setClusterAsFinalizing()
				uploadIngressCert(assistedController.ClusterID)

				// Console
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
				setConsoleAsAvailable("cluster-id")

				// Patching NS
				mockk8sclient.EXPECT().PatchNamespace(defaultTestControllerConf.Namespace, aiNamespaceRunlevelPatch).Return(nil)

				// CVO
				mockk8sclient.EXPECT().GetClusterVersion("version").Return(nil, fmt.Errorf("dummy")).Times(1)

				mockk8sclient.EXPECT().GetClusterVersion("version").Return(progressClusterVersionCondition, nil).Times(1)
				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).
					Return(&models.MonitoredOperator{Status: "", StatusInfo: ""}, nil).Times(1)
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, models.OperatorStatusProgressing, progressClusterVersionCondition.Status.Conditions[0].Message).Times(1)

				mockk8sclient.EXPECT().GetClusterVersion("version").Return(availableClusterVersionCondition, nil).Times(2)
				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).
					Return(&models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: progressClusterVersionCondition.Status.Conditions[0].Message}, nil).Times(1)
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, models.OperatorStatusAvailable, availableClusterVersionCondition.Status.Conditions[0].Message).Times(1)
				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).
					Return(&models.MonitoredOperator{Status: models.OperatorStatusAvailable, StatusInfo: availableClusterVersionCondition.Status.Conditions[0].Message}, nil).Times(1)

				// Completion
				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "custom_manifests.yaml", gomock.Any()).Return(nil).Times(1)
				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "kubeconfig-noingress", gomock.Any()).Return(nil).Times(1)
				mockops.EXPECT().CreateManifests(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return([]models.MonitoredOperator{}, nil).Times(2)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)

				wg.Add(1)
				go assistedController.PostInstallConfigs(context.TODO(), &wg, status)
				wg.Wait()

				Expect(status.HasError()).Should(Equal(false))
			})
			It("failure", func() {
				WaitTimeout = 20 * time.Millisecond
				GeneralProgressUpdateInt = 30 * time.Millisecond
				setClusterAsFinalizing()

				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", false, gomock.Any()).Return(nil).Times(1)

				wg.Add(1)
				go assistedController.PostInstallConfigs(context.TODO(), &wg, status)
				wg.Wait()
				Expect(status.HasError()).Should(Equal(true))
			})
		})

		Context("not waiting for cluster version", func() {
			BeforeEach(func() {
				assistedController.WaitForClusterVersion = false
				GeneralWaitInterval = 10 * time.Millisecond
			})
			It("success", func() {
				installing := models.ClusterStatusInstalling
				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "custom_manifests.yaml", gomock.Any()).Return(nil).Times(1)
				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "kubeconfig-noingress", gomock.Any()).Return(nil).Times(1)
				mockops.EXPECT().CreateManifests(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&models.Cluster{Status: &installing}, nil).Times(1)
				setClusterAsFinalizing()
				uploadIngressCert(assistedController.ClusterID)
				setConsoleAsAvailable("cluster-id")
				mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return([]models.MonitoredOperator{}, nil).AnyTimes()
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)

				// Patching NS
				mockk8sclient.EXPECT().PatchNamespace(defaultTestControllerConf.Namespace, aiNamespaceRunlevelPatch).Return(nil)

				wg.Add(1)
				assistedController.PostInstallConfigs(context.TODO(), &wg, status)
				wg.Wait()
				Expect(status.HasError()).Should(Equal(false))
			})
			It("failure", func() {
				WaitTimeout = 20 * time.Millisecond
				setClusterAsFinalizing()
				mockk8sclient.EXPECT().GetConfigMap(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("aaa")).MinTimes(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", false,
					"Timeout while waiting router ca data").Return(nil).Times(1)

				// Patching NS
				mockk8sclient.EXPECT().PatchNamespace(defaultTestControllerConf.Namespace, aiNamespaceRunlevelPatch).Return(nil)

				wg.Add(1)
				go assistedController.PostInstallConfigs(context.TODO(), &wg, status)
				wg.Wait()
				Expect(status.HasError()).Should(Equal(true))
			})
		})

		Context("waiting for OLM", func() {
			BeforeEach(func() {
				assistedController.WaitForClusterVersion = false
				GeneralWaitInterval = 10 * time.Millisecond
			})

			It("waiting for single OLM operator", func() {
				setControllerWaitForOLMOperators(assistedController.ClusterID)

				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "custom_manifests.yaml", gomock.Any()).Return(nil).Times(1)
				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "kubeconfig-noingress", gomock.Any()).Return(nil).Times(1)
				mockops.EXPECT().CreateManifests(gomock.Any(), gomock.Any()).Return(nil).Times(1)
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
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)

				// Patching NS
				mockk8sclient.EXPECT().PatchNamespace(defaultTestControllerConf.Namespace, aiNamespaceRunlevelPatch).Return(nil)

				wg.Add(1)
				assistedController.PostInstallConfigs(context.TODO(), &wg, status)
				wg.Wait()
				Expect(status.HasError()).Should(Equal(false))
			})
			It("waiting for single OLM operator which timeouts", func() {
				setControllerWaitForOLMOperators(assistedController.ClusterID)

				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "custom_manifests.yaml", gomock.Any()).Return(nil).Times(1)
				mockbmclient.EXPECT().DownloadFile(gomock.Any(), "kubeconfig-noingress", gomock.Any()).Return(nil).Times(1)
				mockops.EXPECT().CreateManifests(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
					[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusProgressing, TimeoutSeconds: 1}}, nil,
				).AnyTimes()
				mockk8sclient.EXPECT().GetCSVFromSubscription("openshift-local-storage", "local-storage-operator").Return("lso-1.1", nil).AnyTimes()
				mockk8sclient.EXPECT().GetCSV("openshift-local-storage", "lso-1.1").Return(&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}}, nil).AnyTimes()
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", models.OperatorStatusProgressing, gomock.Any()).Return(nil).AnyTimes()
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", models.OperatorStatusFailed, "Waiting for operator timed out").Return(nil).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)

				// Patching NS
				mockk8sclient.EXPECT().PatchNamespace(defaultTestControllerConf.Namespace, aiNamespaceRunlevelPatch).Return(nil)

				wg.Add(1)
				assistedController.PostInstallConfigs(context.TODO(), &wg, status)
				wg.Wait()
				Expect(status.HasError()).Should(Equal(false))
			})
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
			go assistedController.UploadLogs(ctx, &wg, status)
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
			go assistedController.UploadLogs(ctx, &wg, status)
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

		It("Validate upload logs happy flow (controllers logs only) and list operators failed ", func() {
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
			go assistedController.UploadLogs(ctx, &wg, status)
			time.Sleep(waitTime)
			cancel()
			wg.Wait()
		}

		successUpload := func() {
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).DoAndReturn(
				func(ctx context.Context, clusterId string, logsType models.LogsType, reader io.Reader) error {
					_, _ = new(bytes.Buffer).ReadFrom(reader)
					return nil
				}).AnyTimes()
		}

		BeforeEach(func() {
			LogsUploadPeriod = 10 * time.Millisecond
			pod = v1.Pod{TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: v1.PodSpec{}, Status: v1.PodStatus{Phase: "Pending"}}

			ctx, cancel = context.WithCancel(context.Background())
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).AnyTimes()
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(r, nil).AnyTimes()
			reportLogProgressSuccess()
		})
		It("Validate upload logs (with must-gather logs)", func() {
			successUpload()
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), assistedController.MustGatherImage).Return("../../test_files/tartest.tar.gz", nil).Times(1)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			status.Error()
			callUploadLogs(150 * time.Millisecond)
		})

		It("Validate must-gather logs are not collected with no error", func() {
			successUpload()
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			callUploadLogs(50 * time.Millisecond)
		})

		It("Validate upload logs exits with no error + failed upload", func() {
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(fmt.Errorf("dummy")).AnyTimes()
			callUploadLogs(50 * time.Millisecond)
		})

		It("Validate must-gather logs are retried on error - while cluster error occurred", func() {
			successUpload()
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Return("", fmt.Errorf("failed"))
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Return("../../test_files/tartest.tar.gz", nil)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
			status.Error()
			callUploadLogs(50 * time.Millisecond)
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
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", gomock.Any(), gomock.Any()).Return(nil).Times(1)
			Expect(assistedController.waitForOLMOperators()).To(Equal(false))
		})
		It("check that we tolerate the failed state reported by CSV", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusProgressing, TimeoutSeconds: 1}}, nil,
			).Times(1)
			mockk8sclient.EXPECT().GetCSVFromSubscription("openshift-local-storage", "local-storage-operator").Return("lso-1.1", nil).Times(1)
			mockk8sclient.EXPECT().GetCSV("openshift-local-storage", "lso-1.1").Return(&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseFailed}}, nil).Times(1)

			Expect(assistedController.waitForOLMOperators()).To(Equal(false))
			Expect(assistedController.retryMap["lso"]).To(Equal(1))

			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusProgressing, TimeoutSeconds: 1}}, nil,
			).Times(1)
			mockk8sclient.EXPECT().GetCSVFromSubscription("openshift-local-storage", "local-storage-operator").Return("lso-1.1", nil).Times(1)
			mockk8sclient.EXPECT().GetCSV("openshift-local-storage", "lso-1.1").Return(&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseSucceeded}}, nil).Times(1)
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", models.OperatorStatusAvailable, gomock.Any()).Return(nil).Times(1)

			Expect(assistedController.waitForOLMOperators()).To(Equal(false))
			Expect(assistedController.retryMap["lso"]).To(Equal(1))

			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any()).Return(
				[]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusAvailable, TimeoutSeconds: 1}}, nil,
			).Times(1)

			Expect(assistedController.waitForOLMOperators()).To(Equal(true))
		})
	})

	Context("waitingForClusterVersion", func() {
		ctx := context.TODO()
		tests := []struct {
			name                    string
			currentServiceCVOStatus *models.MonitoredOperator
			newCVOCondition         configv1.ClusterOperatorStatusCondition
			shouldSendUpdate        bool
		}{
			{
				name:                    "(false, no message) -> (false, no message)",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: ""},
				shouldSendUpdate:        false,
			},
			{
				name:                    "(false, no message) -> (false, with message)",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "message"},
				shouldSendUpdate:        true,
			},
			{
				name:                    "(false, with message) -> (false, same message)",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: "message"},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "message"},
				shouldSendUpdate:        false,
			},
			{
				name:                    "(false, with message) -> (false, new message)",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: "message"},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "new"},
				shouldSendUpdate:        true,
			},
			{
				name:                    "(false, with message) -> (false, no message)",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: "message"},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: ""},
				shouldSendUpdate:        false,
			},
			{
				name:                    "(false, no message) -> (true, no message)",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: ""},
				shouldSendUpdate:        true,
			},
			{
				name:                    "(false, no message) -> (true, with message)",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "message"},
				shouldSendUpdate:        true,
			},
			{
				name:                    "(true) -> exit with success",
				currentServiceCVOStatus: &models.MonitoredOperator{Status: models.OperatorStatusAvailable, StatusInfo: ""},
				newCVOCondition:         configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: ""},
				shouldSendUpdate:        false,
			},
		}

		operatorTypeToOperatorStatus := func(conditionType configv1.ClusterStatusConditionType) models.OperatorStatus {
			switch conditionType {
			case configv1.OperatorAvailable:
				return models.OperatorStatusAvailable
			case configv1.OperatorProgressing:
				return models.OperatorStatusProgressing
			default:
				return models.OperatorStatusFailed
			}
		}

		BeforeEach(func() {
			GeneralProgressUpdateInt = 100 * time.Millisecond
			WaitTimeout = 150 * time.Millisecond
		})

		for i := range tests {
			t := tests[i]
			It(t.name, func() {
				clusterVersionReport := &configv1.ClusterVersion{
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{t.newCVOCondition},
					},
				}
				newServiceCVOStatus := &models.MonitoredOperator{
					Status:     operatorTypeToOperatorStatus(t.newCVOCondition.Type),
					StatusInfo: t.newCVOCondition.Message,
				}

				amountOfSamples := 1

				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).Return(t.currentServiceCVOStatus, nil).Times(1)

				if t.shouldSendUpdate {
					if t.currentServiceCVOStatus.Status != models.OperatorStatusAvailable {
						// If a change occured and it is still false - we expect the timer to be resetted,
						// hence another round would happen.
						amountOfSamples += 1

						mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).Return(newServiceCVOStatus, nil).Times(1)
					}

					mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).Times(1)
				}

				mockk8sclient.EXPECT().GetClusterVersion("version").Return(clusterVersionReport, nil).Times(amountOfSamples)

				if newServiceCVOStatus.Status == models.OperatorStatusAvailable {
					Expect(assistedController.waitingForClusterVersion(ctx)).ShouldNot(HaveOccurred())
				} else {
					Expect(assistedController.waitingForClusterVersion(ctx)).Should(HaveOccurred())
				}
			})
		}

		It("service fail to sync - context cancel", func() {
			currentServiceCVOStatus := &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""}
			clusterVersionReport := &configv1.ClusterVersion{
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: ""},
					},
				},
			}

			mockk8sclient.EXPECT().GetClusterVersion("version").Return(clusterVersionReport, nil).AnyTimes()
			mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).Return(currentServiceCVOStatus, nil).AnyTimes()
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).AnyTimes()

			err := func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()
				return assistedController.waitingForClusterVersion(ctx)
			}()

			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		})

		It("service fail to sync - finally succeed", func() {
			currentServiceCVOStatus := &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""}
			newServiceCVOStatus := &models.MonitoredOperator{Status: models.OperatorStatusAvailable, StatusInfo: ""}
			clusterVersionReport := &configv1.ClusterVersion{
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: ""},
					},
				},
			}

			// Fail twice
			mockk8sclient.EXPECT().GetClusterVersion("version").Return(clusterVersionReport, nil).Times(3)
			mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).Return(currentServiceCVOStatus, nil).Times(2)
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).Times(2)

			// Service succeed
			mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName).Return(newServiceCVOStatus, nil).Times(1)

			Expect(assistedController.waitingForClusterVersion(context.TODO())).ShouldNot(HaveOccurred())
		})
	})

	Context("Hack deleting service that conflicts with DNS IP address", func() {

		const (
			conflictServiceName      = "conflict"
			conflictServiceNamespace = "testing"
		)

		BeforeEach(func() {
			DNSAddressRetryInterval = 1 * time.Microsecond
			DeletionRetryInterval = 1 * time.Microsecond
		})

		hackConflict := func() {
			wg.Add(1)
			assistedController.HackDNSAddressConflict(&wg)
			wg.Wait()
		}

		It("Exit if getting service network fails", func() {
			mockk8sclient.EXPECT().GetServiceNetworks().Return(nil, errors.New("get service network failed"))
			hackConflict()
		})
		It("Exit if service network is IPv6", func() {
			mockk8sclient.EXPECT().GetServiceNetworks().Return([]string{"2002:db8::/64"}, nil)
			hackConflict()
		})
		It("Retry if list services fails", func() {
			returnServiceNetwork()
			mockk8sclient.EXPECT().ListServices("").Return(nil, errors.New("list services failed"))
			returnServiceWithDot10Address(dnsServiceName, dnsServiceNamespace)
			hackConflict()
		})
		It("Kill service and DNS pods if DNS service IP is taken", func() {
			returnServiceNetwork()
			returnServiceWithDot10Address(conflictServiceName, conflictServiceNamespace)
			mockk8sclient.EXPECT().DeleteService(conflictServiceName, conflictServiceNamespace).Return(nil)
			mockk8sclient.EXPECT().DeletePods(dnsOperatorNamespace).Return(nil)
			returnServiceWithDot10Address(dnsServiceName, dnsServiceNamespace)
			hackConflict()
		})
		It("Retry service deletion if deleting conflicting service fails", func() {
			returnServiceNetwork()
			returnServiceWithDot10Address(conflictServiceName, conflictServiceNamespace)
			mockk8sclient.EXPECT().DeleteService(conflictServiceName, conflictServiceNamespace).Return(errors.New("service deletion failed")).Times(4)
			mockk8sclient.EXPECT().DeleteService(conflictServiceName, conflictServiceNamespace).Return(nil)
			mockk8sclient.EXPECT().DeletePods(dnsOperatorNamespace).Return(nil)
			returnServiceWithDot10Address(dnsServiceName, dnsServiceNamespace)
			hackConflict()
		})
		It("Retry pod deletion if deleting DNS operator pods fails", func() {
			returnServiceNetwork()
			returnServiceWithDot10Address(conflictServiceName, conflictServiceNamespace)
			mockk8sclient.EXPECT().DeleteService(conflictServiceName, conflictServiceNamespace).Return(nil)
			mockk8sclient.EXPECT().DeletePods(dnsOperatorNamespace).Return(errors.New("pod deletion failed")).Times(4)
			mockk8sclient.EXPECT().DeletePods(dnsOperatorNamespace).Return(nil)
			returnServiceWithDot10Address(dnsServiceName, dnsServiceNamespace)
			hackConflict()
		})
		It("Retry until timed out if listing services keeps failing", func() {
			returnServiceNetwork()
			mockk8sclient.EXPECT().ListServices("").Return(nil, errors.New("list services failed")).Times(maxDNSServiceIPAttempts)
			hackConflict()
		})
		It("Retry until timed out if no service with requested IP cannot be found", func() {
			returnServiceNetwork()
			mockk8sclient.EXPECT().ListServices("").Return(&v1.ServiceList{Items: []v1.Service{}}, nil).Times(maxDNSServiceIPAttempts)
			hackConflict()
		})
		It("Retry until timed out if deleting conflicting service fails", func() {
			returnServiceNetwork()
			returnServiceWithDot10Address(conflictServiceName, conflictServiceNamespace).Times(maxDNSServiceIPAttempts)
			mockk8sclient.EXPECT().DeleteService(conflictServiceName, conflictServiceNamespace).Return(errors.New("service deletion failed")).Times(maxDeletionAttempts * maxDNSServiceIPAttempts)
			hackConflict()
		})
		It("Retry until timed out if deleting DNS operator pods fails", func() {
			returnServiceNetwork()
			returnServiceWithDot10Address(conflictServiceName, conflictServiceNamespace).Times(maxDNSServiceIPAttempts)
			mockk8sclient.EXPECT().DeleteService(conflictServiceName, conflictServiceNamespace).Return(nil).Times(maxDNSServiceIPAttempts)
			mockk8sclient.EXPECT().DeletePods(dnsOperatorNamespace).Return(errors.New("pod deletion failed")).Times(maxDeletionAttempts * maxDNSServiceIPAttempts)
			hackConflict()
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

func create3Hosts(currentStatus string, stage models.HostStage) map[string]inventory_client.HostData {
	currentState := models.HostProgressInfo{CurrentStage: stage}
	node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
	node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
	node2Id := strfmt.UUID("b898d516-3e16-49d0-86a5-0ad5bd04e3ed")
	return map[string]inventory_client.HostData{
		"node0": {Host: &models.Host{ID: &node0Id, Progress: &currentState, Status: &currentStatus}},
		"node1": {Host: &models.Host{ID: &node1Id, Progress: &currentState, Status: &currentStatus}},
		"node2": {Host: &models.Host{ID: &node2Id, Progress: &currentState, Status: &currentStatus}}}
}

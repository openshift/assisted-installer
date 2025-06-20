package assisted_installer_controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	batchV1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apischema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/google/uuid"
	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/assisted-installer/src/common"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	certificatesv1 "k8s.io/api/certificates/v1"

	"github.com/go-openapi/strfmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	gomock "go.uber.org/mock/gomock"

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

// This custom gomock matcher checks that the call arrived (indirectly) from the function
// controller.isStageTimedOut.  Since this functon might be called from
// other places during a test, this is the way to distinguish between them
// since they need to be handled differently
type stageTimedOutMatcher struct{}

func (l *stageTimedOutMatcher) Matches(_ interface{}) bool {
	pc := reflect.ValueOf((&controller{}).isStageTimedOut("")).Pointer()
	fileName, startLine := runtime.FuncForPC(pc).FileLine(pc)
	stack := make([]uintptr, 10)
	length := runtime.Callers(0, stack)
	frames := runtime.CallersFrames(stack[:length])
	for frame, more := frames.Next(); more; frame, more = frames.Next() {
		diff := frame.Line - startLine
		if frame.File == fileName && diff >= 0 && diff < 5 {
			return true
		}
	}
	return false
}

func (l *stageTimedOutMatcher) String() string {
	return "stage timed out matcher"
}

func calledFromStageTimedOut() gomock.Matcher {
	return &stageTimedOutMatcher{}
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
		l                   = logrus.New()
		ctrl                *gomock.Controller
		mockops             *ops.MockOps
		mockbmclient        *inventory_client.MockInventoryClient
		mockk8sclient       *k8s_client.MockK8SClient
		mockRebootsNotifier *MockRebootsNotifier
		assistedController  *controller
		inventoryNamesIds   map[string]inventory_client.HostData
		kubeNamesIds        map[string]string
		wg                  sync.WaitGroup
		defaultStages       []models.HostStage
	)
	kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
		"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
		"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}

	BeforeEach(func() {
		dnsValidationTimeout = 1 * time.Millisecond
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
		mockRebootsNotifier = NewMockRebootsNotifier(ctrl)
		infraEnvId := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f50")
		node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
		node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
		node2Id := strfmt.UUID("b898d516-3e16-49d0-86a5-0ad5bd04e3ed")
		currentState := models.HostProgressInfo{CurrentStage: models.HostStageJoined}
		currentStatus := models.HostStatusInstallingInProgress
		inventoryNamesIds = map[string]inventory_client.HostData{
			"node0": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node0Id, Progress: &currentState, Status: &currentStatus}},
			"node1": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node1Id, Progress: &currentState, Status: &currentStatus}},
			"node2": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node2Id, Progress: &currentState, Status: &currentStatus}}}
		kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
			"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
			"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
		GeneralWaitInterval = 10 * time.Millisecond
		GeneralProgressUpdateInt = 10 * time.Millisecond

		defaultStages = []models.HostStage{models.HostStageDone,
			models.HostStageDone,
			models.HostStageDone}

		assistedController = newController(l, defaultTestControllerConf, mockops, mockbmclient, mockk8sclient, mockRebootsNotifier)
	})
	AfterEach(func() {
		ctrl.Finish()
	})

	configuringSuccess := func() {
		mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()
		mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), gomock.Any(), gomock.Any(), models.HostStageConfiguring, gomock.Any()).AnyTimes()
	}

	updateProgressSuccess := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
		var hostIds []string
		var infraEnvIds []string
		for _, host := range inventoryNamesIds {
			hostIds = append(hostIds, host.Host.ID.String())
			infraEnvIds = append(infraEnvIds, host.Host.InfraEnvID.String())
		}

		for i, stage := range stages {
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvIds[i], hostIds[i], stage, "").Return(nil).Times(1)
		}
	}

	listNodes := func() {
		mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
	}

	logClusterOperatorsSuccess := func() {
		operators := configv1.ClusterOperatorList{}
		operators.Items = []configv1.ClusterOperator{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "authentication",
				},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{{
						Type:    configv1.OperatorAvailable,
						Status:  configv1.ConditionFalse,
						Message: "This is a test message",
						Reason:  "PreconditionNotReady"}},
				},
			},
		}
		mockk8sclient.EXPECT().ListClusterOperators().Return(&operators, nil).AnyTimes()
	}

	validateClusterOperaotrReport := func(data map[string]interface{}) {
		Expect(data).ShouldNot(BeNil())
		Expect(data[clusterOperatorReportKey]).ShouldNot(BeNil())
		omr := data[clusterOperatorReportKey].([]models.OperatorMonitorReport)[0]
		Expect(omr.Name).Should(Equal("authentication"))
		Expect(omr.Status).Should(Equal(models.OperatorStatusFailed))
		Expect(omr.StatusInfo).Should(Equal("This is a test message"))
	}

	logResolvConfSuccess := func() {
		mockops.EXPECT().ReadFile(gomock.Any()).Return([]byte("test"), nil).MinTimes(3)
	}

	reportLogProgressSuccess := func() {
		mockbmclient.EXPECT().ClusterLogProgressReport(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	}

	mockGetServiceOperators := func(operators []models.MonitoredOperator) {
		for index := range operators {
			if operators[index].Status != models.OperatorStatusAvailable {
				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), operators[index].Name, gomock.Any(), gomock.Any()).Return(&operators[index], nil).Times(1)
			} else {
				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), operators[index].Name, gomock.Any(), gomock.Any()).Return(&operators[index], nil).AnyTimes()
			}
		}
	}

	mockGetCSV := func(operator models.MonitoredOperator, csv *olmv1alpha1.ClusterServiceVersion) {
		csv.Spec.Version.Major = 4
		csv.Spec.Version.Minor = 12
		csv.Spec.Version.Patch = 0
		randomCSV := uuid.New().String()
		mockk8sclient.EXPECT().GetCSVFromSubscription(operator.Namespace, operator.SubscriptionName).Return(randomCSV, nil).Times(1)
		mockk8sclient.EXPECT().GetCSV(operator.Namespace, randomCSV).Return(csv, nil).Times(1)
	}

	setConsoleAsAvailable := func(clusterID string) {
		WaitTimeout = 100 * time.Millisecond

		mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
		mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(validConsoleOperator, nil).Times(1)
		mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), clusterID, consoleOperatorName, "", models.OperatorStatusAvailable, gomock.Any()).Return(nil).Times(1)
	}

	setCvoAsAvailable := func() {
		mockGetServiceOperators([]models.MonitoredOperator{{Name: cvoOperatorName, Status: models.OperatorStatusProgressing}})
		mockk8sclient.EXPECT().GetClusterVersion().Return(availableClusterVersionCondition, nil).Times(1)
		mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, "", models.OperatorStatusAvailable, availableClusterVersionCondition.Status.Conditions[0].Message).Times(1)

		mockGetServiceOperators([]models.MonitoredOperator{{Name: cvoOperatorName, Status: models.OperatorStatusAvailable}})
	}

	setClusterAsFinalizing := func() {
		finalizing := models.ClusterStatusFinalizing
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: &finalizing}, nil).Times(1)
	}

	uploadIngressCert := func(clusterID string) {
		cm := v1.ConfigMap{Data: testIngressConfigMap}
		mockk8sclient.EXPECT().GetConfigMap(ingressConfigMapNamespace, ingressConfigMapName).Return(&cm, nil).Times(1)
		mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), testIngressConfigMap["ca-bundle.crt"], clusterID).Return(nil).Times(1)
	}

	setControllerWaitForOLMOperators := func(clusterID string) {
		setClusterAsFinalizing()
		setConsoleAsAvailable(clusterID)
		uploadIngressCert(clusterID)
	}

	returnServiceWithAddress := func(name, namespace, ip string) *gomock.Call {
		return mockk8sclient.EXPECT().ListServices("").Return(&v1.ServiceList{
			Items: []v1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: v1.ServiceSpec{
						ClusterIP: ip,
					},
				},
			},
		}, nil)
	}

	returnServiceWithDot10Address := func(name, namespace string) *gomock.Call {
		return returnServiceWithAddress(name, namespace, "10.56.20.10")
	}

	returnServiceNetwork := func() {
		mockk8sclient.EXPECT().GetServiceNetworks().Return([]string{"10.56.20.0/24"}, nil)
	}

	mockGetOLMOperators := func(operators []models.MonitoredOperator) {
		mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(operators, nil).Times(1)
	}

	mockApplyPostInstallManifests := func(operators []models.MonitoredOperator) {
		mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(operators, nil).Times(1)
		mockbmclient.EXPECT().DownloadFile(gomock.Any(), customManifestsFile, gomock.Any()).DoAndReturn(
			func(ctx context.Context, filename, dest string) error {
				if err := os.WriteFile(dest, []byte("[]"), 0600); err != nil {
					return err
				}
				return nil
			},
		).Times(1)
		mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), common.KubeconfigFileName, gomock.Any()).Return(nil).Times(1)
	}

	mockSuccessUpdateFinalizingStages := func(stages ...models.FinalizingStage) {
		for _, stage := range stages {
			mockbmclient.EXPECT().UpdateFinalizingProgress(gomock.Any(), gomock.Any(), stage).Times(1)
		}
	}

	mockAllCapabilitiesEnabled := func() {
		mockk8sclient.EXPECT().IsClusterCapabilityEnabled(gomock.Any()).Return(true, nil).AnyTimes()
	}

	mockGetClusterForCancel := func() {
		mockbmclient.EXPECT().GetCluster(calledFromStageTimedOut(), false).Return(&models.Cluster{}, nil).AnyTimes()
	}

	Context("Waiting for 3 nodes", func() {
		It("Set ready event", func() {
			// fail to connect to assisted and then succeed
			mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, nil).Times(3)

			// fail to connect to ocp and then succeed
			mockk8sclient.EXPECT().ListNodes().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().ListNodes().Return(nil, nil).Times(2)

			// fail to create event and then succeed
			mockk8sclient.EXPECT().CreateEvent(assistedController.Namespace, common.AssistedControllerIsReadyEvent, gomock.Any(), common.AssistedControllerPrefix).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().CreateEvent(assistedController.Namespace, common.AssistedControllerIsReadyEvent, gomock.Any(), common.AssistedControllerPrefix).Return(nil, nil).Times(1)

			assistedController.SetReadyState(WaitTimeout)
			Expect(assistedController.status.HasError()).Should(Equal(false))
		})

		It("waitAndUpdateNodesStatus happy flow - all nodes installing", func() {
			updateProgressSuccess([]models.HostStage{models.HostStageJoined,
				models.HostStageJoined,
				models.HostStageJoined}, inventoryNamesIds)
			updateProgressSuccess(defaultStages, inventoryNamesIds)

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageConfiguring, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			configuringSuccess()
			listNodes()

			for name, v := range inventoryNamesIds {
				value := v
				mockRebootsNotifier.EXPECT().Start(gomock.Any(), name, value.Host.ID, &value.Host.InfraEnvID, gomock.Any()).Times(1)
			}
			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
		})

		It("waitAndUpdateNodesStatus happy flow - all nodes installed", func() {

			hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)
			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(true))
		})

		It("waitAndUpdateNodesStatus happy flow - remove uninitialized taint on not ready node", func() {
			joined := []models.HostStage{models.HostStageJoined}
			currentState := models.HostProgressInfo{CurrentStage: models.HostStageRebooting}
			currentStatus := models.HostStatusInstalling
			infraEnvId := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f50")
			node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
			hosts := map[string]inventory_client.HostData{"node0": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node0Id, Progress: &currentState, Status: &currentStatus}}}
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			// not ready nodes with taint
			kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9"}
			nodes := GetKubeNodes(kubeNamesIds)
			for _, node := range nodes.Items {
				for i, cond := range node.Status.Conditions {
					if cond.Type == v1.NodeReady {
						node.Status.Conditions[i].Status = v1.ConditionFalse
					}
				}
				node.Spec.Taints = []v1.Taint{
					{
						Key:    k8s_client.UNINITIALIZED_TAINT_KEY,
						Effect: v1.TaintEffectNoExecute,
						Value:  "true",
					},
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(nodes, nil).Times(1)
			mockk8sclient.EXPECT().UntaintNode(gomock.Any()).Return(nil).Times(1)
			updateProgressSuccess(joined, hosts)
			mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()
			exit := assistedController.waitAndUpdateNodesStatus(true)
			Expect(exit).Should(Equal(false))
		})

		It("WaitAndUpdateNodesStatus including joined state from configuring", func() {
			joined := []models.HostStage{models.HostStageJoined,
				models.HostStageJoined,
				models.HostStageJoined}

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageConfiguring, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
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

			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
		})

		It("WaitAndUpdateNodesStatus including joined state from reboot", func() {
			joined := []models.HostStage{models.HostStageJoined,
				models.HostStageJoined,
				models.HostStageJoined}

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageRebooting, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
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
			mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()

			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
		})

		It("WaitAndUpdateNodesStatus sno including joined state from reboot", func() {
			joined := []models.HostStage{models.HostStageJoined}
			currentState := models.HostProgressInfo{CurrentStage: models.HostStageRebooting}
			currentStatus := models.HostStatusInstalling
			infraEnvId := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f50")
			node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
			hosts := map[string]inventory_client.HostData{"node0": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node0Id, Progress: &currentState, Status: &currentStatus}}}
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			// not ready nodes
			kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9"}
			nodes := GetKubeNodes(kubeNamesIds)
			for _, node := range nodes.Items {
				for i, cond := range node.Status.Conditions {
					if cond.Type == v1.NodeReady {
						node.Status.Conditions[i].Status = v1.ConditionFalse
					}
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(nodes, nil).Times(1)
			updateProgressSuccess(joined, hosts)
			mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()

			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
		})

		It("waitAndUpdateNodesStatus set installed", func() {
			done := []models.HostStage{models.HostStageDone,
				models.HostStageDone,
				models.HostStageDone}

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageJoined, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			nodes := GetKubeNodes(kubeNamesIds)
			mockk8sclient.EXPECT().ListNodes().Return(nodes, nil).Times(1)
			updateProgressSuccess(done, hosts)
			configuringSuccess()
			for name, v := range inventoryNamesIds {
				value := v
				mockRebootsNotifier.EXPECT().Start(gomock.Any(), name, value.Host.ID, &value.Host.InfraEnvID, gomock.Any()).Times(1)
			}
			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
		})
		It("waitAndUpdateNodesStatus don't update progress for hosts in stage done", func() {

			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageDone, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			nodes := GetKubeNodes(kubeNamesIds)
			mockk8sclient.EXPECT().ListNodes().Return(nodes, nil).Times(1)
			configuringSuccess()

			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))

		})

		It("2aitAndUpdateNodesStatus getHost failure", func() {
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(map[string]inventory_client.HostData{}, fmt.Errorf("dummy")).Times(1)

			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
		})

		It("All hosts move to error state - exit true", func() {
			hosts := create3Hosts(models.HostStatusError, models.HostStageJoined, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)
			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(true))
		})
	})

	Context("Waiting for 3 nodes, will appear one by one", func() {
		BeforeEach(func() {
			updateProgressSuccess = func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
				var hostIds []string
				var infraEnvIds []string
				for _, host := range inventoryNamesIds {
					hostIds = append(hostIds, host.Host.ID.String())
					infraEnvIds = append(infraEnvIds, host.Host.InfraEnvID.String())
				}
				for i, stage := range stages {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvIds[i], hostIds[i], stage, "").Return(nil).Times(1)
				}
			}
			kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9",
				"node1": "2834ff2e-8965-48a5-859a-0f1459485a77",
				"node2": "57df89ee-3546-48a5-859a-0f1459485a66"}
		})
		It("WaitAndUpdateNodesStatus one by one", func() {
			for name, v := range inventoryNamesIds {
				value := v
				mockRebootsNotifier.EXPECT().Start(gomock.Any(), name, value.Host.ID, &value.Host.InfraEnvID, gomock.Any()).Times(1)
			}
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
						Return(targetMap, nil).Times(2)
					delete(inventoryNamesIds, name)
				}
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
					Return(inventoryNamesIds, nil).Times(1)
			}

			updateProgressSuccess(defaultStages, inventoryNamesIds)
			listNodesOneByOne()
			configuringSuccess()

			// first host set to installed
			exit := assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
			// second host set to installed
			exit = assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
			// third host set to installed
			exit = assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(false))
			// all hosts were installed
			exit = assistedController.waitAndUpdateNodesStatus(false)
			Expect(exit).Should(Equal(true))
		})
	})

	Context("UpdateStatusFails and then succeeds", func() {
		It("UpdateStatus fails and then succeeds, list nodes failed ", func() {
			for name, v := range inventoryNamesIds {
				value := v
				mockRebootsNotifier.EXPECT().Start(gomock.Any(), name, value.Host.ID, &value.Host.InfraEnvID, gomock.Any()).Times(1)
			}
			updateProgressSuccessFailureTest := func(stages []models.HostStage, inventoryNamesIds map[string]inventory_client.HostData) {
				var hostIds []string
				var infraEnvIds []string
				for _, host := range inventoryNamesIds {
					hostIds = append(hostIds, host.Host.ID.String())
					infraEnvIds = append(infraEnvIds, host.Host.InfraEnvID.String())
				}
				for i, stage := range stages {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvIds[i], hostIds[i], stage, "").Return(fmt.Errorf("dummy")).Times(1)
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvIds[i], hostIds[i], stage, "").Return(nil).Times(1)
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(2)
			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageJoined, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(4)
			updateProgressSuccessFailureTest(defaultStages, hosts)
			hosts = create3Hosts(models.HostStatusInstalled, models.HostStageDone, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(1)

			configuringSuccess()

			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("no matter what")).AnyTimes()
			go assistedController.WaitAndUpdateNodesStatus(context.TODO(), &wg, false)
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
			hosts := create3Hosts(models.HostStatusInstalling, models.HostStageJoined, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)
			updateProgressSuccess(defaultStages, hosts)
			hosts = create3Hosts(models.HostStatusInstalled, models.HostStageDone, "")
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled}).
				Return(hosts, nil).Times(2)

			listNodesOneFailure()
			configuringSuccess()

			mockk8sclient.EXPECT().ListCsrs().Return(nil, fmt.Errorf("no matter what")).AnyTimes()
			for name, v := range inventoryNamesIds {
				value := v
				mockRebootsNotifier.EXPECT().Start(gomock.Any(), name, value.Host.ID, &value.Host.InfraEnvID, gomock.Any()).Times(1)
			}

			go assistedController.WaitAndUpdateNodesStatus(context.TODO(), &wg, false)
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

	Context("waitForCSVBeCreated", func() {
		var (
			operatorName     = "lso"
			subscriptionName = "local-storage-operator"
			namespaceName    = "openshift-local-storage"
		)
		BeforeEach(func() {
			assistedController.WaitForClusterVersion = true
			GeneralWaitInterval = 1 * time.Millisecond
			mockGetClusterForCancel()
		})
		It("empty operators", func() {
			Expect(assistedController.waitForCSVBeCreated([]models.MonitoredOperator{})).Should(Equal(true))
		})
		It("wrong subscription", func() {
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					Name: operatorName, Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm,
				},
			}

			mockk8sclient.EXPECT().ListJobs(gomock.Any()).Return(&batchV1.JobList{}, nil).Times(1)
			mockk8sclient.EXPECT().GetAllInstallPlansOfSubscription(gomock.Any()).Return([]olmv1alpha1.InstallPlan{}, nil).Times(1)
			mockk8sclient.EXPECT().GetCSVFromSubscription(operators[0].Namespace, operators[0].SubscriptionName).Return("", fmt.Errorf("dummy")).Times(1)
			Expect(assistedController.waitForCSVBeCreated(operators)).Should(Equal(false))
		})
		It("non-initialized operator", func() {
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					Name: operatorName, Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm,
				},
			}
			mockk8sclient.EXPECT().ListJobs(gomock.Any()).Return(&batchV1.JobList{}, nil).AnyTimes()
			mockk8sclient.EXPECT().GetAllInstallPlansOfSubscription(gomock.Any()).Return([]olmv1alpha1.InstallPlan{}, nil).AnyTimes()
			mockk8sclient.EXPECT().GetCSVFromSubscription(operators[0].Namespace, operators[0].SubscriptionName).Return("", nil).Times(1)
			mockk8sclient.EXPECT().GetCSV(operators[0].Namespace, gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			Expect(assistedController.waitForCSVBeCreated(operators)).Should(Equal(false))
		})
		It("non-initialized operator fixing olm early setup issue by deleting failed jobs", func() {
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					Name: operatorName, Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm,
				},
			}
			failedJob := batchV1.Job{ObjectMeta: metav1.ObjectMeta{Name: "failed1", Namespace: olmNamespace}, Status: batchV1.JobStatus{Failed: 1}}
			failedJob1 := batchV1.Job{ObjectMeta: metav1.ObjectMeta{Name: "failed1", Namespace: olmNamespace},
				Status: batchV1.JobStatus{Failed: 0, Conditions: []batchV1.JobCondition{{Type: batchV1.JobFailed, Status: v1.ConditionTrue}}}}
			succeededJob := batchV1.Job{ObjectMeta: metav1.ObjectMeta{Name: "succeed", Namespace: olmNamespace}, Status: batchV1.JobStatus{Failed: 0}}
			mockk8sclient.EXPECT().GetCSVFromSubscription(operators[0].Namespace, operators[0].SubscriptionName).Return("", nil).Times(1)
			mockk8sclient.EXPECT().GetCSV(operators[0].Namespace, gomock.Any()).Return(nil, apierrors.NewNotFound(apischema.GroupResource{}, failedJob.Name)).Times(1)
			mockk8sclient.EXPECT().ListJobs(olmNamespace).Return(&batchV1.JobList{Items: []batchV1.Job{failedJob, succeededJob, failedJob1}}, nil).Times(1)
			mockk8sclient.EXPECT().DeleteJob(types.NamespacedName{Name: failedJob.Name, Namespace: failedJob.Namespace}).Return(nil).Times(1)
			mockk8sclient.EXPECT().DeleteJob(types.NamespacedName{Name: failedJob1.Name, Namespace: failedJob1.Namespace}).Return(nil).Times(1)

			mockk8sclient.EXPECT().GetAllInstallPlansOfSubscription(types.NamespacedName{
				Name:      operators[0].SubscriptionName,
				Namespace: operators[0].Namespace,
			}).Return([]olmv1alpha1.InstallPlan{{ObjectMeta: metav1.ObjectMeta{Name: "test-name", Namespace: "test"}, Status: olmv1alpha1.InstallPlanStatus{Phase: olmv1alpha1.InstallPlanPhaseFailed}},
				{ObjectMeta: metav1.ObjectMeta{Name: "test-name-2", Namespace: "test-2"}, Status: olmv1alpha1.InstallPlanStatus{Phase: olmv1alpha1.InstallPlanPhaseComplete}},
				{ObjectMeta: metav1.ObjectMeta{Name: "test-name-2", Namespace: "test-2"}, Status: olmv1alpha1.InstallPlanStatus{Phase: olmv1alpha1.InstallPlanPhaseInstalling}}}, nil).Times(1)
			mockk8sclient.EXPECT().DeleteInstallPlan(types.NamespacedName{Name: "test-name", Namespace: "test"}).Return(nil).Times(1)

			Expect(assistedController.waitForCSVBeCreated(operators)).Should(Equal(false))
		})
		It("initialized operator", func() {
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					Name: operatorName, Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm,
				},
			}
			mockk8sclient.EXPECT().ListJobs(gomock.Any()).Return(&batchV1.JobList{}, nil).Times(1)
			mockk8sclient.EXPECT().GetAllInstallPlansOfSubscription(gomock.Any()).Return([]olmv1alpha1.InstallPlan{}, nil).Times(1)
			mockk8sclient.EXPECT().GetCSVFromSubscription(operators[0].Namespace, operators[0].SubscriptionName).Return("randomCSV", nil).Times(1)
			mockk8sclient.EXPECT().GetCSV(operators[0].Namespace, gomock.Any()).Return(nil, nil).Times(1)
			Expect(assistedController.waitForCSVBeCreated(operators)).Should(Equal(true))
		})
	})

	Context("PostInstallConfigs", func() {
		Context("waiting for cluster version", func() {
			BeforeEach(func() {
				assistedController.WaitForClusterVersion = true
				GeneralWaitInterval = 1 * time.Millisecond
				logClusterOperatorsSuccess()
				mockGetClusterForCancel()
			})

			It("failure if console not available in service or failed to set status and success if available", func() {
				mockAllCapabilitiesEnabled()

				installing := models.ClusterStatusInstalling
				mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: &installing}, nil).Times(2)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(validConsoleOperator, nil).Times(2)
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), assistedController.ClusterID, consoleOperatorName, "", models.OperatorStatusAvailable, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), assistedController.ClusterID, consoleOperatorName, "", models.OperatorStatusAvailable, gomock.Any()).Return(nil).Times(1)

				setClusterAsFinalizing()
				uploadIngressCert(assistedController.ClusterID)
				setCvoAsAvailable()

				// Completion
				mockGetOLMOperators([]models.MonitoredOperator{})
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, gomock.Any(), nil).Return(nil).Times(1)
				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators,
					models.FinalizingStageAddingRouterCa,
					models.FinalizingStageWaitingForOlmOperatorsCsvInitialization,
					models.FinalizingStageDone,
				)

				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					assistedController.PostInstallConfigs(context.TODO(), &wg)
				}()
				wg.Wait()

				Expect(assistedController.status.HasError()).Should(Equal(false))
			})

			It("success", func() {
				mockAllCapabilitiesEnabled()

				installing := models.ClusterStatusInstalling
				mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: &installing}, nil).Times(1)
				setControllerWaitForOLMOperators(assistedController.ClusterID)
				setCvoAsAvailable()

				// Completion
				mockGetOLMOperators([]models.MonitoredOperator{})
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(nil).Times(1)
				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators,
					models.FinalizingStageAddingRouterCa,
					models.FinalizingStageWaitingForOlmOperatorsCsvInitialization,
					models.FinalizingStageDone,
				)

				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					assistedController.PostInstallConfigs(context.TODO(), &wg)
				}()
				wg.Wait()

				Expect(assistedController.status.HasError()).Should(Equal(false))
			})

			It("lots of failures then success", func() {
				mockAllCapabilitiesEnabled()

				installing := models.ClusterStatusInstalling
				mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: &installing}, nil).Times(1)
				setClusterAsFinalizing()

				// Console errors
				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(nil, fmt.Errorf("no-operator")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					&configv1.ClusterOperator{
						Status: configv1.ClusterOperatorStatus{
							Conditions: []configv1.ClusterOperatorStatusCondition{},
						},
					}, fmt.Errorf("no-conditions")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					getClusterOperatorWithCondition(configv1.OperatorDegraded, configv1.ConditionFalse),
					fmt.Errorf("false-degraded-condition")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					getClusterOperatorWithCondition(configv1.OperatorAvailable, configv1.ConditionTrue),
					fmt.Errorf("missing-degraded-condition")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					getClusterOperatorWithCondition(configv1.OperatorAvailable, configv1.ConditionFalse),
					fmt.Errorf("false-available-condition")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					getClusterOperatorWithCondition(configv1.OperatorAvailable, configv1.ConditionTrue),
					fmt.Errorf("true-degraded-condition")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					&configv1.ClusterOperator{
						Status: configv1.ClusterOperatorStatus{
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
							},
						},
					}, fmt.Errorf("missing-conditions")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					getClusterOperatorWithConditionsStatus(configv1.ConditionTrue, configv1.ConditionTrue),
					fmt.Errorf("bad-conditions-status")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					getClusterOperatorWithConditionsStatus(configv1.ConditionFalse, configv1.ConditionTrue),
					fmt.Errorf("bad-conditions-status")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusProgressing}})
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(
					getClusterOperatorWithConditionsStatus(configv1.ConditionFalse, configv1.ConditionFalse),
					fmt.Errorf("bad-conditions-status")).Times(1)

				setConsoleAsAvailable("cluster-id")
				uploadIngressCert(assistedController.ClusterID)

				// CVO errors
				mockGetServiceOperators([]models.MonitoredOperator{{Name: cvoOperatorName, Status: ""}})
				mockk8sclient.EXPECT().GetClusterVersion().Return(nil, fmt.Errorf("dummy")).Times(1)

				mockGetServiceOperators([]models.MonitoredOperator{{Name: cvoOperatorName, Status: ""}})
				mockk8sclient.EXPECT().GetClusterVersion().Return(progressClusterVersionCondition, nil).Times(1)
				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, "", models.OperatorStatusProgressing, progressClusterVersionCondition.Status.Conditions[0].Message).Times(1)

				// Fail 8 more times when console fail
				extraFailTimes := 8
				for i := 0; i < extraFailTimes; i++ {
					mockGetServiceOperators([]models.MonitoredOperator{{Name: cvoOperatorName, Status: ""}})
				}
				mockk8sclient.EXPECT().GetClusterVersion().Return(nil, fmt.Errorf("dummy")).Times(extraFailTimes)

				setCvoAsAvailable()

				mockGetOLMOperators([]models.MonitoredOperator{})
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(nil).Times(1)

				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, "")
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(0)
				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators,
					models.FinalizingStageAddingRouterCa,
					models.FinalizingStageWaitingForOlmOperatorsCsvInitialization,
					models.FinalizingStageDone,
				)

				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					assistedController.PostInstallConfigs(context.TODO(), &wg)
				}()
				wg.Wait()

				Expect(assistedController.status.HasError()).Should(Equal(false))
			})

			It("failure", func() {
				GeneralProgressUpdateInt = 1 * time.Millisecond

				mockAllCapabilitiesEnabled()
				setClusterAsFinalizing()

				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), consoleOperatorName, gomock.Any(), gomock.Any()).
					Return(&models.MonitoredOperator{Status: "", StatusInfo: ""}, nil).AnyTimes()
				mockk8sclient.EXPECT().GetClusterOperator(consoleOperatorName).Return(nil, fmt.Errorf("dummy")).AnyTimes()
				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).
					Return(&models.MonitoredOperator{Status: "", StatusInfo: ""}, nil).AnyTimes()
				mockk8sclient.EXPECT().GetClusterVersion().Return(nil, fmt.Errorf("dummy")).AnyTimes()

				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", false, gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, clusterId string, isSuccess bool, errorInfo string, data map[string]interface{}) error {
						validateClusterOperaotrReport(data)
						return nil
					}).Times(1)
				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators)

				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Millisecond)
					defer cancel()
					assistedController.PostInstallConfigs(ctx, &wg)
				}()
				wg.Wait()
				Expect(assistedController.status.HasError()).Should(Equal(true))
			})
		})

		Context("not waiting for cluster version", func() {
			BeforeEach(func() {
				assistedController.WaitForClusterVersion = false
				GeneralWaitInterval = 10 * time.Millisecond
				logClusterOperatorsSuccess()
				mockGetClusterForCancel()
			})
			It("success", func() {
				mockAllCapabilitiesEnabled()

				installing := models.ClusterStatusInstalling
				mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: &installing}, nil).Times(1)
				setControllerWaitForOLMOperators(assistedController.ClusterID)
				mockGetOLMOperators([]models.MonitoredOperator{})
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(nil).Times(1)
				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators,
					models.FinalizingStageAddingRouterCa,
					models.FinalizingStageWaitingForOlmOperatorsCsvInitialization,
					models.FinalizingStageDone,
				)

				wg.Add(1)
				ctx, cancel := context.WithTimeout(context.TODO(), 30*time.Millisecond)
				defer cancel()
				assistedController.PostInstallConfigs(ctx, &wg)
				wg.Wait()
				Expect(assistedController.status.HasError()).Should(Equal(false))
			})
			It("failure", func() {
				mockAllCapabilitiesEnabled()

				setClusterAsFinalizing()
				setConsoleAsAvailable("cluster-id")
				mockk8sclient.EXPECT().GetConfigMap(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("aaa")).MinTimes(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", false,
					"Stopped waiting for router ca data: context deadline exceeded", gomock.Any()).Return(nil).Times(1)

				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators, models.FinalizingStageAddingRouterCa)

				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Millisecond)
					defer cancel()
					assistedController.PostInstallConfigs(ctx, &wg)
				}()
				wg.Wait()
				Expect(assistedController.status.HasError()).Should(Equal(true))
			})
		})

		Context("waiting for OLM", func() {
			BeforeEach(func() {
				assistedController.WaitForClusterVersion = false
				logClusterOperatorsSuccess()
			})

			It("waiting for single OLM operator", func() {
				mockAllCapabilitiesEnabled()
				mockGetClusterForCancel()

				By("setup", func() {
					setControllerWaitForOLMOperators(assistedController.ClusterID)
					operators := []models.MonitoredOperator{
						{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: "", TimeoutSeconds: 120 * 60},
					}
					mockGetOLMOperators(operators)
					mockApplyPostInstallManifests(operators)
					mockk8sclient.EXPECT().GetCSVFromSubscription(operators[0].Namespace, operators[0].SubscriptionName).Return("local-storage-operator", nil).Times(2)
					mockk8sclient.EXPECT().GetCSV(operators[0].Namespace, operators[0].SubscriptionName).Return(&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseNone}}, nil).Times(2)
				})

				By("empty status", func() {
					mockGetServiceOperators([]models.MonitoredOperator{{Name: "lso", Status: ""}})
					mockGetCSV(
						models.MonitoredOperator{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", Name: "lso"},
						&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}},
					)
				})

				By("in progress", func() {
					mockGetServiceOperators([]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", Name: "lso", Status: models.OperatorStatusProgressing}})
					mockGetCSV(
						models.MonitoredOperator{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", Name: "lso"},
						&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}},
					)
					mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", "4.12.0", models.OperatorStatusProgressing, gomock.Any()).Return(nil).Times(1)
				})

				By("available", func() {
					mockGetServiceOperators([]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", Name: "lso", Status: models.OperatorStatusProgressing}})
					mockGetCSV(
						models.MonitoredOperator{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", Name: "lso"},
						&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseSucceeded}},
					)
					mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", "4.12.0", models.OperatorStatusAvailable, gomock.Any()).Return(nil).Times(1)

					mockGetServiceOperators([]models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", Name: "lso", Status: models.OperatorStatusAvailable}})
				})

				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(nil).Times(1)
				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators,
					models.FinalizingStageAddingRouterCa,
					models.FinalizingStageWaitingForOlmOperatorsCsvInitialization,
					models.FinalizingStageApplyingOlmManifests,
					models.FinalizingStageWaitingForOlmOperatorsCsv,
					models.FinalizingStageDone,
				)

				wg.Add(1)
				mockk8sclient.EXPECT().ListJobs(gomock.Any()).Return(&batchV1.JobList{}, nil).AnyTimes()
				mockk8sclient.EXPECT().GetAllInstallPlansOfSubscription(gomock.Any()).Return([]olmv1alpha1.InstallPlan{}, nil).AnyTimes()
				assistedController.PostInstallConfigs(context.TODO(), &wg)
				wg.Wait()
				Expect(assistedController.status.HasError()).Should(Equal(false))
				Expect(assistedController.status.HasOperatorError()).Should(Equal(false))
			})

			It("waiting for single OLM operator which timeouts", func() {
				mockAllCapabilitiesEnabled()
				mockGetClusterForCancel()

				By("setup", func() {
					setControllerWaitForOLMOperators(assistedController.ClusterID)
					operators := []models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusProgressing, TimeoutSeconds: 0}}
					mockApplyPostInstallManifests(operators)
					mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(operators, nil).AnyTimes()
				})

				By("endless empty status", func() {
					mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), "lso", gomock.Any(), gomock.Any()).Return(&models.MonitoredOperator{Name: "lso", Status: ""}, nil).AnyTimes()
					mockk8sclient.EXPECT().ListJobs(gomock.Any()).Return(&batchV1.JobList{}, nil).AnyTimes()
					mockk8sclient.EXPECT().GetAllInstallPlansOfSubscription(gomock.Any()).Return([]olmv1alpha1.InstallPlan{}, nil).AnyTimes()
					mockk8sclient.EXPECT().GetCSVFromSubscription("openshift-local-storage", "local-storage-operator").Return("lso-1.1", nil).AnyTimes()
					mockk8sclient.EXPECT().GetCSV("openshift-local-storage", "lso-1.1").Return(&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}}, nil).AnyTimes()
					mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", "0.0.0", models.OperatorStatusProgressing, gomock.Any()).Return(nil).AnyTimes()
				})

				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", "", models.OperatorStatusFailed, "Waiting for operator timed out").Return(nil).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(nil).Times(1)
				mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForClusterOperators,
					models.FinalizingStageAddingRouterCa,
					models.FinalizingStageWaitingForOlmOperatorsCsvInitialization,
					models.FinalizingStageApplyingOlmManifests,
					models.FinalizingStageWaitingForOlmOperatorsCsv,
					models.FinalizingStageDone,
				)
				ctx, cancel := context.WithTimeout(context.TODO(), 100*time.Millisecond)
				defer cancel()

				wg.Add(1)
				assistedController.PostInstallConfigs(ctx, &wg)
				wg.Wait()
				Expect(assistedController.status.HasError()).Should(Equal(false))
				Expect(assistedController.status.GetOperatorsInError()).To(ContainElement("lso"))
			})

			It("waiting for single OLM operator which continues installation after timeout occurs", func() {
				mockAllCapabilitiesEnabled()

				By("setup", func() {
					setControllerWaitForOLMOperators(assistedController.ClusterID)
					operators := []models.MonitoredOperator{{SubscriptionName: "local-storage-operator", Namespace: "openshift-local-storage", OperatorType: models.OperatorTypeOlm, Name: "lso", Status: models.OperatorStatusProgressing, TimeoutSeconds: 0}}
					mockApplyPostInstallManifests(operators)
					mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(operators, nil).AnyTimes()
				})

				By("endless empty status", func() {
					mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), "lso", gomock.Any(), gomock.Any()).Return(&models.MonitoredOperator{Name: "lso", Status: ""}, nil).AnyTimes()
					mockk8sclient.EXPECT().ListJobs(gomock.Any()).Return(&batchV1.JobList{}, nil).AnyTimes()
					mockk8sclient.EXPECT().GetAllInstallPlansOfSubscription(gomock.Any()).Return([]olmv1alpha1.InstallPlan{}, nil).AnyTimes()
					mockk8sclient.EXPECT().GetCSVFromSubscription("openshift-local-storage", "local-storage-operator").Return("lso-1.1", nil).AnyTimes()
					mockk8sclient.EXPECT().GetCSV("openshift-local-storage", "lso-1.1").Return(&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}}, nil).AnyTimes()
					mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", "0.0.0", models.OperatorStatusProgressing, gomock.Any()).Return(nil).AnyTimes()
				})

				mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", "lso", "", models.OperatorStatusFailed, "Waiting for operator timed out").Return(nil).Times(1)
				mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "", nil).Return(nil).Times(1)
				var (
					start        time.Time
					currentStage models.FinalizingStage
				)
				for _, stage := range []models.FinalizingStage{
					models.FinalizingStageWaitingForClusterOperators,
					models.FinalizingStageAddingRouterCa,
					models.FinalizingStageWaitingForOlmOperatorsCsvInitialization,
					models.FinalizingStageApplyingOlmManifests,
					models.FinalizingStageWaitingForOlmOperatorsCsv,
					models.FinalizingStageDone,
				} {
					mockbmclient.EXPECT().UpdateFinalizingProgress(gomock.Any(), gomock.Any(), stage).
						Do(func(_ context.Context, _ string, s models.FinalizingStage) {
							currentStage = s
							start = time.Now()
						},
						).Times(1)
				}

				wg.Add(1)
				mockbmclient.EXPECT().GetCluster(calledFromStageTimedOut(), false).
					DoAndReturn(func(ctx context.Context, withHosts bool) (*models.Cluster, error) {
						return &models.Cluster{
							Progress: &models.ClusterProgressInfo{
								FinalizingStage:         currentStage,
								FinalizingStageTimedOut: time.Since(start) > 100*time.Millisecond,
							},
						}, nil
					}).AnyTimes()
				assistedController.PostInstallConfigs(context.TODO(), &wg)
				wg.Wait()
				Expect(assistedController.status.HasError()).Should(Equal(false))
				Expect(assistedController.status.GetOperatorsInError()).To(ContainElement("lso"))
			})
		})

		Context("Patching node labels", func() {
			BeforeEach(func() {
				assistedController.WaitForClusterVersion = false
				GeneralWaitInterval = 10 * time.Millisecond
			})
			It("success", func() {
				nodeLabels := `{"node.ocs.openshift.io/storage":""}`
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeLabels)
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				listNodes()
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeLabels).Return(nil).Times(3)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("Get hosts fails and then succeeds", func() {
				nodeLabels := `{"node.ocs.openshift.io/storage":""}`
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeLabels)
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(nil, fmt.Errorf("dummy")).Times(1)
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				listNodes()
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeLabels).Return(nil).Times(3)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("k8s list nodes fails and then succeeds", func() {
				nodeLabels := `{"node.ocs.openshift.io/storage":""}`
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeLabels)
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(3)
				mockk8sclient.EXPECT().ListNodes().Return(nil, fmt.Errorf("dummy")).Times(2)
				mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeLabels).Return(nil).Times(3)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("k8s list nodes returns less nodes", func() {
				nodeLabels := `{"node.ocs.openshift.io/storage":""}`
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeLabels)
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(2)
				mockk8sclient.EXPECT().ListNodes().Return(&v1.NodeList{}, nil).Times(1)
				mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeLabels).Return(nil).Times(3)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("No hosts with labels", func() {
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, "")
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(0)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("2 with labels and one without", func() {
				nodeLabels := `{"node.ocs.openshift.io/storage":""}`
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeLabels)
				for hostName := range hosts {
					hosts[hostName].Host.NodeLabels = ""
					break
				}
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				listNodes()
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeLabels).Return(nil).Times(2)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			// 1. Set one node as not ready
			// 2. Patch labels on 2 other nodes
			// 3. Set last node as ready and patch labels for it
			It("Set label only in when nodes are ready", func() {
				nodeLabels := `{"node.ocs.openshift.io/storage":""}`
				k8sReadyNodes := GetKubeNodes(kubeNamesIds)
				k8sNodesWith1NotReady := k8sReadyNodes.DeepCopy()
				conds := k8sNodesWith1NotReady.Items[0].Status.Conditions
				for i, cond := range conds {
					if cond.Type == v1.NodeReady {
						conds[i].Status = v1.ConditionFalse
					}
				}
				k8sNodesWith1NotReady.Items[0].Status.Conditions = conds
				notReadyNodeName := k8sNodesWith1NotReady.Items[0].Name

				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeLabels)
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(2)

				// set node labels to k8s object for 2 already patched nodes
				k8sReadyNodes.Items[1].ObjectMeta.Labels["node.ocs.openshift.io/storage"] = ""
				k8sReadyNodes.Items[2].ObjectMeta.Labels["node.ocs.openshift.io/storage"] = ""

				// first run with 2 ready nodes
				gomock.InOrder(
					mockk8sclient.EXPECT().ListNodes().Return(k8sNodesWith1NotReady, nil).Times(1),
					mockk8sclient.EXPECT().ListNodes().Return(k8sReadyNodes, nil).Times(1),
				)

				gomock.InOrder(
					mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeLabels).Return(nil).Times(2),
					mockk8sclient.EXPECT().PatchNodeLabels(notReadyNodeName, nodeLabels).Return(nil).Times(1),
				)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("Set host labels but nodes are not workers, should not pause mcp", func() {
				nodeRoleLabel := fmt.Sprintf("{\"%s\": \"infra\"}", roleLabel)
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeRoleLabel)
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				listNodes()
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeRoleLabel).Return(nil).Times(3)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("Set host labels for worker but don't add custom mcp to it, should not pause", func() {
				nodeRoleLabel := fmt.Sprintf("{\"%s\": \"infra\"}", roleLabel)
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeRoleLabel)
				hosts["node0"].Host.Role = models.HostRoleWorker
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				listNodes()
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeRoleLabel).Return(nil).Times(3)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})

			It("Set host labels with custom mcp - validate pause mcp", func() {
				nodeRoleLabel := fmt.Sprintf("{\"%s\": \"infra\"}", roleLabel)
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeRoleLabel)
				hosts["node0"].Host.Role = models.HostRoleWorker
				hosts["node0"].Host.MachineConfigPoolName = "infra"
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				listNodes()
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeRoleLabel).Return(nil).Times(3)
				gomock.InOrder(
					mockk8sclient.EXPECT().PatchMachineConfigPoolPaused(true, workerMCPName).Return(nil).Times(1),
					mockk8sclient.EXPECT().PatchMachineConfigPoolPaused(false, workerMCPName).Return(nil).Times(1),
				)

				wg.Add(1)
				assistedController.UpdateNodeLabels(context.TODO(), &wg)
				wg.Wait()
			})
			It("Set host labels but pause failed", func() {
				nodeRoleLabel := fmt.Sprintf("{\"%s\": \"infra\"}", roleLabel)
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeRoleLabel)
				hosts["node0"].Host.Role = models.HostRoleWorker
				hosts["node0"].Host.MachineConfigPoolName = "infra"
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				mockk8sclient.EXPECT().PatchMachineConfigPoolPaused(true, workerMCPName).Return(fmt.Errorf("dummy")).Times(1)
				shouldRetry := assistedController.setNodesLabels()
				Expect(shouldRetry).To(Equal(false))
			})
			It("Set host labels but unpause failed", func() {
				nodeRoleLabel := fmt.Sprintf("{\"%s\": \"infra\"}", roleLabel)
				hosts := create3Hosts(models.HostStatusInstalled, models.HostStageDone, nodeRoleLabel)
				hosts["node0"].Host.Role = models.HostRoleWorker
				hosts["node0"].Host.MachineConfigPoolName = "infra"
				mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled, models.HostStatusError}).
					Return(hosts, nil).Times(1)
				listNodes()
				mockk8sclient.EXPECT().PatchNodeLabels(gomock.Any(), nodeRoleLabel).Return(nil).Times(3)
				gomock.InOrder(
					mockk8sclient.EXPECT().PatchMachineConfigPoolPaused(true, workerMCPName).Return(nil).Times(1),
					mockk8sclient.EXPECT().PatchMachineConfigPoolPaused(false, workerMCPName).Return(fmt.Errorf("dummy")).Times(1),
				)

				shouldRetry := assistedController.setNodesLabels()
				Expect(shouldRetry).To(Equal(false))
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
			_, err := os.OpenFile(common.ControllerLogFile, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
			Expect(err).ShouldNot(HaveOccurred())
			LogsUploadPeriod = 100 * time.Millisecond
			dnsValidationTimeout = 1 * time.Millisecond
			pod = v1.Pod{TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: v1.PodSpec{}, Status: v1.PodStatus{Phase: "Pending"}}
		})
		It("Validate upload logs, upload from file if get pods fails", func() {
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(10)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).MinTimes(1)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.UploadLogs(ctx, &wg, common.InvokerAssisted)
			time.Sleep(1 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs, upload from file if get pods fails and resolv_confs fails too", func() {
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(10)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).MinTimes(1)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.UploadLogs(ctx, &wg, common.InvokerAssisted)
			time.Sleep(1 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Validate upload log still sent even if GetPodLogsAsBuffer fails and log file doesn't exists", func() {
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			err := os.Remove(common.ControllerLogFile)
			Expect(err).ShouldNot(HaveOccurred())
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).MinTimes(1)
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(nil, fmt.Errorf("dummy")).MinTimes(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).MinTimes(1)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.UploadLogs(ctx, &wg, common.InvokerAssisted)
			time.Sleep(500 * time.Millisecond)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs, upload from file if GetPodLogsAsBuffer fails", func() {
			logClusterOperatorsSuccess()
			reportLogProgressSuccess()
			mockk8sclient.EXPECT().GetPods(assistedController.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).MinTimes(1)
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(nil, fmt.Errorf("dummy")).MinTimes(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).MinTimes(1)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go assistedController.UploadLogs(ctx, &wg, common.InvokerAssisted)
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
			err := assistedController.uploadSummaryLogs("test", assistedController.Namespace, controllerLogsSecondsAgo)
			Expect(err).To(HaveOccurred())
		})
		It("Validate upload logs happy flow (controllers logs only)", func() {
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedController.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).Times(1)
			reportLogProgressSuccess()
			err := assistedController.uploadSummaryLogs("test", assistedController.Namespace, controllerLogsSecondsAgo)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Upload logs with oc must-gather", func() {
		var pod v1.Pod
		var ctx context.Context
		var cancel context.CancelFunc

		callUploadLogs := func(waitTime time.Duration) {
			wg.Add(1)
			go assistedController.UploadLogs(ctx, &wg, common.InvokerAssisted)
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
			SummaryLogsPeriod = 10 * time.Millisecond
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
			logResolvConfSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), assistedController.MustGatherImage).Return("../../test_files/tartest.tar.gz", nil).Times(1)
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			assistedController.status.Error()
			callUploadLogs(150 * time.Millisecond)
		})

		It("Validate must-gather logs are not collected with no error", func() {
			successUpload()
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			callUploadLogs(50 * time.Millisecond)
		})

		It("Validate upload logs exits with no error + failed upload", func() {
			logClusterOperatorsSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), assistedController.ClusterID, models.LogsTypeController, gomock.Any()).Return(fmt.Errorf("dummy")).AnyTimes()
			callUploadLogs(50 * time.Millisecond)
		})

		It("Validate must-gather logs are retried on error - while cluster error occurred", func() {
			successUpload()
			logClusterOperatorsSuccess()
			logResolvConfSuccess()
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Return("", fmt.Errorf("failed"))
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), gomock.Any()).Return("../../test_files/tartest.tar.gz", nil)
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
			assistedController.status.Error()
			callUploadLogs(50 * time.Millisecond)
		})

		It("Validate router check will not run with no error", func() {
			assistedController.ControlPlaneCount = 1
			successUpload()
			logClusterOperatorsSuccess()
			mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Times(0).Return(&models.Cluster{Name: "test", BaseDNSDomain: "test.com"}, nil)
			callUploadLogs(50 * time.Millisecond)
		})

		It("Validate upload logs not blocked by router validation failure (with must-gather logs)", func() {
			assistedController.ControlPlaneCount = 1
			successUpload()
			logClusterOperatorsSuccess()
			logResolvConfSuccess()
			mockbmclient.EXPECT().GetCluster(gomock.Any(), false).MinTimes(1).Return(nil, fmt.Errorf("dummy"))
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), assistedController.MustGatherImage).Return("../../test_files/tartest.tar.gz", nil).Times(1)
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			assistedController.status.Error()
			callUploadLogs(300 * time.Millisecond)
		})

		It("Validate upload logs (with must-gather logs) with router status on sno", func() {
			assistedController.ControlPlaneCount = 1
			successUpload()
			mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Times(1).Return(&models.Cluster{Name: "test", BaseDNSDomain: "test.com"}, nil)
			logClusterOperatorsSuccess()
			logResolvConfSuccess()
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), assistedController.MustGatherImage).Return("../../test_files/tartest.tar.gz", nil).Times(1)
			assistedController.status.Error()
			callUploadLogs(300 * time.Millisecond)
		})

		It("Validate resolv conf failure will not block must-gather", func() {
			successUpload()
			logClusterOperatorsSuccess()
			mockops.EXPECT().ReadFile(gomock.Any()).Return(nil, fmt.Errorf("fummy")).MinTimes(3)
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any(), assistedController.MustGatherImage).Return("../../test_files/tartest.tar.gz", nil).Times(1)
			mockbmclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			assistedController.status.Error()
			callUploadLogs(300 * time.Millisecond)
		})
	})

	Context("must-gather image set parsing", func() {
		var ac *controller
		BeforeEach(func() {
			ac = newController(l, defaultTestControllerConf, mockops, mockbmclient, mockk8sclient, mockRebootsNotifier)
		})

		It("MustGatherImage is empty", func() {
			ac.MustGatherImage = ""
			Expect(ac.parseMustGatherImages()).To(BeEmpty())
		})
		It("MustGatherImage is string", func() {
			images := ac.parseMustGatherImages()
			Expect(images).NotTo(BeEmpty())
			Expect(images[0]).To(Equal(ac.MustGatherImage))
		})
		It("MustGatherImage is json", func() {
			ac.MustGatherImage = `{"ocp": "quay.io/openshift/must-gather", "cnv": "blah", "ocs": "foo"}`
			ac.status.Error()
			ac.status.OperatorError("cnv")
			images := ac.parseMustGatherImages()
			Expect(len(images)).To(Equal(2))
			Expect(images).To(ContainElement("quay.io/openshift/must-gather"))
			Expect(images).To(ContainElement("blah"))
		})
	})

	Context("waitForOLMOperators", func() {
		var (
			operatorName     = "lso"
			subscriptionName = "local-storage-operator"
			namespaceName    = "openshift-local-storage"
			cancel           context.CancelFunc
			ctx              context.Context
		)

		BeforeEach(func() {
			// run once
			GeneralWaitInterval = 200 * time.Millisecond
			ctx, cancel = context.WithTimeout(context.TODO(), 150*time.Millisecond)
			mockGetClusterForCancel()
		})

		AfterEach(func() { cancel() })

		It("List is empty", func() {
			mockbmclient.EXPECT().GetClusterMonitoredOLMOperators(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.MonitoredOperator{}, nil).Times(1)
			mockSuccessUpdateFinalizingStages(models.FinalizingStageWaitingForOlmOperatorsCsvInitialization)

			Expect(assistedController.waitForOLMOperators(context.TODO())).To(BeNil())
		})
		It("progressing - no update (empty message)", func() {
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					Name: operatorName, Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm,
				},
			}

			mockGetOLMOperators(operators)
			mockGetServiceOperators(operators)
			mockGetCSV(
				operators[0],
				&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}},
			)
			Expect(assistedController.waitForCSV(ctx)).To(HaveOccurred())
		})
		It("progressing - no update (same message)", func() {
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					Name: operatorName, Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm,
					StatusInfo: "same",
				},
			}

			mockGetOLMOperators(operators)
			mockGetServiceOperators(operators)
			mockGetCSV(
				operators[0],
				&olmv1alpha1.ClusterServiceVersion{
					Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling, Message: "same"},
				},
			)
			Expect(assistedController.waitForCSV(ctx)).To(HaveOccurred())
		})
		It("progressing - update (new message)", func() {
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					Name: operatorName, Status: models.OperatorStatusProgressing, OperatorType: models.OperatorTypeOlm,
					StatusInfo: "old",
				},
			}

			mockGetOLMOperators(operators)
			mockGetServiceOperators(operators)
			mockGetCSV(
				operators[0],
				&olmv1alpha1.ClusterServiceVersion{
					Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling, Message: "new"},
				},
			)

			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), "cluster-id", operatorName, "4.12.0", gomock.Any(), gomock.Any()).Return(nil).Times(1)
			Expect(assistedController.waitForCSV(ctx)).To(HaveOccurred())
		})
		It("check that we tolerate the failed state reported by CSV", func() {
			cancel()
			ctx, cancel = context.WithTimeout(context.TODO(), 1500*time.Millisecond)

			operators := []models.MonitoredOperator{
				{
					SubscriptionName: subscriptionName, Namespace: namespaceName,
					OperatorType: models.OperatorTypeOlm, Name: operatorName, Status: models.OperatorStatusProgressing, TimeoutSeconds: 1,
				},
			}

			mockGetOLMOperators(operators)

			mockGetServiceOperators(operators)
			mockGetCSV(
				operators[0],
				&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseFailed}},
			)

			mockGetServiceOperators(operators)
			mockGetCSV(
				operators[0],
				&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseSucceeded}},
			)
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), operatorName, "4.12.0", models.OperatorStatusAvailable, gomock.Any()).Return(nil).Times(1)

			newOperators := make([]models.MonitoredOperator, 0)
			newOperators = append(newOperators, operators...)
			newOperators[0].Status = models.OperatorStatusAvailable
			mockGetServiceOperators(newOperators)
			Expect(assistedController.waitForCSV(ctx)).To(BeNil())
		})

		It("multiple OLMs", func() {
			cancel()
			ctx, cancel = context.WithTimeout(context.TODO(), 1500*time.Millisecond)
			operators := []models.MonitoredOperator{
				{
					SubscriptionName: "subscription-1", Namespace: "namespace-1",
					OperatorType: models.OperatorTypeOlm, Name: "operator-1", Status: models.OperatorStatusProgressing, TimeoutSeconds: 120 * 60,
				},
				{
					SubscriptionName: "subscription-2", Namespace: "namespace-2",
					OperatorType: models.OperatorTypeOlm, Name: "operator-2", Status: models.OperatorStatusProgressing, TimeoutSeconds: 120 * 60,
				},
				{
					SubscriptionName: "subscription-3", Namespace: "namespace-3",
					OperatorType: models.OperatorTypeOlm, Name: "operator-3", Status: models.OperatorStatusProgressing, TimeoutSeconds: 120 * 60,
				},
			}

			mockGetOLMOperators(operators)

			By("first is available", func() {
				newOperators := make([]models.MonitoredOperator, 0)
				newOperators = append(newOperators, operators...)
				newOperators[0].Status = models.OperatorStatusAvailable
				mockGetServiceOperators(newOperators)

				mockGetCSV(
					newOperators[1],
					&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}},
				)
				mockGetCSV(
					newOperators[2],
					&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}},
				)
			})

			By("last is available", func() {
				newerOperators := make([]models.MonitoredOperator, 0)
				newerOperators = append(newerOperators, operators[1], operators[2])
				newerOperators[1].Status = models.OperatorStatusAvailable
				mockGetServiceOperators(newerOperators)

				mockGetCSV(
					newerOperators[0],
					&olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: olmv1alpha1.CSVPhaseInstalling}},
				)
			})

			lastOne := []models.MonitoredOperator{operators[1]}
			lastOne[0].Status = models.OperatorStatusAvailable
			mockGetServiceOperators(lastOne)

			Expect(assistedController.waitForCSV(ctx)).To(BeNil())
		})
	})

	Context("waitingForClusterOperators", func() {
		var (
			ctx    context.Context
			cancel context.CancelFunc
		)
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
			assistedController.WaitForClusterVersion = true
			GeneralProgressUpdateInt = 100 * time.Millisecond
			ctx, cancel = context.WithTimeout(context.TODO(), 150*time.Millisecond)
			CVOMaxTimeout = 1 * time.Second

			mockGetServiceOperators([]models.MonitoredOperator{{Name: consoleOperatorName, Status: models.OperatorStatusAvailable}})
		})

		AfterEach(func() {
			cancel()
		})

		for i := range tests {
			t := tests[i]
			It(t.name, func() {
				mockAllCapabilitiesEnabled()

				clusterVersionReport := &configv1.ClusterVersion{
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{t.newCVOCondition},
					},
				}
				newServiceCVOStatus := &models.MonitoredOperator{
					Status:     operatorTypeToOperatorStatus(t.newCVOCondition.Type),
					StatusInfo: t.newCVOCondition.Message,
				}

				mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).Return(t.currentServiceCVOStatus, nil).Times(1)
				if newServiceCVOStatus.Status != models.OperatorStatusAvailable {
					mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).Return(newServiceCVOStatus, nil).Times(1)
				}
				if t.shouldSendUpdate {

					mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, "", gomock.Any(), gomock.Any()).Times(1)
				}

				amountOfSamples := 0
				if t.currentServiceCVOStatus.Status != models.OperatorStatusAvailable {
					amountOfSamples++
				}
				mockk8sclient.EXPECT().GetClusterVersion().Return(clusterVersionReport, nil).MinTimes(amountOfSamples)

				if newServiceCVOStatus.Status == models.OperatorStatusAvailable {
					Expect(assistedController.waitingForClusterOperators(ctx)).ShouldNot(HaveOccurred())
				} else {
					Expect(assistedController.waitingForClusterOperators(ctx)).Should(HaveOccurred())
				}
			})
		}

		It("service fail to sync - context cancel", func() {
			mockAllCapabilitiesEnabled()

			currentServiceCVOStatus := &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""}
			clusterVersionReport := &configv1.ClusterVersion{
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: ""},
					},
				},
			}

			mockk8sclient.EXPECT().GetClusterVersion().Return(clusterVersionReport, nil).AnyTimes()
			mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).Return(currentServiceCVOStatus, nil).AnyTimes()
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, "", gomock.Any(), gomock.Any()).Return(fmt.Errorf("dummy")).AnyTimes()

			err := func() error {
				ctxTimeout, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()
				return assistedController.waitingForClusterOperators(ctxTimeout)
			}()
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		})

		It("service fail to sync - maxTimeout applied", func() {
			mockAllCapabilitiesEnabled()

			WaitTimeout = 1 * time.Second
			CVOMaxTimeout = 200 * time.Millisecond
			currentServiceCVOStatus := &models.MonitoredOperator{Status: models.OperatorStatusProgressing, StatusInfo: ""}
			clusterVersionReport := &configv1.ClusterVersion{
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: ""},
					},
				},
			}

			mockk8sclient.EXPECT().GetClusterVersion().Return(clusterVersionReport, nil).AnyTimes()
			mockbmclient.EXPECT().GetClusterMonitoredOperator(gomock.Any(), gomock.Any(), cvoOperatorName, gomock.Any(), gomock.Any()).Return(currentServiceCVOStatus, nil).AnyTimes()
			mockbmclient.EXPECT().UpdateClusterOperator(gomock.Any(), gomock.Any(), cvoOperatorName, "", gomock.Any(), gomock.Any()).Return(fmt.Errorf("dummy")).AnyTimes()

			err := func() error {
				return assistedController.waitingForClusterOperators(ctx)
			}()

			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
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
		It("Kill service and DNS pods if DNS service IP is taken in IPV6 env", func() {
			mockk8sclient.EXPECT().GetServiceNetworks().Return([]string{"2002:db8::/64"}, nil)
			returnServiceWithAddress(conflictServiceName, conflictServiceNamespace, "2002:db8::a")
			mockk8sclient.EXPECT().DeleteService(conflictServiceName, conflictServiceNamespace).Return(nil)
			mockk8sclient.EXPECT().DeletePods(dnsOperatorNamespace).Return(nil)
			returnServiceWithAddress(dnsServiceName, dnsServiceNamespace, "2002:db8::a")
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
	file, _ := os.ReadFile("../../test_files/node.json")
	var node v1.Node
	err := json.Unmarshal(file, &node)
	Expect(err).ToNot(HaveOccurred())
	nodeList := &v1.NodeList{}
	for name, id := range kubeNamesIds {
		node.Status.NodeInfo.SystemUUID = id
		node.Name = name
		newNode := node.DeepCopy()
		nodeList.Items = append(nodeList.Items, *newNode)
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

func create3Hosts(currentStatus string, stage models.HostStage, nodeLabels string) map[string]inventory_client.HostData {
	currentState := models.HostProgressInfo{CurrentStage: stage}
	infraEnvId := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f50")
	node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
	node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
	node2Id := strfmt.UUID("b898d516-3e16-49d0-86a5-0ad5bd04e3ed")
	return map[string]inventory_client.HostData{
		"node0": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node0Id, NodeLabels: nodeLabels, Progress: &currentState, Status: &currentStatus}},
		"node1": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node1Id, NodeLabels: nodeLabels, Progress: &currentState, Status: &currentStatus}},
		"node2": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node2Id, NodeLabels: nodeLabels, Progress: &currentState, Status: &currentStatus}}}
}

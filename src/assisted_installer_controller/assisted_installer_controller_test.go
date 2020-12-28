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

	"github.com/go-openapi/strfmt"
	"github.com/golang/mock/gomock"
	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"k8s.io/api/certificates/v1beta1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
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
		status            *ControllerStatus
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
		currentStatus := models.HostStatusInstallingInProgress
		inventoryNamesIds = map[string]inventory_client.HostData{
			"node0": {Host: &models.Host{ID: &node0Id, Progress: &currentState, Status: &currentStatus}},
			"node1": {Host: &models.Host{ID: &node1Id, Progress: &currentState, Status: &currentStatus}},
			"node2": {Host: &models.Host{ID: &node2Id, Progress: &currentState, Status: &currentStatus}}}
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
			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
				models.HostStatusInstalled}).Return(inventoryNamesIds, nil).Times(1)
		}
		mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
			models.HostStatusInstalled}).Return(map[string]inventory_client.HostData{}, nil).Times(1)
		return inventoryNamesIds
	}
	configuringSuccess := func() {
		mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()
		mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
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

	Context("Waiting for 3 nodes", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "4.7",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			status = &ControllerStatus{}
		})
		It("WaitAndUpdateNodesStatus happy flow", func() {
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			getInventoryNodes(1)
			configuringSuccess()
			listNodes()
			c.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})

		It("WaitAndUpdateNodesStatus getHost failure once", func() {
			updateProgressSuccess(defaultStages, inventoryNamesIds)
			configuringSuccess()
			listNodes()

			mockbmclient.EXPECT().GetHosts(gomock.Any(), gomock.Any(), []string{models.HostStatusDisabled,
				models.HostStatusInstalled}).Return(map[string]inventory_client.HostData{}, fmt.Errorf("dummy")).Times(1)
			getInventoryNodes(1)

			c.WaitAndUpdateNodesStatus(status)
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
			c.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(true))
		})
	})
	Context("Waiting for 3 nodes, will appear one by one", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "4.7",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			status = &ControllerStatus{}

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
			c.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})
	})
	Context("UpdateStatusFails and then succeeds", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "4.7",
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
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), hostIds[i], stage, "").Return(fmt.Errorf("dummy")).Times(1)
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), hostIds[i], stage, "").Return(nil).Times(1)
				}
			}
			mockk8sclient.EXPECT().ListNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(2)
			updateProgressSuccessFailureTest(defaultStages, inventoryNamesIds)
			getInventoryNodes(2)
			configuringSuccess()
			c.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})
	})
	Context("ListNodes fails and then succeeds", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "4.7",
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
			c.WaitAndUpdateNodesStatus(status)
			Expect(status.HasError()).Should(Equal(false))
		})
	})
	Context("validating ApproveCsrs", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "4.7",
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
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "4.7",
		}
		BeforeEach(func() {
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			GeneralWaitInterval = 1 * time.Second
			status = &ControllerStatus{}
		})
		It("Run addRouterCAToClusterCA happy flow", func() {
			cmName := "default-ingress-cert"
			cmNamespace := "openshift-config-managed"
			data := make(map[string]string)
			data["ca-bundle.crt"] = "CA"
			cm := v1.ConfigMap{Data: data}
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], c.ClusterID).Return(nil).Times(1)
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
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], c.ClusterID).Return(fmt.Errorf("dummy")).Times(1)
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
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&models.Cluster{Status: &installing}, nil).Times(1)
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&cluster, nil).Times(1)
			mockk8sclient.EXPECT().GetConfigMap(cmNamespace, cmName).Return(&cm, nil).Times(1)
			mockbmclient.EXPECT().UploadIngressCa(gomock.Any(), data["ca-bundle.crt"], c.ClusterID).Return(nil).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any(), "").Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any(), "").Return([]v1.Pod{{Status: v1.PodStatus{Phase: "Pending"}}}, nil).Times(1)
			mockk8sclient.EXPECT().GetPods(consoleNamespace, gomock.Any(), "").Return([]v1.Pod{{Status: v1.PodStatus{Phase: "Running"}}}, nil).Times(1)
			mockk8sclient.EXPECT().GetClusterVersion("version").Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().GetClusterVersion("version").Return(badClusterVersion, nil).Times(1)
			mockk8sclient.EXPECT().GetClusterVersion("version").Return(goodClusterVersion, nil).Times(1)

			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", true, "").Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateClusterInstallProgress(gomock.Any(), gomock.Any(), gomock.Any()).MinTimes(1)

			wg.Add(1)
			go c.PostInstallConfigs(&wg, status)
			wg.Wait()

			Expect(status.HasError()).Should(Equal(false))
		})
		It("Run PostInstallConfigs failed", func() {
			WaitTimeout = 2 * time.Second
			finalizing := models.ClusterStatusFinalizing
			badClusterVersion := &configv1.ClusterVersion{}
			badClusterVersion.Status.Conditions = []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status: configv1.ConditionFalse}}
			cluster := models.Cluster{Status: &finalizing}
			mockbmclient.EXPECT().GetCluster(gomock.Any()).Return(&cluster, nil).Times(1)

			mockbmclient.EXPECT().CompleteInstallation(gomock.Any(), "cluster-id", false, "Timeout while waiting for cluster "+
				"version to be available").Return(nil).Times(1)

			wg.Add(1)
			go c.PostInstallConfigs(&wg, status)
			wg.Wait()
			Expect(status.HasError()).Should(Equal(true))
		})
	})

	Context("update BMHs", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "4.7",
		}
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
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			GeneralWaitInterval = 1 * time.Second
		})
		It("provisioning exists, early return", func() {
			mockk8sclient.EXPECT().IsMetalProvisioningExists().Return(true, nil)
			wg.Add(1)
			go c.UpdateBMHs(&wg)
			wg.Wait()
		})
		It("normal path", func() {
			expect1 := &metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-worker-0",
					Annotations: map[string]string{
						metal3v1alpha1.StatusAnnotation: string(annBytes),
					},
				},
				Status: bmhStatus,
			}

			mockk8sclient.EXPECT().UpdateBMHStatus(expect1).Return(nil)

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
			c.updateBMHs(bmhList, &machineList)
		})
	})

	Context("Upload logs", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			Namespace:        "assisted-installer",
			OpenshiftVersion: "4.7",
		}
		var pod v1.Pod
		BeforeEach(func() {
			status = &ControllerStatus{}
			LogsUploadPeriod = 100 * time.Millisecond
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			pod = v1.Pod{TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: v1.PodSpec{}, Status: v1.PodStatus{Phase: "Pending"}}
		})
		It("Validate upload logs, get pod fails", func() {
			mockk8sclient.EXPECT().GetPods(conf.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return(nil, fmt.Errorf("dummy")).MinTimes(2).MaxTimes(10)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go c.UploadLogs(ctx, cancel, &wg, status)
			time.Sleep(1 * time.Second)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs, Get pods logs failed", func() {
			mockk8sclient.EXPECT().GetPods(conf.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).MinTimes(1)
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(conf.Namespace, "test", gomock.Any()).Return(nil, fmt.Errorf("dummy")).MinTimes(1)
			ctx, cancel := context.WithCancel(context.Background())
			wg.Add(1)
			go c.UploadLogs(ctx, cancel, &wg, status)
			time.Sleep(500 * time.Millisecond)
			cancel()
			wg.Wait()
		})
		It("Validate upload logs (controllers logs only), Upload failed", func() {
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(conf.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), conf.ClusterID, models.LogsTypeController, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			err := c.uploadSummaryLogs("test", conf.Namespace, controllerLogsSecondsAgo, false)
			Expect(err).To(HaveOccurred())
		})
		It("Validate upload logs happy flow (controllers logs only)", func() {
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(conf.Namespace, "test", gomock.Any()).Return(r, nil).Times(1)
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), conf.ClusterID, models.LogsTypeController, gomock.Any()).Return(nil).Times(1)
			err := c.uploadSummaryLogs("test", conf.Namespace, controllerLogsSecondsAgo, false)
			Expect(err).NotTo(HaveOccurred())
		})

	})

	Context("Upload logs with oc must-gather", func() {
		conf := ControllerConfig{
			ClusterID:        "cluster-id",
			URL:              "https://assisted-service.com:80",
			Namespace:        "assisted-installer",
			OpenshiftVersion: "4.7",
		}

		var pod v1.Pod
		var ctx context.Context
		var cancel context.CancelFunc

		callUploadLogs := func() {
			wg.Add(1)
			go c.UploadLogs(ctx, cancel, &wg, status)
			time.Sleep(150 * time.Millisecond)
			cancel()
			wg.Wait()
		}

		BeforeEach(func() {
			status = &ControllerStatus{}
			LogsUploadPeriod = 100 * time.Millisecond
			c = NewController(l, conf, mockops, mockbmclient, mockk8sclient)
			pod = v1.Pod{TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: v1.PodSpec{}, Status: v1.PodStatus{Phase: "Pending"}}

			ctx, cancel = context.WithCancel(context.Background())
			r := bytes.NewBuffer([]byte("test"))
			mockk8sclient.EXPECT().GetPods(conf.Namespace, gomock.Any(), fmt.Sprintf("status.phase=%s", v1.PodRunning)).Return([]v1.Pod{pod}, nil).AnyTimes()
			mockk8sclient.EXPECT().GetPodLogsAsBuffer(conf.Namespace, "test", gomock.Any()).Return(r, nil).AnyTimes()
			mockbmclient.EXPECT().UploadLogs(gomock.Any(), conf.ClusterID, models.LogsTypeController, gomock.Any()).DoAndReturn(
				func(ctx context.Context, clusterId string, logsType models.LogsType, reader io.Reader) error {
					_, _ = new(bytes.Buffer).ReadFrom(reader)
					return nil
				}).AnyTimes()
		})
		It("Validate upload logs (with must-gather logs)", func() {
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any()).Return("../../test_files/tartest.tar.gz", nil).Times(1)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			status.Error()
			callUploadLogs()
		})

		It("Validate must-gather logs are not collected with no error", func() {
			mockops.EXPECT().GetMustGatherLogs(gomock.Any(), gomock.Any()).Times(0)
			mockbmclient.EXPECT().DownloadFile(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			callUploadLogs()
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

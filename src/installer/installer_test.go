package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pkg/errors"

	"github.com/go-openapi/strfmt"

	v1 "k8s.io/api/core/v1"

	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-service/models"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

var _ = Describe("installer HostRoleMaster role", func() {
	var (
		l                  = logrus.New()
		ctrl               *gomock.Controller
		mockops            *ops.MockOps
		mockbmclient       *inventory_client.MockInventoryClient
		mockk8sclient      *k8s_client.MockK8SClient
		installerObj       *installer
		hostId             = "host-id"
		bootstrapIgn       = "bootstrap.ign"
		masterIgn          = "master.ign"
		workerIgn          = "worker.ign"
		openShiftVersion   = "4.4"
		inventoryNamesHost map[string]inventory_client.HostData
		kubeNamesIds       map[string]string
	)
	generalWaitTimeout = 100 * time.Minute
	device := "/dev/vda"
	l.SetOutput(ioutil.Discard)
	mkdirSuccess := func() {
		mockops.EXPECT().Mkdir(InstallDir).Return(nil).Times(1)
	}
	downloadFileSuccess := func(fileName string) {
		mockbmclient.EXPECT().DownloadFile(fileName, filepath.Join(InstallDir, fileName)).Return(nil).Times(1)
	}

	cleanInstallDevice := func() {
		mockops.EXPECT().GetVGByPV(device).Return("", nil).Times(1)
	}

	updateProgressSuccess := func(stages [][]string) {
		for _, stage := range stages {
			if len(stage) == 2 {
				mockbmclient.EXPECT().UpdateHostInstallProgress(hostId, models.HostStage(stage[0]), stage[1]).Return(nil).Times(1)
			} else {
				mockbmclient.EXPECT().UpdateHostInstallProgress(hostId, models.HostStage(stage[0]), "").Return(nil).Times(1)
			}
		}
	}

	writeToDiskSuccess := func() {
		mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device, mockbmclient).Return(nil).Times(1)
	}

	uploadLogsSuccess := func(bootstrap bool) {
		mockops.EXPECT().UploadInstallationLogs(bootstrap).Return("dummy", nil).Times(1)
	}

	waitForControllerSuccessfully := func(clusterId string) {
		mockk8sclient.EXPECT().GetPods("assisted-installer", gomock.Any(), "").Return([]v1.Pod{{TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{Name: assistedControllerPrefix + "aasdasd"},
			Status:     v1.PodStatus{Phase: "Running"}}}, nil).Times(1)
		r := bytes.NewBuffer([]byte("test"))
		mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedControllerNamespace, assistedControllerPrefix+"aasdasd", gomock.Any()).Return(r, nil).Times(1)
		mockbmclient.EXPECT().UploadLogs(clusterId, models.LogsTypeController, gomock.Any()).Return(nil).Times(1)
	}

	resolvConfSuccess := func() {
		mockops.EXPECT().ReloadHostFile("/etc/resolv.conf").Return(nil).Times(1)
	}

	rebootSuccess := func() {
		mockops.EXPECT().Reboot().Return(nil).Times(1)
	}
	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
		node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
		node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
		node2Id := strfmt.UUID("b898d516-3e16-49d0-86a5-0ad5bd04e3ed")
		inventoryNamesHost = map[string]inventory_client.HostData{"node0": {Host: &models.Host{ID: &node0Id}, IPs: []string{"192.168.126.10"}},
			"node1": {Host: &models.Host{ID: &node1Id}, IPs: []string{"192.168.126.11"}},
			"node2": {Host: &models.Host{ID: &node2Id}, IPs: []string{"192.168.126.12"}}}
	})
	k8sBuilder := func(configPath string, logger *logrus.Logger) (k8s_client.K8SClient, error) {
		return mockk8sclient, nil
	}

	Context("Bootstrap role", func() {
		conf := config.Config{Role: string(models.HostRoleBootstrap),
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		mcoImage, _ := utils.GetMCOByOpenshiftVersion(conf.OpenshiftVersion)
		extractIgnitionToFS := func(out string, err error) {
			mockops.EXPECT().ExecPrivilegeCommand(
				gomock.Any(), "podman", "run", "--net", "host",
				"--volume", "/:/rootfs:rw",
				"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
				"--privileged",
				"--entrypoint", "/usr/bin/machine-config-daemon",
				mcoImage,
				"start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from",
				filepath.Join(InstallDir, bootstrapIgn), "--skip-reboot").Return(out, err)
		}
		daemonReload := func(err error) {
			mockops.EXPECT().SystemctlAction("daemon-reload").Return(err).Times(1)
		}
		restartNetworkManager := func(err error) {
			mockops.EXPECT().SystemctlAction("restart", "NetworkManager.service").Return(err).Times(1)
		}
		startServicesSuccess := func() {
			services := []string{"bootkube.service", "progress.service", "approve-csr.service"}
			for i := range services {
				mockops.EXPECT().SystemctlAction("start", services[i]).Return(nil).Times(1)
			}
		}
		WaitMasterNodesSucccess := func() {
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(inventoryNamesHost, nil).AnyTimes()
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(map[string]string{}), nil).Times(1)
			kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65"}
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(inventoryNamesHost["node0"].Host.ID.String(), models.HostStageJoined, "").Times(1)
			kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
				"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238"}
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(inventoryNamesHost["node1"].Host.ID.String(), models.HostStageJoined, "").Times(1)
		}
		patchEtcdSuccess := func() {
			mockk8sclient.EXPECT().PatchEtcd().Return(nil).Times(1)
		}
		prepareControllerSuccess := func() {
			mockops.EXPECT().PrepareController().Return(nil).Times(1)
		}
		waitForBootkubeSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "stat", "/opt/openshift/.bootkube.done").Return("OK", nil).Times(1)
		}
		bootkubeStatusSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "status", "bootkube.service").Return("1", nil).Times(1)
		}

		extractSecretFromIgnitionSuccess := func() {
			mockops.EXPECT().ExtractFromIgnition(filepath.Join(InstallDir, bootstrapIgn), dockerConfigFile).Return(nil).Times(1)
		}

		bootstrapSetup := func() {
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractIgnitionToFS("Sוuccess", nil)
			daemonReload(nil)
		}

		It("bootstrap role happy flow", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageWaitingForControlPlane)},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			bootstrapSetup()
			restartNetworkManager(nil)
			prepareControllerSuccess()
			startServicesSuccess()
			patchEtcdSuccess()
			WaitMasterNodesSucccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			resolvConfSuccess()
			waitForControllerSuccessfully(conf.ClusterID)
			//HostRoleMaster flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			uploadLogsSuccess(true)
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("bootstrap role fail", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageWaitingForControlPlane)},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
			})
			bootstrapSetup()
			restartNetworkManager(nil)
			prepareControllerSuccess()
			startServicesSuccess()
			WaitMasterNodesSucccess()
			err := fmt.Errorf("Etcd patch failed")
			mockk8sclient.EXPECT().PatchEtcd().Return(err).Times(1)
			//HostRoleMaster flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("bootstrap role extract ignition retry", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageWaitingForControlPlane)},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			extractIgnitionToFS("extract failure", fmt.Errorf("extract failed"))
			bootstrapSetup()
			restartNetworkManager(nil)
			prepareControllerSuccess()
			startServicesSuccess()
			patchEtcdSuccess()
			WaitMasterNodesSucccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			resolvConfSuccess()
			waitForControllerSuccessfully(conf.ClusterID)
			//HostRoleMaster flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			uploadLogsSuccess(true)
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("bootstrap role extract ignition retry exhausted", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageWaitingForControlPlane)},
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			extractSecretFromIgnitionSuccess()
			extractIgnitionToFS("extract failure", fmt.Errorf("extract failed"))
			extractIgnitionToFS("extract failure", fmt.Errorf("extract failed"))
			extractIgnitionToFS("extract failure", fmt.Errorf("extract failed"))
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(fmt.Errorf("extract failed")))
		})

		It("bootstrap fail to restart NetworkManager", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageWaitingForControlPlane)},
			})
			bootstrapSetup()
			err := fmt.Errorf("Failed to restart NetworkManager")
			restartNetworkManager(err)
			//HostRoleMaster flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("waitForController reload resolv.conf failed", func() {
			mockops.EXPECT().ReloadHostFile("/etc/resolv.conf").Return(fmt.Errorf("dummy")).Times(1)
			err := installerObj.waitForController()
			Expect(err).To(HaveOccurred())
		})
		It("waitForController reload get pods fails then succeeds", func() {
			resolvConfSuccess()
			mockk8sclient.EXPECT().GetPods("assisted-installer", gomock.Any(), "").Return(nil, fmt.Errorf("dummy")).Times(1)
			waitForControllerSuccessfully(conf.ClusterID)
			err := installerObj.waitForController()
			Expect(err).NotTo(HaveOccurred())
		})
		It("Configuring state", func() {
			var logs string
			generalWaitTimeout = 100 * time.Millisecond
			logsInBytes, _ := ioutil.ReadFile("../../test_files/mcs_logs.txt")
			logs = string(logsInBytes)
			node0Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
			node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")

			testInventoryIdsIps := map[string]inventory_client.HostData{"node0": {Host: &models.Host{ID: &node0Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}},
				IPs: []string{"192.168.126.10", "192.168.11.122", "fe80::5054:ff:fe9a:4738"}},
				"node1": {Host: &models.Host{ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(testInventoryIdsIps, nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("", fmt.Errorf("dummy")).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return(logs, nil).AnyTimes()

			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), models.HostStageConfiguring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress("eb82821f-bf21-4614-9a3b-ecb07929f240", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress("eb82821f-bf21-4614-9a3b-ecb07929f239", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			go installerObj.updateConfiguringStatus(ctx)
			time.Sleep(1 * time.Second)
		})
		It("Configuring state, all hosts were set", func() {
			var logs string
			generalWaitTimeout = 100 * time.Millisecond
			logsInBytes, _ := ioutil.ReadFile("../../test_files/mcs_logs.txt")
			logs = string(logsInBytes)
			node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")

			testInventoryIdsIps := map[string]inventory_client.HostData{
				"node1": {Host: &models.Host{ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts().Return(testInventoryIdsIps, nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("", fmt.Errorf("dummy")).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return(logs, nil).AnyTimes()

			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), models.HostStageConfiguring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress("eb82821f-bf21-4614-9a3b-ecb07929f240", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress("eb82821f-bf21-4614-9a3b-ecb07929f239", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			installerObj.updateConfiguringStatus(ctx)
		})
	})
	Context("Master role", func() {
		conf := config.Config{Role: string(models.HostRoleMaster),
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		It("master role happy flow", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			uploadLogsSuccess(false)
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("HostRoleMaster role happy flow with disk cleanup", func() {
			cleanInstallDeviceClean := func() {
				mockops.EXPECT().GetVGByPV(device).Return("vg1", nil).Times(1)
				mockops.EXPECT().RemoveVG("vg1").Return(nil).Times(1)
				mockops.EXPECT().RemovePV(device).Return(nil).Times(1)
			}
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
			cleanInstallDeviceClean()
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to cleanup disk", func() {
			err := fmt.Errorf("Failed to remove vg")
			cleanInstallDeviceError := func() {
				mockops.EXPECT().GetVGByPV(device).Return("vg1", nil).Times(1)
				mockops.EXPECT().RemoveVG("vg1").Return(err).Times(1)
			}
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
			cleanInstallDeviceError()
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to create dir", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
			cleanInstallDevice()
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to get ignition", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
			})
			cleanInstallDevice()
			mkdirSuccess()
			err := fmt.Errorf("failed to fetch file")
			mockbmclient.EXPECT().DownloadFile(masterIgn, filepath.Join(InstallDir, masterIgn)).Return(err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to write image to disk", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			err := fmt.Errorf("failed to write image to disk")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device, mockbmclient).Return(err).Times(3)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(fmt.Errorf("failed after 3 attempts, last error: failed to write image to disk")))
		})
		It("HostRoleMaster role failed to reboot", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			uploadLogsSuccess(false)
			writeToDiskSuccess()
			err := fmt.Errorf("failed to reboot")
			mockops.EXPECT().Reboot().Return(err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
	})
	Context("Worker role", func() {
		conf := config.Config{Role: string(models.HostRoleWorker),
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		It("worker role happy flow", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(workerIgn)
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, workerIgn), device, mockbmclient).Return(nil).Times(1)
			// failure must do nothing
			mockops.EXPECT().UploadInstallationLogs(false).Return("", errors.Errorf("Dummy")).Times(1)
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})
	})
	Context("Bad openshift version", func() {
		conf := config.Config{Role: string(models.HostRoleMaster),
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: "Bad version",
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		It("Bad openshift version", func() {
			err := installerObj.InstallNode()
			Expect(err).To(HaveOccurred())
		})
	})
	AfterEach(func() {
		ctrl.Finish()
	})
})

func GetKubeNodes(kubeNamesIds map[string]string) *v1.NodeList {
	file, _ := ioutil.ReadFile("../../test_files/node.json")
	var node v1.Node
	_ = json.Unmarshal(file, &node)
	nodeList := &v1.NodeList{}
	for name, id := range kubeNamesIds {
		node.Status.Conditions = []v1.NodeCondition{{Status: v1.ConditionTrue, Type: v1.NodeReady}}
		node.Status.NodeInfo.SystemUUID = id
		node.Name = name
		nodeList.Items = append(nodeList.Items, node)
	}
	return nodeList
}

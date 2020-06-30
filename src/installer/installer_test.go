package installer

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/eranco74/assisted-installer/src/k8s_client"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/utils"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
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
		i                 *installer
		hostId            = "host-id"
		bootstrapIgn      = "bootstrap.ign"
		masterIgn         = "master.ign"
		workerIgn         = "worker.ign"
		openShiftVersion  = "4.4"
		image, _          = utils.GetRhcosImageByOpenshiftVersion(openShiftVersion)
		inventoryNamesIds map[string]string
		kubeNamesIds      map[string]string
	)
	generalWaitTimeout = 100 * time.Millisecond
	inventoryNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
		"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238",
		"node2": "b898d516-3e16-49d0-86a5-0ad5bd04e3ed"}
	device := "/dev/vda"
	l.SetOutput(ioutil.Discard)
	mkdirSuccess := func() {
		mockops.EXPECT().Mkdir(InstallDir).Return(nil).Times(1)
	}
	downloadFileSuccess := func(fileName string) {
		mockbmclient.EXPECT().DownloadFile(fileName, filepath.Join(InstallDir, fileName)).Return(nil).Times(1)
	}

	updateStatusConfiguringSuccess := func() {
		inventoryIdsIps := map[string][]string{"eb82821f-bf21-4614-9a3b-ecb07929f238": {"192.168.126.10"}}
		mockbmclient.EXPECT().GetEnabledIdsIps().Return(inventoryIdsIps, nil).AnyTimes()
		mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).AnyTimes()

	}

	cleanInstallDevice := func() {
		mockops.EXPECT().GetVGByPV(device).Return("", nil).Times(1)
	}

	udpateStatusSuccess := func(statuses []string) {
		for _, status := range statuses {
			mockbmclient.EXPECT().UpdateHostStatus(status, hostId).Return(nil).Times(1)

		}
	}

	writeToDiskSuccess := func() {
		mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device, image, mockbmclient).Return(nil).Times(1)
	}
	rebootSuccess := func() {
		mockops.EXPECT().Reboot().Return(nil).Times(1)
	}
	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
	})
	k8sBuilder := func(configPath string, logger *logrus.Logger) (k8s_client.K8SClient, error) {
		return mockk8sclient, nil
	}

	Context("Bootstrap role", func() {
		conf := config.Config{Role: HostRoleBootstrap,
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			Host:             "https://bm-inventory.com",
			Port:             80,
			OpenshiftVersion: openShiftVersion,
		}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		mcoImage, _ := utils.GetMCOByOpenshiftVersion(conf.OpenshiftVersion)
		extractSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(
				gomock.Any(), "podman", "run", "--net", "host",
				"--volume", "/:/rootfs:rw",
				"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
				"--privileged",
				"--entrypoint", "/usr/bin/machine-config-daemon",
				mcoImage,
				"start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from",
				filepath.Join(InstallDir, bootstrapIgn), "--skip-reboot")
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
			mockbmclient.EXPECT().GetEnabledHostsNamesIds().Return(inventoryNamesIds, nil).Times(1)
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(map[string]string{}), nil).Times(1)
			kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65"}
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			mockbmclient.EXPECT().UpdateHostStatus(Joined, inventoryNamesIds["node0"]).Times(1)
			kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
				"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238"}
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			mockbmclient.EXPECT().UpdateHostStatus(Joined, inventoryNamesIds["node1"]).Times(1)
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
		It("bootstrap role happy flow", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				RunningBootstrap,
				WaitForControlPlane,
				fmt.Sprintf(InstallingAs, HostRoleMaster),
				WritingImageToDisk,
				Reboot,
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractSuccess()
			daemonReload(nil)
			restartNetworkManager(nil)
			prepareControllerSuccess()
			updateStatusConfiguringSuccess()
			startServicesSuccess()
			patchEtcdSuccess()
			WaitMasterNodesSucccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			//HostRoleMaster flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("bootstrap role fail", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				RunningBootstrap,
				WaitForControlPlane,
				fmt.Sprintf(InstallingAs, HostRoleMaster),
				WritingImageToDisk,
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractSuccess()
			daemonReload(nil)
			restartNetworkManager(nil)
			updateStatusConfiguringSuccess()
			prepareControllerSuccess()
			startServicesSuccess()
			WaitMasterNodesSucccess()
			err := fmt.Errorf("Etcd patch failed")
			mockk8sclient.EXPECT().PatchEtcd().Return(err).Times(1)
			//HostRoleMaster flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("bootstrap fail to restart NetworkManager", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				RunningBootstrap,
				fmt.Sprintf(InstallingAs, HostRoleMaster),
				WritingImageToDisk,
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractSuccess()
			updateStatusConfiguringSuccess()
			daemonReload(nil)
			err := fmt.Errorf("Failed to restart NetworkManager")
			restartNetworkManager(err)
			//HostRoleMaster flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
	})
	Context("Master role", func() {
		conf := config.Config{Role: HostRoleMaster,
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			Host:             "https://bm-inventory.com",
			Port:             80,
			OpenshiftVersion: openShiftVersion,
		}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		It("master role happy flow", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf(InstallingAs, conf.Role),
				WritingImageToDisk,
				Reboot,
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("HostRoleMaster role happy flow with disk cleanup", func() {
			cleanInstallDeviceClean := func() {
				mockops.EXPECT().GetVGByPV(device).Return("vg1", nil).Times(1)
				mockops.EXPECT().RemoveVG("vg1").Return(nil).Times(1)
				mockops.EXPECT().RemovePV(device).Return(nil).Times(1)
			}
			udpateStatusSuccess([]string{StartingInstallation})
			cleanInstallDeviceClean()
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to cleanup disk", func() {
			err := fmt.Errorf("Failed to remove vg")
			cleanInstallDeviceError := func() {
				mockops.EXPECT().GetVGByPV(device).Return("vg1", nil).Times(1)
				mockops.EXPECT().RemoveVG("vg1").Return(err).Times(1)
			}
			udpateStatusSuccess([]string{StartingInstallation})
			cleanInstallDeviceError()
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to create dir", func() {
			udpateStatusSuccess([]string{StartingInstallation})
			cleanInstallDevice()
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to get ignition", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf(InstallingAs, conf.Role),
			})
			cleanInstallDevice()
			mkdirSuccess()
			err := fmt.Errorf("failed to fetch file")
			mockbmclient.EXPECT().DownloadFile(masterIgn, filepath.Join(InstallDir, masterIgn)).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to write image to disk", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf(InstallingAs, conf.Role),
				WritingImageToDisk,
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			err := fmt.Errorf("failed to write image to disk")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device, image, mockbmclient).Return(err).Times(3)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(fmt.Errorf("failed after 3 attempts, last error: failed to write image to disk")))
		})
		It("HostRoleMaster role failed to reboot", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf(InstallingAs, conf.Role),
				WritingImageToDisk,
				Reboot,
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			err := fmt.Errorf("failed to reboot")
			mockops.EXPECT().Reboot().Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
	})
	Context("Worker role", func() {
		conf := config.Config{Role: "worker",
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			Host:             "https://bm-inventory.com",
			Port:             80,
			OpenshiftVersion: openShiftVersion,
		}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		It("worker role happy flow", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf(InstallingAs, conf.Role),
				WritingImageToDisk,
				Reboot,
			})
			cleanInstallDevice()
			mkdirSuccess()
			downloadFileSuccess(workerIgn)
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, workerIgn), device, image, mockbmclient).Return(nil).Times(1)
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})
	})
	Context("Bad openshift version", func() {
		conf := config.Config{Role: HostRoleMaster,
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			Host:             "https://bm-inventory.com",
			Port:             80,
			OpenshiftVersion: "Bad version",
		}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
		})
		It("Bad openshift version", func() {
			err := i.InstallNode()
			Expect(err).To(HaveOccurred())
		})
	})
	AfterEach(func() {
		ctrl.Finish()
	})
	Context("Configuring state", func() {
		var testInventoryIdsIps map[string][]string
		var logs string
		conf := config.Config{Role: HostRoleBootstrap,
			ClusterID:        "cluster-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			Host:             "https://bm-inventory.com",
			Port:             80,
			OpenshiftVersion: "Bad version",
		}
		BeforeEach(func() {
			logsInBytes, _ := ioutil.ReadFile("../../test_files/mcs_logs.txt")
			logs = string(logsInBytes)
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder)
			testInventoryIdsIps = map[string][]string{"eb82821f-bf21-4614-9a3b-ecb07929f238": {"192.168.126.10", "192.168.11.122", "fe80::5054:ff:fe9a:4738"},
				"eb82821f-bf21-4614-9a3b-ecb07929f239": {"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"},
				"eb82821f-bf21-4614-9a3b-ecb07929f240": {"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}
		})
		It("Configuring state flow", func() {
			mockbmclient.EXPECT().GetEnabledIdsIps().Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetEnabledIdsIps().Return(testInventoryIdsIps, nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("", fmt.Errorf("dummy")).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return(logs, nil).AnyTimes()
			mockbmclient.EXPECT().UpdateHostStatus(Configuring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostStatus(Configuring, "eb82821f-bf21-4614-9a3b-ecb07929f240").Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostStatus(Configuring, "eb82821f-bf21-4614-9a3b-ecb07929f239").Return(nil).Times(1)

			done := make(chan bool)
			go i.updateConfiguringStatus(done)
			time.Sleep(3 * time.Second)
			done <- true
		})
	})
	//AfterEach(func() {
	//	ctrl.Finish()
	//})

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

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

	"github.com/go-openapi/strfmt"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/ignition"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-service/models"
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
		mockIgnition       *ignition.MockIgnition
		installerObj       *installer
		hostId             = "host-id"
		infraEnvId         = "infra-env-id"
		bootstrapIgn       = "bootstrap.ign"
		openShiftVersion   = "4.7"
		inventoryNamesHost map[string]inventory_client.HostData
		kubeNamesIds       map[string]string
		events             v1.EventList
	)
	generalWaitTimeout = 100 * time.Millisecond
	generalWaitInterval = 5 * time.Millisecond
	device := "/dev/vda"
	events = v1.EventList{TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{}, Items: []v1.Event{{TypeMeta: metav1.TypeMeta{}, ObjectMeta: metav1.ObjectMeta{UID: "7916fa89-ea7a-443e-a862-b3e930309f65", Name: common.AssistedControllerIsReadyEvent}, Message: "aaaa"}}}
	l.SetOutput(ioutil.Discard)
	evaluateDiskSymlinkSuccess := func() {
		mockops.EXPECT().EvaluateDiskSymlink(device).Return(device).Times(1)
	}

	mkdirSuccess := func(filepath string) {
		mockops.EXPECT().Mkdir(filepath).Return(nil).Times(1)
	}
	downloadFileSuccess := func(fileName string) {
		mockbmclient.EXPECT().DownloadFile(gomock.Any(), fileName, filepath.Join(InstallDir, fileName)).Return(nil).Times(1)
	}
	downloadHostIgnitionSuccess := func(infraEnvID string, hostID string, fileName string) {
		mockbmclient.EXPECT().DownloadHostIgnition(gomock.Any(), infraEnvID, hostID, filepath.Join(InstallDir, fileName)).Return(nil).Times(1)
	}

	reportLogProgressSuccess := func() {
		mockbmclient.EXPECT().HostLogProgressReport(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		mockbmclient.EXPECT().ClusterLogProgressReport(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	}

	singleNodeMergeIgnitionSuccess := func() {
		conf := ignition.EmptyIgnition
		mockIgnition.EXPECT().ParseIgnitionFile("/opt/install-dir/master-host-id.ign").Return(&conf, nil).Times(1)
		mockIgnition.EXPECT().ParseIgnitionFile(singleNodeMasterIgnitionPath).Return(&conf, nil).Times(1)
		mockIgnition.EXPECT().MergeIgnitionConfig(gomock.Any(), gomock.Any()).Return(&conf, nil).Times(1)
		mockIgnition.EXPECT().WriteIgnitionFile(singleNodeMasterIgnitionPath, gomock.Any()).Return(nil).Times(1)
	}

	cleanInstallDevice := func() {
		mockops.EXPECT().GetVolumeGroupsByDisk(device).Return([]string{}, nil).Times(1)
		mockops.EXPECT().RemoveAllPVsOnDevice(device).Return(nil).Times(1)
		mockops.EXPECT().RemoveAllDMDevicesOnDisk(device).Return(nil).Times(1)
		mockops.EXPECT().IsRaidMember(device).Return(false).Times(1)
		mockops.EXPECT().Wipefs(device).Return(nil).Times(1)
	}

	updateProgressSuccess := func(stages [][]string) {
		for _, stage := range stages {
			if len(stage) == 2 {
				mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStage(stage[0]), stage[1]).Return(nil).Times(1)
			} else {
				mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStage(stage[0]), "").Return(nil).Times(1)
			}
		}
	}

	writeToDiskSuccess := func(extra interface{}) {
		mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, "master-host-id.ign"), device, mockbmclient, extra).Return(nil).Times(1)
	}

	setBootOrderSuccess := func(extra interface{}) {
		mockops.EXPECT().SetBootOrder(device).Return(nil).Times(1)
	}

	uploadLogsSuccess := func(bootstrap bool) {
		mockops.EXPECT().UploadInstallationLogs(bootstrap).Return("dummy", nil).Times(1)
	}

	waitForControllerSuccessfully := func(clusterId string) {
		mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStageWaitingForController, "waiting for controller pod ready event").Return(nil).Times(1)
		mockk8sclient.EXPECT().GetPods("assisted-installer", gomock.Any(), "").Return([]v1.Pod{{TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{Name: common.AssistedControllerPrefix + "aasdasd"},
			Status:     v1.PodStatus{Phase: "Running"}}}, nil).Times(1)
		r := bytes.NewBuffer([]byte("test"))
		mockk8sclient.EXPECT().GetPodLogsAsBuffer(assistedControllerNamespace, common.AssistedControllerPrefix+"aasdasd", gomock.Any()).Return(r, nil).Times(1)
		mockk8sclient.EXPECT().ListEvents(assistedControllerNamespace).Return(nil, fmt.Errorf("dummy")).Times(1)
		mockk8sclient.EXPECT().ListEvents(assistedControllerNamespace).Return(&events, nil).Times(1)
		mockbmclient.EXPECT().UploadLogs(gomock.Any(), clusterId, models.LogsTypeController, gomock.Any()).Return(nil).Times(1)
	}

	resolvConfSuccess := func() {
		mockops.EXPECT().ReloadHostFile("/etc/resolv.conf").Return(nil).Times(1)
	}

	rebootSuccess := func() {
		mockops.EXPECT().Reboot().Return(nil).Times(1)
	}
	ironicAgentDoesntExist := func() {
		mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "list-units", "--no-legend", "ironic-agent.service").Return("", nil).Times(1)
	}
	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
		mockIgnition = ignition.NewMockIgnition(ctrl)
		nodesInfraEnvId := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f50")
		node0Id := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
		node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
		node2Id := strfmt.UUID("b898d516-3e16-49d0-86a5-0ad5bd04e3ed")
		inventoryNamesHost = map[string]inventory_client.HostData{"node0": {Host: &models.Host{InfraEnvID: nodesInfraEnvId, ID: &node0Id}, IPs: []string{"192.168.126.10"}},
			"node1": {Host: &models.Host{InfraEnvID: nodesInfraEnvId, ID: &node1Id}, IPs: []string{"192.168.126.11"}},
			"node2": {Host: &models.Host{InfraEnvID: nodesInfraEnvId, ID: &node2Id}, IPs: []string{"192.168.126.12"}}}
	})
	k8sBuilder := func(configPath string, logger logrus.FieldLogger) (k8s_client.K8SClient, error) {
		return mockk8sclient, nil
	}

	Context("LVM volume group cleanup", func() {
		conf := config.Config{Role: string(models.HostRoleBootstrap),
			ClusterID:        "cluster-id",
			InfraEnvID:       "infra-env-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
			MCOImage:         "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221",
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition)
		})

		It("Should clean up the PV and all volume groups for a disk when asked to do so", func() {
			mockedVgsResult := []string{
				"vg1",
				"vg2",
			}
			mockops.EXPECT().GetVolumeGroupsByDisk("/dev/vda").Times(1).Return(mockedVgsResult, nil)
			mockops.EXPECT().RemoveVG("vg1").Times(1)
			mockops.EXPECT().RemoveVG("vg2").Times(1)
			mockops.EXPECT().RemoveAllPVsOnDevice("/dev/vda").Return(nil).Times(1)
			mockops.EXPECT().RemoveAllDMDevicesOnDisk("/dev/vda").Return(nil).Times(1)
			err := installerObj.cleanupDevice("/dev/vda")
			Expect(err).ToNot(HaveOccurred())
		})

		It("If there is a failure during the removal of a volume group, the PV removal and subsequent volume group removal should fail", func() {
			mockedVgsResult := []string{
				"vg1",
				"vg2",
				"vg3",
			}
			mockops.EXPECT().GetVolumeGroupsByDisk("/dev/vda").Times(1).Return(mockedVgsResult, nil)
			mockops.EXPECT().RemoveVG("vg1").Times(1)
			mockops.EXPECT().RemoveVG("vg2").Times(1).Return(errors.New(fmt.Sprintf("Failed to remove VG %s, output %s, error %s", "vg2", "some arbitrary output", "some arbitrary error")))
			mockops.EXPECT().RemoveVG("vg3").Times(0)
			mockops.EXPECT().RemoveAllPVsOnDevice("/dev/vda").Return(nil).Times(0)
			mockops.EXPECT().RemoveAllDMDevicesOnDisk("/dev/vda").Return(nil).Times(0)
			err := installerObj.cleanupDevice("/dev/vda")
			Expect(err).To(HaveOccurred())
		})

	})

	Context("Bootstrap role", func() {

		conf := config.Config{Role: string(models.HostRoleBootstrap),
			ClusterID:        "cluster-id",
			InfraEnvID:       "infra-env-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
			MCOImage:         "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221",
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition)
			evaluateDiskSymlinkSuccess()
		})
		mcoImage := conf.MCOImage
		extractIgnitionToFS := func(out string, err error) {
			mockops.EXPECT().ExecPrivilegeCommand(
				gomock.Any(), "podman", "run", "--net", "host",
				"--pid=host",
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
		checkLocalHostname := func(hostname string, err error) {
			mockops.EXPECT().GetHostname().Return(hostname, err).Times(1)
			if hostname == "localhost" {
				mockops.EXPECT().CreateRandomHostname(gomock.Any()).Return(nil).Times(1)
			}
		}
		startServicesSuccess := func() {
			services := []string{"bootkube.service", "progress.service", "approve-csr.service"}
			for i := range services {
				mockops.EXPECT().SystemctlAction("start", services[i]).Return(nil).Times(1)
			}
		}
		WaitMasterNodesSucccess := func() {
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts(gomock.Any(), gomock.Any()).Return(inventoryNamesHost, nil).AnyTimes()
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(map[string]string{}), nil).Times(1)
			kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65"}
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), inventoryNamesHost["node0"].Host.InfraEnvID.String(), inventoryNamesHost["node0"].Host.ID.String(), models.HostStageJoined, "").Times(1)
			kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
				"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238"}
			mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), inventoryNamesHost["node1"].Host.InfraEnvID.String(), inventoryNamesHost["node1"].Host.ID.String(), models.HostStageJoined, "").Times(1)
		}
		prepareControllerSuccess := func() {
			mockops.EXPECT().PrepareController().Return(nil).Times(1)
		}
		waitForBootkubeSuccess := func() {
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStageWaitingForBootkube, "").Return(nil).Times(1)
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "stat", "/opt/openshift/.bootkube.done").Return("OK", nil).Times(1)
		}
		bootkubeStatusSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "status", "bootkube.service").Return("1", nil).Times(1)
		}

		extractSecretFromIgnitionSuccess := func() {
			mockops.EXPECT().ExtractFromIgnition(filepath.Join(InstallDir, bootstrapIgn), dockerConfigFile).Return(nil).Times(1)
		}
		generateSshKeyPairSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "ssh-keygen", "-q", "-f", sshKeyPath, "-N", "").Return("OK", nil).Times(1)
		}
		createOpenshiftSshManifestSuccess := func() {
			mockops.EXPECT().CreateOpenshiftSshManifest(assistedInstallerSshManifest, sshManifestTmpl, sshPubKeyPath).Return(nil).Times(1)
		}

		bootstrapSetup := func() {
			cleanInstallDevice()
			mkdirSuccess(sshDir)
			mkdirSuccess(InstallDir)
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractIgnitionToFS("Success", nil)
			generateSshKeyPairSuccess()
			createOpenshiftSshManifestSuccess()
			daemonReload(nil)
		}
		for _, version := range []string{"4.7", "4.7.1", "4.7-pre-release", "4.8"} {
			Context(version, func() {
				BeforeEach(func() {
					conf.OpenshiftVersion = version
				})
				AfterEach(func() {
					conf.OpenshiftVersion = openShiftVersion
				})
				It("bootstrap role happy flow", func() {
					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
						{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
						{string(models.HostStageWaitingForControlPlane), waitingForMastersStatusInfo},
						{string(models.HostStageInstalling), string(models.HostRoleMaster)},
						{string(models.HostStageWritingImageToDisk)},
						{string(models.HostStageRebooting)},
					})
					bootstrapSetup()
					checkLocalHostname("not localhost", nil)
					restartNetworkManager(nil)
					prepareControllerSuccess()
					startServicesSuccess()
					WaitMasterNodesSucccess()
					waitForBootkubeSuccess()
					bootkubeStatusSuccess()
					resolvConfSuccess()
					waitForControllerSuccessfully(conf.ClusterID)
					//HostRoleMaster flow:
					downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
					writeToDiskSuccess(gomock.Any())
					reportLogProgressSuccess()
					setBootOrderSuccess(gomock.Any())
					uploadLogsSuccess(true)
					ironicAgentDoesntExist()
					rebootSuccess()
					ret := installerObj.InstallNode()
					Expect(ret).Should(BeNil())
				})
				It("bootstrap role happy flow ovn-kubernetes", func() {
					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
						{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
						{string(models.HostStageWaitingForControlPlane), waitingForMastersStatusInfo},
						{string(models.HostStageInstalling), string(models.HostRoleMaster)},
						{string(models.HostStageWritingImageToDisk)},
						{string(models.HostStageRebooting)},
					})
					bootstrapSetup()
					checkLocalHostname("localhost", nil)
					restartNetworkManager(nil)
					prepareControllerSuccess()
					startServicesSuccess()
					WaitMasterNodesSucccess()
					waitForBootkubeSuccess()
					bootkubeStatusSuccess()
					resolvConfSuccess()
					waitForControllerSuccessfully(conf.ClusterID)
					//HostRoleMaster flow:
					downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
					writeToDiskSuccess(gomock.Any())
					setBootOrderSuccess(gomock.Any())
					uploadLogsSuccess(true)
					reportLogProgressSuccess()
					ironicAgentDoesntExist()
					rebootSuccess()
					ret := installerObj.InstallNode()
					Expect(ret).Should(BeNil())
				})
			})
		}
		It("bootstrap role creating SSH manifest failed", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
			})
			cleanInstallDevice()
			mkdirSuccess(InstallDir)
			mkdirSuccess(sshDir)
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractIgnitionToFS("Success", nil)
			generateSshKeyPairSuccess()
			err := fmt.Errorf("generate SSH keys failed")
			mockops.EXPECT().CreateOpenshiftSshManifest(assistedInstallerSshManifest, sshManifestTmpl, sshPubKeyPath).Return(err).Times(1)
			//HostRoleMaster flow:
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			writeToDiskSuccess(gomock.Any())
			setBootOrderSuccess(gomock.Any())
			ret := installerObj.InstallNode()
			Expect(ret).To(HaveOccurred())
		})
		It("bootstrap role extract ignition retry", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
				{string(models.HostStageWaitingForControlPlane), waitingForMastersStatusInfo},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			extractIgnitionToFS("extract failure", fmt.Errorf("extract failed"))
			bootstrapSetup()
			checkLocalHostname("not localhost", nil)
			restartNetworkManager(nil)
			prepareControllerSuccess()
			startServicesSuccess()
			WaitMasterNodesSucccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			resolvConfSuccess()
			waitForControllerSuccessfully(conf.ClusterID)
			//HostRoleMaster flow:
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			writeToDiskSuccess(gomock.Any())
			setBootOrderSuccess(gomock.Any())
			uploadLogsSuccess(true)
			reportLogProgressSuccess()
			ironicAgentDoesntExist()
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("bootstrap role extract ignition retry exhausted", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
			})
			cleanInstallDevice()
			mkdirSuccess(InstallDir)
			mkdirSuccess(sshDir)
			downloadFileSuccess(bootstrapIgn)
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			writeToDiskSuccess(gomock.Any())
			setBootOrderSuccess(gomock.Any())
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
				{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
			})
			bootstrapSetup()
			checkLocalHostname("not localhost", nil)
			err := fmt.Errorf("Failed to restart NetworkManager")
			restartNetworkManager(err)
			//HostRoleMaster flow:
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			writeToDiskSuccess(gomock.Any())
			setBootOrderSuccess(gomock.Any())
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
	})
	Context("Bootstrap role waiting for control plane", func() {

		conf := config.Config{Role: string(models.HostRoleBootstrap),
			ClusterID:        "cluster-id",
			InfraEnvID:       "infra-env-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
			MCOImage:         "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221",
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition)
		})
		It("waitForControlPlane reload resolv.conf failed", func() {
			mockops.EXPECT().ReloadHostFile("/etc/resolv.conf").Return(fmt.Errorf("failed to load file")).Times(1)

			err := installerObj.waitForControlPlane(context.Background())
			Expect(err).To(HaveOccurred())
		})

		It("waitForController reload get pods fails then succeeds", func() {
			reportLogProgressSuccess()
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStageWaitingForController, "waiting for controller pod ready event").Return(nil).Times(1)
			mockk8sclient.EXPECT().GetPods("assisted-installer", gomock.Any(), "").Return(nil, fmt.Errorf("dummy")).Times(1)
			mockk8sclient.EXPECT().ListEvents(assistedControllerNamespace).Return(&events, nil).Times(1)
			err := installerObj.waitForController(mockk8sclient)
			Expect(err).NotTo(HaveOccurred())
		})
		It("Configuring state", func() {
			var logs string
			logsInBytes, _ := ioutil.ReadFile("../../test_files/mcs_logs.txt")
			logs = string(logsInBytes)
			infraEnvID := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f250")
			node0Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
			node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")

			testInventoryIdsIps := map[string]inventory_client.HostData{"node0": {Host: &models.Host{InfraEnvID: infraEnvID, ID: &node0Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}},
				IPs: []string{"192.168.126.10", "192.168.11.122", "fe80::5054:ff:fe9a:4738"}},
				"node1": {Host: &models.Host{InfraEnvID: infraEnvID, ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{InfraEnvID: infraEnvID, ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts(gomock.Any(), gomock.Any()).Return(testInventoryIdsIps, nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("", fmt.Errorf("dummy")).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return(logs, nil).AnyTimes()

			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), gomock.Any(), gomock.Any(), models.HostStageConfiguring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), "eb82821f-bf21-4614-9a3b-ecb07929f250", "eb82821f-bf21-4614-9a3b-ecb07929f240", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), "eb82821f-bf21-4614-9a3b-ecb07929f250", "eb82821f-bf21-4614-9a3b-ecb07929f239", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			go installerObj.updateConfiguringStatus(ctx)
			time.Sleep(1 * time.Second)
		})
		It("Configuring state, all hosts were set", func() {
			var logs string
			logsInBytes, _ := ioutil.ReadFile("../../test_files/mcs_logs.txt")
			logs = string(logsInBytes)
			infraEnvId := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f250")
			node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")

			testInventoryIdsIps := map[string]inventory_client.HostData{
				"node1": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().GetEnabledHostsNamesHosts(gomock.Any(), gomock.Any()).Return(testInventoryIdsIps, nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("", fmt.Errorf("dummy")).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return("dummy logs", nil).Times(1)
			mockops.EXPECT().GetMCSLogs().Return(logs, nil).AnyTimes()

			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), gomock.Any(), gomock.Any(), models.HostStageConfiguring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), "eb82821f-bf21-4614-9a3b-ecb07929f250", "eb82821f-bf21-4614-9a3b-ecb07929f240", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), "eb82821f-bf21-4614-9a3b-ecb07929f250", "eb82821f-bf21-4614-9a3b-ecb07929f239", models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			installerObj.updateConfiguringStatus(ctx)
		})
	})
	Context("Master role", func() {
		installerArgs := []string{"-n", "--append-karg", "nameserver=8.8.8.8"}
		raidDevice := "/dev/md0"
		conf := config.Config{Role: string(models.HostRoleMaster),
			ClusterID:        "cluster-id",
			InfraEnvID:       "infra-env-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
			InstallerArgs:    installerArgs,
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition)
			evaluateDiskSymlinkSuccess()

		})
		It("master role happy flow", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			cleanInstallDevice()
			mkdirSuccess(InstallDir)
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			writeToDiskSuccess(installerArgs)
			setBootOrderSuccess(gomock.Any())
			uploadLogsSuccess(false)
			reportLogProgressSuccess()
			ironicAgentDoesntExist()
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})

		It("HostRoleMaster role happy flow with skipping disk cleanup", func() {
			installerObj.Config.SkipInstallationDiskCleanup = true
			// verify none of cleanup function runs
			mockops.EXPECT().GetVolumeGroupsByDisk(device).Return([]string{"vg1"}, nil).Times(0)
			mockops.EXPECT().RemoveVG("vg1").Return(nil).Times(0)
			mockops.EXPECT().IsRaidMember(device).Return(false).Times(0)
			mockops.EXPECT().Wipefs(device).Return(nil).Times(0)
			mockops.EXPECT().RemovePV(device).Return(nil).Times(0)
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			mkdirSuccess(InstallDir)
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			writeToDiskSuccess(installerArgs)
			setBootOrderSuccess(gomock.Any())
			uploadLogsSuccess(false)
			reportLogProgressSuccess()
			ironicAgentDoesntExist()
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})

		It("HostRoleMaster role happy flow with disk cleanup", func() {
			cleanInstallDeviceClean := func() {
				mockops.EXPECT().GetVolumeGroupsByDisk(device).Return([]string{"vg1"}, nil).Times(1)
				mockops.EXPECT().RemoveVG("vg1").Return(nil).Times(1)
				mockops.EXPECT().IsRaidMember(device).Return(false).Times(1)
				mockops.EXPECT().Wipefs(device).Return(nil).Times(1)
				mockops.EXPECT().RemoveAllPVsOnDevice(device).Return(nil).Times(1)
				mockops.EXPECT().RemoveAllDMDevicesOnDisk(device).Return(nil).Times(1)
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
				mockops.EXPECT().GetVolumeGroupsByDisk(device).Return([]string{"vg1"}, nil).Times(1)
				mockops.EXPECT().RemoveVG("vg1").Return(err).Times(1)
			}
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
			cleanInstallDeviceError()
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role raid cleanup disk - happy flow", func() {
			cleanInstallDeviceClean := func() {
				mockops.EXPECT().GetVolumeGroupsByDisk(device).Return([]string{}, nil).Times(1)
				mockops.EXPECT().RemoveAllPVsOnDevice(device).Return(nil).Times(1)
				mockops.EXPECT().RemoveAllDMDevicesOnDisk(device).Return(nil).Times(1)
				mockops.EXPECT().IsRaidMember(device).Return(true).Times(1)
				mockops.EXPECT().GetRaidDevices(device).Return([]string{raidDevice}, nil).Times(1)
				mockops.EXPECT().GetVolumeGroupsByDisk(raidDevice).Return([]string{}, nil).Times(1)
				mockops.EXPECT().RemoveAllPVsOnDevice(raidDevice).Return(nil).Times(1)
				mockops.EXPECT().RemoveAllDMDevicesOnDisk(raidDevice).Return(nil).Times(1)
				mockops.EXPECT().CleanRaidMembership(device).Return(nil).Times(1)
				mockops.EXPECT().Wipefs(device).Return(nil).Times(1)
			}
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
			cleanInstallDeviceClean()
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role raid cleanup disk - failed", func() {
			err := fmt.Errorf("failed cleaning raid device")

			cleanInstallDeviceClean := func() {
				mockops.EXPECT().GetVolumeGroupsByDisk(device).Return([]string{}, nil).Times(1)
				mockops.EXPECT().RemoveAllPVsOnDevice(device).Return(nil).Times(1)
				mockops.EXPECT().RemoveAllDMDevicesOnDisk(device).Return(nil).Times(1)
				mockops.EXPECT().IsRaidMember(device).Return(true).Times(1)
				mockops.EXPECT().GetRaidDevices(device).Return([]string{raidDevice}, nil).Times(1)
				mockops.EXPECT().GetVolumeGroupsByDisk(raidDevice).Return([]string{}, nil).Times(1)
				mockops.EXPECT().RemoveAllPVsOnDevice(raidDevice).Return(nil).Times(1)
				mockops.EXPECT().RemoveAllDMDevicesOnDisk(raidDevice).Return(nil).Times(1)
				mockops.EXPECT().CleanRaidMembership(device).Return(err).Times(1)
			}
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
			cleanInstallDeviceClean()
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("master role happy flow with ironic agent", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting), "Ironic will reboot the node shortly"},
			})
			cleanInstallDevice()
			mkdirSuccess(InstallDir)
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			writeToDiskSuccess(installerArgs)
			setBootOrderSuccess(gomock.Any())
			uploadLogsSuccess(false)
			reportLogProgressSuccess()
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "list-units", "--no-legend", "ironic-agent.service").Return("ironic-agent.service loaded active ", nil).Times(1)
			mockops.EXPECT().SystemctlAction("stop", "agent.service").Return(nil).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
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
			mkdirSuccess(InstallDir)
			err := fmt.Errorf("failed to fetch file")
			mockbmclient.EXPECT().DownloadHostIgnition(gomock.Any(), infraEnvId, hostId, filepath.Join(InstallDir, "master-host-id.ign")).Return(err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to write image to disk", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
			})
			cleanInstallDevice()
			mkdirSuccess(InstallDir)
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			err := fmt.Errorf("failed to write image to disk")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, "master-host-id.ign"), device, mockbmclient, installerArgs).Return(err).Times(3)
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
			mkdirSuccess(InstallDir)
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			uploadLogsSuccess(false)
			reportLogProgressSuccess()
			writeToDiskSuccess(installerArgs)
			setBootOrderSuccess(gomock.Any())
			ironicAgentDoesntExist()
			err := fmt.Errorf("failed to reboot")
			mockops.EXPECT().Reboot().Return(err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
	})
	Context("Worker role", func() {
		conf := config.Config{Role: string(models.HostRoleWorker),
			ClusterID:        "cluster-id",
			InfraEnvID:       "infra-env-id",
			HostID:           "host-id",
			Device:           "/dev/vda",
			URL:              "https://assisted-service.com:80",
			OpenshiftVersion: openShiftVersion,
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition)
			evaluateDiskSymlinkSuccess()
		})
		It("worker role happy flow", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), conf.Role},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageWaitingForControlPlane)},
				{string(models.HostStageRebooting)},
			})
			cluster := models.Cluster{}
			hosts := models.HostList{
				{
					Role: models.HostRoleMaster,
					Progress: &models.HostProgressInfo{
						CurrentStage: models.HostStageDone,
					},
				},
				{
					Role: models.HostRoleMaster,
					Progress: &models.HostProgressInfo{
						CurrentStage: models.HostStageDone,
					},
				},
			}
			mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&cluster, nil).Times(1)
			mockbmclient.EXPECT().ListsHostsForRole(gomock.Any(), "master").Return(hosts, nil).Times(1)
			cleanInstallDevice()
			mkdirSuccess(InstallDir)
			downloadHostIgnitionSuccess(infraEnvId, hostId, "worker-host-id.ign")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, "worker-host-id.ign"), device, mockbmclient, nil).Return(nil).Times(1)
			setBootOrderSuccess(gomock.Any())
			// failure must do nothing
			reportLogProgressSuccess()
			mockops.EXPECT().UploadInstallationLogs(false).Return("", errors.Errorf("Dummy")).Times(1)
			ironicAgentDoesntExist()
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})
	})
	Context("None HA mode ", func() {

		conf := config.Config{Role: string(models.HostRoleMaster),
			ClusterID:            "cluster-id",
			InfraEnvID:           "infra-env-id",
			HostID:               "host-id",
			Device:               "/dev/vda",
			URL:                  "https://assisted-service.com:80",
			OpenshiftVersion:     openShiftVersion,
			MCOImage:             "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221",
			HighAvailabilityMode: models.ClusterHighAvailabilityModeNone,
		}
		BeforeEach(func() {
			installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition)
			evaluateDiskSymlinkSuccess()
		})
		mcoImage := conf.MCOImage
		extractIgnitionToFS := func(out string, err error) {
			mockops.EXPECT().ExecPrivilegeCommand(
				gomock.Any(), "podman", "run", "--net", "host",
				"--pid=host",
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
		checkLocalHostname := func(hostname string, err error) {
			mockops.EXPECT().GetHostname().Return(hostname, err).Times(1)
			if hostname == "localhost" {
				mockops.EXPECT().CreateRandomHostname(gomock.Any()).Return(nil).Times(1)
			}
		}
		startServicesSuccess := func() {
			services := []string{"bootkube.service", "progress.service", "approve-csr.service"}
			for i := range services {
				mockops.EXPECT().SystemctlAction("start", services[i]).Return(nil).Times(1)
			}
		}
		prepareControllerSuccess := func() {
			mockops.EXPECT().PrepareController().Return(nil).Times(1)
		}
		waitForBootkubeSuccess := func() {
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStageWaitingForBootkube, "").Return(nil).Times(1)
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "stat", "/opt/openshift/.bootkube.done").Return("OK", nil).Times(1)
		}
		bootkubeStatusSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "status", "bootkube.service").Return("1", nil).Times(1)
		}
		extractSecretFromIgnitionSuccess := func() {
			mockops.EXPECT().ExtractFromIgnition(filepath.Join(InstallDir, bootstrapIgn), dockerConfigFile).Return(nil).Times(1)
		}
		singleNodeBootstrapSetup := func() {
			cleanInstallDevice()
			mkdirSuccess(InstallDir)
			mkdirSuccess(sshDir)
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractIgnitionToFS("Success", nil)
			daemonReload(nil)
		}
		verifySingleNodeMasterIgnitionSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "stat", singleNodeMasterIgnitionPath).Return("", nil).Times(1)
		}

		It("single node happy flow", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
				{string(models.HostStageWritingImageToDisk)},
				{string(models.HostStageRebooting)},
			})
			// single node bootstrap flow
			singleNodeBootstrapSetup()
			checkLocalHostname("localhost", nil)
			prepareControllerSuccess()
			startServicesSuccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			//HostRoleMaster flow:
			verifySingleNodeMasterIgnitionSuccess()
			singleNodeMergeIgnitionSuccess()
			downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
			mockops.EXPECT().WriteImageToDisk(singleNodeMasterIgnitionPath, device, mockbmclient, nil).Return(nil).Times(1)
			setBootOrderSuccess(gomock.Any())
			uploadLogsSuccess(true)
			reportLogProgressSuccess()
			ironicAgentDoesntExist()
			rebootSuccess()
			ret := installerObj.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("single node bootstrap fail", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
			})
			// single node bootstrap flow
			singleNodeBootstrapSetup()
			err := fmt.Errorf("Failed to restart NetworkManager")
			checkLocalHostname("not localhost", err)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("Failed to find master ignition", func() {
			updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
				{string(models.HostStageInstalling), string(models.HostRoleMaster)},
			})
			// single node bootstrap flow
			singleNodeBootstrapSetup()
			checkLocalHostname("localhost", nil)
			prepareControllerSuccess()
			startServicesSuccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			//HostRoleMaster flow:
			err := fmt.Errorf("Failed to find master ignition")
			mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "stat", singleNodeMasterIgnitionPath).Return("", err).Times(1)
			ret := installerObj.InstallNode()
			Expect(ret).Should(Equal(err))
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

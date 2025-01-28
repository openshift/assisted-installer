package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	preinstallUtils "github.com/rh-ecosystem-edge/preinstall-utils/pkg"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-openapi/strfmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/ignition"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-service/models"
	"github.com/openshift/assisted-service/pkg/validations"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	gomock "go.uber.org/mock/gomock"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

var _ = Describe("installer HostRoleMaster role", func() {
	for _, b := range []bool{false, true} {
		withEnableSkipMcoReboot := b
		title := "without enable skip mco reboot"
		if withEnableSkipMcoReboot {
			title = "with enable skip mco reboot"
		}
		Context(title, func() {
			var (
				l                  = logrus.New()
				ctrl               *gomock.Controller
				mockops            *ops.MockOps
				mockbmclient       *inventory_client.MockInventoryClient
				mockk8sclient      *k8s_client.MockK8SClient
				mockIgnition       *ignition.MockIgnition
				cleanupDevice      *preinstallUtils.MockCleanupDevice
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
			l.SetOutput(io.Discard)
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
				cleanupDevice.EXPECT().CleanupInstallDevice(device).Return(nil).Times(1)
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
				mockops.EXPECT().WriteImageToDisk(gomock.Any(), filepath.Join(InstallDir, "master-host-id.ign"), device, extra).Return(nil).Times(1)
			}

			setBootOrderSuccess := func() {
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

			waitForWorkersSuccessfully := func() {
				mockbmclient.EXPECT().ListsHostsForRole(gomock.Any(), gomock.Any()).Times(1).Return(
					models.HostList{
						{
							Progress: &models.HostProgressInfo{
								CurrentStage: models.HostStageRebooting,
							},
						},
					}, nil)
			}

			setupInvoker := func(invoker ...string) {
				i := "assisted-service"
				if len(invoker) > 0 {
					i = invoker[0]
				}
				mockk8sclient.EXPECT().GetConfigMap("openshift-config", "openshift-install-manifests").AnyTimes().Return(
					&v1.ConfigMap{
						Data: map[string]string{
							"invoker": i,
						},
					}, nil)
			}

			resolvConfSuccess := func() {
				mockops.EXPECT().ReloadHostFile("/etc/resolv.conf").Return(nil).Times(1)
			}

			rebootSuccess := func() {
				mockops.EXPECT().Reboot("+1").Return(nil).Times(1)
			}
			rebootNowSuccess := func() {
				mockops.EXPECT().Reboot("now").Return(nil).Times(1)
			}
			ironicAgentDoesntExist := func() {
				mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "list-units", "--no-legend", "ironic-agent.service").Return("", nil).Times(1)
			}
			getEncapsulatedMcSuccess := func(kargs []string) {
				if withEnableSkipMcoReboot {
					ret := mcfgv1.MachineConfig{}
					ret.Spec.KernelArguments = kargs
					ret.Spec.OSImageURL = "abc"
					mockops.EXPECT().GetEncapsulatedMC(gomock.Any()).Return(&ret, nil).Times(1)
				}
			}
			overwriteImageSuccess := func(expectedArgs ...string) {
				if withEnableSkipMcoReboot {
					m := mockops.EXPECT().OverwriteOsImage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
					if len(expectedArgs) > 0 {
						m.Do(func(osImage, device string, extraArgs []string) {
							var args []string
							for i := 0; i != len(extraArgs); {
								if extraArgs[i] == "--karg" && i+1 < len(extraArgs) {
									i++
									args = append(args, extraArgs[i])
								}
								i++
							}
							equal := func(i int) bool {
								for j := 0; j != len(expectedArgs); j++ {
									if args[i+j] != expectedArgs[j] {
										return false
									}
								}
								return true
							}
							for i := 0; i <= (len(args) - len(expectedArgs)); i++ {
								if equal(i) {
									return
								}
							}
							Fail(fmt.Sprintf("No matching arg sequence found: %+v", expectedArgs))
						})
					}
				}
			}
			BeforeEach(func() {
				ctrl = gomock.NewController(GinkgoT())
				mockops = ops.NewMockOps(ctrl)
				mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
				mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
				mockIgnition = ignition.NewMockIgnition(ctrl)
				cleanupDevice = preinstallUtils.NewMockCleanupDevice(ctrl)
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

			Context("Bootstrap role", func() {

				conf := config.Config{Role: string(models.HostRoleBootstrap),
					ClusterID:           "cluster-id",
					InfraEnvID:          "infra-env-id",
					HostID:              "host-id",
					Device:              "/dev/vda",
					URL:                 "https://assisted-service.com:80",
					OpenshiftVersion:    openShiftVersion,
					MCOImage:            "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221",
					EnableSkipMcoReboot: withEnableSkipMcoReboot,
				}
				BeforeEach(func() {
					installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition, cleanupDevice)
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
					validateErr := validations.ValidateHostname(hostname)
					if hostname == "localhost" || validateErr != nil {
						mockops.EXPECT().CreateRandomHostname(gomock.Any()).Return(nil).Times(1)
					}
				}
				checkOcBinary := func(exists bool) {
					mockops.EXPECT().FileExists(openshiftClientBin).Return(exists).Times(1)
				}
				checkOverlayService := func(name string, injectError bool) {
					// verify that we retry if `systemctl show` fails for some reason
					mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "show", "-P", "ActiveState", name).Return("", errors.New("bad")).Times(1)
					// verify that we retry if service is still inactive (hasn't started yet)
					mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "show", "-P", "ActiveState", name).Return("inactive", nil).Times(1)
					if !injectError {
						// ok, succeed this time
						mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "show", "-P", "ActiveState", name).Return("active", nil).Times(1)
					} else {
						// oh no! the service failed!
						mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "show", "-P", "ActiveState", name).Return("failed", nil).Times(1)
						mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "status", name).Return("status", nil).Times(1)
					}
				}
				overlayNodeImage := func(injectError bool) {
					mockops.EXPECT().SystemctlAction("start", "--no-block", nodeImagePullService, nodeImageOverlayService).Return(nil).Times(1)
					checkOverlayService(nodeImagePullService, false)
					checkOverlayService(nodeImageOverlayService, injectError)
				}
				startServicesSuccess := func() {
					services := []string{"bootkube.service", "progress.service", "approve-csr.service"}
					for i := range services {
						mockops.EXPECT().SystemctlAction("start", services[i]).Return(nil).Times(1)
					}
				}
				WaitMasterNodesSucccessWithCluster := func(cluster *models.Cluster) {
					mockbmclient.EXPECT().GetEnabledHostsNamesHosts(gomock.Any(), gomock.Any()).Return(inventoryNamesHost, nil).AnyTimes()
					mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(map[string]string{}), nil).Times(1)
					kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65"}
					mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), inventoryNamesHost["node0"].Host.InfraEnvID.String(), inventoryNamesHost["node0"].Host.ID.String(), models.HostStageJoined, "").Times(1)
					kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
						"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238"}
					mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), inventoryNamesHost["node1"].Host.InfraEnvID.String(), inventoryNamesHost["node1"].Host.ID.String(), models.HostStageJoined, "").Times(1)
					mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(cluster, nil).Times(2)
				}
				WaitMasterNodesSucccess := func() {
					WaitMasterNodesSucccessWithCluster(&models.Cluster{})
				}
				prepareControllerSuccess := func() {
					mockops.EXPECT().PrepareController().Return(nil).Times(1)
				}
				waitForBootkubeSuccess := func() {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStageWaitingForBootkube, "").Return(nil).Times(1)
					mockops.EXPECT().FileExists("/opt/openshift/.bootkube.done").Return(true).Times(1)
				}
				bootkubeStatusSuccess := func() {
					mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "status", "bootkube.service").Return("1", nil).Times(1)
				}

				waitForETCDBootstrapSuccess := func() {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId, hostId, models.HostStageWaitingForBootkube, "waiting for ETCD bootstrap to be complete").Return(nil).Times(1)
					mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "is-active", "progress.service").Return("inactive", nil).Times(1)
				}

				bootstrapETCDStatusSuccess := func() {
					mockops.EXPECT().ExecPrivilegeCommand(gomock.Any(), "systemctl", "status", "progress.service").Return("1", nil).Times(1)
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

				bootstrapSetup := func(invoker ...string) {
					cleanInstallDevice()
					mkdirSuccess(sshDir)
					mkdirSuccess(InstallDir)
					downloadFileSuccess(bootstrapIgn)
					extractSecretFromIgnitionSuccess()
					extractIgnitionToFS("Success", nil)
					generateSshKeyPairSuccess()
					createOpenshiftSshManifestSuccess()
					daemonReload(nil)
					setupInvoker(invoker...)
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
							checkLocalHostname("notlocalhost", nil)
							restartNetworkManager(nil)
							prepareControllerSuccess()
							checkOcBinary(true)
							startServicesSuccess()
							WaitMasterNodesSucccess()
							waitForBootkubeSuccess()
							bootkubeStatusSuccess()
							waitForETCDBootstrapSuccess()
							bootstrapETCDStatusSuccess()
							resolvConfSuccess()
							waitForControllerSuccessfully(conf.ClusterID)
							//HostRoleMaster flow:
							downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
							writeToDiskSuccess(gomock.Any())
							reportLogProgressSuccess()
							setBootOrderSuccess()
							uploadLogsSuccess(true)
							ironicAgentDoesntExist()
							rebootSuccess()
							getEncapsulatedMcSuccess(nil)
							overwriteImageSuccess()
							ret := installerObj.InstallNode()
							Expect(ret).Should(BeNil())
						})
						It("bootstrap role happy flow on RHEL-only bootimage", func() {
							updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
								{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
								{string(models.HostStageWaitingForControlPlane), waitingForMastersStatusInfo},
								{string(models.HostStageInstalling), string(models.HostRoleMaster)},
								{string(models.HostStageWritingImageToDisk)},
								{string(models.HostStageRebooting)},
							})
							bootstrapSetup()
							checkLocalHostname("notlocalhost", nil)
							restartNetworkManager(nil)
							prepareControllerSuccess()
							checkOcBinary(false)
							overlayNodeImage(false)
							checkOcBinary(true)
							startServicesSuccess()
							WaitMasterNodesSucccess()
							waitForBootkubeSuccess()
							bootkubeStatusSuccess()
							waitForETCDBootstrapSuccess()
							bootstrapETCDStatusSuccess()
							resolvConfSuccess()
							waitForControllerSuccessfully(conf.ClusterID)
							//HostRoleMaster flow:
							downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
							writeToDiskSuccess(gomock.Any())
							reportLogProgressSuccess()
							setBootOrderSuccess()
							uploadLogsSuccess(true)
							ironicAgentDoesntExist()
							rebootSuccess()
							getEncapsulatedMcSuccess(nil)
							overwriteImageSuccess()
							ret := installerObj.InstallNode()
							Expect(ret).Should(BeNil())
						})
						It("bootstrap role fails on RHEL-only bootimage if can't overlay node image", func() {
							updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
								{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
								{string(models.HostStageInstalling), string(models.HostRoleMaster)},
								{string(models.HostStageWritingImageToDisk)},
							})
							bootstrapSetup()
							checkLocalHostname("notlocalhost", nil)
							restartNetworkManager(nil)
							prepareControllerSuccess()
							checkOcBinary(false)
							overlayNodeImage(true)
							//HostRoleMaster flow:
							downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
							writeToDiskSuccess(gomock.Any())
							setBootOrderSuccess()
							getEncapsulatedMcSuccess(nil)
							overwriteImageSuccess()
							ret := installerObj.InstallNode()
							Expect(ret).To(HaveOccurred())
						})
						It("bootstrap role happy flow with invalid hostname", func() {
							updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
								{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
								{string(models.HostStageWaitingForControlPlane), waitingForMastersStatusInfo},
								{string(models.HostStageInstalling), string(models.HostRoleMaster)},
								{string(models.HostStageWritingImageToDisk)},
								{string(models.HostStageRebooting)},
							})
							bootstrapSetup()
							checkLocalHostname("InvalidHostname", nil)
							restartNetworkManager(nil)
							prepareControllerSuccess()
							checkOcBinary(true)
							startServicesSuccess()
							WaitMasterNodesSucccess()
							waitForBootkubeSuccess()
							bootkubeStatusSuccess()
							waitForETCDBootstrapSuccess()
							bootstrapETCDStatusSuccess()
							resolvConfSuccess()
							waitForControllerSuccessfully(conf.ClusterID)
							//HostRoleMaster flow:
							downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
							writeToDiskSuccess(gomock.Any())
							reportLogProgressSuccess()
							setBootOrderSuccess()
							uploadLogsSuccess(true)
							ironicAgentDoesntExist()
							rebootSuccess()
							getEncapsulatedMcSuccess(nil)
							overwriteImageSuccess()
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
							checkOcBinary(true)
							startServicesSuccess()
							WaitMasterNodesSucccess()
							waitForBootkubeSuccess()
							bootkubeStatusSuccess()
							waitForETCDBootstrapSuccess()
							bootstrapETCDStatusSuccess()
							resolvConfSuccess()
							waitForControllerSuccessfully(conf.ClusterID)
							//HostRoleMaster flow:
							downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
							writeToDiskSuccess(gomock.Any())
							setBootOrderSuccess()
							uploadLogsSuccess(true)
							reportLogProgressSuccess()
							ironicAgentDoesntExist()
							rebootSuccess()
							kargs := []string{"arg1", "arg2=val2"}
							getEncapsulatedMcSuccess(kargs)
							overwriteImageSuccess(kargs...)
							ret := installerObj.InstallNode()
							Expect(ret).Should(BeNil())
						})
						It("bootstrap role happy flow (agent-based installer)", func() {
							updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
								{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
								{string(models.HostStageWaitingForControlPlane), waitingForMastersStatusInfo},
								{string(models.HostStageInstalling), string(models.HostRoleMaster)},
								{string(models.HostStageWritingImageToDisk)},
								{string(models.HostStageRebooting)},
							})
							bootstrapSetup("agent-installer")
							checkLocalHostname("notlocalhost", nil)
							restartNetworkManager(nil)
							prepareControllerSuccess()
							checkOcBinary(true)
							startServicesSuccess()
							WaitMasterNodesSucccessWithCluster(&models.Cluster{
								Platform: &models.Platform{
									Type: models.PlatformTypeBaremetal.Pointer(),
								},
							})
							waitForBootkubeSuccess()
							bootkubeStatusSuccess()
							waitForETCDBootstrapSuccess()
							bootstrapETCDStatusSuccess()
							resolvConfSuccess()
							waitForControllerSuccessfully(conf.ClusterID)
							waitForWorkersSuccessfully()
							//HostRoleMaster flow:
							downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
							writeToDiskSuccess(gomock.Any())
							reportLogProgressSuccess()
							setBootOrderSuccess()
							uploadLogsSuccess(true)
							ironicAgentDoesntExist()
							rebootSuccess()
							getEncapsulatedMcSuccess(nil)
							overwriteImageSuccess()
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
					setBootOrderSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
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
					checkLocalHostname("notlocalhost", nil)
					restartNetworkManager(nil)
					prepareControllerSuccess()
					checkOcBinary(true)
					startServicesSuccess()
					WaitMasterNodesSucccess()
					waitForBootkubeSuccess()
					bootkubeStatusSuccess()
					waitForETCDBootstrapSuccess()
					bootstrapETCDStatusSuccess()
					resolvConfSuccess()
					waitForControllerSuccessfully(conf.ClusterID)
					//HostRoleMaster flow:
					downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
					writeToDiskSuccess(gomock.Any())
					setBootOrderSuccess()
					uploadLogsSuccess(true)
					reportLogProgressSuccess()
					ironicAgentDoesntExist()
					rebootSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
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
					setBootOrderSuccess()
					extractSecretFromIgnitionSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
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
					checkLocalHostname("notlocalhost", nil)
					err := fmt.Errorf("Failed to restart NetworkManager")
					restartNetworkManager(err)
					//HostRoleMaster flow:
					downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
					writeToDiskSuccess(gomock.Any())
					setBootOrderSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
					ret := installerObj.InstallNode()
					Expect(ret).Should(Equal(err))
				})

				for _, test := range []struct {
					PlatformType                     models.PlatformType
					ExpectedRemoveUninitializedTaint bool
				}{
					{
						PlatformType:                     models.PlatformTypeNutanix,
						ExpectedRemoveUninitializedTaint: true,
					},
					{
						PlatformType:                     models.PlatformTypeVsphere,
						ExpectedRemoveUninitializedTaint: true,
					},
					{
						PlatformType:                     models.PlatformTypeNone,
						ExpectedRemoveUninitializedTaint: false,
					},
					{
						PlatformType:                     models.PlatformTypeBaremetal,
						ExpectedRemoveUninitializedTaint: false,
					},
				} {
					platformType := test.PlatformType
					expectedRemoveUninitializedTaint := test.ExpectedRemoveUninitializedTaint

					Context("untaint nodes on bootstrap", func() {

						var cluster *models.Cluster
						BeforeEach(func() {
							cluster = &models.Cluster{
								Platform: &models.Platform{
									Type: &platformType,
								},
							}
						})

						It(fmt.Sprintf("for platform type %v is expected to remove uninitialized taint = %v", platformType, expectedRemoveUninitializedTaint), func() {
							updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
								{string(models.HostStageWaitingForControlPlane), waitingForBootstrapToPrepare},
								{string(models.HostStageWaitingForControlPlane), waitingForMastersStatusInfo},
								{string(models.HostStageInstalling), string(models.HostRoleMaster)},
								{string(models.HostStageWritingImageToDisk)},
								{string(models.HostStageRebooting)},
							})
							extractIgnitionToFS("extract failure", fmt.Errorf("extract failed"))

							bootstrapSetup()
							checkLocalHostname("notlocalhost", nil)
							restartNetworkManager(nil)
							prepareControllerSuccess()
							checkOcBinary(true)
							startServicesSuccess()

							mockbmclient.EXPECT().GetEnabledHostsNamesHosts(gomock.Any(), gomock.Any()).Return(inventoryNamesHost, nil).AnyTimes()
							mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(map[string]string{}), nil).Times(1)
							kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65"}
							mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
							mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), inventoryNamesHost["node0"].Host.InfraEnvID.String(), inventoryNamesHost["node0"].Host.ID.String(), models.HostStageJoined, "").Times(1)
							// node not ready
							mockk8sclient.EXPECT().ListMasterNodes().Return(GetNotReadyKubeNodes(kubeNamesIds), nil).Times(1)
							mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(cluster, nil).Times(2)
							if expectedRemoveUninitializedTaint {
								mockk8sclient.EXPECT().UntaintNode("node0").Return(nil).Times(1)
							} else {
								mockk8sclient.EXPECT().UntaintNode(gomock.Any()).Times(0)
							}
							// node becomes ready
							mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
							kubeNamesIds = map[string]string{"node0": "7916fa89-ea7a-443e-a862-b3e930309f65",
								"node1": "eb82821f-bf21-4614-9a3b-ecb07929f238"}
							mockk8sclient.EXPECT().ListMasterNodes().Return(GetKubeNodes(kubeNamesIds), nil).Times(1)
							mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), inventoryNamesHost["node1"].Host.InfraEnvID.String(), inventoryNamesHost["node1"].Host.ID.String(), models.HostStageJoined, "").Times(1)
							waitForBootkubeSuccess()
							bootkubeStatusSuccess()
							waitForETCDBootstrapSuccess()
							bootstrapETCDStatusSuccess()
							resolvConfSuccess()
							waitForControllerSuccessfully(conf.ClusterID)
							//HostRoleMaster flow:
							downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
							writeToDiskSuccess(gomock.Any())
							setBootOrderSuccess()
							uploadLogsSuccess(true)
							reportLogProgressSuccess()
							ironicAgentDoesntExist()
							rebootSuccess()
							getEncapsulatedMcSuccess(nil)
							overwriteImageSuccess()
							ret := installerObj.InstallNode()
							Expect(ret).Should(BeNil())
						})
					})
				}
			})
			Context("Bootstrap role waiting for control plane", func() {

				conf := config.Config{Role: string(models.HostRoleBootstrap),
					ClusterID:           "cluster-id",
					InfraEnvID:          "infra-env-id",
					HostID:              "host-id",
					Device:              "/dev/vda",
					URL:                 "https://assisted-service.com:80",
					OpenshiftVersion:    openShiftVersion,
					MCOImage:            "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221",
					EnableSkipMcoReboot: withEnableSkipMcoReboot,
				}
				BeforeEach(func() {
					installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition, cleanupDevice)
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
					logsInBytes, _ := os.ReadFile("../../test_files/mcs_logs.txt")
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
					logsInBytes, _ := os.ReadFile("../../test_files/mcs_logs.txt")
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
				var (
					conf          config.Config
					installerArgs []string
				)

				BeforeEach(func() {
					installerArgs = []string{"-n", "--append-karg", "nameserver=8.8.8.8"}
					conf = config.Config{Role: string(models.HostRoleMaster),
						ClusterID:           "cluster-id",
						InfraEnvID:          "infra-env-id",
						HostID:              "host-id",
						Device:              "/dev/vda",
						URL:                 "https://assisted-service.com:80",
						OpenshiftVersion:    openShiftVersion,
						InstallerArgs:       installerArgs,
						EnableSkipMcoReboot: withEnableSkipMcoReboot,
					}
					installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition, cleanupDevice)
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
					setBootOrderSuccess()
					uploadLogsSuccess(false)
					reportLogProgressSuccess()
					ironicAgentDoesntExist()
					rebootSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
					ret := installerObj.InstallNode()
					Expect(ret).Should(BeNil())
				})

				It("installs to the existing root and does not skip reboot, clean install device, or set boot order when coreosImage is set", func() {
					installerObj.Config.CoreosImage = "example.com/coreos/os:latest"
					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
						{string(models.HostStageInstalling), conf.Role},
						{string(models.HostStageWritingImageToDisk)},
						{string(models.HostStageRebooting)},
					})
					cleanupDevice.EXPECT().CleanupInstallDevice(device).Times(0)
					mkdirSuccess(InstallDir)
					downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
					mockops.EXPECT().WriteImageToExistingRoot(gomock.Any(), filepath.Join(InstallDir, "master-host-id.ign"), gomock.Any()).Return(nil).Times(1)
					mockops.EXPECT().SetBootOrder(device).Times(0)
					uploadLogsSuccess(false)
					reportLogProgressSuccess()
					ironicAgentDoesntExist()
					rebootSuccess()
					mockops.EXPECT().GetEncapsulatedMC(gomock.Any()).Times(0)
					mockops.EXPECT().OverwriteOsImage(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
					ret := installerObj.InstallNode()
					Expect(ret).Should(BeNil())
				})

				It("HostRoleMaster role happy flow with skipping disk cleanup", func() {
					installerObj.Config.SkipInstallationDiskCleanup = true
					cleanupDevice.EXPECT().CleanupInstallDevice(gomock.Any()).Return(nil).Times(0)

					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role},
						{string(models.HostStageInstalling), conf.Role},
						{string(models.HostStageWritingImageToDisk)},
						{string(models.HostStageRebooting)},
					})
					mkdirSuccess(InstallDir)
					downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
					writeToDiskSuccess(installerArgs)
					setBootOrderSuccess()
					uploadLogsSuccess(false)
					reportLogProgressSuccess()
					ironicAgentDoesntExist()
					rebootSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
					ret := installerObj.InstallNode()
					Expect(ret).Should(BeNil())
				})

				It("HostRoleMaster role happy flow with disk cleanup", func() {
					cleanInstallDeviceClean := func() {
						cleanupDevice.EXPECT().CleanupInstallDevice(device).Return(nil).Times(1)
					}
					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
					cleanInstallDeviceClean()
					err := fmt.Errorf("failed to create dir")
					mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
					ret := installerObj.InstallNode()
					Expect(ret).Should(Equal(err))
				})
				It("HostRoleMaster role failed to cleanup disk continues installation", func() {
					err := fmt.Errorf("Failed to remove vg")
					cleanupErrorText := fmt.Sprintf("Could not clean install device %s. The installation will continue. If the installation fails, clean the disk and try again", device)
					cleanInstallDeviceError := func() {
						cleanupDevice.EXPECT().CleanupInstallDevice(device).Return(err).Times(1)
						mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
					}
					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}, {string(models.HostStageStartingInstallation), cleanupErrorText}})
					cleanInstallDeviceError()
					ret := installerObj.InstallNode()
					Expect(ret).Should(Equal(err))
				})
				It("HostRoleMaster role raid cleanup disk - happy flow", func() {
					cleanInstallDeviceClean := func() {
						cleanupDevice.EXPECT().CleanupInstallDevice(device).Return(nil).Times(1)
					}
					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}})
					cleanInstallDeviceClean()
					err := fmt.Errorf("failed to create dir")
					mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
					ret := installerObj.InstallNode()
					Expect(ret).Should(Equal(err))
				})
				It("HostRoleMaster role raid cleanup disk - failed continues installation", func() {
					err := fmt.Errorf("failed cleaning raid device")
					cleanupErrorText := fmt.Sprintf("Could not clean install device %s. The installation will continue. If the installation fails, clean the disk and try again", device)
					cleanInstallDeviceClean := func() {
						cleanupDevice.EXPECT().CleanupInstallDevice(device).Return(err).Times(1)
						mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
					}
					updateProgressSuccess([][]string{{string(models.HostStageStartingInstallation), conf.Role}, {string(models.HostStageStartingInstallation), cleanupErrorText}})
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
					setBootOrderSuccess()
					uploadLogsSuccess(false)
					reportLogProgressSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
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
					err := fmt.Errorf("failed writing image to disk")
					mockops.EXPECT().WriteImageToDisk(gomock.Any(), filepath.Join(InstallDir, "master-host-id.ign"), device, installerArgs).Return(err).Times(3)
					ret := installerObj.InstallNode()
					Expect(ret).To(HaveOccurred())
					Expect(ret.Error()).Should(ContainSubstring("failed writing image to disk"))
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
					setBootOrderSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
					ironicAgentDoesntExist()
					err := fmt.Errorf("failed to reboot")
					mockops.EXPECT().Reboot("+1").Return(err).Times(1)
					ret := installerObj.InstallNode()
					Expect(ret).Should(Equal(err))
				})
			})
			Context("Worker role", func() {
				conf := config.Config{Role: string(models.HostRoleWorker),
					ClusterID:           "cluster-id",
					InfraEnvID:          "infra-env-id",
					HostID:              "host-id",
					Device:              "/dev/vda",
					URL:                 "https://assisted-service.com:80",
					OpenshiftVersion:    openShiftVersion,
					EnableSkipMcoReboot: withEnableSkipMcoReboot,
				}
				BeforeEach(func() {
					installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition, cleanupDevice)
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
					mockops.EXPECT().WriteImageToDisk(gomock.Any(), filepath.Join(InstallDir, "worker-host-id.ign"), device, nil).Return(nil).Times(1)
					setBootOrderSuccess()
					// failure must do nothing
					reportLogProgressSuccess()
					mockops.EXPECT().UploadInstallationLogs(false).Return("", errors.Errorf("Dummy")).Times(1)
					ironicAgentDoesntExist()
					rebootSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
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
					EnableSkipMcoReboot:  withEnableSkipMcoReboot,
				}
				BeforeEach(func() {
					installerObj = NewAssistedInstaller(l, conf, mockops, mockbmclient, k8sBuilder, mockIgnition, cleanupDevice)
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
					validateErr := validations.ValidateHostname(hostname)
					if hostname == "localhost" || validateErr != nil {
						mockops.EXPECT().CreateRandomHostname(gomock.Any()).Return(nil).Times(1)
					}
				}
				checkOcBinary := func(exists bool) {
					mockops.EXPECT().FileExists(openshiftClientBin).Return(exists).Times(1)
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
					mockops.EXPECT().FileExists("/opt/openshift/.bootkube.done").Return(true).Times(1)
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
					checkOcBinary(true)
					startServicesSuccess()
					waitForBootkubeSuccess()
					bootkubeStatusSuccess()
					//HostRoleMaster flow:
					verifySingleNodeMasterIgnitionSuccess()
					singleNodeMergeIgnitionSuccess()
					downloadHostIgnitionSuccess(infraEnvId, hostId, "master-host-id.ign")
					mockops.EXPECT().WriteImageToDisk(gomock.Any(), singleNodeMasterIgnitionPath, device, nil).Return(nil).Times(1)
					setBootOrderSuccess()
					uploadLogsSuccess(true)
					reportLogProgressSuccess()
					ironicAgentDoesntExist()
					rebootNowSuccess()
					getEncapsulatedMcSuccess(nil)
					overwriteImageSuccess()
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
					checkLocalHostname("notlocalhost", err)
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
					checkOcBinary(true)
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
	}
})

func GetKubeNodes(kubeNamesIds map[string]string) *v1.NodeList {
	file, _ := os.ReadFile("../../test_files/node.json")
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

func GetNotReadyKubeNodes(kubeNamesIds map[string]string) *v1.NodeList {
	nodeList := GetKubeNodes(kubeNamesIds)
	newNodeList := &v1.NodeList{}
	for _, node := range nodeList.Items {
		node.Status.Conditions = []v1.NodeCondition{{Status: v1.ConditionFalse, Type: v1.NodeReady}}
		newNodeList.Items = append(newNodeList.Items, node)
	}
	return newNodeList
}

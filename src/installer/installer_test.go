package installer

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

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
		l                = logrus.New()
		ctrl             *gomock.Controller
		mockops          *ops.MockOps
		mockbmclient     *inventory_client.MockInventoryClient
		mockk8sclien     *k8s_client.MockK8SClient
		i                *installer
		bootstrapIgn     = "bootstrap.ign"
		masterIgn        = "master.ign"
		workerIgn        = "worker.ign"
		openShiftVersion = "4.4"
		image, _         = utils.GetRhcosImageByOpenshiftVersion(openShiftVersion)
	)
	device := "/dev/vda"
	l.SetOutput(ioutil.Discard)
	mkdirSuccess := func() {
		mockops.EXPECT().Mkdir(InstallDir).Return(nil).Times(1)
	}
	downloadFileSuccess := func(fileName string) {
		mockbmclient.EXPECT().DownloadFile(fileName, filepath.Join(InstallDir, fileName)).Return(nil).Times(1)
	}
	udpateStatusSuccess := func(statuses []string) {
		for _, status := range statuses {
			mockbmclient.EXPECT().UpdateHostStatus(status).Return(nil).Times(1)

		}
	}

	writeToDiskSuccess := func() {
		mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device, image).Return(nil).Times(1)
	}
	rebootSuccess := func() {
		mockops.EXPECT().Reboot().Return(nil).Times(1)
	}
	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockk8sclien = k8s_client.NewMockK8SClient(ctrl)
	})

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
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, mockk8sclien)
		})
		mcoImage, _ := utils.GetMCOByOpenshiftVersion(conf.OpenshiftVersion)
		extractSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(
				"podman", "run", "--net", "host",
				"--volume", "/:/rootfs:rw",
				"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
				"--privileged",
				"--entrypoint", "/usr/bin/machine-config-daemon",
				mcoImage,
				"start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from",
				filepath.Join(InstallDir, bootstrapIgn), "--skip-reboot")
		}
		startServicesSuccess := func() {
			services := []string{"bootkube.service", "progress.service", "approve-csr.service"}
			for i := range services {
				mockops.EXPECT().ExecPrivilegeCommand("systemctl", "start", services[i]).Return("", nil).Times(1)
			}
		}
		WaitMasterNodesSucccess := func() {
			mockk8sclien.EXPECT().WaitForMasterNodes(gomock.Any(), 2).Return(nil).Times(1)
		}
		patchEtcdSuccess := func() {
			mockk8sclien.EXPECT().PatchEtcd().Return(nil).Times(1)
		}
		waitForBootkubeSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("bash", "-c", "systemctl status bootkube.service | grep 'bootkube.service: Succeeded' | wc -l").Return("1", nil).Times(1)
		}
		bootkubeStatusSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("systemctl", "status", "bootkube.service").Return("1", nil).Times(1)
		}
		extractSecretFromIgnitionSuccess := func() {
			mockops.EXPECT().ExtractFromIgnition(filepath.Join(InstallDir, bootstrapIgn), dockerConfigFile).Return(nil).Times(1)
		}
		It("bootstrap role happy flow", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				RunningBootstrap,
				WaitForControlPlane,
				fmt.Sprintf("Runing %s installation", HostRoleMaster),
				WritingImageToDisk,
				Reboot,
			})
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractSuccess()
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
				fmt.Sprintf("Runing %s installation", HostRoleMaster),
				WritingImageToDisk,
			})
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			extractSecretFromIgnitionSuccess()
			extractSuccess()
			startServicesSuccess()
			WaitMasterNodesSucccess()
			err := fmt.Errorf("Etcd patch failed")
			mockk8sclien.EXPECT().PatchEtcd().Return(err).Times(1)
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
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, mockk8sclien)
		})
		It("master role happy flow", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf("Runing %s installation", conf.Role),
				WritingImageToDisk,
				Reboot,
			})
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("HostRoleMaster role failed to create dir", func() {
			udpateStatusSuccess([]string{StartingInstallation})
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to get ignition", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf("Runing %s installation", conf.Role),
			})
			mkdirSuccess()
			err := fmt.Errorf("failed to fetch file")
			mockbmclient.EXPECT().DownloadFile(masterIgn, filepath.Join(InstallDir, masterIgn)).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to write image to disk", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf("Runing %s installation", conf.Role),
				WritingImageToDisk,
			})
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			err := fmt.Errorf("failed to write image to disk")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device, image).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to reboot", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf("Runing %s installation", conf.Role),
				WritingImageToDisk,
				Reboot,
			})
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
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, mockk8sclien)
		})
		It("worker role happy flow", func() {
			udpateStatusSuccess([]string{StartingInstallation,
				fmt.Sprintf("Runing %s installation", conf.Role),
				WritingImageToDisk,
				Reboot,
			})
			mkdirSuccess()
			downloadFileSuccess(workerIgn)
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, workerIgn), device, image).Return(nil).Times(1)
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
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, mockk8sclien)
		})
		It("Bad openshift version", func() {
			err := i.InstallNode()
			Expect(err).To(HaveOccurred())
		})
	})
	AfterEach(func() {
		ctrl.Finish()
	})

})

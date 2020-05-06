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
		l            = logrus.New()
		ctrl         *gomock.Controller
		mockops      *ops.MockOps
		mockbmclient *inventory_client.MockInventoryClient
		mockk8sclien *k8s_client.MockK8SClient
		i            *installer
		bootstrapIgn = "bootstrap.ign"
		masterIgn    = "master.ign"
	)
	device := "/dev/vda"
	l.SetOutput(ioutil.Discard)
	mkdirSuccess := func() {
		mockops.EXPECT().Mkdir(InstallDir).Return(nil).Times(1)
	}
	downloadFileSuccess := func(fileName string) {
		mockbmclient.EXPECT().DownloadFile(fileName, filepath.Join(InstallDir, fileName)).Return(nil).Times(1)
	}
	writeToDiskSuccess := func() {
		mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device).Return(nil).Times(1)
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

	Context("HostRoleBootstrap role", func() {
		conf := config.Config{Role: HostRoleBootstrap, ClusterID: "cluster-id", Device: "/dev/vda", Host: "https://bm-inventory.com", Port: 80}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, mockk8sclien)
		})
		extractSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(
				"podman", "run", "--net", "host",
				"--volume", "/:/rootfs:rw",
				"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
				"--privileged",
				"--entrypoint", "/usr/bin/machine-config-daemon",
				machineConfigImage,
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
			mockk8sclien.EXPECT().WaitForMasterNodes(2).Return(nil).Times(1)
		}
		patchEtcdSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("oc", "--kubeconfig", filepath.Join(InstallDir, Kubeconfig),
				"patch", "etcd", "cluster", "-p",
				`{"spec": {"unsupportedConfigOverrides": {"useUnsupportedUnsafeNonHANonProductionUnstableEtcd": true}}}`,
				"--type", "merge")
		}
		waitForBootkubeSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("bash", "-c", "systemctl status bootkube.service | grep 'bootkube.service: Succeeded' | wc -l").Return("1", nil).Times(1)
		}
		bootkubeStatusSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("systemctl", "status", "bootkube.service").Return("1", nil).Times(1)
		}
		extractFromIgnition := func() {
			mockops.EXPECT().ExtractFromIgnition(filepath.Join(InstallDir, bootstrapIgn), dockerConfigFile).Return(nil).Times(1)
		}
		It("HostRoleBootstrap role happy flow", func() {
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
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
			extractFromIgnition()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})

	})
	Context("Master role", func() {
		conf := config.Config{Role: "master", ClusterID: "cluster-id", Device: "/dev/vda", Host: "https://bm-inventory.com", Port: 80}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient, mockk8sclien)
		})
		It("HostRoleMaster role happy flow", func() {
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("HostRoleMaster role failed to create dir", func() {
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(InstallDir).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to get ignition", func() {
			mkdirSuccess()
			err := fmt.Errorf("failed to fetch file")
			mockbmclient.EXPECT().DownloadFile(masterIgn, filepath.Join(InstallDir, masterIgn)).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to write image to disk", func() {
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			err := fmt.Errorf("failed to write image to disk")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(InstallDir, masterIgn), device).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("HostRoleMaster role failed to reboot", func() {
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			err := fmt.Errorf("failed to reboot")
			mockops.EXPECT().Reboot().Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
	})
	AfterEach(func() {
		ctrl.Finish()
	})

})

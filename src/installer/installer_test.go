package installer

import (
	"fmt"
	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"path/filepath"
	"testing"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

var _ = Describe("installer master role", func() {
	var (
		l            = logrus.New()
		ctrl    *gomock.Controller
		mockops      *ops.MockOps
		mockbmclient      *inventory_client.MockInventoryClient
		i            *installer
		bootstrapIgn = "bootstrap.ign"
		masterIgn    = "master.ign"
	)
	device := "/dev/vda"
	l.SetOutput(ioutil.Discard)
	mkdirSuccess := func() {
		mockops.EXPECT().Mkdir(installDir).Return(nil).Times(1)
	}
	downloadFileSuccess := func(fileName string) {
		mockbmclient.EXPECT().DownloadFile(fileName, filepath.Join(installDir, fileName)).Return(nil).Times(1)
	}
	writeToDiskSuccess := func() {
		mockops.EXPECT().WriteImageToDisk(filepath.Join(installDir, "master.ign"), device).Return(nil).Times(1)
	}
	rebootSuccess := func() {
		mockops.EXPECT().Reboot().Return(nil).Times(1)
	}
	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
	})

	Context("Bootstrap role", func() {
		conf := config.Config{Role: "bootstrap", ClusterID: "cluster-id", Device: "/dev/vda", Host: "https://bm-inventory.com", Port: 80}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient)
		})
		extractSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand(
				"podman", "run", "--net", "host",
				"--volume", "/:/rootfs:rw",
				"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
				"--privileged",
				"--entrypoint", "/machine-config-daemon",
				"docker.io/eranco/mcd:latest",
				"start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from",
				filepath.Join(installDir, "bootstrap.ign"), "--skip-reboot")
		}
		startServicesSuccess := func() {
			services := []string{"bootkube.service", "progress.service", "approve-csr.service"}
			for i := range services {
				mockops.EXPECT().ExecPrivilegeCommand("systemctl", "start", services[i]).Return("", nil).Times(1)
			}
		}
		GetNodesSucccess := func() {

			mockops.EXPECT().ExecPrivilegeCommand("kubectl",
				"--kubeconfig", filepath.Join(installDir, kubeconfig), "get", "nodes").Return("2", nil).Times(2)
		}
		GetMasterNodesSucccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("bash",
				"-c",
				fmt.Sprintf("/usr/bin/kubectl --kubeconfig %s get nodes | grep master | wc -l", filepath.Join(installDir, kubeconfig)),
			).Return("2", nil).Times(1)
		}
		GetReadyNodesSucccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("bash",
				"-c",
				fmt.Sprintf("/usr/bin/kubectl --kubeconfig %s get nodes | grep master | grep -v NotReady | grep Ready | wc -l", filepath.Join(installDir, kubeconfig)),
			).Return("2", nil).Times(1)
		}
		patchEtcdSuccess := func() {
			mockops.EXPECT().ExecPrivilegeCommand("until", "oc", "--kubeconfig", filepath.Join(installDir, kubeconfig),
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
		It("bootstrap role happy flow", func() {
			mkdirSuccess()
			downloadFileSuccess(bootstrapIgn)
			extractSuccess()
			startServicesSuccess()
			downloadFileSuccess(kubeconfig)
			patchEtcdSuccess()
			GetMasterNodesSucccess()
			GetNodesSucccess()
			GetReadyNodesSucccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			//master flow:
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})

	})
	Context("Master role", func() {
		conf := config.Config{Role: "master", ClusterID: "cluster-id", Device: "/dev/vda", Host: "https://bm-inventory.com", Port: 80}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops, mockbmclient)
		})
		It("master role happy flow", func() {
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			writeToDiskSuccess()
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})
		It("master role failed to create dir", func() {
			err := fmt.Errorf("failed to create dir")
			mockops.EXPECT().Mkdir(installDir).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("master role failed to get ignition", func() {
			mkdirSuccess()
			err := fmt.Errorf("failed to fetch file")
			mockbmclient.EXPECT().DownloadFile(masterIgn, filepath.Join(installDir, masterIgn)).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("master role failed to write image to disk", func() {
			mkdirSuccess()
			downloadFileSuccess(masterIgn)
			err := fmt.Errorf("failed to write image to disk")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(installDir, masterIgn), device).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("master role failed to reboot", func() {
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

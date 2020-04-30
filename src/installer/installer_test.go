package installer

import (
	"fmt"
	"github.com/eranco74/assisted-installer/src/config"
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
		i            *installer
		bootstrapIgn = "bootstrap.ign"
		masterIgn    = "master.ign"
	)
	device := "/dev/vda"
	baseUrl := "https://s3.com/test/cluster-id/"
	l.SetOutput(ioutil.Discard)
	mkdirSuccess := func() {
		mockops.EXPECT().Mkdir(installDir).Return(nil).Times(1)
	}
	curlSuccess := func(fileName string) {
		mockops.EXPECT().ExecPrivilegeCommand("curl", "-s", baseUrl+fileName, "-o", filepath.Join(installDir, fileName)).Return("", nil).Times(1)
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
	})

	Context("Bootstrap role", func() {
		conf := config.Config{Role: "bootstrap", ClusterID: "cluster-id", Device: "/dev/vda", S3EndpointURL: "https://s3.com", S3Bucket: "test"}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops)
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
			mockops.EXPECT().ExecPrivilegeCommand("oc", "--kubeconfig", filepath.Join(installDir, kubeconfig),
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
		conf = config.Config{Role: "bootstrap", ClusterID: "cluster-id", Device: "/dev/vda", S3EndpointURL: "https://s3.com", S3Bucket: "test"}
		It("bootstrap role happy flow", func() {
			mkdirSuccess()
			curlSuccess(bootstrapIgn)
			extractSuccess()
			startServicesSuccess()
			curlSuccess(kubeconfig)
			patchEtcdSuccess()
			GetMasterNodesSucccess()
			GetNodesSucccess()
			GetReadyNodesSucccess()
			waitForBootkubeSuccess()
			bootkubeStatusSuccess()
			//master flow:
			curlSuccess(masterIgn)
			writeToDiskSuccess()
			rebootSuccess()
			ret := i.InstallNode()
			Expect(ret).Should(BeNil())
		})

	})
	Context("Master role", func() {
		conf := config.Config{Role: "master", ClusterID: "cluster-id", Device: "/dev/vda", S3EndpointURL: "https://s3.com", S3Bucket: "test"}
		BeforeEach(func() {
			i = NewAssistedInstaller(l, conf, mockops)
		})
		It("master role happy flow", func() {
			mkdirSuccess()
			curlSuccess(masterIgn)
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
			mockops.EXPECT().ExecPrivilegeCommand("curl", "-s", baseUrl+masterIgn,
				"-o", filepath.Join(installDir, masterIgn)).Return("", err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("master role failed to write image to disk", func() {
			mkdirSuccess()
			curlSuccess(masterIgn)
			err := fmt.Errorf("failed to write image to disk")
			mockops.EXPECT().WriteImageToDisk(filepath.Join(installDir, masterIgn), device).Return(err).Times(1)
			ret := i.InstallNode()
			Expect(ret).Should(Equal(err))
		})
		It("master role failed to reboot", func() {
			mkdirSuccess()
			curlSuccess(masterIgn)
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

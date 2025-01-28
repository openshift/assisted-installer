package ops

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"

	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/openshift/assisted-installer/src/utils"

	"github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-openapi/swag"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"github.com/openshift/assisted-installer/src/config"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
	"github.com/vincent-petithory/dataurl"
	gomock "go.uber.org/mock/gomock"
)

var _ = Describe("installerArgs", func() {
	var (
		device       = "/dev/sda"
		ignitionPath = "/tmp/ignition.ign"
	)

	It("Returns the correct list with no extra args", func() {
		args := installerArgs(ignitionPath, device, nil)
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})

	It("Returns the correct list with empty extra args", func() {
		args := installerArgs(ignitionPath, device, []string{})
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})

	It("Returns the correct list with extra args", func() {
		args := installerArgs(ignitionPath, device, []string{"-n", "--append-karg", "nameserver=8.8.8.8"})
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "-n", "--append-karg", "nameserver=8.8.8.8", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})
})

type MatcherContainsStringElements struct {
	Elements    []string
	ShouldMatch bool
}

func (o MatcherContainsStringElements) Matches(x interface{}) bool {
	switch reflect.TypeOf(x).Kind() {
	case reflect.Array, reflect.Slice:
		break
	default:
		return false
	}

	for _, e := range o.Elements {
		contains := funk.Contains(x, e)
		if !contains && o.ShouldMatch {
			return false
		} else if contains && !o.ShouldMatch {
			return false
		}
	}
	return true
}

func (o MatcherContainsStringElements) String() string {
	if o.ShouldMatch {
		return "All given elements should be in provided array"
	}
	return "All given elements should not be in provided array"
}

var _ = Describe("Upload logs", func() {
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		conf     *config.Config
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		conf = &config.Config{}
	})

	It("Upload logs with ca path", func() {
		conf = &config.Config{CACertPath: "test.ca"}
		m := MatcherContainsStringElements{[]string{"test.ca:test.ca", "-cacert=test.ca"}, true}
		o := NewOpsWithConfig(conf, l, execMock)
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m).Times(1)
		_, err := o.UploadInstallationLogs(true)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Upload logs without ca path", func() {
		m := MatcherContainsStringElements{[]string{"test.ca:test.ca", "-cacert=test.ca"}, false}
		o := NewOpsWithConfig(conf, l, execMock)
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m).Times(1)
		_, err := o.UploadInstallationLogs(true)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("Set Boot Order", func() {
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		conf     *config.Config
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		conf = &config.Config{}
	})

	for _, d := range []string{"redhat", "centos"} {
		efiDirname := d
		It(fmt.Sprintf("Set boot order for %s", efiDirname), func() {
			m1 := MatcherContainsStringElements{[]string{"/usr/sbin/bootlist"}, true}
			execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m1).Times(1).Return("", errors.New("Bootlist is not exist."))
			m2 := MatcherContainsStringElements{[]string{"test", "-d", "/sys/firmware/efi"}, true}
			execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m2).Times(1)
			m3 := MatcherContainsStringElements{[]string{"efibootmgr", "/dev/sda", "Red Hat Enterprise Linux"}, true}
			execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m3).Times(1).Return("", nil)
			m4 := MatcherContainsStringElements{[]string{"efibootmgr", "-l"}, true}
			execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m4).Times(1)
			m5 := MatcherContainsStringElements{[]string{"mount", "/dev/sda2", "/mnt"}, true}
			execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m5).Times(1).Return("", nil)
			m6 := MatcherContainsStringElements{[]string{"ls", "-1", "/mnt/EFI"}, true}
			execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m6).Times(1).Return(fmt.Sprintf("BOOT\n%s\n", efiDirname), nil)
			m7 := MatcherContainsStringElements{[]string{"umount", "/mnt"}, true}
			execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m7).Times(1).Return("", nil)
			o := NewOpsWithConfig(conf, l, execMock)
			err := o.SetBootOrder("/dev/sda")
			Expect(err).ToNot(HaveOccurred())
		})
	}

	It("Set boot order for ppc64le", func() {
		m1 := MatcherContainsStringElements{[]string{"/usr/sbin/bootlist"}, true}
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m1).Times(1)
		m2 := MatcherContainsStringElements{[]string{"bootlist", "/dev/sda"}, true}
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m2).Times(1)
		o := NewOpsWithConfig(conf, l, execMock)
		err := o.SetBootOrder("/dev/sda")
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("Get encapsulated machine config", func() {
	var (
		l = logrus.New()
	)
	var localhostCert = []byte(`-----BEGIN CERTIFICATE-----
MIIDOTCCAiGgAwIBAgIQSRJrEpBGFc7tNb1fb5pKFzANBgkqhkiG9w0BAQsFADAS
MRAwDgYDVQQKEwdBY21lIENvMCAXDTcwMDEwMTAwMDAwMFoYDzIwODQwMTI5MTYw
MDAwWjASMRAwDgYDVQQKEwdBY21lIENvMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEA6Gba5tHV1dAKouAaXO3/ebDUU4rvwCUg/CNaJ2PT5xLD4N1Vcb8r
bFSW2HXKq+MPfVdwIKR/1DczEoAGf/JWQTW7EgzlXrCd3rlajEX2D73faWJekD0U
aUgz5vtrTXZ90BQL7WvRICd7FlEZ6FPOcPlumiyNmzUqtwGhO+9ad1W5BqJaRI6P
YfouNkwR6Na4TzSj5BrqUfP0FwDizKSJ0XXmh8g8G9mtwxOSN3Ru1QFc61Xyeluk
POGKBV/q6RBNklTNe0gI8usUMlYyoC7ytppNMW7X2vodAelSu25jgx2anj9fDVZu
h7AXF5+4nJS4AAt0n1lNY7nGSsdZas8PbQIDAQABo4GIMIGFMA4GA1UdDwEB/wQE
AwICpDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MB0GA1Ud
DgQWBBStsdjh3/JCXXYlQryOrL4Sh7BW5TAuBgNVHREEJzAlggtleGFtcGxlLmNv
bYcEfwAAAYcQAAAAAAAAAAAAAAAAAAAAATANBgkqhkiG9w0BAQsFAAOCAQEAxWGI
5NhpF3nwwy/4yB4i/CwwSpLrWUa70NyhvprUBC50PxiXav1TeDzwzLx/o5HyNwsv
cxv3HdkLW59i/0SlJSrNnWdfZ19oTcS+6PtLoVyISgtyN6DpkKpdG1cOkW3Cy2P2
+tK/tKHRP1Y/Ra0RiDpOAmqn0gCOFGz8+lqDIor/T7MTpibL3IxqWfPrvfVRHL3B
grw/ZQTTIVjjh4JBSW3WyWgNo/ikC1lrVxzl4iPUGptxT36Cr7Zk2Bsg0XqwbOvK
5d+NTDREkSnUbie4GeutujmX3Dsx88UiV6UY/4lHJa6I5leHUNOHahRbpbWeOfs/
WkBKOclmOV2xlTVuPw==
-----END CERTIFICATE-----`)
	buildPointerIgnition := func(source string) types.Config {
		ret := types.Config{}
		ret.Ignition.Version = "3.2.0"
		ret.Ignition.Config.Merge = append(ret.Ignition.Config.Merge,
			types.Resource{
				Source: swag.String(source),
			})
		ret.Ignition.Security.TLS.CertificateAuthorities = append(ret.Ignition.Security.TLS.CertificateAuthorities,
			types.Resource{
				Source: swag.String(dataurl.EncodeBytes(localhostCert)),
			})
		return ret
	}
	buildPointerIgnitionFile := func(source string) string {
		cfg := buildPointerIgnition(source)
		b, err := json.Marshal(&cfg)
		Expect(err).ToNot(HaveOccurred())
		f, err := os.CreateTemp("", "ign")
		Expect(err).ToNot(HaveOccurred())
		_, err = f.Write(b)
		Expect(err).ToNot(HaveOccurred())
		f.Close()
		return f.Name()
	}
	Context("get pointed ignition", func() {
		checkSource := func(source string) {
			ignitionPath := buildPointerIgnitionFile(source)
			defer func() {
				_ = os.RemoveAll(ignitionPath)
			}()
			o := NewOps(l, nil).(*ops)
			ign, ca, err := o.getPointedIgnitionAndCA(ignitionPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(ign).To(Equal(source))
			Expect(ca).To(Equal(string(localhostCert)))
		}
		It("from bootstrap", func() {
			checkSource("https://abc.com")
		})
		It("embedded", func() {
			checkSource(dataurl.EncodeBytes([]byte("source")))
		})
	})
	Context("get MCS ignition", func() {
		var (
			osImageURL      string
			kernelArguments []string
		)
		buildMcsIgnition := func(osImageURL string, kernelArguments []string) string {
			type file struct {
				Path     string
				Contents struct {
					Source string
				}
			}
			var ignition struct {
				Storage struct {
					Files []file
				}
			}
			var machineConfig mcfgv1.MachineConfig
			machineConfig.Spec.OSImageURL = osImageURL
			machineConfig.Spec.KernelArguments = kernelArguments
			b, err := json.Marshal(&machineConfig)
			Expect(err).ToNot(HaveOccurred())
			f := file{
				Path: encapsulatedMachineConfigFile,
			}
			f.Contents.Source = dataurl.EncodeBytes(b)
			ignition.Storage.Files = append(ignition.Storage.Files,
				file{Path: "/tmp/abc"},
				f,
				file{Path: "/zzz"})
			b, err = json.Marshal(&ignition)
			Expect(err).ToNot(HaveOccurred())
			return string(b)
		}
		checkMcsIgnition := func(source string, shouldSucceed bool) {
			ignitionPath := buildPointerIgnitionFile(source)
			defer func() {
				_ = os.RemoveAll(ignitionPath)
			}()
			o := NewOps(l, nil)
			mc, err := o.GetEncapsulatedMC(ignitionPath)
			if shouldSucceed {
				Expect(err).ToNot(HaveOccurred())
				Expect(mc).ToNot(BeNil())
				Expect(mc.Spec.OSImageURL).To(Equal(osImageURL))
				Expect(mc.Spec.KernelArguments).To(Equal(kernelArguments))
			} else {
				Expect(err).To(HaveOccurred())
			}
		}
		compress := func(data []byte) []byte {
			var buf bytes.Buffer
			w := gzip.NewWriter(&buf)
			_, err := w.Write(data)
			Expect(err).ToNot(HaveOccurred())
			w.Close()
			return buf.Bytes()
		}
		BeforeEach(func() {
			osImageURL = "https://os.machine.url"
			kernelArguments = []string{
				"arg1",
				"arg2",
			}
		})
		It("from bootstrap - non existant URL", func() {
			checkMcsIgnition("https://127.0.0.1:44", false)
		})
		It("from bootstrap - success", func() {
			s := ghttp.NewTLSServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, buildMcsIgnition(osImageURL, kernelArguments))
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), true)
			s.Close()
		})
		It("from bootstrap - empty response", func() {
			s := ghttp.NewTLSServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, "")
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), false)
			s.Close()
		})
		It("embedded - success", func() {
			checkMcsIgnition(dataurl.EncodeBytes(compress([]byte(buildMcsIgnition(osImageURL, kernelArguments)))), true)
		})
	})
})

var _ = Describe("overwrite OS image", func() {
	const lsblkResultFormat = `{
   "blockdevices": [
		{
         "name": "%s",
         "size": 100000000000,
         "ro": false,
         "type": "disk",
         "mountpoints": [
             null
         ],
         "children": [
            {
               "name": "%s",
               "maj:min": "8:1",
               "rm": false,
               "size": 1048576,
               "ro": false,
               "type": "part",
               "mountpoints": [
                   null
               ]
            },{
               "name": "%s",
               "maj:min": "8:2",
               "rm": false,
               "size": 133169152,
               "ro": false,
               "type": "part",
               "mountpoints": [
                   null
               ]
            },{
               "name": "%s",
               "maj:min": "8:3",
               "rm": false,
               "size": 402653184,
               "ro": false,
               "type": "part",
               "mountpoints": [
                   null
               ]
            },{
               "name": "%s",
               "maj:min": "8:4",
               "rm": false,
               "size": 3272588800,
               "ro": false,
               "type": "part",
               "mountpoints": [
                   null
               ]
            }
         ]
      }
   ]
}`
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		conf     *config.Config
		o        Ops
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		conf = &config.Config{}
		o = NewOpsWithConfig(conf, l, execMock)
	})

	mockPrivileged := func(args ...interface{}) {
		execMock.EXPECT().ExecCommand(nil, "nsenter",
			append(append([]interface{}{},
				"--target",
				"1",
				"--cgroup",
				"--mount",
				"--ipc",
				"--pid",
				"--"), args...)...).Times(1)
	}
	formatResult := func(device string) string {
		deviceName := stripDev(device)
		return fmt.Sprintf(lsblkResultFormat, deviceName,
			partitionNameForDeviceName(deviceName, "1"),
			partitionNameForDeviceName(deviceName, "2"),
			partitionNameForDeviceName(deviceName, "3"),
			partitionNameForDeviceName(deviceName, "4"))
	}
	runTest := func(device, part3, part4 string) {
		execMock.EXPECT().ExecCommand(nil, "nsenter",
			append([]interface{}{},
				"--target",
				"1",
				"--cgroup",
				"--mount",
				"--ipc",
				"--pid",
				"--",
				"lsblk",
				"-b",
				"-J")...).Return(formatResult(device), nil)
		osImage := "quay.io/release-image:latest"
		extraArgs := []string{
			"--karg",
			"abc",
		}
		mockPrivileged("cat", "/proc/cmdline")
		mockPrivileged("uname", "-m")
		mockPrivileged("mount", part4, "/mnt")
		mockPrivileged("mount", part3, "/mnt/boot")
		mockPrivileged("growpart", "--free-percent=92", device, "4")
		mockPrivileged("xfs_growfs", "/mnt")
		mockPrivileged("setenforce", "0")
		mockPrivileged("ostree",
			"container",
			"image",
			"deploy",
			"--sysroot",
			"/mnt",
			"--authfile",
			"/root/.docker/config.json",
			"--imgref",
			"ostree-unverified-registry:quay.io/release-image:latest",
			"--karg",
			"ignition.platform.id=metal",
			"--karg",
			"$ignition_firstboot",
			"--stateroot",
			"rhcos",
			"--karg",
			"abc")
		mockPrivileged("fsfreeze", "--freeze", "/mnt/boot")
		mockPrivileged("umount", "/mnt/boot")
		mockPrivileged("fsfreeze", "--freeze", "/mnt")
		mockPrivileged("umount", "/mnt")
		err := o.OverwriteOsImage(osImage, device, extraArgs)
		Expect(err).ToNot(HaveOccurred())
	}
	It("overwrite OS image - sda", func() {
		runTest("/dev/sda", "/dev/sda3", "/dev/sda4")
	})
	It("overwrite OS image - nvme", func() {
		runTest("/dev/nvme0n1", "/dev/nvme0n1p3", "/dev/nvme0n1p4")
	})
	It("overwrite OS image - mmcblk", func() {
		runTest("/dev/mmcblk1", "/dev/mmcblk1P3", "/dev/mmcblk1P4")
	})
})

var _ = Describe("get number of reboots", func() {
	const (
		kubeconfigPath = "/kubeconfig"
		nodeName       = "node1"
	)
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		conf     *config.Config
		o        Ops
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		conf = &config.Config{}
		o = NewOpsWithConfig(conf, l, execMock)
	})
	expect := func(ret string, err error) {
		execMock.EXPECT().ExecCommandWithContext(gomock.Any(), gomock.Any(), "oc",
			"--kubeconfig",
			kubeconfigPath,
			"debug",
			fmt.Sprintf("node/%s", nodeName),
			"--",
			"chroot",
			"/host",
			"last",
			"reboot").Return(ret, err)
	}
	It("1 reboot", func() {
		expect("reboot   system boot  4.18.0-372.9.1.e Tue Mar  7 04:13   still running\n", nil)
		numReboots, err := o.GetNumberOfReboots(context.TODO(), nodeName, kubeconfigPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(numReboots).To(Equal(1))
	})
	It("2 reboot", func() {
		expect("reboot   system boot  4.18.0-372.9.1.e Tue Mar  7 04:13   still running\nreboot   system boot  4.18.0-372.9.1.e Sun Mar  5 07:29 - 09:11 (2+01:41)\n", nil)
		numReboots, err := o.GetNumberOfReboots(context.TODO(), nodeName, kubeconfigPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(numReboots).To(Equal(2))
	})
	It("with error", func() {
		expect("", errors.New("An error"))
		_, err := o.GetNumberOfReboots(context.TODO(), nodeName, kubeconfigPath)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("importOSTreeCommit", func() {
	const osImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:d21f2ed754a66d18b0a13a59434fa4dc36abd4320e78f3be83a3e29e21e3c2f9"
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		o        *ops
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		o = &ops{
			log:       l,
			logWriter: utils.NewLogWriter(l),
			installerConfig: &config.Config{
				CoreosImage: osImage,
			},
			executor: execMock,
		}
	})

	It("returns the commit when successful", func() {
		pullSpec := "ostree-unverified-registry:" + osImage
		execMock.EXPECT().ExecCommand(gomock.Any(),
			"nsenter", "--target", "1", "--cgroup", "--mount", "--ipc", "--pid", "--",
			"ostree", "container", "unencapsulate",
			"--authfile", "/root/.docker/config.json",
			"--quiet",
			"--repo", "/ostree/repo",
			pullSpec,
		).Return("Imported: bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25\n", nil)
		commit, err := o.importOSTreeCommit(io.Discard)
		Expect(err).NotTo(HaveOccurred())
		Expect(commit).To(Equal("bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25"))
	})

	It("fails when the command fails", func() {
		pullSpec := "ostree-unverified-registry:" + osImage
		execMock.EXPECT().ExecCommand(gomock.Any(),
			"nsenter", "--target", "1", "--cgroup", "--mount", "--ipc", "--pid", "--",
			"ostree", "container", "unencapsulate",
			"--authfile", "/root/.docker/config.json",
			"--quiet",
			"--repo", "/ostree/repo",
			pullSpec,
		).Return("", fmt.Errorf("failed"))
		_, err := o.importOSTreeCommit(io.Discard)
		Expect(err).To(HaveOccurred())
	})

	It("fails when the output is not as expected", func() {
		pullSpec := "ostree-unverified-registry:" + osImage
		execMock.EXPECT().ExecCommand(gomock.Any(),
			"nsenter", "--target", "1", "--cgroup", "--mount", "--ipc", "--pid", "--",
			"ostree", "container", "unencapsulate",
			"--authfile", "/root/.docker/config.json",
			"--quiet",
			"--repo", "/ostree/repo",
			pullSpec,
		).Return("commit bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25\n", nil)
		_, err := o.importOSTreeCommit(io.Discard)
		Expect(err).To(HaveOccurred())
	})

	It("fails when the output cannot be parsed", func() {
		pullSpec := "ostree-unverified-registry:" + osImage
		execMock.EXPECT().ExecCommand(gomock.Any(),
			"nsenter", "--target", "1", "--cgroup", "--mount", "--ipc", "--pid", "--",
			"ostree", "container", "unencapsulate",
			"--authfile", "/root/.docker/config.json",
			"--quiet",
			"--repo", "/ostree/repo",
			pullSpec,
		).Return("some nonsense here", nil)
		_, err := o.importOSTreeCommit(io.Discard)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ostreeArgs", func() {
	It("returns the basic args when no additional installer args are provided", func() {
		args := ostreeArgs("commit", nil)
		expectedArgs := []string{
			"admin", "deploy",
			"--stateroot", "install",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"commit",
		}
		Expect(args).To(Equal(expectedArgs))
	})

	It("ignores non append-karg or delete-karg args", func() {
		args := ostreeArgs("commit", []string{"--copy-network", "--network-dir", "/some/dir/"})
		expectedArgs := []string{
			"admin", "deploy",
			"--stateroot", "install",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"commit",
		}
		Expect(args).To(Equal(expectedArgs))
	})

	It("adds append args", func() {
		args := ostreeArgs("commit", []string{"--append-karg", "nameserver=8.8.8.8"})
		expectedArgs := []string{
			"admin", "deploy",
			"--stateroot", "install",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--karg-append", "nameserver=8.8.8.8",
			"commit",
		}
		Expect(args).To(Equal(expectedArgs))
	})

	It("adds remove args", func() {
		args := ostreeArgs("commit", []string{"--delete-karg", "console"})
		expectedArgs := []string{
			"admin", "deploy",
			"--stateroot", "install",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--karg-delete", "console",
			"commit",
		}
		Expect(args).To(Equal(expectedArgs))
	})

	It("works with multiple instances of append and remove", func() {
		args := ostreeArgs("commit", []string{"--append-karg", "nameserver=8.8.8.8", "--delete-karg", "console", "--append-karg", "ip=192.0.2.100"})
		expectedArgs := []string{
			"admin", "deploy",
			"--stateroot", "install",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--karg-append", "nameserver=8.8.8.8",
			"--karg-delete", "console",
			"--karg-append", "ip=192.0.2.100",
			"commit",
		}
		Expect(args).To(Equal(expectedArgs))
	})
})

var _ = Describe("WriteImageToExistingRoot", func() {
	const osImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:d21f2ed754a66d18b0a13a59434fa4dc36abd4320e78f3be83a3e29e21e3c2f9"
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		o        *ops
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		o = &ops{
			log:       l,
			logWriter: utils.NewLogWriter(l),
			installerConfig: &config.Config{
				CoreosImage: osImage,
			},
			executor: execMock,
		}
	})

	expectExec := func(out string, err error, additionalArgs ...any) {
		baseArgs := []any{"--target", "1", "--cgroup", "--mount", "--ipc", "--pid", "--"}
		args := append(baseArgs, additionalArgs...)
		execMock.EXPECT().ExecCommand(gomock.Any(),
			"nsenter", args...).Return(out, err)
	}

	It("runs the correct commands", func() {
		ignitionPath := "/tmp/ignition.ign"
		expectExec("", nil, "mount", "/sysroot", "-o", "remount,rw")
		expectExec("", nil, "mount", "/boot", "-o", "remount,rw")
		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("Imported: bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25\n", nil,
			"ostree", "container", "unencapsulate",
			"--authfile", "/root/.docker/config.json",
			"--quiet",
			"--repo", "/ostree/repo",
			"ostree-unverified-registry:"+osImage,
		)
		expectExec("", nil, "ostree", "admin", "deploy", "--stateroot", "install", "--karg", "$ignition_firstboot", "--karg", defaultIgnitionPlatformId, "bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25")
		expectExec("", nil, "mkdir", "/boot/ignition")
		expectExec("", nil, "cp", ignitionPath, "/boot/ignition/config.ign")
		expectExec("", nil, "touch", "/boot/ignition.firstboot")

		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, nil)).To(Succeed())
	})

	It("copies the network files when -n is provided", func() {
		ignitionPath := "/tmp/ignition.ign"
		expectExec("", nil, "mount", "/sysroot", "-o", "remount,rw")
		expectExec("", nil, "mount", "/boot", "-o", "remount,rw")
		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("Imported: bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25\n", nil,
			"ostree", "container", "unencapsulate",
			"--authfile", "/root/.docker/config.json",
			"--quiet",
			"--repo", "/ostree/repo",
			"ostree-unverified-registry:"+osImage,
		)
		expectExec("", nil, "mkdir", "/boot/coreos-firstboot-network")
		expectExec("", nil, "sh", "-c", "cp /etc/NetworkManager/system-connections/* /boot/coreos-firstboot-network")
		expectExec("", nil, "ostree", "admin", "deploy",
			"--stateroot", "install",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--karg-append", "nameserver=8.8.8.8",
			"bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25")
		expectExec("", nil, "mkdir", "/boot/ignition")
		expectExec("", nil, "cp", ignitionPath, "/boot/ignition/config.ign")
		expectExec("", nil, "touch", "/boot/ignition.firstboot")

		installerArgs := []string{"--append-karg", "nameserver=8.8.8.8", "-n"}
		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, installerArgs)).To(Succeed())
	})

	It("copies the network files when --copy-network is provided", func() {
		ignitionPath := "/tmp/ignition.ign"
		expectExec("", nil, "mount", "/sysroot", "-o", "remount,rw")
		expectExec("", nil, "mount", "/boot", "-o", "remount,rw")
		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("Imported: bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25\n", nil,
			"ostree", "container", "unencapsulate",
			"--authfile", "/root/.docker/config.json",
			"--quiet",
			"--repo", "/ostree/repo",
			"ostree-unverified-registry:"+osImage,
		)
		expectExec("", nil, "mkdir", "/boot/coreos-firstboot-network")
		expectExec("", nil, "sh", "-c", "cp /etc/NetworkManager/system-connections/* /boot/coreos-firstboot-network")
		expectExec("", nil, "ostree", "admin", "deploy",
			"--stateroot", "install",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--karg-append", "nameserver=8.8.8.8",
			"bd58af6b04f8ed7e3e72d1af34439fb03a5640a516edd200fb26775df346ae25")
		expectExec("", nil, "mkdir", "/boot/ignition")
		expectExec("", nil, "cp", ignitionPath, "/boot/ignition/config.ign")
		expectExec("", nil, "touch", "/boot/ignition.firstboot")

		installerArgs := []string{"--append-karg", "nameserver=8.8.8.8", "--copy-network"}
		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, installerArgs)).To(Succeed())
	})
})

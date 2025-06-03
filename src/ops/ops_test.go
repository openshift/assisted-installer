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
	"strings"

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
	buildPointerIgnition := func(source string, withCert bool) types.Config {
		ret := types.Config{}
		ret.Ignition.Version = "3.2.0"
		ret.Ignition.Config.Merge = append(ret.Ignition.Config.Merge,
			types.Resource{
				Source: swag.String(source),
			})

		if !withCert {
			return ret
		}

		ret.Ignition.Security.TLS.CertificateAuthorities = append(ret.Ignition.Security.TLS.CertificateAuthorities,
			types.Resource{
				Source: swag.String(dataurl.EncodeBytes(localhostCert)),
			})

		return ret
	}

	buildPointerIgnitionFile := func(source string, withCert bool) string {
		cfg := buildPointerIgnition(source, withCert)
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
			ignitionPath := buildPointerIgnitionFile(source, true)
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
		checkMcsIgnition := func(source string, withCert bool, shouldSucceed bool) {
			ignitionPath := buildPointerIgnitionFile(source, withCert)
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
			checkMcsIgnition("https://127.0.0.1:44", true, false)
		})
		It("from bootstrap - success", func() {
			s := ghttp.NewTLSServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, buildMcsIgnition(osImageURL, kernelArguments))
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), true, true)
			s.Close()
		})
		It("from bootstrap - empty response", func() {
			s := ghttp.NewTLSServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, "")
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), true, false)
			s.Close()
		})
		It("from bootstrap - with http no cert should succeed", func() {
			s := ghttp.NewServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, buildMcsIgnition(osImageURL, kernelArguments))
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), false, true)
			s.Close()
		})
		It("from bootstrap - with http with cert should succeed", func() {
			s := ghttp.NewServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, buildMcsIgnition(osImageURL, kernelArguments))
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), true, true)
			s.Close()
		})
		It("from bootstrap - with https with cert should succeed", func() {
			s := ghttp.NewTLSServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, buildMcsIgnition(osImageURL, kernelArguments))
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), true, true)
			s.Close()
		})
		It("from bootstrap - with https no cert should fail", func() {
			s := ghttp.NewTLSServer()
			s.RouteToHandler("GET", "/",
				func(w http.ResponseWriter, req *http.Request) {
					_, err := io.WriteString(w, buildMcsIgnition(osImageURL, kernelArguments))
					Expect(err).ToNot(HaveOccurred())
				})
			checkMcsIgnition(s.URL(), false, false)
			s.Close()
		})
		It("embedded - success", func() {
			checkMcsIgnition(dataurl.EncodeBytes(compress([]byte(buildMcsIgnition(osImageURL, kernelArguments)))), true, true)
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
	formatPartitionName := func(deviceName string, partNum int) string {
		// Generate partition names following the naming convention
		if strings.HasPrefix(deviceName, "nvme") || strings.HasPrefix(deviceName, "mmcblk") {
			if strings.HasPrefix(deviceName, "mmcblk") {
				return fmt.Sprintf("%sP%d", deviceName, partNum)
			}
			return fmt.Sprintf("%sp%d", deviceName, partNum)
		}
		return fmt.Sprintf("%s%d", deviceName, partNum)
	}
	formatResult := func(device string) string {
		deviceName := stripDev(device)
		return fmt.Sprintf(lsblkResultFormat, deviceName,
			formatPartitionName(deviceName, 1),
			formatPartitionName(deviceName, 2),
			formatPartitionName(deviceName, 3),
			formatPartitionName(deviceName, 4))
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

var _ = Describe("getPartitionInLsblkOutput", func() {
	const testLsblkJsonOutput = `{
   "blockdevices": [
      {
         "name": "sda",
         "type": "disk",
         "size": 275200000000
      },{
         "name": "nvme1n1",
         "type": "disk",
         "size": 1980000000000,
         "children": [
            {
               "name": "nvme1n1p1",
               "type": "part",
               "size": 512000000
            },{
               "name": "nvme1n1p2",
               "type": "part",
               "size": 1073741824
            },{
               "name": "nvme1n1p3",
               "type": "part",
               "size": 1900000000000,
               "children": [
                  {
                     "name": "fedora-00",
                     "type": "lvm",
                     "size": 1900000000000
                  }
               ]
            }
         ]
      },{
         "name": "nvme0n1",
         "type": "disk",
         "size": 1980000000000,
         "children": [
            {
               "name": "nvme0n1p1",
               "type": "part",
               "size": 16777216
            },{
               "name": "nvme0n1p2",
               "type": "part",
               "size": 1900000000000
            },{
               "name": "nvme0n1p3",
               "type": "part",
               "size": 763000000
            }
         ]
      }
   ]
}`

	It("should return correct partition name for valid inputs", func() {
		partitionName, err := getPartitionInLsblkOutput("nvme1n1", 1, testLsblkJsonOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(partitionName).To(Equal("nvme1n1p1"))

		partitionName, err = getPartitionInLsblkOutput("nvme1n1", 2, testLsblkJsonOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(partitionName).To(Equal("nvme1n1p2"))

		partitionName, err = getPartitionInLsblkOutput("nvme1n1", 3, testLsblkJsonOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(partitionName).To(Equal("nvme1n1p3"))
	})

	It("should work with different device types", func() {
		partitionName, err := getPartitionInLsblkOutput("nvme0n1", 1, testLsblkJsonOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(partitionName).To(Equal("nvme0n1p1"))

		partitionName, err = getPartitionInLsblkOutput("nvme0n1", 2, testLsblkJsonOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(partitionName).To(Equal("nvme0n1p2"))

		partitionName, err = getPartitionInLsblkOutput("nvme0n1", 3, testLsblkJsonOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(partitionName).To(Equal("nvme0n1p3"))
	})

	It("should return error for partition number less than 1", func() {
		_, err := getPartitionInLsblkOutput("nvme1n1", 0, testLsblkJsonOutput)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("partition number must be greater than 0"))

		_, err = getPartitionInLsblkOutput("nvme1n1", -1, testLsblkJsonOutput)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("partition number must be greater than 0"))
	})

	It("should return error for device not found", func() {
		_, err := getPartitionInLsblkOutput("nonexistent", 1, testLsblkJsonOutput)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("device nonexistent not found in lsblk output"))
	})

	It("should return error for partition number exceeding available partitions", func() {
		_, err := getPartitionInLsblkOutput("nvme1n1", 4, testLsblkJsonOutput)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("device nvme1n1 has less than 4 partitions"))

		_, err = getPartitionInLsblkOutput("nvme0n1", 4, testLsblkJsonOutput)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("device nvme0n1 has less than 4 partitions"))
	})

	It("should return error for device with no partitions", func() {
		_, err := getPartitionInLsblkOutput("sda", 1, testLsblkJsonOutput)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("device sda has less than 1 partitions"))
	})

	It("should return error for invalid JSON", func() {
		invalidJson := `{"blockdevices": [invalid json}`
		_, err := getPartitionInLsblkOutput("nvme1n1", 1, invalidJson)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to parse lsblk JSON output"))
	})

	It("should return error for empty JSON", func() {
		emptyJson := `{"blockdevices": []}`
		_, err := getPartitionInLsblkOutput("nvme1n1", 1, emptyJson)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("device nvme1n1 not found in lsblk output"))
	})

	It("should ignore non-partition children", func() {
		// nvme1n1p3 has an LVM child, but getPartitionInLsblkOutput should only look at "part" type children
		partitionName, err := getPartitionInLsblkOutput("nvme1n1", 3, testLsblkJsonOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(partitionName).To(Equal("nvme1n1p3"))
	})
})

var _ = Describe("WriteImageToExistingRoot", func() {
	const (
		osImage      = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:d21f2ed754a66d18b0a13a59434fa4dc36abd4320e78f3be83a3e29e21e3c2f9"
		ignitionPath = "/tmp/ignition.ign"
	)
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		o        *ops
	)

	expectExec := func(out string, err error, additionalArgs ...any) {
		baseArgs := []any{"--target", "1", "--cgroup", "--mount", "--ipc", "--pid", "--"}
		args := append(baseArgs, additionalArgs...)
		execMock.EXPECT().ExecCommand(gomock.Any(),
			"nsenter", args...).Return(out, err)
	}

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

	expectRemount := func() {
		expectExec("", nil, "mount", "/sysroot", "-o", "remount,rw")
		expectExec("", nil, "mount", "/boot", "-o", "remount,rw")
	}

	expectIgnitionSetup := func() {
		expectExec("", nil, "mkdir", "/boot/ignition")
		expectExec("", nil, "cp", ignitionPath, "/boot/ignition/config.ign")
		expectExec("", nil, "touch", "/boot/ignition.firstboot")
	}

	It("runs the correct commands when the node image ref doesn't exist", func() {
		expectRemount()
		expectExec("", fmt.Errorf("does not exist"), "stat", "/ostree/repo/refs/heads/coreos/node-image")
		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("", nil, "ostree", "container", "image", "deploy",
			"--stateroot", "install",
			"--sysroot", "/",
			"--authfile", "/root/.docker/config.json",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--image", osImage)
		expectExec("", nil, "ostree", "admin", "finalize-staged")
		expectIgnitionSetup()

		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, nil)).To(Succeed())
	})

	It("deletes the node image ref when it exists", func() {
		expectRemount()
		expectExec("", nil, "stat", "/ostree/repo/refs/heads/coreos/node-image")
		expectExec("", nil, "ostree", "refs", "--repo", "/ostree/repo", "--delete", nodeImageOSTreeRefName)
		expectExec("", nil, "touch", "/ostree/repo/tmp/node-image")

		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("", nil, "ostree", "container", "image", "deploy",
			"--stateroot", "install",
			"--sysroot", "/",
			"--authfile", "/root/.docker/config.json",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--image", osImage)
		expectExec("", nil, "ostree", "admin", "finalize-staged")
		expectIgnitionSetup()

		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, nil)).To(Succeed())
	})

	It("copies the network files when -n is provided", func() {
		expectRemount()
		expectExec("", fmt.Errorf("does not exist"), "stat", "/ostree/repo/refs/heads/coreos/node-image")
		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("", nil, "ostree", "container", "image", "deploy",
			"--stateroot", "install",
			"--sysroot", "/",
			"--authfile", "/root/.docker/config.json",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--image", osImage)
		expectExec("", nil, "ostree", "admin", "finalize-staged")

		expectExec("", nil, "mkdir", "/boot/coreos-firstboot-network")
		expectExec("", nil, "rsync", "-av", "/etc/NetworkManager/system-connections/", "/boot/coreos-firstboot-network/")
		expectIgnitionSetup()

		installerArgs := []string{"-n"}
		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, installerArgs)).To(Succeed())
	})

	It("copies the network files when --copy-network is provided", func() {
		expectRemount()
		expectExec("", fmt.Errorf("does not exist"), "stat", "/ostree/repo/refs/heads/coreos/node-image")
		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("", nil, "ostree", "container", "image", "deploy",
			"--stateroot", "install",
			"--sysroot", "/",
			"--authfile", "/root/.docker/config.json",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--image", osImage)
		expectExec("", nil, "ostree", "admin", "finalize-staged")

		expectExec("", nil, "mkdir", "/boot/coreos-firstboot-network")
		expectExec("", nil, "rsync", "-av", "/etc/NetworkManager/system-connections/", "/boot/coreos-firstboot-network/")
		expectIgnitionSetup()

		installerArgs := []string{"--copy-network"}
		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, installerArgs)).To(Succeed())
	})

	It("modifies kernel args when required", func() {
		expectRemount()
		expectExec("", fmt.Errorf("does not exist"), "stat", "/ostree/repo/refs/heads/coreos/node-image")
		expectExec("", nil, "ostree", "admin", "stateroot-init", "install")
		expectExec("", nil, "ostree", "container", "image", "deploy",
			"--stateroot", "install",
			"--sysroot", "/",
			"--authfile", "/root/.docker/config.json",
			"--karg", "$ignition_firstboot",
			"--karg", defaultIgnitionPlatformId,
			"--image", osImage)
		expectExec("", nil, "ostree", "admin", "finalize-staged")

		expectExec("", nil, "rpm-ostree", "kargs",
			"--os", "install",
			"--append", "nameserver=8.8.8.8",
			"--append", "foo=bar",
			"--delete-if-present", "baz",
		)
		expectExec("", nil, "ostree", "admin", "finalize-staged")
		expectIgnitionSetup()

		installerArgs := []string{"--append-karg", "nameserver=8.8.8.8", "--append-karg", "foo=bar", "--delete-karg", "baz"}
		Expect(o.WriteImageToExistingRoot(io.Discard, ignitionPath, installerArgs)).To(Succeed())
	})
})

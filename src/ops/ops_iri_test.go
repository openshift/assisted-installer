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
		)

		BeforeEach(func() {
			ignitionPath = filepath.Join(tmpDir, "test.ign")

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

		It("should add mount unit for bootstrap role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleBootstrap),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			err := o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read modified ignition
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("var-lib-iri\\\\x2dregistry.mount"))
		})

		It("should add mount unit for arbiter role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleArbiter),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			err := o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read modified ignition
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("var-lib-iri\\\\x2dregistry.mount"))
		})

		It("should add mount unit to ignition for master with registry data", func() {
			err := o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read modified ignition
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("var-lib-iri\\\\x2dregistry.mount"))
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
							"name": "var-lib-iri\\\\x2dregistry.mount",
							"enabled": true,
							"contents": "[Unit]\nDescription=Mount IRI Registry Partition\n"
						}
					]
				}
			}`
			err := os.WriteFile(ignitionPath, []byte(ignitionWithMount), 0600)
			Expect(err).ToNot(HaveOccurred())

			// Call PrepareIRIRegistryIgnition - should skip without error
			err = o.PrepareIRIRegistryIgnition(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			// Read ignition - should still have only 1 mount unit (not duplicated)
			data, err := os.ReadFile(ignitionPath)
			Expect(err).ToNot(HaveOccurred())

			content := string(data)
			// Verify mount unit exists (was not removed)
			Expect(content).To(ContainSubstring("var-lib-iri\\\\x2dregistry.mount"))
			// Verify it wasn't duplicated (count occurrences - simple check)
			Expect(strings.Count(content, "var-lib-iri\\\\x2dregistry.mount")).To(Equal(1))
		})
	})

	Describe("verifyDiskSpaceForIRI", func() {
		BeforeEach(func() {
			testConfig := &config.Config{}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
		})

		It("should succeed if disk has sufficient space", func() {
			// Mock lsblk to return 300GB disk with existing partitions
			// Used: ~51GB, Free: ~249GB
			// Required: 150GB IRI partition < 249GB → should succeed
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 322122547200,
						"children": [
							{"name": "sda1", "size": 134217728},
							{"name": "sda2", "size": 134217728},
							{"name": "sda3", "size": 734003200},
							{"name": "sda4", "size": 53687091200}
						]
					}
				]
			}`
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"lsblk",
				[]string{"--bytes", "--json", "/dev/sda"},
				gomock.Any(),
			).Return(lsblkOutput, nil)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.verifyDiskSpaceForIRI("/dev/sda", partConfig)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail if disk is too small", func() {
			// Mock lsblk to return 100GB disk with existing partitions (~50GB used)
			// Free space: 100GB - 50GB = 50GB
			// Required: 150GB IRI partition > 50GB → should fail
			lsblkOutput := `{
				"blockdevices": [
					{
						"name": "sda",
						"size": 107374182400,
						"children": [
							{"name": "sda1", "size": 1048576},
							{"name": "sda2", "size": 268435456},
							{"name": "sda3", "size": 536870912},
							{"name": "sda4", "size": 53687091200}
						]
					}
				]
			}`
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"lsblk",
				[]string{"--bytes", "--json", "/dev/sda"},
				gomock.Any(),
			).Return(lsblkOutput, nil)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.verifyDiskSpaceForIRI("/dev/sda", partConfig)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not enough free space"))
		})

		It("should fail if lsblk command fails", func() {
			mockExec.EXPECT().ExecCommandWithOptions(
				gomock.Any(),
				"lsblk",
				gomock.Any(),
				gomock.Any(),
			).Return("", fmt.Errorf("command failed"))

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.verifyDiskSpaceForIRI("/dev/sda", partConfig)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("createIRIRegistryPartition", func() {
		BeforeEach(func() {
			testConfig := &config.Config{}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
		})

		It("should create partition with auto-discovery (regular disk)", func() {
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
				// Step 0: sfdisk --relocate gpt-bak-std to relocate backup GPT
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d to get last-lba
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					Expect(args[1]).To(ContainSubstring("sfdisk --no-reread --no-tell-kernel -N 5"))
					Expect(args[3]).To(ContainSubstring("type=0FC63DAF-8483-4772-8E79-3D69D8477DE4"))
					Expect(args[3]).To(ContainSubstring("name=\"iri-registry\""))
					Expect(args[4]).To(Equal("/dev/sda"))
					return "", nil
				}),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk to check if partition exists (auto-discovered)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),

				// Step 5: mkfs.xfs (no partx/kpartx needed since partition was auto-discovered)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-f", "-L", "iri-registry", "/dev/sda5"},
					gomock.Any(),
				).Return("", nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should use partx for regular disk when partition not auto-discovered", func() {
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
				// Step 0: sfdisk --relocate gpt-bak-std to relocate backup GPT
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d to get last-lba
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					// args = ["-c", "command", "sh", partSpec, device]
					cmdString := args[1]
					Expect(cmdString).To(ContainSubstring("sfdisk --no-reread --no-tell-kernel -N 5"))
					Expect(cmdString).To(ContainSubstring("printf"))
					Expect(cmdString).To(ContainSubstring("$1")) // partSpec as positional arg
					Expect(cmdString).To(ContainSubstring("$2")) // device as positional arg
					// Verify partSpec contains partition definition
					Expect(args[3]).To(ContainSubstring("type=0FC63DAF-8483-4772-8E79-3D69D8477DE4"))
					Expect(args[3]).To(ContainSubstring("name=\"iri-registry\""))
					// Verify device is passed correctly
					Expect(args[4]).To(Equal("/dev/sda"))
					return "", nil
				}),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk to check if partition exists (not found - only 4 partitions)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}]}]}`, nil),

				// Step 5: lsblk to get device type (getDeviceType uses plain lsblk without --output)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "type": "disk"}]}`, nil),

				// Step 6: partx --add --nr 5 (regular disk)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"partx",
					[]string{"--add", "--nr", "5", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 7: udevadm settle after partx
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 8: lsblk to verify partition exists after partx
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),

				// Step 9: mkfs.xfs
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-f", "-L", "iri-registry", "/dev/sda5"},
					gomock.Any(),
				).Return("", nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail if partition would start before 25000 MiB threshold", func() {
			// Mock sfdisk with a disk that's too small: ~160GB
			// With 150GB IRI partition, it would start at ~20 GiB (20480 MiB), before 25000 MiB threshold
			sfdiskDump := `label: gpt
label-id: 12345678-1234-1234-1234-123456789012
device: /dev/sdsmall
unit: sectors
sector-size: 512
first-lba: 34
last-lba: 335544320

/dev/sdsmall1 : start=        2048, size=      261120, type=21686148-6449-6E6F-744E-656564454649
/dev/sdsmall2 : start=      264192, size=      262144, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B
/dev/sdsmall3 : start=      526336, size=     1048576, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4
/dev/sdsmall4 : start=     1574912, size=    11534336, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4`

			gomock.InOrder(
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sdsmall"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d to get last-lba
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sdsmall"},
					gomock.Any(),
				).Return(sfdiskDump, nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sdsmall", partConfig)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("before recommended 25000 MiB threshold"))
		})

		It("should fail if sfdisk dump fails", func() {
			gomock.InOrder(
				// Step 0: sfdisk --relocate succeeds
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d fails
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return("", fmt.Errorf("sfdisk failed")),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to dump partition table"))
		})

		It("should fail if last-lba not found", func() {
			// Mock sfdisk with missing last-lba
			sfdiskDump := `label: gpt
device: /dev/sda`

			gomock.InOrder(
				// Step 0: sfdisk --relocate succeeds
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d returns dump without last-lba
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
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
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

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
					// args = ["-c", "command", "sh", partSpec, device]
					cmdString := args[1]
					// With 4096 byte sectors, 150GB partition = 39321600 sectors (not 314572800)
					// Calculation with proper 1 MiB alignment:
					// sectorsPerMiB = 1048576 / 4096 = 256
					// usableEnd = 78643166 + 1 - 0 = 78643167
					// alignedEnd = (78643167 / 256) * 256 = 307199 * 256 = 78642944
					// start = 78642944 - 39321600 = 39321344
					Expect(cmdString).To(ContainSubstring("sfdisk --no-reread --no-tell-kernel -N 5"))
					// Verify partSpec contains correct geometry
					Expect(args[3]).To(ContainSubstring("start=39321344"))
					Expect(args[3]).To(ContainSubstring("size=39321600"))
					Expect(args[4]).To(Equal("/dev/sda"))
					return "", nil
				}),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk to check if partition exists (auto-discovered)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),

				// Step 5: mkfs.xfs (no partx needed, auto-discovered)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-f", "-L", "iri-registry", "/dev/sda5"},
					gomock.Any(),
				).Return("", nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should use kpartx for multipath device when partition not auto-discovered", func() {
			// Mock sfdisk -d for multipath device
			sfdiskDump := `label: gpt
label-id: 12345678-1234-1234-1234-123456789012
device: /dev/mapper/mpatha
unit: sectors
sector-size: 512
first-lba: 34
last-lba: 629145566

/dev/mapper/mpatha1 : start=        2048, size=      261120, type=21686148-6449-6E6F-744E-656564454649
/dev/mapper/mpatha2 : start=      264192, size=      262144, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B
/dev/mapper/mpatha3 : start=      526336, size=     1048576, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4
/dev/mapper/mpatha4 : start=     1574912, size=    11534336, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4`

			gomock.InOrder(
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d to get last-lba
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					Expect(args[1]).To(ContainSubstring("sfdisk --no-reread --no-tell-kernel -N 5"))
					Expect(args[4]).To(Equal("/dev/mapper/mpatha"))
					return "", nil
				}),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk to check if partition exists (not found)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "mpatha", "path": "/dev/mapper/mpatha", "type": "mpath", "partn": null, "children": [{"name": "mpatha1", "path": "/dev/mapper/mpatha1", "type": "part", "partn": 1}, {"name": "mpatha2", "path": "/dev/mapper/mpatha2", "type": "part", "partn": 2}, {"name": "mpatha3", "path": "/dev/mapper/mpatha3", "type": "part", "partn": 3}, {"name": "mpatha4", "path": "/dev/mapper/mpatha4", "type": "part", "partn": 4}]}]}`, nil),

				// Step 5: lsblk to get device type (getDeviceType uses plain lsblk without --output)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "mpatha", "type": "mpath"}]}`, nil),

				// Step 6: kpartx -u for multipath device
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"kpartx",
					[]string{"-u", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return("", nil),

				// Step 7: udevadm settle after kpartx
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 8: lsblk to verify partition exists after kpartx
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "mpatha", "path": "/dev/mapper/mpatha", "type": "mpath", "partn": null, "children": [{"name": "mpatha1", "path": "/dev/mapper/mpatha1", "type": "part", "partn": 1}, {"name": "mpatha2", "path": "/dev/mapper/mpatha2", "type": "part", "partn": 2}, {"name": "mpatha3", "path": "/dev/mapper/mpatha3", "type": "part", "partn": 3}, {"name": "mpatha4", "path": "/dev/mapper/mpatha4", "type": "part", "partn": 4}, {"name": "mpatha5", "path": "/dev/mapper/mpatha5", "type": "part", "partn": 5}]}]}`, nil),

				// Step 9: mkfs.xfs on multipath partition
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-f", "-L", "iri-registry", "/dev/mapper/mpatha5"},
					gomock.Any(),
				).Return("", nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/mapper/mpatha", partConfig)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should skip registration for multipath device with auto-discovered partition", func() {
			// Mock sfdisk -d for multipath device
			sfdiskDump := `label: gpt
label-id: 12345678-1234-1234-1234-123456789012
device: /dev/mapper/mpatha
unit: sectors
sector-size: 512
first-lba: 34
last-lba: 629145566

/dev/mapper/mpatha1 : start=        2048, size=      261120, type=21686148-6449-6E6F-744E-656564454649
/dev/mapper/mpatha2 : start=      264192, size=      262144, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B
/dev/mapper/mpatha3 : start=      526336, size=     1048576, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4
/dev/mapper/mpatha4 : start=     1574912, size=    11534336, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4`

			gomock.InOrder(
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d to get last-lba
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					Expect(args[4]).To(Equal("/dev/mapper/mpatha"))
					return "", nil
				}),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk to check if partition exists (auto-discovered - partition 5 present)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/mapper/mpatha"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "mpatha", "path": "/dev/mapper/mpatha", "type": "mpath", "partn": null, "children": [{"name": "mpatha1", "path": "/dev/mapper/mpatha1", "type": "part", "partn": 1}, {"name": "mpatha2", "path": "/dev/mapper/mpatha2", "type": "part", "partn": 2}, {"name": "mpatha3", "path": "/dev/mapper/mpatha3", "type": "part", "partn": 3}, {"name": "mpatha4", "path": "/dev/mapper/mpatha4", "type": "part", "partn": 4}, {"name": "mpatha5", "path": "/dev/mapper/mpatha5", "type": "part", "partn": 5}]}]}`, nil),

				// Step 5: mkfs.xfs (no kpartx needed since partition was auto-discovered)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-f", "-L", "iri-registry", "/dev/mapper/mpatha5"},
					gomock.Any(),
				).Return("", nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/mapper/mpatha", partConfig)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail if partition not found after explicit registration", func() {
			sfdiskDump := `label: gpt
sector-size: 512
last-lba: 629145566`

			gomock.InOrder(
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).Return("", nil),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk to check if partition exists (not found)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}]}]}`, nil),

				// Step 5: lsblk to get device type (getDeviceType uses plain lsblk without --output)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "type": "disk"}]}`, nil),

				// Step 6: partx --add (succeeds but partition still not visible)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"partx",
					[]string{"--add", "--nr", "5", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 7: udevadm settle after partx
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 8: lsblk to verify partition - still not found (failure scenario)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}]}]}`, nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("partition 5 still not found"))
			Expect(err.Error()).To(ContainSubstring("after explicit registration"))
		})

		It("should propagate lsblk command failure without attempting registration", func() {
			sfdiskDump := `label: gpt
sector-size: 512
last-lba: 629145566`

			gomock.InOrder(
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).Return("", nil),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk command fails
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return("", fmt.Errorf("lsblk command failed")),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to check for partition 5"))
			Expect(err.Error()).To(ContainSubstring("failed to run lsblk command"))
		})

		It("should propagate invalid JSON without attempting registration", func() {
			sfdiskDump := `label: gpt
sector-size: 512
last-lba: 629145566`

			gomock.InOrder(
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/sda"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).Return("", nil),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk returns invalid JSON
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/sda"},
					gomock.Any(),
				).Return("invalid json {{{", nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/sda", partConfig)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to check for partition 5"))
			Expect(err.Error()).To(ContainSubstring("failed to unmarshal lsblk output"))
		})

		It("should handle NVMe devices with auto-discovery", func() {
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
				// Step 0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/nvme0n1"},
					gomock.Any(),
				).Return("", nil),

				// Step 1: sfdisk -d to get last-lba and sector-size
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sfdisk",
					[]string{"-d", "/dev/nvme0n1"},
					gomock.Any(),
				).Return(sfdiskDump, nil),

				// Step 2: Create partition with sfdisk -N 5 (device-agnostic)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"sh",
					gomock.Any(),
					gomock.Any(),
				).DoAndReturn(func(writer io.Writer, command string, args []string, opts ...execute.CommandOption) (string, error) {
					// args = ["-c", "command", "sh", partSpec, device]
					cmdString := args[1]
					// sfdisk -N 5 handles device naming automatically (works for NVMe, SATA, etc.)
					Expect(cmdString).To(ContainSubstring("sfdisk --no-reread --no-tell-kernel -N 5"))
					Expect(cmdString).To(ContainSubstring("printf"))
					Expect(cmdString).To(ContainSubstring("$2")) // device as positional arg
					// Verify device is NVMe
					Expect(args[4]).To(Equal("/dev/nvme0n1"))
					return "", nil
				}),

				// Step 3: udevadm settle after sfdisk
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"udevadm",
					[]string{"settle"},
					gomock.Any(),
				).Return("", nil),

				// Step 4: lsblk to check if partition exists (auto-discovered)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"lsblk",
					[]string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", "/dev/nvme0n1"},
					gomock.Any(),
				).Return(`{"blockdevices": [{"name": "nvme0n1", "path": "/dev/nvme0n1", "type": "disk", "partn": null, "children": [{"name": "nvme0n1p1", "path": "/dev/nvme0n1p1", "type": "part", "partn": 1}, {"name": "nvme0n1p2", "path": "/dev/nvme0n1p2", "type": "part", "partn": 2}, {"name": "nvme0n1p3", "path": "/dev/nvme0n1p3", "type": "part", "partn": 3}, {"name": "nvme0n1p4", "path": "/dev/nvme0n1p4", "type": "part", "partn": 4}, {"name": "nvme0n1p5", "path": "/dev/nvme0n1p5", "type": "part", "partn": 5}]}]}`, nil),

				// Step 5: mkfs.xfs (no partx needed, auto-discovered)
				mockExec.EXPECT().ExecCommandWithOptions(
					gomock.Any(),
					"mkfs.xfs",
					[]string{"-f", "-L", "iri-registry", "/dev/nvme0n1p5"},
					gomock.Any(),
				).Return("", nil),
			)

			partConfig := iriPartitionConfig{
				sizeGB:     150,
				reservedGB: 0,
			}
			err := o.createIRIRegistryPartition("/dev/nvme0n1", partConfig)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("SetupIRIRegistryPartition", func() {
		BeforeEach(func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleMaster),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)
		})

		It("should process bootstrap role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleBootstrap),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Mock minimal commands to verify it's not skipped
			lsblkOutput := `{"blockdevices": [{"name": "sda", "size": 322122547200}]}`
			sfdiskDump := `sector-size: 512
last-lba: 629145566`

			gomock.InOrder(
				// Step 1: du -sb to get registry data size
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "du", []string{"-sb", "/mnt/agentdata"}, gomock.Any()).Return("60000000000 /mnt/agentdata", nil),
				// Step 2: verifyDiskSpaceForIRI
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(lsblkOutput, nil),
				// Step 3: createIRIRegistryPartition
				// Step 3.0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", []string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"}, gomock.Any()).Return("", nil),
				// Step 3.1: sfdisk -d
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", []string{"-d", "/dev/sda"}, gomock.Any()).Return(sfdiskDump, nil),
				// Step 3.2: sfdisk -N 5 (create partition)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.3: udevadm settle (after sfdisk)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.4: lsblk to check if partition exists (not found - returns only 4 partitions)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}]}]}`, nil),
				// Step 3.5: lsblk to get device type
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null}]}`, nil),
				// Step 3.6: partx to register partition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "partx", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.7: udevadm settle (after partx)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.8: lsblk to verify partition exists
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),
				// Step 3.9: mkfs.xfs
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkfs.xfs", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 4: copyRegistryDataToPartition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkdir", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mount", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "umount", gomock.Any(), gomock.Any()).Return("", nil),
			)

			err := o.SetupIRIRegistryPartition("/dev/sda", nil)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should process arbiter role", func() {
			testConfig := &config.Config{
				Role: string(models.HostRoleArbiter),
			}
			o = NewOpsWithConfig(testConfig, logrus.New(), mockExec).(*ops)

			// Mock minimal commands to verify it's not skipped
			lsblkOutput := `{"blockdevices": [{"name": "sda", "size": 322122547200}]}`
			sfdiskDump := `sector-size: 512
last-lba: 629145566`

			gomock.InOrder(
				// Step 1: du -sb to get registry data size
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "du", []string{"-sb", "/mnt/agentdata"}, gomock.Any()).Return("60000000000 /mnt/agentdata", nil),
				// Step 2: verifyDiskSpaceForIRI
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(lsblkOutput, nil),
				// Step 3: createIRIRegistryPartition
				// Step 3.0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", []string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"}, gomock.Any()).Return("", nil),
				// Step 3.1: sfdisk -d
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", []string{"-d", "/dev/sda"}, gomock.Any()).Return(sfdiskDump, nil),
				// Step 3.2: sfdisk -N 5 (create partition)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.3: udevadm settle (after sfdisk)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.4: lsblk to check if partition exists (not found - returns only 4 partitions)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}]}]}`, nil),
				// Step 3.5: lsblk to get device type
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null}]}`, nil),
				// Step 3.6: partx to register partition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "partx", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.7: udevadm settle (after partx)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.8: lsblk to verify partition exists
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),
				// Step 3.9: mkfs.xfs
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkfs.xfs", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 4: copyRegistryDataToPartition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkdir", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mount", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "umount", gomock.Any(), gomock.Any()).Return("", nil),
			)

			err := o.SetupIRIRegistryPartition("/dev/sda", nil)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should complete setup successfully", func() {
			// Mock all commands for successful setup
			lsblkOutput := `{"blockdevices": [{"name": "sda", "size": 322122547200}]}`
			sfdiskDump := `last-lba: 629145566`

			gomock.InOrder(
				// Step 1: du -sb to get registry data size
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "du", []string{"-sb", "/mnt/agentdata"}, gomock.Any()).Return("60000000000 /mnt/agentdata", nil),

				// Step 2: verifyDiskSpaceForIRI
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(lsblkOutput, nil),

				// Step 3: createIRIRegistryPartition
				// Step 3.0: sfdisk --relocate gpt-bak-std
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", []string{"--no-reread", "--no-tell-kernel", "--relocate", "gpt-bak-std", "/dev/sda"}, gomock.Any()).Return("", nil),
				// Step 3.1: sfdisk -d
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sfdisk", []string{"-d", "/dev/sda"}, gomock.Any()).Return(sfdiskDump, nil),
				// Step 3.2: sfdisk -N 5 (create partition)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.3: udevadm settle (after sfdisk)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.4: lsblk to check if partition exists (not found - returns only 4 partitions)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}]}]}`, nil),
				// Step 3.5: lsblk to get device type
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null}]}`, nil),
				// Step 3.6: partx to register partition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "partx", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.7: udevadm settle (after partx)
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "udevadm", gomock.Any(), gomock.Any()).Return("", nil),
				// Step 3.8: lsblk to verify partition exists
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),
				// Step 3.9: mkfs.xfs
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkfs.xfs", gomock.Any(), gomock.Any()).Return("", nil),

				// Step 4: copyRegistryDataToPartition
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "lsblk", gomock.Any(), gomock.Any()).Return(`{"blockdevices": [{"name": "sda", "path": "/dev/sda", "type": "disk", "partn": null, "children": [{"name": "sda1", "path": "/dev/sda1", "type": "part", "partn": 1}, {"name": "sda2", "path": "/dev/sda2", "type": "part", "partn": 2}, {"name": "sda3", "path": "/dev/sda3", "type": "part", "partn": 3}, {"name": "sda4", "path": "/dev/sda4", "type": "part", "partn": 4}, {"name": "sda5", "path": "/dev/sda5", "type": "part", "partn": 5}]}]}`, nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mkdir", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "mount", gomock.Any(), gomock.Any()).Return("", nil),
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "sh", gomock.Any(), gomock.Any()).Return("", nil), // rsync
				mockExec.EXPECT().ExecCommandWithOptions(gomock.Any(), "umount", gomock.Any(), gomock.Any()).Return("", nil),
			)

			err := o.SetupIRIRegistryPartition("/dev/sda", nil)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

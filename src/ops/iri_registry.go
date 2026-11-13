package ops

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/coreos/ignition/v2/config/validate"
	"github.com/openshift/assisted-installer/src/ignition"
	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	registryDataDirOnMnt      = "/mnt/agentdata"
	iriRegistryPartitionLabel = "iri-registry"
	iriRegistryTempMount      = "/mnt/iri-registry"
)

// iriRegistryPartition handles IRI registry partition creation and management.
type iriRegistryPartition struct {
	executor  execute.Execute
	log       logrus.FieldLogger
	logWriter io.Writer
}

// newIRIRegistryPartition creates a new IRI registry partition handler.
func newIRIRegistryPartition(executor execute.Execute, log logrus.FieldLogger, logWriter io.Writer) *iriRegistryPartition {
	return &iriRegistryPartition{
		executor:  executor,
		log:       log,
		logWriter: logWriter,
	}
}

// iriPartitionConfig holds IRI partition geometry configuration.
// The partition size is dynamically calculated based on registry data size * 2.5.
type iriPartitionConfig struct {
	sizeGB     int // Partition size in GB
	reservedGB int // Reserved space after partition in GB (for future expansion)
}

// partitionGeometry holds calculated partition boundaries in sectors.
type partitionGeometry struct {
	startSector int64
	sizeSectors int64
	sectorSize  int64
}

// Setup creates partition 5 and copies IRI registry data.
// This must be called AFTER writeImageToDisk() because:
// 1. coreos-installer creates partitions 1-4 during writeImageToDisk
// 2. We create partition 5 at the end of the disk after partitions 1-4 exist
//
// The function performs the following steps:
// 1. Calculates partition size based on registry data size * 2.5
// 2. Verifies disk has sufficient space
// 3. Creates partition 5 at the END of the disk (preserves root auto-expansion)
// 4. Formats partition 5 with XFS filesystem
// 5. Copies registry data from /run/media/iso/registry to partition 5
func (irp *iriRegistryPartition) Setup(device string, liveLogger io.Writer) error {
	irp.log.Info("Starting IRI registry partition setup")

	// Calculate partition size based on actual registry data size
	registryDataSize, err := irp.getRegistryDataSize(registryDataDirOnMnt)
	if err != nil {
		return errors.Wrapf(err, "failed to get registry data size from %s", registryDataDirOnMnt)
	}

	partConfig := irp.calculatePartitionConfig(registryDataSize)

	irp.log.Infof("Verifying disk space and creating partition 5 on %s", device)

	if err := irp.verifyDiskSpaceForIRI(device, partConfig); err != nil {
		return err
	}

	if err := irp.create(device, partConfig); err != nil {
		return err
	}

	irp.log.Info("Copying registry data to partition 5")
	if err := irp.copyRegistryDataToPartition(liveLogger, device); err != nil {
		return err
	}

	irp.log.Info("IRI registry partition setup completed successfully")
	return nil
}

// execPrivileged executes a command with elevated privileges.
func (irp *iriRegistryPartition) execPrivileged(liveLogger io.Writer, command string, args ...string) (string, error) {
	return irp.executor.ExecCommandWithOptions(liveLogger, command, args, execute.WithPrivilege())
}

func (irp *iriRegistryPartition) getRegistryDataSize(dataPath string) (int64, error) {
	// Get size of dataPath directory using du command
	// du -sb outputs: "size_bytes path"
	out, err := irp.execPrivileged(nil, "du", "-sb", dataPath)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to get size of %s", dataPath)
	}

	// Parse the output: "size_bytes path" -> extract just the number
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, errors.Errorf("invalid du output for %s: %s", dataPath, out)
	}

	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse size from du output: %s", out)
	}

	return size, nil
}

// calculatePartitionConfig computes the partition configuration based on registry data size.
// Applies a 2.5x multiplier to accommodate future growth and ensures a minimum of 1GB.
func (irp *iriRegistryPartition) calculatePartitionConfig(registryDataSize int64) iriPartitionConfig {
	// Convert bytes to GB (round up to next GB)
	registryDataSizeGB := (registryDataSize + (1024*1024*1024 - 1)) / (1024 * 1024 * 1024)

	// Calculate partition size: registry data size * 2.5
	partitionSizeGB := int(float64(registryDataSizeGB) * 2.5)

	// Ensure minimum partition size of 1GB
	if partitionSizeGB < 1 {
		partitionSizeGB = 1
	}

	irp.log.Infof("Registry data size: %d bytes (%d GB), calculated partition size: %d GB",
		registryDataSize, registryDataSizeGB, partitionSizeGB)

	return iriPartitionConfig{
		sizeGB:     partitionSizeGB,
		reservedGB: 0,
	}
}

// verifyDiskSpaceForIRI checks if the device has enough free space for the IRI partition
func (irp *iriRegistryPartition) verifyDiskSpaceForIRI(device string, partConfig iriPartitionConfig) error {
	type node struct {
		Name     string  `json:"name"`
		Size     int64   `json:"size"`
		Children []*node `json:"children,omitempty"`
	}
	var disks struct {
		Blockdevices []*node `json:"blockdevices"`
	}

	ret, err := irp.execPrivileged(nil, "lsblk", "--bytes", "--json", device)
	if err != nil {
		return errors.Wrap(err, "failed to run lsblk command")
	}
	if err = json.Unmarshal([]byte(ret), &disks); err != nil {
		return errors.Wrap(err, "failed to unmarshal lsblk output")
	}

	if len(disks.Blockdevices) == 0 {
		return errors.Errorf("no block device information returned for %s", device)
	}

	disk := disks.Blockdevices[0]
	totalDiskSize := disk.Size

	// Calculate actually used space by summing existing partitions
	var usedSpace int64
	for _, partition := range disk.Children {
		usedSpace += partition.Size
	}
	freeSpace := totalDiskSize - usedSpace

	// Use partition config (may be dynamically calculated from registry data size)
	requiredSize := int64(partConfig.sizeGB) * 1024 * 1024 * 1024
	requiredSize += int64(partConfig.reservedGB) * 1024 * 1024 * 1024

	if freeSpace < requiredSize {
		return errors.Errorf("not enough free space on %s: free=%d bytes, required=%d bytes (%dGB IRI partition + %dGB reserved)",
			device, freeSpace, requiredSize, partConfig.sizeGB, partConfig.reservedGB)
	}

	irp.log.Infof("Disk %s has sufficient free space: %d bytes (required: %d bytes for %dGB IRI partition + %dGB reserved)",
		device, freeSpace, requiredSize, partConfig.sizeGB, partConfig.reservedGB)
	return nil
}

// relocateBackupGPT moves the backup GPT table to the end of disk (idempotent).
// This is necessary because OverwriteOsImage() might not be invoked (it calls growpart which
// automatically fixes secondary GPT). If not invoked, we must ensure backup GPT is correctly
// positioned before creating partition 5 at the end of disk.
func (irp *iriRegistryPartition) relocateBackupGPT(device string) error {
	_, err := irp.execPrivileged(irp.logWriter, "sfdisk",
		"--no-reread", "--no-tell-kernel",
		"--relocate", "gpt-bak-std", device)
	if err != nil {
		return errors.Wrapf(err, "failed to relocate backup GPT on %s", device)
	}
	return nil
}

// parsePartitionTableMetadata extracts last-lba and sector-size from sfdisk dump output.
// GPT reserves sectors at end for backup partition table, so we must use last-lba
// instead of total disk size to avoid "sector beyond last-lba" errors.
func (irp *iriRegistryPartition) parsePartitionTableMetadata(dump, device string) (lastLBA, sectorSize int64, err error) {
	sectorSize = 512 // default fallback
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "last-lba:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				lastLBA, err = strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return 0, 0, errors.Wrap(err, "failed to parse last-lba")
				}
			}
		} else if strings.HasPrefix(line, "sector-size:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				sectorSize, err = strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return 0, 0, errors.Wrap(err, "failed to parse sector-size")
				}
			}
		}
	}
	if lastLBA == 0 {
		return 0, 0, errors.Errorf("could not find last-lba in partition table for %s", device)
	}
	return lastLBA, sectorSize, nil
}

// computeAlignedPartitionBoundaries calculates partition start and size with 1 MiB alignment.
// Validates that the partition starts after the 25000 MiB threshold recommended by Red Hat.
// See: https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/postinstallation_configuration/post-install-node-tasks
// This ensures proper alignment and leaves sufficient space for root partition expansion.
// CoreOS also requires root >= 8 GiB to boot successfully:
// See: https://github.com/coreos/fedora-coreos-docs/blob/main/modules/ROOT/pages/storage.adoc
func (irp *iriRegistryPartition) computeAlignedPartitionBoundaries(lastLBA, sectorSize int64, partConfig iriPartitionConfig) (*partitionGeometry, error) {
	// Calculate partition size in sectors
	partitionSizeBytes := int64(partConfig.sizeGB) * 1024 * 1024 * 1024
	partitionSizeSectors := partitionSizeBytes / sectorSize

	// Calculate reserved space (space to leave unallocated after partition)
	reservedSpaceBytes := int64(partConfig.reservedGB) * 1024 * 1024 * 1024
	reservedSpaceSectors := reservedSpaceBytes / sectorSize

	// Align partition end to 1 MiB boundary
	// last-lba already excludes space for GPT secondary header, no need for additional margin
	// See: https://www.man7.org/linux/man-pages/man8/sfdisk.8.html
	sectorsPerMiB := int64(1024*1024) / sectorSize
	usableEnd := lastLBA + 1 - reservedSpaceSectors
	alignedEnd := (usableEnd / sectorsPerMiB) * sectorsPerMiB

	// Calculate start sector
	// Since partitionSizeSectors represents whole GiB (already MiB-aligned),
	// startSector will also be MiB-aligned
	startSector := alignedEnd - partitionSizeSectors

	// Verify partition starts after the 25000 MiB threshold
	startBytes := startSector * sectorSize
	startMiB := startBytes / (1024 * 1024)
	const minStartMiB = 25000

	if startMiB < minStartMiB {
		return nil, errors.Errorf("IRI partition would start at %d MiB, before recommended %d MiB threshold (disk too small or partitions too large)",
			startMiB, minStartMiB)
	}

	irp.log.Infof("Creating %dGB partition (last-lba: %d, start: %d, size: %d sectors, start offset: %d MiB, reserved: %dGB)",
		partConfig.sizeGB, lastLBA, startSector, partitionSizeSectors, startMiB, partConfig.reservedGB)

	return &partitionGeometry{
		startSector: startSector,
		sizeSectors: partitionSizeSectors,
		sectorSize:  sectorSize,
	}, nil
}

// calculatePartitionGeometry orchestrates partition geometry calculation (steps 1-3).
// Dumps partition table, parses metadata, and computes aligned boundaries.
func (irp *iriRegistryPartition) calculatePartitionGeometry(device string, partConfig iriPartitionConfig) (*partitionGeometry, error) {
	// Step 1: Dump partition table
	dump, err := irp.execPrivileged(nil, "sfdisk", "-d", device)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dump partition table for %s", device)
	}

	// Step 2: Parse metadata
	lastLBA, sectorSize, err := irp.parsePartitionTableMetadata(dump, device)
	if err != nil {
		return nil, err
	}

	// Step 3: Compute aligned boundaries
	return irp.computeAlignedPartitionBoundaries(lastLBA, sectorSize, partConfig)
}

// createPartitionEntry creates partition 5 entry in GPT using sfdisk -N.
// This is safer than the dump+append approach because:
// - No need to rebuild entire GPT table
// - No risk of shell pipe failure (echo executing if sfdisk -d fails)
// - Atomic modification of partition 5 only
// GPT type GUID: 0FC63DAF-8483-4772-8E79-3D69D8477DE4 = Linux filesystem
// name sets the partition label (creates /dev/disk/by-partlabel/iri-registry symlink)
// Use positional arguments ($1, $2) to avoid shell injection from device path
func (irp *iriRegistryPartition) createPartitionEntry(device string, geometry *partitionGeometry) error {
	partSpec := fmt.Sprintf("start=%d, size=%d, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"%s\"",
		geometry.startSector, geometry.sizeSectors, iriRegistryPartitionLabel)
	sfdiskCmd := `printf '%s\n' "$1" | sfdisk --no-reread --no-tell-kernel -N 5 "$2"`

	irp.log.Infof("Creating partition 5 with sfdisk -N: %s", partSpec)
	_, err := irp.execPrivileged(irp.logWriter, "sh", "-c", sfdiskCmd, "sh", partSpec, device)
	if err != nil {
		return errors.Wrapf(err, "failed to create partition 5 on device %s", device)
	}
	return nil
}

// waitForUdevSettle waits for udev to finish processing device events.
func (irp *iriRegistryPartition) waitForUdevSettle(device string) error {
	_, err := irp.execPrivileged(irp.logWriter, "udevadm", "settle")
	if err != nil {
		return errors.Wrapf(err, "failed to wait for udev to settle on %s", device)
	}
	return nil
}

// getDeviceType returns the device type (disk, mpath, etc.) from lsblk.
func (irp *iriRegistryPartition) getDeviceType(device string) (string, error) {
	type node struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var disks struct {
		Blockdevices []*node `json:"blockdevices"`
	}

	ret, err := irp.execPrivileged(nil, "lsblk", "--bytes", "--json", device)
	if err != nil {
		return "", errors.Wrap(err, "failed to run lsblk command")
	}
	if err = json.Unmarshal([]byte(ret), &disks); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal lsblk output")
	}

	if len(disks.Blockdevices) == 0 {
		return "", errors.Errorf("no block device information returned for %s", device)
	}

	return disks.Blockdevices[0].Type, nil
}

// registerPartitionExplicitly performs explicit partition registration with kernel.
// Determines device type and uses appropriate command:
// - Multipath devices: kpartx -u
// - Regular disks: partx --add --nr 5
func (irp *iriRegistryPartition) registerPartitionExplicitly(device, partitionNumber string) error {
	irp.log.Infof("Partition %s not auto-discovered, performing explicit registration", partitionNumber)

	// Determine device type for appropriate registration command
	deviceType, err := irp.getDeviceType(device)
	if err != nil {
		return errors.Wrapf(err, "failed to determine device type for %s", device)
	}

	// Register partition based on device type
	if deviceType == "mpath" {
		// Multipath device - use kpartx
		irp.log.Infof("Using kpartx for multipath device %s", device)
		_, err = irp.execPrivileged(irp.logWriter, "kpartx", "-u", device)
		if err != nil {
			return errors.Wrapf(err, "failed to register partitions with kpartx on multipath device %s", device)
		}
	} else {
		// Regular disk - use partx
		irp.log.Infof("Using partx for regular disk %s (type: %s)", device, deviceType)
		_, err = irp.execPrivileged(irp.logWriter, "partx", "--add", "--nr", partitionNumber, device)
		if err != nil {
			return errors.Wrapf(err, "failed to add partition %s to kernel partition table for %s", partitionNumber, device)
		}
	}

	// Wait for udev to settle after explicit registration
	irp.log.Info("Waiting for udev to settle after explicit registration")
	if err := irp.waitForUdevSettle(device); err != nil {
		return errors.Wrapf(err, "after partition registration on %s", device)
	}

	return nil
}

// ensurePartitionRegistered ensures the partition is registered with the kernel and returns its path.
// Uses happy path pattern with early returns:
// - Path 1: Partition auto-discovered after udev settle
// - Path 2: Partition not found, perform explicit registration
func (irp *iriRegistryPartition) ensurePartitionRegistered(device, partitionNumber string) (string, error) {
	// Precondition: wait for udev to settle after partition creation
	irp.log.Info("Waiting for udev to settle after partition creation")
	if err := irp.waitForUdevSettle(device); err != nil {
		return "", err
	}

	// Try to find partition (might be auto-discovered)
	partitionPath, err := getPartitionPathFromLsblk(irp.executor, device, partitionNumber)

	// Propagate unexpected errors immediately
	if err != nil && !errors.Is(err, errPartitionNotFound) {
		return "", errors.Wrapf(err, "failed to check for partition %s on %s", partitionNumber, device)
	}

	// Happy path 1: Partition auto-discovered
	if err == nil {
		irp.log.Infof("Partition %s auto-discovered at %s, skipping explicit registration", partitionNumber, partitionPath)
		return partitionPath, nil
	}

	// Happy path 2: Partition not found, need explicit registration
	if err := irp.registerPartitionExplicitly(device, partitionNumber); err != nil {
		return "", err
	}

	// Verify partition now exists
	partitionPath, err = getPartitionPathFromLsblk(irp.executor, device, partitionNumber)
	if err != nil {
		return "", errors.Wrapf(err, "partition %s still not found on %s after explicit registration", partitionNumber, device)
	}

	irp.log.Infof("Partition %s successfully registered at %s", partitionNumber, partitionPath)
	return partitionPath, nil
}

// formatPartitionXFS formats the partition with XFS filesystem.
// The -f flag forces overwrite, supporting reinstall scenarios.
func (irp *iriRegistryPartition) formatPartitionXFS(partitionPath string) error {
	irp.log.Infof("Formatting %s with XFS", partitionPath)
	_, err := irp.execPrivileged(irp.logWriter, "mkfs.xfs", "-f", "-L", iriRegistryPartitionLabel, partitionPath)
	if err != nil {
		return errors.Wrapf(err, "failed to format partition %s with XFS", partitionPath)
	}
	return nil
}

// createIRIRegistryPartition creates partition 5 at the end of the disk for IRI registry.
//
// This function executes the following commands:
//
//  0. sfdisk --no-reread --no-tell-kernel --relocate gpt-bak-std /dev/sda
//     Relocates the backup GPT to the end of disk (idempotent).
//     Necessary because OverwriteOsImage() might not be invoked (which calls growpart that
//     automatically fixes secondary GPT). Ensures backup GPT is correctly positioned.
//
//  1. sfdisk -d /dev/sda
//     Dumps the current partition table to get the GPT last-lba (last usable sector)
//     and sector-size. GPT reserves sectors at the end of the disk for the backup
//     partition table, so we can't use the full disk size.
//
// 2. Calculate partition geometry with 1 MiB alignment:
//
//   - Partition size: dynamically calculated based on registry data size * 2.5
//
//   - Align partition end to 1 MiB: alignedEnd = (usableEnd / sectorsPerMiB) * sectorsPerMiB
//
//   - Start sector: alignedEnd - partitionSizeSectors (MiB-aligned, since partition size is whole GiB)
//
//     3. printf 'start=X, size=Y, type=GUID, name="LABEL"\n' | sfdisk --no-reread --no-tell-kernel -N 5 /dev/sda
//     Creates partition 5 by modifying partition table entry 5 directly.
//     This is safer than dump+append because it avoids shell pipe failure modes.
//     Uses --no-reread --no-tell-kernel because disk is in use (partitions 1-4 mounted).
//     GPT type GUID: 0FC63DAF-8483-4772-8E79-3D69D8477DE4 = Linux filesystem
//
//     4. udevadm settle
//     Waits for udev to finish processing partition table events before checking if the
//     partition was auto-discovered.
//
//     5. Check if partition 5 exists using getPartitionPathFromLsblk()
//     If partition was auto-discovered (common on multipath devices), skip explicit registration.
//     If not found, determine device type and use appropriate registration command:
//
//   - Regular disks: partx --add --nr 5 /dev/sda
//
//   - Multipath devices: kpartx -u /dev/mapper/mpatha
//
//     6. udevadm settle (again, after explicit registration if needed)
//     Ensures device nodes are created before formatting.
//
// create creates partition 5 at the end of the disk for IRI registry storage.
//
// Workflow:
//  1. Relocate GPT backup header (sfdisk --relocate gpt-bak-std)
//  2. Parse partition table metadata (sfdisk -d → extract last-lba, sector-size)
//  3. Calculate geometry with 1 MiB alignment (validates 25000 MiB start threshold)
//  4. Create partition entry in GPT (sfdisk -N 5)
//  5. Register partition with kernel (udev settle + auto-discovery or partx/kpartx)
//  6. Format with XFS filesystem (mkfs.xfs -f -L iri-registry)
//
// The partition is created at the disk end to preserve root partition auto-expansion.
// Uses --no-reread --no-tell-kernel because disk is in use during installation.
func (irp *iriRegistryPartition) create(device string, partConfig iriPartitionConfig) error {
	irp.log.Infof("Creating IRI registry partition on device %s", device)

	// Step 0: Ensure GPT backup is positioned correctly
	if err := irp.relocateBackupGPT(device); err != nil {
		return err
	}

	// Steps 1-3: Calculate partition geometry (dump table, parse, compute boundaries)
	geometry, err := irp.calculatePartitionGeometry(device, partConfig)
	if err != nil {
		return err
	}

	// Step 4: Create partition entry in GPT
	if err := irp.createPartitionEntry(device, geometry); err != nil {
		return err
	}

	// Steps 5-6: Ensure kernel recognizes partition (udev + registration)
	partitionPath, err := irp.ensurePartitionRegistered(device, "5")
	if err != nil {
		return err
	}

	// Step 7: Format with XFS filesystem
	if err := irp.formatPartitionXFS(partitionPath); err != nil {
		return err
	}

	irp.log.Infof("Successfully created and formatted IRI registry partition at %s", partitionPath)
	return nil
}

// copyRegistryDataToPartition mounts partition 5 and copies registry data to it
func (irp *iriRegistryPartition) copyRegistryDataToPartition(liveLogger io.Writer, device string) error {
	irp.log.Info("Copying registry data to IRI partition")

	// Get partition 5 path
	partition5, err := getPartitionPathFromLsblk(irp.executor, device, "5")
	if err != nil {
		return errors.Wrapf(err, "failed to find partition 5 on device %s", device)
	}

	// Create temporary mount point
	if _, err = irp.execPrivileged(irp.logWriter, "mkdir", "-p", iriRegistryTempMount); err != nil {
		return errors.Wrapf(err, "failed to create temporary mount point %s", iriRegistryTempMount)
	}

	// Mount partition 5 with retry (device might not be ready immediately after udevadm settle)
	irp.log.Infof("Mounting %s to %s", partition5, iriRegistryTempMount)
	if err = utils.Retry(3, 5*time.Second, irp.log, func() (err error) {
		_, err = irp.execPrivileged(irp.logWriter, "mount", partition5, iriRegistryTempMount)
		return
	}); err != nil {
		return errors.Wrapf(err, "failed to mount %s to %s", partition5, iriRegistryTempMount)
	}

	// Ensure unmount on exit
	defer func() {
		irp.log.Infof("Unmounting %s", iriRegistryTempMount)
		if _, unmountErr := irp.execPrivileged(irp.logWriter, "umount", iriRegistryTempMount); unmountErr != nil {
			irp.log.WithError(unmountErr).Warnf("Failed to unmount %s", iriRegistryTempMount)
		}
	}()

	// Copy registry data using rsync with progress reporting and retry
	// Source: /mnt/agentdata/  (registryDataDirOnMnt)
	// Destination: /mnt/iri-registry/
	rsyncCmd := fmt.Sprintf("rsync -ah --info=progress2 %s/ %s/", registryDataDirOnMnt, iriRegistryTempMount)

	irp.log.Infof("Running rsync to copy registry data")
	if err = utils.Retry(3, 5*time.Second, irp.log, func() (err error) {
		_, err = irp.execPrivileged(liveLogger, "sh", "-c", rsyncCmd)
		return
	}); err != nil {
		return errors.Wrapf(err, "failed to copy registry data from %s to %s", registryDataDirOnMnt, iriRegistryTempMount)
	}

	irp.log.Info("Successfully copied registry data to IRI partition")
	return nil
}

// PrepareIgnition adds the IRI registry mount unit to ignition if not already present.
// This must be called BEFORE writeImageToDisk() so that coreos-installer
// embeds the modified ignition with the mount unit into the disk image.
//
// TEMPORARY: This function is idempotent and will be removed once MCO provides
// the mount unit in the master machineconfig (3-phase migration):
// Phase 1 (now): AI adds mount unit
// Phase 2: MCO adds mount unit (AI detects and skips)
// Phase 3: Remove this function from AI
func (irp *iriRegistryPartition) PrepareIgnition(ignitionPath string) error {
	irp.log.Info("Preparing IRI registry mount unit in ignition")

	// Parse existing ignition config
	ign := ignition.NewIgnition()
	config, err := ign.ParseIgnitionFile(ignitionPath)
	if err != nil {
		return errors.Wrapf(err, "failed to parse ignition file %s", ignitionPath)
	}

	// Check if mount unit already exists (idempotency check)
	// This allows smooth migration: MCO can add the mount unit and AI will skip it
	mountUnitName := "var-lib-iri\\x2dregistry.mount"
	for _, unit := range config.Systemd.Units {
		if unit.Name == mountUnitName {
			irp.log.Infof("IRI registry mount unit '%s' already present in ignition, skipping (likely from MCO)", mountUnitName)
			return nil
		}
	}

	irp.log.Info("Adding IRI registry mount unit to ignition (temporary workaround until MCO provides it)")

	// Create systemd mount unit content
	// Note: The mount unit name MUST match the Where= path with systemd escaping rules:
	// - '/' becomes '-'
	// - '-' in the original path becomes '\x2d'
	// Path /var/lib/iri-registry -> var-lib-iri\x2dregistry.mount
	mountUnitContent := `[Unit]
Description=Mount IRI Registry Partition
DefaultDependencies=no
Conflicts=umount.target
Before=local-fs.target umount.target iri-registry.service

[Mount]
What=/dev/disk/by-partlabel/iri-registry
Where=/var/lib/iri-registry
Type=xfs
Options=defaults

[Install]
WantedBy=local-fs.target
`

	// Create the mount unit
	// Name uses \\x2d (double backslash in Go string) -> becomes \x2d in ignition
	enabled := true
	mountUnit := types.Unit{
		Name:     "var-lib-iri\\x2dregistry.mount",
		Enabled:  &enabled,
		Contents: &mountUnitContent,
	}

	// Add mount unit to systemd units
	config.Systemd.Units = append(config.Systemd.Units, mountUnit)

	// Validate the modified config
	report := validate.ValidateWithContext(*config, nil)
	if report.IsFatal() {
		return errors.Errorf("modified ignition config is invalid: %s", report.String())
	}

	// Write the modified ignition back
	if err := ign.WriteIgnitionFile(ignitionPath, config); err != nil {
		return errors.Wrapf(err, "failed to write modified ignition file %s", ignitionPath)
	}

	irp.log.Info("Successfully prepared IRI registry ignition")
	return nil
}

// getPartitionPathFromLsblk uses lsblk to find the actual partition device path
// for a given device and partition number. This handles all device types including
// device mapper devices correctly.
//
// The function queries lsblk with NAME,PATH,TYPE,PARTN fields and iterates through
// the children to find the partition by its PARTN number (not array position),
// ensuring correct identification even if partitions are missing or out of order.
func getPartitionPathFromLsblk(executor execute.Execute, device, partitionNumber string) (string, error) {
	type node struct {
		Name     string  `json:"name"`
		Path     string  `json:"path"`
		Type     string  `json:"type"`
		PartN    *int    `json:"partn"` // Nullable: null for parent device, number for partitions
		Children []*node `json:"children,omitempty"`
	}
	var disks struct {
		Blockdevices []*node `json:"blockdevices"`
	}

	// Query lsblk with explicit fields including PARTN for partition number matching
	ret, err := executor.ExecCommandWithOptions(nil, "lsblk", []string{"--bytes", "--json", "--output", "NAME,PATH,TYPE,PARTN", device}, execute.WithPrivilege())
	if err != nil {
		return "", errors.Wrap(err, "failed to run lsblk command")
	}
	if err = json.Unmarshal([]byte(ret), &disks); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal lsblk output")
	}

	if len(disks.Blockdevices) == 0 {
		return "", errors.Errorf("no block device information returned for %s", device)
	}
	diskNode := disks.Blockdevices[0] // lsblk with device filter returns only that device

	// Convert partition number to integer for matching
	partNum, err := strconv.Atoi(partitionNumber)
	if err != nil {
		return "", errors.Errorf("invalid partition number %s", partitionNumber)
	}

	// Find partition by PARTN field, not by array position
	// This correctly handles missing partitions and out-of-order results
	for _, child := range diskNode.Children {
		// Skip if PARTN is null or doesn't match requested partition
		if child.PartN == nil || *child.PartN != partNum {
			continue
		}

		// Validate partition type
		if child.Type != "part" {
			return "", errors.Errorf("found device with PARTN=%d but TYPE=%s (expected TYPE=part)", partNum, child.Type)
		}

		// Validate path is non-empty
		if child.Path == "" {
			return "", errors.Errorf("partition %d found but PATH is empty", partNum)
		}

		// Return the PATH field directly (works for both regular disks and multipath)
		return child.Path, nil
	}

	// Partition genuinely not found - return sentinel error
	return "", errors.Wrapf(errPartitionNotFound, "partition %s not found on device %s", partitionNumber, device)
}

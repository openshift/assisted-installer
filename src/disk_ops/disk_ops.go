package disk_ops

import (
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"path/filepath"
	"regexp"
	"strings"
)

type DiskOps struct {
	log      logrus.Logger
	executor Execute
}

func NewDiskOps(logger logrus.Logger, executor Execute) DiskOps {
	return DiskOps{log: logger, executor: executor}
}

func (r *DiskOps) CleanupInstallDevice(device string) error {

	var ret error
	r.log.Infof("Start cleaning up device %s", device)
	err := r.cleanupDevice(device)

	if err != nil {
		ret = CombineErrors(ret, err)
	}

	if r.IsRaidMember(device) {
		r.log.Infof("A raid was detected on the device (%s) - cleaning", device)
		var devices []string
		devices, err = r.GetRaidDevices(device)

		if err != nil {
			ret = CombineErrors(ret, err)
		}

		for _, raidDevice := range devices {
			// Cleaning the raid device itself before removing membership.
			err = r.cleanupDevice(raidDevice)

			if err != nil {
				ret = CombineErrors(ret, err)
			}
		}

		err = r.CleanRaidMembership(device)

		if err != nil {
			ret = CombineErrors(ret, err)
		}
		r.log.Infof("Finished cleaning up device %s", device)
	}

	err = r.Wipefs(device)
	if err != nil {
		ret = CombineErrors(ret, err)
	}

	return ret
}

func (r *DiskOps) cleanupDevice(device string) error {
	var ret error
	vgNames, err := r.GetVolumeGroupsByDisk(device)
	if err != nil {
		ret = err
	}

	if len(vgNames) > 0 {
		if err = r.RemoveVolumeGroupsFromDevice(vgNames, device); err != nil {
			ret = CombineErrors(ret, err)
		}
	}

	if err = r.RemoveAllPVsOnDevice(device); err != nil {
		ret = CombineErrors(ret, err)
	}

	if err = r.RemoveAllDMDevicesOnDisk(device); err != nil {
		ret = CombineErrors(ret, err)
	}

	return ret
}

func (r *DiskOps) RemoveVolumeGroupsFromDevice(vgNames []string, device string) error {
	var ret error
	for _, vgName := range vgNames {
		r.log.Infof("A volume group (%s) was detected on the installation device (%s) - cleaning", vgName, device)
		err := r.RemoveVG(vgName)

		if err != nil {
			r.log.Errorf("Could not delete volume group (%s) due to error: %w", vgName, err)
			ret = CombineErrors(ret, err)
		}
	}
	return ret
}

func (r *DiskOps) IsRaidMember(device string) bool {
	raidDevices, err := r.getRaidDevices2Members()

	if err != nil {
		r.log.WithError(err).Errorf("Error occurred while trying to get list of raid devices - continue without cleaning")
		return false
	}

	// The device itself or one of its partitions
	expression, _ := regexp.Compile(device + "[\\d]*")

	for _, raidArrayMembers := range raidDevices {
		for _, raidMember := range raidArrayMembers {
			if expression.MatchString(raidMember) {
				return true
			}
		}
	}

	return false
}

func (r *DiskOps) CleanRaidMembership(device string) error {
	raidDevices, err := r.getRaidDevices2Members()

	if err != nil {
		return err
	}

	for raidDeviceName, raidArrayMembers := range raidDevices {
		err = r.removeDeviceFromRaidArray(device, raidDeviceName, raidArrayMembers)

		if err != nil {
			return err
		}
	}

	return nil
}

func (r *DiskOps) GetRaidDevices(deviceName string) ([]string, error) {
	raidDevices, err := r.getRaidDevices2Members()
	var result []string

	if err != nil {
		return result, err
	}

	for raidDeviceName, raidArrayMembers := range raidDevices {
		expression, _ := regexp.Compile(deviceName + "[\\d]*")

		for _, raidMember := range raidArrayMembers {
			// A partition or the device itself is part of the raid array.
			if expression.MatchString(raidMember) {
				result = append(result, raidDeviceName)
				break
			}
		}
	}

	return result, nil
}

func (r *DiskOps) getRaidDevices2Members() (map[string][]string, error) {
	output, err := r.ExecPrivilegeCommand("mdadm", "-v", "--query", "--detail", "--scan")

	if err != nil {
		return nil, err
	}

	lines := strings.Split(output, "\n")
	result := make(map[string][]string)

	/*
		The output pattern is:
		ARRAY /dev/md0 level=raid1 num-devices=2 metadata=1.2 name=0 UUID=77e1b6f2:56530ebd:38bd6808:17fd01c4
		   devices=/dev/vda2,/dev/vda3
		ARRAY /dev/md1 level=raid1 num-devices=1 metadata=1.2 name=1 UUID=aad7aca9:81db82f3:2f1fedb1:f89ddb43
		   devices=/dev/vda1
	*/
	for i := 0; i < len(lines); {
		if !strings.Contains(lines[i], "ARRAY") {
			i++
			continue
		}

		fields := strings.Fields(lines[i])
		// In case of symlink, get real file path
		raidDeviceName, err := filepath.EvalSymlinks(fields[1])
		if err != nil {
			return nil, errors.Wrapf(err, "failed to evaluate raid symlink")
		}

		i++

		// Ensuring that we have at least two lines per device.
		if len(lines) == i {
			break
		}

		raidArrayMembersStr := strings.TrimSpace(lines[i])
		prefix := "devices="

		if !strings.HasPrefix(raidArrayMembersStr, prefix) {
			continue
		}

		raidArrayMembersStr = raidArrayMembersStr[len(prefix):]
		result[raidDeviceName] = strings.Split(raidArrayMembersStr, ",")
		i++
	}

	return result, nil
}

func (r *DiskOps) removeDeviceFromRaidArray(deviceName string, raidDeviceName string, raidArrayMembers []string) error {
	raidStopped := false

	expression, _ := regexp.Compile(deviceName + "[\\d]*")

	for _, raidMember := range raidArrayMembers {
		// A partition or the device itself is part of the raid array.
		if expression.MatchString(raidMember) {
			// Stop the raid device.
			if !raidStopped {
				r.log.Info("Stopping raid device: " + raidDeviceName)
				_, err := r.ExecPrivilegeCommand("mdadm", "--stop", raidDeviceName)

				if err != nil {
					return err
				}

				raidStopped = true
			}

			// Clean the raid superblock from the device
			r.log.Infof("Cleaning raid member %s superblock", raidMember)
			_, err := r.ExecPrivilegeCommand("mdadm", "--zero-superblock", raidMember)

			if err != nil {
				return err
			}
		}
	}
	return nil
}

// ExecPrivilegeCommand execute a command in the host environment via nsenter
func (r *DiskOps) ExecPrivilegeCommand(command string, args ...string) (string, error) {
	// nsenter is used here to launch processes inside the container in a way that makes said processes feel
	// and behave as if they're running on the host directly rather than inside the container
	commandBase := "nsenter"

	arguments := []string{
		"--target", "1",
		// Entering the cgroup namespace is not required for podman on CoreOS (where the
		// agent typically runs), but it's needed on some Fedora versions and
		// some other systemd based systems. Those systems are used to run dry-mode
		// agents for load testing. If this flag is not used, Podman will sometimes
		// have trouble creating a systemd cgroup slice for new containers.
		"--cgroup",
		// The mount namespace is required for podman to access the host's container
		// storage
		"--mount",
		// TODO: Document why we need the IPC namespace
		"--ipc",
		"--pid",
		"--",
		command,
	}

	arguments = append(arguments, args...)
	return r.executor.ExecCommand(r.log.Writer(), commandBase, arguments...)
}

func (r *DiskOps) Wipefs(device string) error {
	_, err := r.ExecPrivilegeCommand("wipefs", "--all", "--force", device)

	if err != nil {
		_, err = r.ExecPrivilegeCommand("wipefs", "--all", device)
	}

	return err
}

func (r *DiskOps) GetVolumeGroupsByDisk(diskName string) ([]string, error) {
	var vgs []string

	output, err := r.ExecPrivilegeCommand("vgs", "--noheadings", "-o", "vg_name,pv_name")
	if err != nil {
		r.log.Errorf("Failed to list VGs in the system")
		return vgs, err
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		res := strings.Fields(line)
		if len(res) < 2 {
			continue
		}

		r.log.Infof("Found LVM Volume Group %s in disk %s", res[0], res[1])
		if strings.Contains(res[1], diskName) {
			vgs = append(vgs, res[0])
		} else {
			r.log.Infof("Skipping removal of Volume Group %s, does not belong to disk %s", res[0], diskName)
		}
	}
	return vgs, nil
}

func (r *DiskOps) getDiskPVs(diskName string) ([]string, error) {
	var pvs []string
	output, err := r.ExecPrivilegeCommand("pvs", "--noheadings", "-o", "pv_name")
	if err != nil {
		r.log.Errorf("Failed to list PVs in the system")
		return pvs, err
	}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, diskName) {
			pvs = append(pvs, strings.TrimSpace(line))
		}
	}
	return pvs, nil
}

func (r *DiskOps) RemoveAllPVsOnDevice(diskName string) error {
	var ret error
	pvs, err := r.getDiskPVs(diskName)
	if err != nil {
		return err
	}
	for _, pv := range pvs {
		r.log.Infof("Removing pv %s from disk %s", pv, diskName)
		err = r.RemovePV(pv)
		if err != nil {
			r.log.Errorf("Failed remove pv %s from disk %s", pv, diskName)
			ret = CombineErrors(ret, err)
		}
	}
	return ret
}

func (r *DiskOps) getDMDevices(diskName string) ([]string, error) {
	var dmDevices []string
	output, err := r.ExecPrivilegeCommand("dmsetup", "ls")
	if err != nil {
		r.log.Errorf("Failed to list DM devices in the system")
		return dmDevices, err
	}

	if strings.TrimSuffix(output, "\n") == "No devices found" {
		return dmDevices, nil
	}

	diskBasename := filepath.Base(diskName)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		dmDevice := strings.Split(line, "\t")[0]
		output, err = r.ExecPrivilegeCommand("dmsetup", "deps", "-o", "devname", dmDevice)
		if err != nil {
			r.log.Errorf("Failed to get parent device for DM device %s", dmDevice)
			return dmDevices, err
		}
		if strings.Contains(output, "("+diskBasename) {
			dmDevices = append(dmDevices, dmDevice)
		}
	}
	return dmDevices, nil
}

func (r *DiskOps) RemoveDMDevice(dmDevice string) error {
	output, err := r.ExecPrivilegeCommand("dmsetup", "remove", "--retry", dmDevice)
	if err != nil {
		r.log.Errorf("Failed to remove DM device %s, output %s, error %s", dmDevice, output, err)
	}
	return err
}

func (r *DiskOps) RemoveAllDMDevicesOnDisk(diskName string) error {
	var ret error
	dmDevices, err := r.getDMDevices(diskName)
	if err != nil {
		return err
	}
	for _, dmDevice := range dmDevices {
		r.log.Infof("Removing DM device %s", dmDevice)
		err = r.RemoveDMDevice(dmDevice)
		if err != nil {
			r.log.Errorf("Failed to remove DM device %s", dmDevice)
			ret = CombineErrors(ret, err)
		}
	}
	return ret
}

func (r *DiskOps) RemoveVG(vgName string) error {
	output, err := r.ExecPrivilegeCommand("vgremove", vgName, "-y")
	if err != nil {
		r.log.Errorf("Failed to remove VG %s, output %s, error %s", vgName, output, err)
	}
	return err
}

func (r *DiskOps) RemovePV(pvName string) error {
	output, err := r.ExecPrivilegeCommand("pvremove", pvName, "-y", "-ff")
	if err != nil {
		r.log.Errorf("Failed to remove PV %s, output %s, error %s", pvName, output, err)
	}
	return err
}

func CombineErrors(error1 error, error2 error) error {
	if error1 != nil {
		return errors.Wrapf(error1, "%s", error2)
	}
	return error2
}

package shared_ops

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

//go:generate mockgen -source=disk_ops.go -package=shared_ops -destination=mock_disk_ops.go
type DiskOps interface {
	GetVolumeGroupsByDisk(diskName string) ([]string, error)
	RemoveAllPVsOnDevice(diskName string) error
	RemoveAllDMDevicesOnDisk(diskName string) error
	RemoveVG(vgName string) error
	RemovePV(pvName string) error
	Wipefs(device string) error
	IsRaidMember(device string) bool
	GetRaidDevices(device string) ([]string, error)
	CleanRaidMembership(device string) error
}

type CleanupDevice interface {
	CleanupInstallDevice(device string) error
}

type diskOps struct {
	log      *logrus.Logger
	executor Execute
}

type cleanupDevice struct {
	log     *logrus.Logger
	diskOps DiskOps
}

func NewCleanupDevice(logger *logrus.Logger, diskOps DiskOps) CleanupDevice {
	return &cleanupDevice{log: logger, diskOps: diskOps}
}

func (r *cleanupDevice) CleanupInstallDevice(device string) error {

	var ret error
	r.log.Infof("Start cleaning up device %s", device)
	err := r.cleanupDevice(device)

	if err != nil {
		ret = combineErrors(ret, err)
	}

	if r.diskOps.IsRaidMember(device) {
		r.log.Infof("A raid was detected on the device (%s) - cleaning", device)
		var devices []string
		devices, err = r.diskOps.GetRaidDevices(device)

		if err != nil {
			ret = combineErrors(ret, err)
		}

		for _, raidDevice := range devices {
			// Cleaning the raid device itself before removing membership.
			err = r.cleanupDevice(raidDevice)

			if err != nil {
				ret = combineErrors(ret, err)
			}
		}

		err = r.diskOps.CleanRaidMembership(device)

		if err != nil {
			ret = combineErrors(ret, err)
		}
		r.log.Infof("Finished cleaning up device %s", device)
	}

	err = r.diskOps.Wipefs(device)
	if err != nil {
		ret = combineErrors(ret, err)
	}

	return ret
}

func (r *cleanupDevice) cleanupDevice(device string) error {
	var ret error
	vgNames, err := r.diskOps.GetVolumeGroupsByDisk(device)
	if err != nil {
		ret = err
	}

	if len(vgNames) > 0 {
		if err = r.removeVolumeGroupsFromDevice(vgNames, device); err != nil {
			ret = combineErrors(ret, err)
		}
	}

	if err = r.diskOps.RemoveAllPVsOnDevice(device); err != nil {
		ret = combineErrors(ret, err)
	}

	if err = r.diskOps.RemoveAllDMDevicesOnDisk(device); err != nil {
		ret = combineErrors(ret, err)
	}

	return ret
}

func (r *cleanupDevice) removeVolumeGroupsFromDevice(vgNames []string, device string) error {
	var ret error
	for _, vgName := range vgNames {
		r.log.Infof("A volume group (%s) was detected on the installation device (%s) - cleaning", vgName, device)
		err := r.diskOps.RemoveVG(vgName)

		if err != nil {
			r.log.Errorf("Could not delete volume group (%s) due to error: %v", vgName, err)
			ret = combineErrors(ret, err)
		}
	}
	return ret
}

func NewDiskOps(logger *logrus.Logger, executor Execute) DiskOps {
	return &diskOps{log: logger, executor: executor}
}

func (r *diskOps) IsRaidMember(device string) bool {
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

func (r *diskOps) CleanRaidMembership(device string) error {
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

func (r *diskOps) GetRaidDevices(deviceName string) ([]string, error) {
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

func (r *diskOps) getRaidDevices2Members() (map[string][]string, error) {
	output, err := r.executor.Execute("mdadm", "-v", "--query", "--detail", "--scan")

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

func (r *diskOps) removeDeviceFromRaidArray(deviceName string, raidDeviceName string, raidArrayMembers []string) error {
	raidStopped := false

	expression, _ := regexp.Compile(deviceName + "[\\d]*")

	for _, raidMember := range raidArrayMembers {
		// A partition or the device itself is part of the raid array.
		if expression.MatchString(raidMember) {
			// Stop the raid device.
			if !raidStopped {
				r.log.Info("Stopping raid device: " + raidDeviceName)
				_, err := r.executor.Execute("mdadm", "--stop", raidDeviceName)

				if err != nil {
					return err
				}

				raidStopped = true
			}

			// Clean the raid superblock from the device
			r.log.Infof("Cleaning raid member %s superblock", raidMember)
			_, err := r.executor.Execute("mdadm", "--zero-superblock", raidMember)

			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *diskOps) Wipefs(device string) error {
	_, err := r.executor.Execute("wipefs", "--all", "--force", device)

	if err != nil {
		_, err = r.executor.Execute("wipefs", "--all", device)
	}

	return err
}

func (r *diskOps) GetVolumeGroupsByDisk(diskName string) ([]string, error) {
	var vgs []string

	output, err := r.executor.Execute("vgs", "--noheadings", "-o", "vg_name,pv_name")
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

func (r *diskOps) getDiskPVs(diskName string) ([]string, error) {
	var pvs []string
	output, err := r.executor.Execute("pvs", "--noheadings", "-o", "pv_name")
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

func (r *diskOps) RemoveAllPVsOnDevice(diskName string) error {
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
			ret = combineErrors(ret, err)
		}
	}
	return ret
}

func (r *diskOps) getDMDevices(diskName string) ([]string, error) {
	var dmDevices []string
	output, err := r.executor.Execute("dmsetup", "ls")
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
		output, err = r.executor.Execute("dmsetup", "deps", "-o", "devname", dmDevice)
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

func (r *diskOps) RemoveDMDevice(dmDevice string) error {
	output, err := r.executor.Execute("dmsetup", "remove", "--retry", dmDevice)
	if err != nil {
		r.log.Errorf("Failed to remove DM device %s, output %s, error %s", dmDevice, output, err)
	}
	return err
}

func (r *diskOps) RemoveAllDMDevicesOnDisk(diskName string) error {
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
			ret = combineErrors(ret, err)
		}
	}
	return ret
}

func (r *diskOps) RemoveVG(vgName string) error {
	output, err := r.executor.Execute("vgremove", vgName, "-y")
	if err != nil {
		r.log.Errorf("Failed to remove VG %s, output %s, error %s", vgName, output, err)
	}
	return err
}

func (r *diskOps) RemovePV(pvName string) error {
	output, err := r.executor.Execute("pvremove", pvName, "-y", "-ff")
	if err != nil {
		r.log.Errorf("Failed to remove PV %s, output %s, error %s", pvName, output, err)
	}
	return err
}

func combineErrors(error1 error, error2 error) error {
	if error1 != nil {
		return errors.Wrapf(error1, "%s", error2)
	}
	return error2
}

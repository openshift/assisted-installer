package ops

import (
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

type DiskOps struct {
	log logrus.FieldLogger
	ops Ops
}

func NewDiskOps(logger logrus.FieldLogger, ops Ops) DiskOps {
	return DiskOps{log: logger, ops: ops}
}

func (r *DiskOps) CleanupInstallDevice(device string) error {

	var ret error
	r.log.Infof("Start cleaning up device %s", device)
	err := r.cleanupDevice(device)

	if err != nil {
		ret = utils.CombineErrors(ret, err)
	}

	if r.ops.IsRaidMember(device) {
		r.log.Infof("A raid was detected on the device (%s) - cleaning", device)
		var devices []string
		devices, err = r.ops.GetRaidDevices(device)

		if err != nil {
			ret = utils.CombineErrors(ret, err)
		}

		for _, raidDevice := range devices {
			// Cleaning the raid device itself before removing membership.
			err = r.cleanupDevice(raidDevice)

			if err != nil {
				ret = utils.CombineErrors(ret, err)
			}
		}

		err = r.ops.CleanRaidMembership(device)

		if err != nil {
			ret = utils.CombineErrors(ret, err)
		}
		r.log.Infof("Finished cleaning up device %s", device)
	}

	err = r.ops.Wipefs(device)
	if err != nil {
		ret = utils.CombineErrors(ret, err)
	}

	return ret
}

func (r *DiskOps) cleanupDevice(device string) error {
	var ret error
	vgNames, err := r.ops.GetVolumeGroupsByDisk(device)
	if err != nil {
		ret = err
	}

	if len(vgNames) > 0 {
		if err = r.RemoveVolumeGroupsFromDevice(vgNames, device); err != nil {
			ret = utils.CombineErrors(ret, err)
		}
	}

	if err = r.ops.RemoveAllPVsOnDevice(device); err != nil {
		ret = utils.CombineErrors(ret, err)
	}

	if err = r.ops.RemoveAllDMDevicesOnDisk(device); err != nil {
		ret = utils.CombineErrors(ret, err)
	}

	return ret
}

func (r *DiskOps) RemoveVolumeGroupsFromDevice(vgNames []string, device string) error {
	var ret error
	for _, vgName := range vgNames {
		r.log.Infof("A volume group (%s) was detected on the installation device (%s) - cleaning", vgName, device)
		err := r.ops.RemoveVG(vgName)

		if err != nil {
			r.log.Errorf("Could not delete volume group (%s) due to error: %w", vgName, err)
			ret = utils.CombineErrors(ret, err)
		}
	}
	return ret
}

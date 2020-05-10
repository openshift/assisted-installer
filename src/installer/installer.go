package installer

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/eranco74/assisted-installer/src/k8s_client"
	"golang.org/x/sync/errgroup"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

//const baseHref = "/api/bm-inventory/v1"
const (
	HostRoleMaster    = "master"
	HostRoleBootstrap = "bootstrap"
	InstallDir        = "/opt/install-dir"
	Kubeconfig        = "kubeconfig"
	// Change this to the MCD image from the relevant openshift release image
	minMasterNodes   = 2
	dockerConfigFile = "/root/.docker/config.json"
)

const (
	StartingInstallation = "Starting Installation"
	RunningBootstrap     = "Running bootstrap"
	WaitForControlPlane  = "Waiting for control plane"
	WritingImageToDisk   = "Writing image to disk"
	Reboot               = "Reboot"
)

// Installer will run the install operations on the node
type Installer interface {
	InstallNode() error
	UpdateHostStatus(newStatus string)
}

type installer struct {
	config.Config
	log *logrus.Logger
	ops ops.Ops
	ic  inventory_client.InventoryClient
	kc  k8s_client.K8SClient
}

func NewAssistedInstaller(log *logrus.Logger, cfg config.Config, ops ops.Ops, ic inventory_client.InventoryClient, kc k8s_client.K8SClient) *installer {
	return &installer{
		log:    log,
		Config: cfg,
		ops:    ops,
		ic:     ic,
		kc:     kc,
	}
}

func (i *installer) InstallNode() error {
	i.log.Infof("Installing node with role: %s", i.Config.Role)

	if !utils.IsOpenshiftVersionIsSupported(i.OpenshiftVersion) {
		err := fmt.Errorf("openshift version %s is not supported", i.OpenshiftVersion)
		i.log.Error(err)
		return err
	}

	i.UpdateHostStatus(StartingInstallation)
	if err := i.ops.Mkdir(InstallDir); err != nil {
		i.log.Errorf("Failed to create install dir: %s", err)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs, _ := errgroup.WithContext(ctx)
	//cancel the context in case this method ends
	defer cancel()
	if i.Config.Role == HostRoleBootstrap {
		errs.Go(func() error {
			return i.runBootstrap(ctx)
		})
		i.Config.Role = HostRoleMaster
	}

	i.UpdateHostStatus(fmt.Sprintf("Runing %s installation", i.Config.Role))
	ignitionFileName := i.Config.Role + ".ign"
	ignitionPath, err := i.getFileFromService(ignitionFileName)
	if err != nil {
		return err
	}

	i.UpdateHostStatus(WritingImageToDisk)

	image, _ := utils.GetRhcosImageByOpenshiftVersion(i.OpenshiftVersion)
	i.log.Infof("Going to use image: %s", image)
	// TODO report image to disk progress

	err = i.ops.WriteImageToDisk(ignitionPath, i.Device, image)
	if err != nil {
		i.log.Errorf("Failed to write image to disk %s", err)
		return err
	}
	if err = errs.Wait(); err != nil {
		i.log.Error(err)
		return err
	}
	i.UpdateHostStatus(Reboot)
	if err = i.ops.Reboot(); err != nil {
		return err
	}
	return nil
}

func (i *installer) runBootstrap(ctx context.Context) error {
	i.UpdateHostStatus(RunningBootstrap)
	err := i.startBootstrap()
	if err != nil {
		i.log.Errorf("Bootstrap failed %s", err)
		return err
	}
	i.UpdateHostStatus(WaitForControlPlane)
	if err = i.waitForControlPlane(ctx); err != nil {
		return err
	}
	i.log.Info("Setting bootstrap node new role to master")
	return nil
}

func (i *installer) startBootstrap() error {
	i.log.Infof("Running bootstrap")
	ignitionFileName := "bootstrap.ign"
	ignitionPath, err := i.getFileFromService(ignitionFileName)
	if err != nil {
		return err
	}

	// We need to extract pull secret from ignition and save it in docker config
	// to be able to pull MCO official image
	if err = i.ops.ExtractFromIgnition(ignitionPath, dockerConfigFile); err != nil {
		return err
	}

	mcoImage, _ := utils.GetMCOByOpenshiftVersion(i.OpenshiftVersion)
	i.log.Infof("Extracting ignition to disk using %s mcoImage", mcoImage)

	out, err := i.ops.ExecPrivilegeCommand("podman", "run", "--net", "host",
		"--volume", "/:/rootfs:rw",
		"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
		"--privileged",
		"--entrypoint", "/usr/bin/machine-config-daemon",
		mcoImage,
		"start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from", ignitionPath, "--skip-reboot")
	if err != nil {
		i.log.Errorf("Failed to extract ignition to disk")
		return err
	}
	i.log.Info(out)

	servicesToStart := []string{"bootkube.service", "approve-csr.service", "progress.service"}
	for _, service := range servicesToStart {
		i.log.Infof("Starting %s", service)
		_, err = i.ops.ExecPrivilegeCommand("systemctl", "start", service)
		if err != nil {
			i.log.Errorf("Failed to start service %s", service)
			return err
		}

	}
	i.log.Info("Done setting up bootstrap")
	return nil
}

func (i *installer) getFileFromService(filename string) (string, error) {
	i.log.Infof("Getting %s file", filename)
	dest := filepath.Join(InstallDir, filename)
	err := i.ic.DownloadFile(filename, dest)
	if err != nil {
		i.log.Errorf("Failed to fetch file (%s) from server. err: %s", filename, err)
	}
	return dest, err
}

func (i *installer) waitForControlPlane(ctx context.Context) error {
	if err := i.kc.WaitForMasterNodes(ctx, minMasterNodes); err != nil {
		i.log.Errorf("Timeout waiting for master nodes, %s", err)
		return err
	}
	if err := i.kc.PatchEtcd(); err != nil {
		i.log.Error(err)
		return err
	}
	i.waitForBootkube(ctx)
	return nil
}

func (i *installer) UpdateHostStatus(newStatus string) {
	i.log.Infof("Updating node installation status: %s", newStatus)
	if i.HostID != "" {
		if err := i.ic.UpdateHostStatus(newStatus); err != nil {
			i.log.Errorf("Failed to update node installation status, %s", err)
		}
	}
}

func (i *installer) waitForBootkube(ctx context.Context) {
	i.log.Infof("Waiting for bootkube to complete")
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("Context cancelled, terminting wait for bootkube\n")
			return
		case <-time.After(time.Second * time.Duration(5)):
			// check if bootkube is done every 5 seconds
			out, _ := i.ops.ExecPrivilegeCommand("bash", "-c", "systemctl status bootkube.service | grep 'bootkube.service: Succeeded' | wc -l")
			if out == "1" {
				// in case bootkube is done log the status and return
				i.log.Info("bootkube service completed")
				out, _ := i.ops.ExecPrivilegeCommand("systemctl", "status", "bootkube.service")
				i.log.Info(out)
				return
			}
		}
	}
}

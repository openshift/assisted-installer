package installer

import (
	"path/filepath"

	"github.com/eranco74/assisted-installer/src/k8s_client"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/sirupsen/logrus"
)

//const baseHref = "/api/bm-inventory/v1"
const (
	HostRoleMaster    = "master"
	HostRoleBootstrap = "bootstrap"
	InstallDir        = "/opt/install-dir"
	Kubeconfig        = "kubeconfig"
	// Change this to the MCD image from the relevant openshift release image
	machineConfigImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:301586e92bbd07ead7c5d3f342899e5923d4ef2e0f1c0cf08ecaae96568d16ed"
	minMasterNodes     = 2
	dockerConfigFile   = "/root/.docker/config.json"
)

// Installer will run the install operations on the node
type Installer interface {
	InstallNode() error
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
	if err := i.ops.Mkdir(InstallDir); err != nil {
		i.log.Errorf("Failed to create install dir: %s", err)
		return err
	}
	if i.Config.Role == HostRoleBootstrap {
		err := i.runBootstrap()
		if err != nil {
			i.log.Errorf("Bootstrap failed %s", err)
			return err
		}
		if err = i.waitForControlPlane(); err != nil {
			return err
		}
		i.log.Info("Setting bootstrap node new role to master")
		i.Config.Role = HostRoleMaster
	}

	ignitionFileName := i.Config.Role + ".ign"
	ignitionPath, err := i.getFileFromService(ignitionFileName)
	if err != nil {
		return err
	}
	err = i.ops.WriteImageToDisk(ignitionPath, i.Device)
	if err != nil {
		i.log.Errorf("Failed to write image to disk %s", err)
		return err
	}
	if err = i.ops.Reboot(); err != nil {
		return err
	}
	i.updateNodeStatus("Installed", "")
	return nil
}

func (i *installer) runBootstrap() error {
	i.log.Infof("Installing node with role: %s", i.Config.Role)
	ignitionFileName := i.Config.Role + ".ign"
	ignitionPath, err := i.getFileFromService(ignitionFileName)
	if err != nil {
		return err
	}

	// We need to extract pull secret from ignition and save it in docker config
	// to be able to pull MCO official image
	if err = i.ops.ExtractFromIgnition(ignitionPath, dockerConfigFile); err != nil {
		return err
	}

	i.log.Infof("Extracting ignition to disk")
	out, err := i.ops.ExecPrivilegeCommand("podman", "run", "--net", "host",
		"--volume", "/:/rootfs:rw",
		"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
		"--privileged",
		"--entrypoint", "/usr/bin/machine-config-daemon",
		machineConfigImage,
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

func (i *installer) waitForControlPlane() error {
	if err := i.kc.WaitForMasterNodes(minMasterNodes); err != nil {
		i.log.Errorf("Timeout waiting for master nodes, %s", err)
		return err
	}

	kubeconfigPath := filepath.Join(InstallDir, Kubeconfig)
	i.patchEtcd(kubeconfigPath)
	i.waitForBootkube()
	return nil
}

func (i *installer) updateNodeStatus(newStatus string, reason string) {
	i.log.Infof("Updating node installation status, status: %s, reason: %s", newStatus, reason)
	//TODO: add the API call
}

func (i *installer) patchEtcd(kubeconfigPath string) {
	//TODO: Change this method to use k8s client
	i.log.Info("Patching etcd")
	for {
		out, err := i.ops.ExecPrivilegeCommand("oc", "--kubeconfig", kubeconfigPath, "patch", "etcd",
			"cluster", "-p", `{"spec": {"unsupportedConfigOverrides": {"useUnsupportedUnsafeNonHANonProductionUnstableEtcd": true}}}`, "--type", "merge")
		if err == nil {
			i.log.Info(out)
			break
		} else {
			i.log.Infof("Failed to patch etcd: %s", out)
		}
	}
}

func (i *installer) waitForBootkube() {
	//TODO: Change this method to use k8s client
	i.log.Infof("Waiting for bootkube to complete")
	for {
		out, _ := i.ops.ExecPrivilegeCommand("bash", "-c", "systemctl status bootkube.service | grep 'bootkube.service: Succeeded' | wc -l")
		if out == "1" {
			break
		}
	}
	i.log.Info("bootkube service completed")
	out, _ := i.ops.ExecPrivilegeCommand("systemctl", "status", "bootkube.service")
	i.log.Info(out)
}

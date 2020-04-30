package installer

import (
	"fmt"
	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/sirupsen/logrus"
	"path/filepath"
	"time"
)

//const baseHref = "/api/bm-inventory/v1"
const (
	master          = "master"
	bootstrap       = "bootstrap"
	installDir     = "/opt/install-dir"
	kubeconfig = "kubeconfig"
	// Change this to the MCD image from the relevant openshift release image
	machineConfigImage = "docker.io/eranco/mcd:latest"
)

// Installer will run the install operations on the node
type Installer interface {
	InstallNode() error
}

type installer struct {
	config.Config
	log *logrus.Logger
	ops ops.Ops
	ic inventory_client.InventoryClient
}

func NewAssistedInstaller(log *logrus.Logger, cfg config.Config, ops ops.Ops, ic inventory_client.InventoryClient) *installer {
	return &installer{
		log:    log,
		Config: cfg,
		ops:    ops,
		ic:    ic,
	}
}

func (i *installer) InstallNode() error {
	if err := i.ops.Mkdir(installDir); err != nil {
		i.log.Errorf("Failed to create install dir: %s", err)
		return err
	}
	if i.Config.Role == bootstrap {
		err := i.runBootstrap()
		if err != nil {
			i.log.Errorf("Bootstrap failed %s", err)
			return err
		}
		if err = i.waitForControlPlane(); err != nil {
			return err
		}
		i.log.Info("Setting bootstrap node new role to master")
		i.Config.Role = master
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
	if err = i.ops.Reboot(); err != nil{
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
	i.log.Infof("Extracting ignition to disk")
	out, err := i.ops.ExecPrivilegeCommand("podman", "run", "--net", "host",
		"--volume", "/:/rootfs:rw",
		"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
		"--privileged",
		"--entrypoint", "/machine-config-daemon",
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
	dest := filepath.Join(installDir, filename)
	err := i.ic.DownloadFile(filename, dest)
	if err !=  nil {
		i.log.Errorf("Failed to fetch file (%s) from server. err: %s", filename, err)
	}
	return dest, err
}

func (i *installer) waitForControlPlane() error{
	kubeconfigPath, err := i.getFileFromService(kubeconfig)
	if err != nil {
		return err
	}
	i.waitForMasterNodes(kubeconfigPath)
	i.patchEtcd(kubeconfigPath)
	i.waitForReadyMasterNodes(kubeconfigPath)
	i.waitForBootkube()
	return nil
}

func (i *installer) updateNodeStatus(newStatus string, reason string) {
	i.log.Infof("Updating node installation status, status: %s, reason: %s", newStatus, reason)
	//TODO: add the API call
}

func (i *installer) waitForMasterNodes(kubeconfigPath string) {
	i.log.Info("Waiting for 2 master nodes")
	for ok := true; ok; {
		out, _ := i.ops.ExecPrivilegeCommand("bash", "-c", fmt.Sprintf("/usr/bin/kubectl --kubeconfig %s get nodes | grep master | wc -l", kubeconfigPath))
		if out == "2" {
			break
		}
		time.Sleep(time.Second * 5)
	}
	i.log.Info("Got 2 master nodes")
	out, _ := i.ops.ExecPrivilegeCommand("kubectl", "--kubeconfig", kubeconfigPath, "get", "nodes")
	i.log.Info(out)
}

func (i *installer) waitForReadyMasterNodes(kubeconfigPath string) {
	i.log.Info("Waiting for 2 ready master nodes")
	for ok := true; ok; {
		out, _ := i.ops.ExecPrivilegeCommand("bash", "-c", fmt.Sprintf("/usr/bin/kubectl --kubeconfig %s get nodes | grep master | grep -v NotReady | grep Ready | wc -l", kubeconfigPath))
		if out == "2" {
			break
		}
		time.Sleep(time.Second * 5)

	}
	i.log.Info("Got 2 master nodes")
	out, _ := i.ops.ExecPrivilegeCommand("kubectl", "--kubeconfig", kubeconfigPath, "get", "nodes")
	i.log.Info(out)
}

func (i *installer) patchEtcd(kubeconfigPath string) {
	i.log.Info("Patching etcd")
	for ok := true; ok; {
		out, err := i.ops.ExecPrivilegeCommand("until", "oc", "--kubeconfig", kubeconfigPath, "patch", "etcd",
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
	i.log.Infof("Waiting for bootkube to complete")
	for ok := true; ok; {
		out, _ := i.ops.ExecPrivilegeCommand("bash", "-c", "systemctl status bootkube.service | grep 'bootkube.service: Succeeded' | wc -l")
		if out == "1" {
			break
		}
	}
	i.log.Info("bootkube service completed")
	out, _ := i.ops.ExecPrivilegeCommand("systemctl", "status", "bootkube.service")
	i.log.Info(out)
}

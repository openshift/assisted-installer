package installer

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thoas/go-funk"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

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
	KubeconfigPath    = "/opt/openshift/auth/kubeconfig-loopback"
	// Change this to the MCD image from the relevant openshift release image
	minMasterNodes   = 2
	dockerConfigFile = "/root/.docker/config.json"
)

const (
	StartingInstallation = "Starting installation"
	InstallingAs         = "Installing as %s"
	RunningBootstrap     = "Bootstrapping installation"
	WaitForControlPlane  = "Waiting for control plane"
	WritingImageToDisk   = "Writing image to disk"
	Reboot               = "Rebooting"
	Joined               = "Joined"
	Configuring          = "Configuring"
)

var generalWaitTimeout = 30 * time.Second

// Installer will run the install operations on the node
type Installer interface {
	InstallNode() error
	UpdateHostStatus(newStatus string)
}

type installer struct {
	config.Config
	log             *logrus.Logger
	ops             ops.Ops
	inventoryClient inventory_client.InventoryClient
	kcBuilder       k8s_client.K8SClientBuilder
}

func NewAssistedInstaller(log *logrus.Logger, cfg config.Config, ops ops.Ops, ic inventory_client.InventoryClient, kcb k8s_client.K8SClientBuilder) *installer {
	return &installer{
		log:             log,
		Config:          cfg,
		ops:             ops,
		inventoryClient: ic,
		kcBuilder:       kcb,
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

	i.log.Infof("Start cleaning up device %s", i.Device)
	err := i.cleanupInstallDevice()
	if err != nil {
		i.log.Errorf("failed to prepare install device %s, err %s", i.Device, err)
		return err
	}
	i.log.Infof("Fnished cleaning up device %s", i.Device)

	if err = i.ops.Mkdir(InstallDir); err != nil {
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

	i.UpdateHostStatus(fmt.Sprintf(InstallingAs, i.Config.Role))
	ignitionFileName := i.Config.Role + ".ign"
	ignitionPath, err := i.getFileFromService(ignitionFileName)
	if err != nil {
		return err
	}

	i.UpdateHostStatus(WritingImageToDisk)

	image, _ := utils.GetRhcosImageByOpenshiftVersion(i.OpenshiftVersion)
	i.log.Infof("Going to use image: %s", image)
	// TODO report image to disk progress

	err = utils.Retry(3, time.Second, i.log, func() error {
		return i.ops.WriteImageToDisk(ignitionPath, i.Device, image, i.inventoryClient)
	})
	if err != nil {
		i.log.Errorf("Failed to write image to disk %s", err)
		return err
	} else {
		i.log.Info("Done writing image to disk")
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
	done := make(chan bool)
	defer func() {
		done <- true
	}()
	go i.updateConfiguringStatus(done)
	err := i.startBootstrap()
	if err != nil {
		i.log.Errorf("Bootstrap failed %s", err)
		return err
	}
	kc, err := i.kcBuilder(KubeconfigPath, i.log)
	if err != nil {
		i.log.Error(err)
		return err
	}

	i.UpdateHostStatus(WaitForControlPlane)
	if err = i.waitForControlPlane(ctx, kc); err != nil {
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

	_, err = i.ops.ExecPrivilegeCommand(utils.NewLogWriter(i.log), "podman", "run", "--net", "host",
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
	i.log.Info("Done extracting ignition to filesystem")

	// reload systemd configurations from filesystem and regenerate dependency trees
	err = i.ops.SystemctlAction("daemon-reload")
	if err != nil {
		return err
	}

	// restart NetworkManager to trigger NetworkManager/dispatcher.d/30-local-dns-prepender
	err = i.ops.SystemctlAction("restart", "NetworkManager.service")
	if err != nil {
		i.log.Error(err)
		return err
	}

	if err = i.ops.PrepareController(); err != nil {
		i.log.Error(err)
		return err
	}

	servicesToStart := []string{"bootkube.service", "approve-csr.service", "progress.service"}
	for _, service := range servicesToStart {
		err = i.ops.SystemctlAction("start", service)
		if err != nil {
			return err
		}
	}
	i.log.Info("Done setting up bootstrap")
	return nil
}

func (i *installer) getFileFromService(filename string) (string, error) {
	i.log.Infof("Getting %s file", filename)
	dest := filepath.Join(InstallDir, filename)
	err := i.inventoryClient.DownloadFile(filename, dest)
	if err != nil {
		i.log.Errorf("Failed to fetch file (%s) from server. err: %s", filename, err)
	}
	return dest, err
}

func (i *installer) waitForControlPlane(ctx context.Context, kc k8s_client.K8SClient) error {
	if err := i.waitForMasterNodes(ctx, minMasterNodes, kc); err != nil {
		i.log.Errorf("Timeout waiting for master nodes, %s", err)
		return err
	}

	if err := kc.PatchEtcd(); err != nil {
		i.log.Error(err)
		return err
	}

	i.waitForBootkube(ctx)

	return nil
}

func (i *installer) UpdateHostStatus(newStatus string) {
	i.log.Infof("Updating node installation status: %s", newStatus)
	if i.HostID != "" {
		if err := i.inventoryClient.UpdateHostStatus(newStatus, i.HostID); err != nil {
			i.log.Errorf("Failed to update node installation status, %s", err)
		}
	}
}

func (i *installer) waitForBootkube(ctx context.Context) {
	i.log.Infof("Waiting for bootkube to complete")
	for {
		select {
		case <-ctx.Done():
			i.log.Info("Context cancelled, terminating wait for bootkube\n")
			return
		case <-time.After(time.Second * time.Duration(5)):
			// check if bootkube is done every 5 seconds
			if _, err := i.ops.ExecPrivilegeCommand(nil, "stat", "/opt/openshift/.bootkube.done"); err == nil {
				// in case bootkube is done log the status and return
				i.log.Info("bootkube service completed")
				out, _ := i.ops.ExecPrivilegeCommand(utils.NewLogWriter(i.log), "systemctl", "status", "bootkube.service")
				i.log.Info(out)
				return
			}
		}
	}
}

// wait for minimum master nodes to be in ready status
func (i *installer) waitForMasterNodes(ctx context.Context, minMasterNodes int, kc k8s_client.K8SClient) error {
	nodesTimeout := 120 * time.Minute
	var readyMasters []string
	var inventoryHostsMap map[string]string
	i.log.Infof("Waiting up to %v for %d master nodes", nodesTimeout, minMasterNodes)
	apiContext, cancel := context.WithTimeout(ctx, nodesTimeout)
	defer cancel()

	wait.Until(func() {
		inventoryHostsMap = i.getInventoryHostsMap(inventoryHostsMap)
		if inventoryHostsMap == nil {
			return
		}
		nodes, err := kc.ListMasterNodes()
		if err != nil {
			i.log.Warnf("Still waiting for master nodes: %v", err)
			return
		}
		i.updateReadyMasters(nodes, &readyMasters, inventoryHostsMap)
		i.log.Infof("Found %d ready master nodes", len(readyMasters))
		if len(readyMasters) >= minMasterNodes {
			i.log.Infof("Waiting for master nodes - Done")
			cancel()
		}
	}, 5*time.Second, apiContext.Done())
	err := apiContext.Err()
	if err != nil && err != context.Canceled {
		return errors.Wrap(err, "Waiting for master nodes")
	}
	return nil
}

func (i *installer) getInventoryHostsMap(hostsMap map[string]string) map[string]string {
	var err error
	if hostsMap == nil {
		hostsMap, err = i.inventoryClient.GetEnabledHostsNamesIds()
		if err != nil {
			i.log.Warnf("Failed to get hosts info from inventory, err %s", err)
			return nil
		}
	}
	return hostsMap
}

func (i *installer) updateReadyMasters(nodes *v1.NodeList, readyMasters *[]string, inventoryHostsMap map[string]string) {
	nodeNameAndCondition := map[string][]v1.NodeCondition{}
	for _, node := range nodes.Items {
		nodeNameAndCondition[node.Name] = node.Status.Conditions
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue &&
				!funk.ContainsString(*readyMasters, node.Status.NodeInfo.SystemUUID) {

				i.log.Infof("Found a new ready master node %s with id %s", node.Name, node.Status.NodeInfo.SystemUUID)
				*readyMasters = append(*readyMasters, node.Status.NodeInfo.SystemUUID)
				hostId, ok := inventoryHostsMap[node.Name]
				if !ok {
					i.log.Warnf("Node %s is not in inventory hosts", node.Name)
					break
				}
				if err := i.inventoryClient.UpdateHostStatus(Joined, hostId); err != nil {
					i.log.Errorf("Failed to update node installation status, %s", err)
				}
			}
		}
	}
	i.log.Infof("Found %d master nodes: %+v", len(nodes.Items), nodeNameAndCondition)
}

func (i *installer) cleanupInstallDevice() error {
	vgName, err := i.ops.GetVGByPV(i.Device)
	if err != nil {
		return err
	}

	if vgName == "" {
		i.log.Infof("No VG/LVM was found on device %s, no need to clean", i.Device)
		return nil
	}

	err = i.ops.RemoveVG(vgName)
	if err != nil {
		return err
	}

	return i.ops.RemovePV(i.Device)
}

func (i *installer) verifyHostCanMoveToConfigurationStatus(inventoryHostsMapWithIp map[string][]string) {
	logs, err := i.ops.GetMCSLogs()
	if err != nil {
		i.log.Infof("Failed to get MCS logs, will retry")
		return
	}
	for hostId, ips := range inventoryHostsMapWithIp {
		pat := fmt.Sprintf("(%s)", strings.Join(ips, "|"))
		pattern, err := regexp.Compile(pat)
		if err != nil {
			i.log.WithError(err).Errorf("Failed to compile regex from host %s ips list", hostId)
			return
		}

		if pattern.MatchString(logs) {
			i.log.Infof("Host %s found in mcs logs, moving it to %s state", hostId, Configuring)
			if err := i.inventoryClient.UpdateHostStatus(Configuring, hostId); err != nil {
				i.log.Errorf("Failed to update node installation status, %s", err)
				continue
			}
			delete(inventoryHostsMapWithIp, hostId)
		}
	}
}

// will run as go routine and tries to find nodes that pulled ignition from mcs
// it will get mcs logs of static pod that runs on bootstrap and will search for matched ip
// when match is found it will update inventory service with new host status
func (i *installer) updateConfiguringStatus(done <-chan bool) {
	i.log.Infof("Start waiting for configuring state")
	ticker := time.NewTicker(generalWaitTimeout)
	var inventoryHostsMapWithIp map[string][]string
	var err error
	for {
		select {
		case <-done:
			i.log.Infof("Exiting updateConfiguringStatus go routine")
			return
		case <-ticker.C:
			i.log.Infof("searching for hosts that had pulled ignition already")
			if inventoryHostsMapWithIp == nil {
				inventoryHostsMapWithIp, err = i.inventoryClient.GetEnabledIdsIps()
				if err != nil {
					i.log.Warnf("Failed to get hosts info from inventory, err %s", err)
					continue
				}
			}
			i.verifyHostCanMoveToConfigurationStatus(inventoryHostsMapWithIp)
		}
	}
}

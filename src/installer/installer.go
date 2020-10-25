package installer

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift/assisted-installer/src/common"

	"path/filepath"
	"time"

	"github.com/openshift/assisted-service/models"
	"github.com/thoas/go-funk"

	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	v1 "k8s.io/api/core/v1"

	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

const (
	InstallDir                  = "/opt/install-dir"
	KubeconfigPathLoopBack      = "/opt/openshift/auth/kubeconfig-loopback"
	KubeconfigPath              = "/opt/openshift/auth/kubeconfig"
	minMasterNodes              = 2
	dockerConfigFile            = "/root/.docker/config.json"
	assistedControllerPrefix    = "assisted-installer-controller"
	assistedControllerNamespace = "assisted-installer"
	extractRetryCount           = 3
)

var generalWaitTimeout = 30 * time.Second

// Installer will run the install operations on the node
type Installer interface {
	InstallNode() error
	UpdateHostInstallProgress(newStage models.HostStage, info string)
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

	i.UpdateHostInstallProgress(models.HostStageStartingInstallation, i.Config.Role)

	i.log.Infof("Start cleaning up device %s", i.Device)
	err := i.cleanupInstallDevice()
	if err != nil {
		i.log.Errorf("failed to prepare install device %s, err %s", i.Device, err)
		return err
	}
	i.log.Infof("Finished cleaning up device %s", i.Device)

	if err = i.ops.Mkdir(InstallDir); err != nil {
		i.log.Errorf("Failed to create install dir: %s", err)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs, _ := errgroup.WithContext(ctx)
	//cancel the context in case this method ends
	defer cancel()
	isBootstrap := false
	if i.Config.Role == string(models.HostRoleBootstrap) {
		isBootstrap = true
		errs.Go(func() error {
			return i.runBootstrap(ctx)
		})
		i.Config.Role = string(models.HostRoleMaster)
	}

	i.UpdateHostInstallProgress(models.HostStageInstalling, i.Config.Role)
	ignitionFileName := i.Config.Role + ".ign"
	ignitionPath, err := i.getFileFromService(ignitionFileName)
	if err != nil {
		return err
	}

	err = i.setHostnameInIgnition(ignitionPath)
	if err != nil {
		i.log.Errorf("Failed to set hostname %s in ignition %s", i.Hostname, ignitionPath)
		return err
	}

	i.UpdateHostInstallProgress(models.HostStageWritingImageToDisk, "")

	err = utils.Retry(3, time.Second, i.log, func() error {
		return i.ops.WriteImageToDisk(ignitionPath, i.Device, i.inventoryClient)
	})
	if err != nil {
		i.log.Errorf("Failed to write image to disk %s", err)
		return err
	} else {
		i.log.Info("Done writing image to disk")
	}
	if isBootstrap {
		i.UpdateHostInstallProgress(models.HostStageWaitingForControlPlane, "")
		if err = errs.Wait(); err != nil {
			i.log.Error(err)
			return err
		}
	}
	i.UpdateHostInstallProgress(models.HostStageRebooting, "")
	_, err = i.ops.UploadInstallationLogs(isBootstrap)
	if err != nil {
		i.log.Errorf("upload installation logs %s", err)
	}
	if err = i.ops.Reboot(); err != nil {
		return err
	}
	return nil
}

func (i *installer) runBootstrap(ctx context.Context) error {
	ctxLocal, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
	}()
	go i.updateConfiguringStatus(ctxLocal)

	err := i.startBootstrap()
	if err != nil {
		i.log.Errorf("Bootstrap failed %s", err)
		return err
	}
	kc, err := i.kcBuilder(KubeconfigPathLoopBack, i.log)
	if err != nil {
		i.log.Error(err)
		return err
	}
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

	err = i.extractIgnitionToFS(ignitionPath)
	if err != nil {
		return err
	}

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

func (i *installer) extractIgnitionToFS(ignitionPath string) (err error) {
	mcoImage, _ := utils.GetMCOByOpenshiftVersion(i.OpenshiftVersion)
	i.log.Infof("Extracting ignition to disk using %s mcoImage", mcoImage)
	for j := 0; j < extractRetryCount; j++ {
		_, err = i.ops.ExecPrivilegeCommand(utils.NewLogWriter(i.log), "podman", "run", "--net", "host",
			"--volume", "/:/rootfs:rw",
			"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
			"--privileged",
			"--entrypoint", "/usr/bin/machine-config-daemon",
			mcoImage,
			"start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from", ignitionPath, "--skip-reboot")
		if err != nil {
			i.log.Errorf("Failed to extract ignition to disk")
		} else {
			i.log.Info("Done extracting ignition to filesystem")
			return nil
		}
	}
	i.log.Errorf("Failed to extract ignition to disk, giving up")
	return err
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
	i.waitForMasterNodes(ctx, minMasterNodes, kc)

	if err := kc.PatchEtcd(); err != nil {
		i.log.Error(err)
		return err
	}

	i.waitForBootkube(ctx)

	// waiting for controller pod to be running
	if err := i.waitForController(); err != nil {
		i.log.Error(err)
		return err
	}

	return nil
}

func (i *installer) UpdateHostInstallProgress(newStage models.HostStage, info string) {
	i.log.Infof("Updating node installation stage: %s - %s", newStage, info)
	if i.HostID != "" {
		if err := i.inventoryClient.UpdateHostInstallProgress(i.HostID, newStage, info); err != nil {
			i.log.Errorf("Failed to update node installation stage, %s", err)
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

func (i *installer) waitForController() error {
	i.log.Infof("Waiting for controller pod to start running")
	err := i.ops.ReloadHostFile("/etc/resolv.conf")
	if err != nil {
		i.log.WithError(err).Error("Failed to reload resolv.conf")
		return err
	}
	kc, err := i.kcBuilder(KubeconfigPath, i.log)
	if err != nil {
		i.log.WithError(err).Errorf("Failed to create kc client from %s", KubeconfigPath)
		return err
	}
	predicate := func() bool {
		controllerPod := common.GetPodInStatus(kc, assistedControllerPrefix, assistedControllerNamespace,
			map[string]string{"job-name": assistedControllerPrefix}, v1.PodRunning, i.log)
		if controllerPod != nil {
			// uploading logs here cause we will finish installing bootstrap in couple of seconds
			// just didn't wanted to start handling this logic in other place
			// controller must be running for at least couple of minutes before this code so it will cover 90% of the cases
			// if we will see that it is not enough, we will change the place.
			err = common.UploadPodLogs(kc, i.inventoryClient, i.ClusterID, controllerPod.Name, assistedControllerNamespace, common.ControllerLogsSecondsAgo, i.log)
			// if failed to upload logs, log why and continue
			if err != nil {
				i.log.WithError(err).Warnf("Failed to upload controller logs")
			}
		}
		return controllerPod != nil
	}
	err = utils.WaitForPredicate(5*time.Minute, 5*time.Second, predicate)
	if err != nil {
		return errors.Errorf("Timeout while waiting for controller pod to be running")
	}
	return nil
}

// wait for minimum master nodes to be in ready status
func (i *installer) waitForMasterNodes(ctx context.Context, minMasterNodes int, kc k8s_client.K8SClient) {

	var readyMasters []string
	var inventoryHostsMap map[string]inventory_client.HostData
	i.log.Infof("Waiting for %d master nodes", minMasterNodes)
	sufficientMasterNodes := func() bool {
		var err error
		inventoryHostsMap, err = i.getInventoryHostsMap(inventoryHostsMap)
		if err != nil {
			return false
		}
		nodes, err := kc.ListMasterNodes()
		if err != nil {
			i.log.Warnf("Still waiting for master nodes: %v", err)
			return false
		}
		i.updateReadyMasters(nodes, &readyMasters, inventoryHostsMap)
		i.log.Infof("Found %d ready master nodes", len(readyMasters))
		if len(readyMasters) >= minMasterNodes {
			i.log.Infof("Waiting for master nodes - Done")
			return true
		}
		return false
	}

	for {
		select {
		case <-ctx.Done():
			i.log.Info("Context cancelled, terminating wait for master nodes\n")
			return
		case <-time.After(time.Second * time.Duration(5)):
			// check if we have sufficient master nodes is done every 5 seconds
			if sufficientMasterNodes() {
				return
			}
		}
	}
}

func (i *installer) getInventoryHostsMap(hostsMap map[string]inventory_client.HostData) (map[string]inventory_client.HostData, error) {
	var err error
	if hostsMap == nil {
		hostsMap, err = i.inventoryClient.GetEnabledHostsNamesHosts()
		if err != nil {
			i.log.Warnf("Failed to get hosts info from inventory, err %s", err)
			return nil, err
		}
		// no need for current host
		delete(hostsMap, i.Hostname)
	}
	return hostsMap, nil
}

func (i *installer) updateReadyMasters(nodes *v1.NodeList, readyMasters *[]string, inventoryHostsMap map[string]inventory_client.HostData) {
	nodeNameAndCondition := map[string][]v1.NodeCondition{}
	for _, node := range nodes.Items {
		nodeNameAndCondition[node.Name] = node.Status.Conditions
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue &&
				!funk.ContainsString(*readyMasters, node.Status.NodeInfo.SystemUUID) {

				i.log.Infof("Found a new ready master node %s with id %s", node.Name, node.Status.NodeInfo.SystemUUID)
				*readyMasters = append(*readyMasters, node.Status.NodeInfo.SystemUUID)
				host, ok := inventoryHostsMap[strings.ToLower(node.Name)]
				if !ok {
					i.log.Warnf("Node %s is not in inventory hosts", node.Name)
					break
				}
				if err := i.inventoryClient.UpdateHostInstallProgress(host.Host.ID.String(), models.HostStageJoined, ""); err != nil {
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

func (i *installer) verifyHostCanMoveToConfigurationStatus(inventoryHostsMapWithIp map[string]inventory_client.HostData) {
	logs, err := i.ops.GetMCSLogs()
	if err != nil {
		i.log.Infof("Failed to get MCS logs, will retry")
		return
	}
	common.SetConfiguringStatusForHosts(i.inventoryClient, inventoryHostsMapWithIp, logs, true, i.log)
}

func (i *installer) filterAlreadyUpdatedHosts(inventoryHostsMapWithIp map[string]inventory_client.HostData) {
	statesToFilter := map[models.HostStage]struct{}{models.HostStageConfiguring: {}, models.HostStageJoined: {},
		models.HostStageDone: {}, models.HostStageWaitingForIgnition: {}}
	for name, host := range inventoryHostsMapWithIp {
		fmt.Println(name, host.Host.Progress.CurrentStage)
		_, ok := statesToFilter[host.Host.Progress.CurrentStage]
		if ok {
			delete(inventoryHostsMapWithIp, name)
		}
	}
}

// will run as go routine and tries to find nodes that pulled ignition from mcs
// it will get mcs logs of static pod that runs on bootstrap and will search for matched ip
// when match is found it will update inventory service with new host status
func (i *installer) updateConfiguringStatus(ctx context.Context) {
	i.log.Infof("Start waiting for configuring state")
	ticker := time.NewTicker(generalWaitTimeout)
	var inventoryHostsMapWithIp map[string]inventory_client.HostData
	var err error
	for {
		select {
		case <-ctx.Done():
			i.log.Infof("Exiting updateConfiguringStatus go routine")
			return
		case <-ticker.C:
			i.log.Infof("searching for hosts that pulled ignition already")
			inventoryHostsMapWithIp, err = i.getInventoryHostsMap(inventoryHostsMapWithIp)
			if err != nil {
				continue
			}
			i.verifyHostCanMoveToConfigurationStatus(inventoryHostsMapWithIp)
			i.filterAlreadyUpdatedHosts(inventoryHostsMapWithIp)
			if len(inventoryHostsMapWithIp) == 0 {
				i.log.Infof("Exiting updateConfiguringStatus go routine")
				return
			}
		}
	}
}

func (i *installer) setHostnameInIgnition(ignitionPath string) error {
	if i.Hostname == "" {
		i.log.Infof("No hostname to set, continuing")
		return nil
	}
	return i.ops.SetFileInIgnition(ignitionPath, "/etc/hostname", fmt.Sprintf("data:,%s", i.Hostname), 420)
}

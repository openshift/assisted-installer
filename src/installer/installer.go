package installer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
	"golang.org/x/sync/errgroup"
	v1 "k8s.io/api/core/v1"

	config31types "github.com/coreos/ignition/v2/config/v3_1/types"

	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/ignition"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
)

const (
	InstallDir                   = "/opt/install-dir"
	KubeconfigPathLoopBack       = "/opt/openshift/auth/kubeconfig-loopback"
	KubeconfigPath               = "/opt/openshift/auth/kubeconfig"
	minMasterNodes               = 2
	dockerConfigFile             = "/root/.docker/config.json"
	assistedControllerPrefix     = "assisted-installer-controller"
	assistedControllerNamespace  = "assisted-installer"
	extractRetryCount            = 3
	waitForeverTimeout           = time.Duration(1<<63 - 1) // wait forever ~ 292 years
	ovnKubernetes                = "OVNKubernetes"
	numMasterNodes               = 3
	singleNodeMasterIgnitionPath = "/opt/openshift/master.ign"
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
	ign             ignition.Ignition
}

func NewAssistedInstaller(log *logrus.Logger, cfg config.Config, ops ops.Ops, ic inventory_client.InventoryClient, kcb k8s_client.K8SClientBuilder, ign ignition.Ignition) *installer {
	return &installer{
		log:             log,
		Config:          cfg,
		ops:             ops,
		inventoryClient: ic,
		kcBuilder:       kcb,
		ign:             ign,
	}
}

func (i *installer) InstallNode() error {
	i.log.Infof("Installing node with role: %s", i.Config.Role)

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
	if i.Config.Role == string(models.HostRoleBootstrap) && i.HighAvailabilityMode != models.ClusterHighAvailabilityModeNone {
		isBootstrap = true
		errs.Go(func() error {
			return i.runBootstrap(ctx)
		})
		i.Config.Role = string(models.HostRoleMaster)
	}

	i.UpdateHostInstallProgress(models.HostStageInstalling, i.Config.Role)
	var ignitionPath string
	if i.HighAvailabilityMode == models.ClusterHighAvailabilityModeNone {
		i.log.Info("Installing single node openshift")
		ignitionPath, err = i.createSingleNodeMasterIgnition()
		if err != nil {
			return err
		}
	} else {
		ignitionPath, err = i.downloadHostIgnition()
		if err != nil {
			return err
		}

	}

	if err = i.writeImageToDisk(ignitionPath); err != nil {
		return err
	}
	if isBootstrap {
		i.UpdateHostInstallProgress(models.HostStageWaitingForControlPlane, "")
		if err = errs.Wait(); err != nil {
			i.log.Error(err)
			return err
		}
	}
	_, err = i.ops.UploadInstallationLogs(isBootstrap || i.HighAvailabilityMode == models.ClusterHighAvailabilityModeNone)
	if err != nil {
		i.log.Errorf("upload installation logs %s", err)
	}
	i.UpdateHostInstallProgress(models.HostStageRebooting, "")
	if err = i.ops.Reboot(); err != nil {
		return err
	}
	return nil
}

//updateSingleNodeIgnition will download the host ignition config and add the files under storage
func (i *installer) updateSingleNodeIgnition(singleNodeIgnitionPath string) error {
	hostIgnitionPath, err := i.downloadHostIgnition()
	if err != nil {
		return err
	}
	fmt.Println(i.ign)
	singleNodeconfig, err := i.ign.ParseIgnitionFile(singleNodeIgnitionPath)
	if err != nil {
		return err
	}
	hostConfig, err := i.ign.ParseIgnitionFile(hostIgnitionPath)
	if err != nil {
		return err
	}
	// TODO: update this once we can get the full host specific overrides we have in the ignition
	// Remove the Config part since we only want the rest of the overrides
	hostConfig.Ignition.Config = config31types.IgnitionConfig{}
	merged, mergeErr := i.ign.MergeIgnitionConfig(singleNodeconfig, hostConfig)
	if mergeErr != nil {
		return errors.Wrapf(mergeErr, "failed to apply host ignition config overrides")
	}
	err = i.ign.WriteIgnitionFile(singleNodeIgnitionPath, merged)
	if err != nil {
		return err
	}
	return nil
}

func (i *installer) writeImageToDisk(ignitionPath string) error {
	i.UpdateHostInstallProgress(models.HostStageWritingImageToDisk, "")
	interval := time.Second
	err := utils.Retry(3, interval, i.log, func() error {
		return i.ops.WriteImageToDisk(ignitionPath, i.Device, i.inventoryClient, i.Config.InstallerArgs)
	})
	if err != nil {
		i.log.Errorf("Failed to write image to disk %s", err)
		return err
	}
	i.log.Info("Done writing image to disk")
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
	servicesToStart := []string{"bootkube.service", "approve-csr.service", "progress.service"}
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

	if i.HighAvailabilityMode != models.ClusterHighAvailabilityModeNone {
		err = i.generateSshKeyPair()
		if err != nil {
			return err
		}
		err = i.ops.CreateOpenshiftSshManifest(assistedInstallerSshManifest, sshManifestTmpl, sshPubKeyPath)
		if err != nil {
			return err
		}
	} else {
		//TODO for removing this once SNO is supported
		servicesToStart = append(servicesToStart, "patch.service")
	}

	// reload systemd configurations from filesystem and regenerate dependency trees
	err = i.ops.SystemctlAction("daemon-reload")
	if err != nil {
		return err
	}

	/* in case hostname is localhost, we need to set hostname to some random value, in order to
	   disable network manager activating hostname service, which will in turn may reset /etc/resolv.conf and
	   remove the work done by 30-local-dns-prepender. This will cause DNS issue in bootkube and it will fail to complete
	   successfully
	*/
	err = i.checkLocalhostName()
	if err != nil {
		i.log.Error(err)
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
	mcoImage := i.MCOImage

	i.log.Infof("Extracting ignition to disk using %s mcoImage", mcoImage)
	for j := 0; j < extractRetryCount; j++ {
		_, err = i.ops.ExecPrivilegeCommand(utils.NewLogWriter(i.log), "podman", "run", "--net", "host",
			"--pid=host",
			"--volume", "/:/rootfs:rw",
			"--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree",
			"--privileged",
			"--entrypoint", "/usr/bin/machine-config-daemon",
			mcoImage,
			"start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from", ignitionPath, "--skip-reboot")
		if err != nil {
			i.log.WithError(err).Error("Failed to extract ignition to disk")
		} else {
			i.log.Info("Done extracting ignition to filesystem")
			return nil
		}
	}
	i.log.Errorf("Failed to extract ignition to disk, giving up")
	return err
}

func (i *installer) generateSshKeyPair() error {
	i.log.Info("Generating new SSH key pair")
	if err := i.ops.Mkdir(sshDir); err != nil {
		i.log.WithError(err).Error("Failed to create SSH dir")
		return err
	}
	if _, err := i.ops.ExecPrivilegeCommand(utils.NewLogWriter(i.log), "ssh-keygen", "-q", "-f", sshKeyPath, "-N", ""); err != nil {
		i.log.WithError(err).Error("Failed to generate SSH key pair")
		return err
	}
	return nil
}

func (i *installer) getFileFromService(filename string) (string, error) {
	ctx := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctx, i.log)
	log.Infof("Getting %s file", filename)
	dest := filepath.Join(InstallDir, filename)
	err := i.inventoryClient.DownloadFile(ctx, filename, dest)
	if err != nil {
		log.Errorf("Failed to fetch file (%s) from server. err: %s", filename, err)
	}
	return dest, err
}

func (i *installer) downloadHostIgnition() (string, error) {
	ctx := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctx, i.log)
	filename := fmt.Sprintf("%s-%s.ign", i.Config.Role, i.Config.HostID)
	log.Infof("Getting %s file", filename)

	dest := filepath.Join(InstallDir, filename)
	err := i.inventoryClient.DownloadHostIgnition(ctx, i.Config.HostID, dest)
	if err != nil {
		log.Errorf("Failed to fetch file (%s) from server. err: %s", filename, err)
	}
	return dest, err
}

func (i *installer) waitForNetworkType(kc k8s_client.K8SClient) error {
	return utils.WaitForPredicate(waitForeverTimeout, 5*time.Second, func() bool {
		_, err := kc.GetNetworkType()
		if err != nil {
			i.log.WithError(err).Error("Failed to get network type")
		}
		return err == nil
	})
}

func (i *installer) waitForControlPlane(ctx context.Context, kc k8s_client.K8SClient) error {
	if err := i.waitForMinMasterNodes(ctx, kc); err != nil {
		return err
	}

	patch, err := utils.EtcdPatchRequired(i.Config.OpenshiftVersion)
	if err != nil {
		i.log.Error(err)
		return err
	}
	if patch {
		if err := kc.PatchEtcd(); err != nil {
			i.log.Error(err)
			return err
		}
	} else {
		i.log.Infof("Skipping etcd patch for cluster version %s", i.Config.OpenshiftVersion)
	}

	i.waitForBootkube(ctx)

	// waiting for controller pod to be running
	if err := i.waitForController(); err != nil {
		i.log.Error(err)
		return err
	}

	return nil
}

func (i *installer) waitForMinMasterNodes(ctx context.Context, kc k8s_client.K8SClient) error {
	if err := i.waitForNetworkType(kc); err != nil {
		i.log.WithError(err).Error("failed to wait for network type")
		return err
	}
	nt, err := kc.GetNetworkType()
	if err != nil {
		i.log.WithError(err).Error("failed to get network type")
		return err
	}
	var origControlPlaneReplicas int
	if nt == ovnKubernetes {
		// OVNKubernetes waits the number that is defined in controlPlane.Replicas to be
		// available before starting OVN.  Since assisted installer has a bootstrap node
		// that later becomes a master, there is a need to patch the install-config to
		// set the controlPlane.replicas to 2 until all masters which are not the
		// bootstrap are ready.
		// On single node this is not the case since bootstrap in place is used.
		// Therefore, the patch is not relevant to single node.
		if origControlPlaneReplicas, err = kc.GetControlPlaneReplicas(); err != nil {
			i.log.WithError(err).Error("Failed to get control plane replicas")
			return err
		}
		if origControlPlaneReplicas == numMasterNodes {
			if err = kc.PatchControlPlaneReplicas(); err != nil {
				i.log.WithError(err).Error("Failed to patch control plane replicas")
				return err
			}
		}
	}

	i.waitForMasterNodes(ctx, minMasterNodes, kc)
	if nt == ovnKubernetes && origControlPlaneReplicas == numMasterNodes {
		if err = kc.UnPatchControlPlaneReplicas(); err != nil {
			i.log.WithError(err).Error("Failed to unPatch control plane replicas")
			return err
		}
	}
	return nil
}

func (i *installer) UpdateHostInstallProgress(newStage models.HostStage, info string) {
	ctx := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctx, i.log)
	log.Infof("Updating node installation stage: %s - %s", newStage, info)
	if i.HostID != "" {
		if err := i.inventoryClient.UpdateHostInstallProgress(ctx, i.HostID, newStage, info); err != nil {
			log.Errorf("Failed to update node installation stage, %s", err)
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
	i.UpdateHostInstallProgress(models.HostStageWaitingForControlPlane, "waiting for controller pod")
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
	// wait forever
	err = utils.WaitForPredicate(waitForeverTimeout, 5*time.Second, predicate)
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
		ctx := utils.GenerateRequestContext()
		log := utils.RequestIDLogger(ctx, i.log)
		hostsMap, err = i.inventoryClient.GetEnabledHostsNamesHosts(ctx, log)
		if err != nil {
			log.Warnf("Failed to get hosts info from inventory, err %s", err)
			return nil, err
		}
		// no need for current host
		for name, hostData := range hostsMap {
			if hostData.Host.ID.String() == i.HostID {
				delete(hostsMap, name)
				break
			}
		}
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
				ctx := utils.GenerateRequestContext()
				log := utils.RequestIDLogger(ctx, i.log)
				log.Infof("Found a new ready master node %s with id %s", node.Name, node.Status.NodeInfo.SystemUUID)
				*readyMasters = append(*readyMasters, node.Status.NodeInfo.SystemUUID)
				host, ok := inventoryHostsMap[strings.ToLower(node.Name)]
				if !ok {
					log.Warnf("Node %s is not in inventory hosts", node.Name)
					break
				}
				ctx = utils.GenerateRequestContext()
				if err := i.inventoryClient.UpdateHostInstallProgress(ctx, host.Host.ID.String(), models.HostStageJoined, ""); err != nil {
					utils.RequestIDLogger(ctx, i.log).Errorf("Failed to update node installation status, %s", err)
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

	_ = i.ops.Wipefs(i.Device)

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

// createSingleNodeMasterIgnition will start the bootstrap flow and wait for bootkube
// when bootkube complete the single node master ignition will be under singleNodeMasterIgnitionPath
func (i *installer) createSingleNodeMasterIgnition() (string, error) {
	if err := i.startBootstrap(); err != nil {
		i.log.Errorf("Bootstrap failed %s", err)
		return "", err
	}
	i.waitForBootkube(context.Background())
	_, err := i.ops.ExecPrivilegeCommand(utils.NewLogWriter(i.log), "stat", singleNodeMasterIgnitionPath)
	if err != nil {
		i.log.Errorf("Failed to find single node master ignition: %s", err)
		return "", err
	}
	i.Config.Role = string(models.HostRoleMaster)
	err = i.updateSingleNodeIgnition(singleNodeMasterIgnitionPath)
	if err != nil {
		return "", err
	}

	return singleNodeMasterIgnitionPath, nil
}

func (i *installer) checkLocalhostName() error {
	i.log.Infof("Start checking localhostname")
	hostname, err := i.ops.GetHostname()
	if err != nil {
		i.log.Errorf("Failed to get hostname from kernel, err %s157", err)
		return err
	}
	if hostname != "localhost" {
		i.log.Infof("hostname is not localhost, no need to do anything")
		return nil
	}

	data := fmt.Sprintf("random-hostname-%s", uuid.New().String())
	i.log.Infof("write data into /etc/hostname")
	return i.ops.CreateRandomHostname(data)
}

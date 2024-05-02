package installer

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	"golang.org/x/sync/errgroup"
	v1 "k8s.io/api/core/v1"

	"github.com/go-openapi/swag"
	"github.com/google/uuid"
	"github.com/openshift/assisted-installer/src/main/drymock"
	"github.com/openshift/assisted-service/pkg/secretdump"
	"github.com/openshift/assisted-service/pkg/validations"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"

	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/coreos_logger"
	"github.com/openshift/assisted-installer/src/ignition"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	preinstallUtils "github.com/rh-ecosystem-edge/preinstall-utils/pkg"
)

// In dry run mode we prefer to get quick feedback about errors rather
// than keep retrying many times.
const dryRunMaximumInventoryClientRetries = 3

const (
	InstallDir                   = "/opt/install-dir"
	KubeconfigPath               = "/opt/openshift/auth/kubeconfig"
	minMasterNodes               = 2
	dockerConfigFile             = "/root/.docker/config.json"
	assistedControllerNamespace  = "assisted-installer"
	extractRetryCount            = 3
	waitForeverTimeout           = time.Duration(1<<63 - 1) // wait forever ~ 292 years
	singleNodeMasterIgnitionPath = "/opt/openshift/master.ign"
	waitingForMastersStatusInfo  = "Waiting for masters to join bootstrap control plane"
	waitingForBootstrapToPrepare = "Waiting for bootstrap node preparation"
)

var generalWaitTimeout = 30 * time.Second
var generalWaitInterval = 5 * time.Second

// Installer will run the install operations on the node
type Installer interface {
	// FormatDisks formats all disks that have been configured to be formatted
	FormatDisks() error
	InstallNode() error
	UpdateHostInstallProgress(newStage models.HostStage, info string)
}

type installer struct {
	config.Config
	log             logrus.FieldLogger
	ops             ops.Ops
	inventoryClient inventory_client.InventoryClient
	kcBuilder       k8s_client.K8SClientBuilder
	ign             ignition.Ignition
	cleanupDevice   preinstallUtils.CleanupDevice
}

func NewAssistedInstaller(log logrus.FieldLogger, cfg config.Config, ops ops.Ops, ic inventory_client.InventoryClient, kcb k8s_client.K8SClientBuilder, ign ignition.Ignition, cleanupDevice preinstallUtils.CleanupDevice) *installer {
	return &installer{
		log:             log,
		Config:          cfg,
		ops:             ops,
		inventoryClient: ic,
		kcBuilder:       kcb,
		ign:             ign,
		cleanupDevice:   cleanupDevice,
	}
}

func (i *installer) FormatDisks() {
	for _, diskToFormat := range i.Config.DisksToFormat {
		if err := i.ops.FormatDisk(diskToFormat); err != nil {
			// This is best effort - keep trying to format other disks
			// and go on with the installation, log a warning
			i.log.Warnf("Failed to format disk %s, err %s", diskToFormat, err)
		}
	}
}

func (i *installer) InstallNode() error {
	i.log.Infof("Installing node with role: %s", i.Config.Role)

	i.UpdateHostInstallProgress(models.HostStageStartingInstallation, i.Config.Role)
	i.Config.Device = i.ops.EvaluateDiskSymlink(i.Config.Device)
	i.cleanupInstallDevice()
	if err := i.ops.Mkdir(InstallDir); err != nil {
		i.log.Errorf("Failed to create install dir: %s", err)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	bootstrapErrGroup, _ := errgroup.WithContext(ctx)
	//cancel the context in case this method ends
	defer cancel()
	isBootstrap := false
	if i.Config.Role == string(models.HostRoleBootstrap) && i.HighAvailabilityMode != models.ClusterHighAvailabilityModeNone {
		isBootstrap = true
		bootstrapErrGroup.Go(func() error {
			return i.startBootstrap()
		})
		go i.updateConfiguringStatus(ctx)
		i.Config.Role = string(models.HostRoleMaster)
	}

	i.UpdateHostInstallProgress(models.HostStageInstalling, i.Config.Role)
	var ignitionPath string
	var err error

	// i.HighAvailabilityMode is set as an empty string for workers
	// regardless of the availability mode of the cluster they are joining
	// as it is of no consequence to them.
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

	if i.Config.Role == string(models.HostRoleWorker) {
		// Wait for 2 masters to be ready before rebooting
		if err = i.workerWaitFor2ReadyMasters(ctx); err != nil {
			return err
		}
	}

	if i.EnableSkipMcoReboot {
		i.skipMcoReboot(ignitionPath)
	}

	if err = i.ops.SetBootOrder(i.Device); err != nil {
		i.log.WithError(err).Warnf("Failed to set boot order")
		// Ignore the error for now so it doesn't fail the installation in case it fails
		//return err
	}

	if isBootstrap {
		i.UpdateHostInstallProgress(models.HostStageWaitingForControlPlane, waitingForBootstrapToPrepare)
		if err = bootstrapErrGroup.Wait(); err != nil {
			i.log.Errorf("Bootstrap failed %s", err)
			return err
		}
		if err = i.waitForControlPlane(ctx); err != nil {
			return err
		}
		i.log.Info("Setting bootstrap node new role to master")
	}

	//upload host logs and report log status before reboot
	i.log.Infof("Uploading logs and reporting status before rebooting the node %s for cluster %s", i.Config.HostID, i.Config.ClusterID)
	i.inventoryClient.HostLogProgressReport(ctx, i.Config.InfraEnvID, i.Config.HostID, models.LogsStateRequested)
	_, err = i.ops.UploadInstallationLogs(isBootstrap || i.HighAvailabilityMode == models.ClusterHighAvailabilityModeNone)
	if err != nil {
		i.log.Errorf("upload installation logs %s", err)
	}
	return i.finalize()
}

func (i *installer) finalize() error {
	if i.DryRunEnabled {
		i.UpdateHostInstallProgress(models.HostStageRebooting, "")
		_, err := i.ops.ExecPrivilegeCommand(nil, "touch", i.Config.FakeRebootMarkerPath)
		return errors.Wrap(err, "failed to touch fake reboot marker")
	}

	// in case ironic-agent exists on the host we should stop the assisted-agent service instead of rebooting the node.
	// the assisted agent service stop will signal the ironic agent that we are done so that IPA can continue with its flow.
	ironicAgentServiceName := "ironic-agent.service"
	out, err := i.ops.ExecPrivilegeCommand(nil, "systemctl", "list-units", "--no-legend", ironicAgentServiceName)
	if err != nil {
		i.log.Errorf("Failed to check if ironic agent service exists on the node %s", err)
		return err
	}
	if strings.Contains(out, ironicAgentServiceName) {
		i.UpdateHostInstallProgress(models.HostStageRebooting, "Ironic will reboot the node shortly")
		if err = i.ops.SystemctlAction("stop", "agent.service"); err != nil {
			return err
		}
	} else {
		i.UpdateHostInstallProgress(models.HostStageRebooting, "")
		// Deu to a race condition in etcd bootstrap strategy we need to bring back this delay.
		// See: https://issues.redhat.com/browse/OCPBUGS-5988
		whenToReboot := "+1"
		if i.HighAvailabilityMode == models.ClusterHighAvailabilityModeNone {
			whenToReboot = "now"
		}
		if err = i.ops.Reboot(whenToReboot); err != nil {
			return err
		}
	}
	return nil
}

// updateSingleNodeIgnition will download the host ignition config and add the files under storage
func (i *installer) updateSingleNodeIgnition(singleNodeIgnitionPath string) error {
	if i.DryRunEnabled {
		return nil
	}

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
	hostConfig.Ignition.Config = ignition.EmptyIgnitionConfig
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

func convertToInstallerKargs(kargs []string) []string {
	var ret []string
	for _, karg := range kargs {
		ret = append(ret, "--append-karg", karg)
	}
	return ret
}

func convertToOverwriteKargs(args []string) []string {
	var ret []string
	i := 0
	for i < len(args) {
		if args[i] == "--append-karg" && i+1 < len(args) {
			ret = append(ret, "--karg", args[i+1])
			i += 2
		} else {
			i++
		}
	}
	return ret
}

func (i *installer) cleanupInstallDevice() {
	if i.DryRunEnabled || i.Config.SkipInstallationDiskCleanup {
		i.log.Infof("skipping installation disk cleanup")
	} else {
		err := i.cleanupDevice.CleanupInstallDevice(i.Config.Device)
		if err != nil {
			i.UpdateHostInstallProgress(models.HostStageStartingInstallation, fmt.Sprintf("Could not clean install device %s. The installation will continue. If the installation fails, clean the disk and try again", i.Device))
			// Do not change the phrasing of this error message, as we rely on it in a triage signature
			i.log.Errorf("failed to prepare install device %s, err %s", i.Device, err)
		}
	}
}

func (i *installer) getEncapsulatedMC(ignitionPath string) (*mcfgv1.MachineConfig, error) {
	var mc *mcfgv1.MachineConfig
	var err error
	waitErr := utils.WaitForPredicate(20*time.Minute, 5*time.Second, func() bool {
		mc, err = i.ops.GetEncapsulatedMC(ignitionPath)
		if err != nil {
			i.log.WithError(err).Warningf("failed to get encapsulated Machine Config")
		}
		return err == nil
	})
	if waitErr != nil {
		return nil, stderrors.Join(waitErr, err)
	}
	return mc, nil
}

func (i *installer) overwriteOsImage(overwriteImage string, kargs []string) error {
	err := i.ops.OverwriteOsImage(overwriteImage, i.Device,
		convertToOverwriteKargs(append(i.Config.InstallerArgs, convertToInstallerKargs(kargs)...)))
	if err != nil {
		i.log.WithError(err).Error("failed to overwrite install image")
		return err
	}
	return nil
}

func (i *installer) writeImageToDisk(ignitionPath string) error {
	i.UpdateHostInstallProgress(models.HostStageWritingImageToDisk, "")

	interval := time.Second
	liveLogger := coreos_logger.NewCoreosInstallerLogWriter(i.log, i.inventoryClient, i.Config.InfraEnvID, i.Config.HostID)
	err := utils.Retry(3, interval, i.log, func() error {
		return i.ops.WriteImageToDisk(liveLogger, ignitionPath, i.Device, i.Config.InstallerArgs)
	})
	if err != nil {
		i.log.WithError(err).Error("Failed to write image to disk")
		return err
	}
	i.log.Info("Done writing image to disk")
	return nil
}

func (i *installer) skipMcoReboot(ignitionPath string) {
	var mc *mcfgv1.MachineConfig
	mc, err := i.getEncapsulatedMC(ignitionPath)
	if err != nil {
		i.log.WithError(err).Warning("failed getting encapsulated machine config.  Continuing installation without skipping MCO reboot")
		return
	}
	if err = i.overwriteOsImage(mc.Spec.OSImageURL, mc.Spec.KernelArguments); err != nil {
		i.log.WithError(err).Warning("failed overwriting OS image.  Continuing installation without skipping MCO reboot")
	}
}

func (i *installer) startBootstrap() error {
	i.log.Infof("Running bootstrap")
	// This is required for the log collection command to work since it will try to mount this directory
	// This directory is also required by `generateSshKeyPair` as it will place the key there
	if err := i.ops.Mkdir(sshDir); err != nil {
		i.log.WithError(err).Error("Failed to create SSH dir")
		return err
	}
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
	err = i.checkHostname()
	if err != nil {
		i.log.Error(err)
		return err
	}

	// restart NetworkManager to trigger NetworkManager/dispatcher.d/30-local-dns-prepender
	// we don't do it on SNO because the "local-dns-prepender" is not even
	// available on none-platform
	if i.HighAvailabilityMode != models.ClusterHighAvailabilityModeNone {
		err = i.ops.SystemctlAction("restart", "NetworkManager.service")
		if err != nil {
			i.log.Error(err)
			return err
		}
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
	if i.DryRunEnabled {
		return nil
	}

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
	if i.DryRunEnabled {
		return nil
	}

	i.log.Info("Generating new SSH key pair")
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
	err := i.inventoryClient.DownloadHostIgnition(ctx, i.Config.InfraEnvID, i.Config.HostID, dest)
	if err != nil {
		log.Errorf("Failed to fetch file (%s) from server. err: %s", filename, err)
	}
	return dest, err
}

func (i *installer) waitForControlPlane(ctx context.Context) error {
	err := i.ops.ReloadHostFile("/etc/resolv.conf")
	if err != nil {
		i.log.WithError(err).Error("Failed to reload resolv.conf")
		return err
	}
	kc, err := i.kcBuilder(KubeconfigPath, i.log)
	if err != nil {
		i.log.Error(err)
		return err
	}
	i.UpdateHostInstallProgress(models.HostStageWaitingForControlPlane, waitingForMastersStatusInfo)

	hasValidvSphereCredentials := common.HasValidvSphereCredentials(ctx, i.inventoryClient, i.log)
	if hasValidvSphereCredentials {
		i.log.Infof("Has valid vSphere credentials: %v", hasValidvSphereCredentials)
	}

	cluster, callErr := i.inventoryClient.GetCluster(ctx, false)
	if callErr != nil {
		i.log.WithError(callErr).Errorf("Getting cluster %s", i.ClusterID)
		return callErr
	}

	if err = i.waitForMinMasterNodes(ctx, kc, cluster.Platform, hasValidvSphereCredentials); err != nil {
		return err
	}

	i.waitForBootkube(ctx)
	if err = i.waitForETCDBootstrap(ctx); err != nil {
		i.log.Error(err)
		return err
	}

	// waiting for controller pod to be running
	if err = i.waitForController(kc); err != nil {
		i.log.Error(err)
		return err
	}

	return nil
}

func (i *installer) waitForETCDBootstrap(ctx context.Context) error {
	i.UpdateHostInstallProgress(models.HostStageWaitingForBootkube, "waiting for ETCD bootstrap to be complete")
	i.log.Infof("Started waiting for ETCD bootstrap to complete")
	return utils.WaitForPredicate(waitForeverTimeout, generalWaitInterval, func() bool {
		// check if ETCD bootstrap has completed every 5 seconds
		if result, err := i.ops.ExecPrivilegeCommand(nil, "systemctl", "is-active", "progress.service"); result == "inactive" {
			i.log.Infof("ETCD bootstrap progress service status: %s", result)
			out, _ := i.ops.ExecPrivilegeCommand(nil, "systemctl", "status", "progress.service")
			i.log.Info(out)
			return true
		} else if err != nil {
			i.log.WithError(err).Warnf("error occurred checking ETCD bootstrap progress: %s", result)
		}
		return false
	})
}

func numDone(hosts models.HostList) int {
	numDone := 0
	for _, h := range hosts {
		if h.Progress.CurrentStage == models.HostStageDone {
			numDone++
		}
	}
	return numDone
}

func (i *installer) workerWaitFor2ReadyMasters(ctx context.Context) error {
	var cluster *models.Cluster

	i.log.Info("Waiting for 2 ready masters")
	i.UpdateHostInstallProgress(models.HostStageWaitingForControlPlane, "")
	_ = utils.WaitForPredicate(waitForeverTimeout, generalWaitInterval, func() bool {
		if cluster == nil {
			var callErr error
			cluster, callErr = i.inventoryClient.GetCluster(ctx, false)
			if callErr != nil {
				i.log.WithError(callErr).Errorf("Getting cluster %s", i.ClusterID)
				return false
			}
		}
		if swag.StringValue(cluster.Kind) == models.ClusterKindAddHostsCluster {
			return true
		}

		hosts, callErr := i.inventoryClient.ListsHostsForRole(ctx, string(models.HostRoleMaster))
		if callErr != nil {
			i.log.WithError(callErr).Errorf("Getting cluster %s hosts", i.ClusterID)
			return false
		}
		return numDone(hosts) >= minMasterNodes

	})

	return nil
}

func (i *installer) waitForMinMasterNodes(ctx context.Context, kc k8s_client.K8SClient, platform *models.Platform, hasValidCredentials bool) error {
	i.waitForMasterNodes(ctx, minMasterNodes, kc, platform, hasValidCredentials)
	return nil
}

func (i *installer) UpdateHostInstallProgress(newStage models.HostStage, info string) {
	ctx := utils.GenerateRequestContext()
	log := utils.RequestIDLogger(ctx, i.log)
	log.Infof("Updating node installation stage: %s - %s", newStage, info)
	if i.HostID != "" {
		if err := i.inventoryClient.UpdateHostInstallProgress(ctx, i.Config.InfraEnvID, i.Config.HostID, newStage, info); err != nil {
			log.Errorf("Failed to update node installation stage, %s", err)
		}
	}
}

func (i *installer) waitForBootkube(ctx context.Context) {
	i.log.Infof("Waiting for bootkube to complete")
	i.UpdateHostInstallProgress(models.HostStageWaitingForBootkube, "")

	for {
		select {
		case <-ctx.Done():
			i.log.Info("Context cancelled, terminating wait for bootkube\n")
			return
		case <-time.After(generalWaitInterval):
			// check if bootkube is done every 5 seconds
			if _, err := i.ops.ExecPrivilegeCommand(nil, "stat", "/opt/openshift/.bootkube.done"); err == nil {
				// in case bootkube is done log the status and return
				i.log.Info("bootkube service completed")
				out, _ := i.ops.ExecPrivilegeCommand(nil, "systemctl", "status", "bootkube.service")
				i.log.Info(out)
				return
			}
		}
	}
}

func (i *installer) waitForController(kc k8s_client.K8SClient) error {
	i.log.Infof("Waiting for controller to be ready")
	i.UpdateHostInstallProgress(models.HostStageWaitingForController, "waiting for controller pod ready event")

	events := map[string]string{}
	tickerUploadLogs := time.NewTicker(5 * time.Minute)
	tickerWaitForController := time.NewTicker(generalWaitInterval)
	for {
		select {
		case <-tickerWaitForController.C:
			if i.wasControllerReadyEventSet(kc, events) {
				i.log.Infof("Assisted controller is ready")
				i.inventoryClient.ClusterLogProgressReport(utils.GenerateRequestContext(), i.ClusterID, models.LogsStateRequested)
				i.uploadControllerLogs(kc)
				return nil
			}
		case <-tickerUploadLogs.C:
			i.uploadControllerLogs(kc)
		}
	}
}

func (i *installer) uploadControllerLogs(kc k8s_client.K8SClient) {
	controllerPod := common.GetPodInStatus(kc, common.AssistedControllerPrefix, assistedControllerNamespace,
		map[string]string{"job-name": common.AssistedControllerPrefix}, v1.PodRunning, i.log)
	if controllerPod != nil {
		//do not report the progress of this pre-fetching of controller logs to the service
		//since controller may not be ready at all and we'll end up waiting for a timeout to expire
		//in the service with no good reason before giving up on the logs
		//when controller is ready - it will report its log progress by itself
		err := common.UploadPodLogs(kc, i.inventoryClient, i.ClusterID, controllerPod.Name, assistedControllerNamespace, common.ControllerLogsSecondsAgo, i.log)
		// if failed to upload logs, log why and continue
		if err != nil {
			i.log.WithError(err).Warnf("Failed to upload controller logs")
		}
	}
}

func (i *installer) wasControllerReadyEventSet(kc k8s_client.K8SClient, previousEvents map[string]string) bool {
	newEvents, errEvents := kc.ListEvents(assistedControllerNamespace)
	if errEvents != nil {
		logrus.WithError(errEvents).Warnf("Failed to get controller events")
		return false
	}

	readyEventFound := false
	for _, event := range newEvents.Items {
		if _, ok := previousEvents[string(event.UID)]; !ok {
			i.log.Infof("Assisted controller new event: %s", event.Message)
			previousEvents[string(event.UID)] = event.Name
		}
		if event.Name == common.AssistedControllerIsReadyEvent {
			readyEventFound = true
		}
	}

	return readyEventFound
}

// wait for minimum master nodes to be in ready status
func (i *installer) waitForMasterNodes(ctx context.Context, minMasterNodes int, kc k8s_client.K8SClient, platform *models.Platform, hasValidvSphereCredentials bool) {
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

		if len(nodes.Items) > 0 {
			// GetInvoker reads the openshift-install-manifests in the openshift-config namespace.
			// This configmap exists after nodes start to appear in the cluster.
			invoker := common.GetInvoker(kc, i.log)
			removeUninitializedTaint := common.RemoveUninitializedTaint(platform, invoker,
				hasValidvSphereCredentials, i.OpenshiftVersion)
			i.log.Infof("Remove uninitialized taint: %v", removeUninitializedTaint)
			if removeUninitializedTaint {
				for _, node := range nodes.Items {
					if common.IsK8sNodeIsReady(node) {
						continue
					}
					if err = kc.UntaintNode(node.Name); err != nil {
						i.log.Warnf("Failed to untaint node %s: %v", node.Name, err)
					}
				}
			}
		}
		if err = i.updateReadyMasters(nodes, &readyMasters, inventoryHostsMap); err != nil {
			i.log.WithError(err).Warnf("Failed to update ready with masters")
			return false
		}
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
		case <-time.After(generalWaitInterval):
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

func (i *installer) updateReadyMasters(nodes *v1.NodeList, readyMasters *[]string, inventoryHostsMap map[string]inventory_client.HostData) error {
	nodeNameAndCondition := map[string][]v1.NodeCondition{}
	knownIpAddresses := common.BuildHostsMapIPAddressBased(inventoryHostsMap)

	for _, node := range nodes.Items {
		nodeNameAndCondition[node.Name] = node.Status.Conditions
		if common.IsK8sNodeIsReady(node) && !funk.ContainsString(*readyMasters, node.Name) {
			ctx := utils.GenerateRequestContext()
			log := utils.RequestIDLogger(ctx, i.log)
			log.Infof("Found a new ready master node %s with id %s", node.Name, node.Status.NodeInfo.SystemUUID)

			host, ok := common.HostMatchByNameOrIPAddress(node, inventoryHostsMap, knownIpAddresses)
			if ok && (host.Host.Status == nil || *host.Host.Status != models.HostStatusInstalled) {
				if err := i.inventoryClient.UpdateHostInstallProgress(ctx, host.Host.InfraEnvID.String(), host.Host.ID.String(), models.HostStageJoined, ""); err != nil {
					log.Errorf("Failed to update node installation status, %s", err)
					continue
				}
			}
			*readyMasters = append(*readyMasters, node.Name)
			if !ok {
				return fmt.Errorf("node %s is not in inventory hosts", node.Name)
			}
		}
	}

	i.log.Infof("Found %d master nodes: %+v", len(nodes.Items), nodeNameAndCondition)
	return nil
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

func (i *installer) checkHostname() error {
	if i.DryRunEnabled {
		return nil
	}

	i.log.Infof("Start checking hostname")
	hostname, err := i.ops.GetHostname()
	if err != nil {
		i.log.Errorf("Failed to get hostname from kernel, err %s157", err)
		return err
	}

	if err := validations.ValidateHostname(hostname); err == nil && hostname != "localhost" {
		i.log.Infof("hostname [%s] is not localhost or invalid, no need to do anything", hostname)
		return nil
	}

	data := fmt.Sprintf("random-hostname-%s", uuid.New().String())
	i.log.Infof("Hostname [%s] is invalid, generated random hostname [%s] and writing data into /etc/hostname")
	return i.ops.CreateRandomHostname(data)
}

func RunInstaller(installerConfig *config.Config, logger *logrus.Logger) error {
	logger.Infof("Assisted installer started. Configuration is:\n %s", secretdump.DumpSecretStruct(*installerConfig))
	logger.Infof("Dry configuration is:\n %s", secretdump.DumpSecretStruct(installerConfig.DryRunConfig))

	numRetries := inventory_client.DefaultMaxRetries
	if installerConfig.DryRunEnabled {
		numRetries = dryRunMaximumInventoryClientRetries
	}

	client, err := inventory_client.CreateInventoryClientWithDelay(
		installerConfig.ClusterID,
		installerConfig.URL,
		installerConfig.PullSecretToken,
		installerConfig.SkipCertVerification,
		installerConfig.CACertPath,
		logger,
		http.ProxyFromEnvironment,
		inventory_client.DefaultRetryMinDelay,
		inventory_client.DefaultRetryMaxDelay,
		numRetries,
		inventory_client.DefaultMinRetries,
	)

	if err != nil {
		logger.Fatalf("Failed to create inventory client %e", err)
	}

	executor := execute.NewExecutor(installerConfig, logger, true)
	o := ops.NewOpsWithConfig(installerConfig, logger, executor)
	cleanupDevice := preinstallUtils.NewCleanupDevice(logger, preinstallUtils.NewDiskOps(logger, executor))
	var k8sClientBuilder k8s_client.K8SClientBuilder
	if !installerConfig.DryRunEnabled {
		k8sClientBuilder = k8s_client.NewK8SClient
	} else {
		k8sClientBuilder = drymock.NewDryRunK8SClientBuilder(installerConfig, o)
	}

	ai := NewAssistedInstaller(logger,
		*installerConfig,
		o,
		client,
		k8sClientBuilder,
		ignition.NewIgnition(),
		cleanupDevice,
	)

	// Try to format requested disks. May fail formatting some disks, this is not an error.
	ai.FormatDisks()

	if err = ai.InstallNode(); err != nil {
		ai.UpdateHostInstallProgress(models.HostStageFailed, err.Error())
		return err
	}
	return nil
}

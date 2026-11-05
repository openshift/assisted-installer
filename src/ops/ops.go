package ops

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"

	config_latest "github.com/coreos/ignition/v2/config/v3_2"
	"github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/coreos/ignition/v2/config/validate"
	"github.com/go-openapi/swag"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/ignition"
	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
	"github.com/vincent-petithory/dataurl"
)

const (
	dockerConfigFile                = "/root/.docker/config.json"
	coreosInstallerExecutable       = "coreos-installer"
	dryRunCoreosInstallerExecutable = "dry-installer"
	encapsulatedMachineConfigFile   = "/etc/ignition-machine-config-encapsulated.json"
	defaultIgnitionPlatformId       = "ignition.platform.id=metal"
	ignitionContent                 = "application/vnd.coreos.ignition+json; version=3.4.0"
	schemeHTTPS                     = "https"
	registryDataDirOnDevice         = "var/lib/iri-registry"
	registryDataDirOnMnt            = "/mnt/agentdata"
	ostreeDeployPath                = "/mnt/root/ostree/deploy/rhcos"
	networkManagerKeyfileDir        = "/etc/NetworkManager/system-connections"
	firstBootNetworkDirOnBoot       = "/mnt/boot/coreos-firstboot-network"
	firstBootNetworkSentinelOnBoot  = "/mnt/boot/coreos-firstboot-network.deliberate"
	networkDirArg                   = "--network-dir"

	iriRegistryPartitionLabel = "iri-registry"
	iriRegistryMountPoint     = "/var/lib/iri-registry"
	iriRegistryTempMount      = "/mnt/iri-registry"
)

// iriPartitionConfig holds IRI partition geometry configuration.
// Currently hardcoded with defaults. In the future, these values may come
// from Assisted Service via installer Config, but for now we keep them
// internal to ops package.
type iriPartitionConfig struct {
	sizeGB     int // Partition size in GB
	reservedGB int // Reserved space after partition in GB (for future expansion)
}

// defaultIRIPartitionConfig defines default IRI partition configuration.
// sizeGB: 150GB partition size
// reservedGB: 0 = no reserved space (partition at end of disk)
var defaultIRIPartitionConfig = iriPartitionConfig{
	sizeGB:     150,
	reservedGB: 0,
}

// Supports: registry.io/repo:tag, registry.io:port/repo:tag, repo@sha256:digest, repo:tag@sha256:digest
var imageReferencePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+(:[0-9]+)?(/[a-zA-Z0-9._/-]+)?(:[a-zA-Z0-9._-]+)?(@sha256:[a-fA-F0-9]{64})?$`)

//go:generate mockgen -source=ops.go -package=ops -destination=mock_ops.go
type Ops interface {
	Mkdir(dirName string) error
	WriteImageToDisk(liveLogger io.Writer, ignitionPath string, device string, extraArgs []string) error
	WriteImageToExistingRoot(liveLogger io.Writer, ignitionPath string, installerArgs []string) error
	Reboot(delay string) error
	SetBootOrder(device string) error
	ExtractFromIgnition(ignitionPath string, fileToExtract string) error
	SystemctlAction(action string, args ...string) error
	PrepareController() error
	GetMCSLogs() (string, error)
	UploadInstallationLogs(isBootstrap bool) (string, error)
	ReloadHostFile(filepath string) error
	CreateOpenshiftSshManifest(filePath, template, sshPubKeyPath string) error
	GetNumberOfReboots(ctx context.Context, nodeName, kubeconfigPath string) (int, error)
	GetMustGatherLogs(workDir, kubeconfigPath string, images ...string) (string, error)
	CreateRandomHostname(hostname string) error
	GetHostname() (string, error)
	EvaluateDiskSymlink(string) string
	FormatDisk(string) error
	CreateManifests(string, []byte) error
	DryRebootHappened(markerPath string) bool
	FileExists(path string) bool
	ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	ReadFile(filePath string) ([]byte, error)
	GetEncapsulatedMC(ignitionPath string) (*mcfgv1.MachineConfig, error)
	OverwriteOsImage(osImage, device string, extraArgs []string) error
	PrepareIRIRegistryIgnition(ignitionPath string) error
	SetupIRIRegistryPartition(device string, liveLogger io.Writer, updateProgress func(models.HostStage, string)) error
}

const (
	controllerDeployFolder         = "/assisted-installer-controller/deploy"
	manifestsFolder                = "/opt/openshift/manifests"
	renderedControllerCm           = "assisted-installer-controller-cm.yaml"
	controllerDeployCmTemplate     = "assisted-installer-controller-cm.yaml.template"
	renderedControllerPod          = "assisted-installer-controller-pod.yaml"
	controllerDeployPodTemplate    = "assisted-installer-controller-pod.yaml.template"
	renderedControllerSecret       = "assisted-installer-controller-secret.yaml"
	controllerDeploySecretTemplate = "assisted-installer-controller-secret.yaml.template"
	ostreeRepo                     = "/ostree/repo"
	assistedInstallOSTreeRefName   = "assisted-installer/install-image"
	nodeImageOSTreeRefName         = "coreos/node-image"
	installStateRootName           = "install"
	MustGatherFileName             = "must-gather.tar.gz"
)

type ops struct {
	log             logrus.FieldLogger
	logWriter       *utils.LogWriter
	installerConfig *config.Config
	executor        execute.Execute
}

func NewOps(logger *logrus.Logger, executor execute.Execute) Ops {
	return NewOpsWithConfig(&config.Config{}, logger, executor)
}

// NewOps return a new ops interface
func NewOpsWithConfig(installerConfig *config.Config, logger logrus.FieldLogger, executor execute.Execute) Ops {
	return &ops{
		log:             logger,
		logWriter:       utils.NewLogWriter(logger),
		installerConfig: installerConfig,
		executor:        executor,
	}
}

func (o *ops) Mkdir(dirName string) error {
	o.log.Infof("Creating directory: %s", dirName)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "mkdir", "-p", dirName)
	return err
}

func (o *ops) SystemctlAction(action string, args ...string) error {
	if o.installerConfig.DryRunEnabled {
		return nil
	}

	o.log.Infof("Running systemctl %s %s", action, args)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "systemctl", append([]string{action}, args...)...)
	if err != nil {
		o.log.Errorf("Failed executing systemctl %s %s", action, args)
	}
	return errors.Wrapf(err, "Failed executing systemctl %s %s", action, args)
}

func (o *ops) remountFilesystems(liveLogger io.Writer) error {
	out, err := o.ExecPrivilegeCommand(liveLogger, "mount", "/sysroot", "-o", "remount,rw")
	if err != nil {
		return errors.Wrapf(err, "failed to remount sysroot: %s", out)
	}
	out, err = o.ExecPrivilegeCommand(liveLogger, "mount", "/boot", "-o", "remount,rw")
	if err != nil {
		return errors.Wrapf(err, "failed to remount boot: %s", out)
	}

	return nil
}

// Removes the node image reference if it still exists, but also ensure it isn't garbage collected just yet
// Workaround until https://github.com/openshift/installer/pull/9570 is included in all 4.19 releases
func (o *ops) removeNodeImage(liveLogger io.Writer) error {
	if !o.FileExists(path.Join(ostreeRepo, "refs/heads", nodeImageOSTreeRefName)) {
		return nil
	}

	o.log.Info("Removing existing node-image ref")

	out, err := o.ExecPrivilegeCommand(liveLogger, "ostree", "refs", "--repo", ostreeRepo, "--delete", nodeImageOSTreeRefName)
	if err != nil {
		return errors.Wrapf(err, "failed deleting the coreos node image ref: %s", out)
	}
	out, err = o.ExecPrivilegeCommand(liveLogger, "touch", "/ostree/repo/tmp/node-image")
	if err != nil {
		return errors.Wrapf(err, "failed to update temp node image checkout: %s", out)
	}

	return nil
}

func (o *ops) installToNewStateroot(liveLogger io.Writer) error {
	out, err := o.ExecPrivilegeCommand(liveLogger, "ostree", "admin", "stateroot-init", installStateRootName)
	if err != nil {
		return errors.Wrapf(err, "failed creating new stateroot: %s", out)
	}

	out, err = o.ExecPrivilegeCommand(liveLogger, "ostree", "container", "image", "deploy",
		"--stateroot", installStateRootName,
		"--sysroot", "/",
		"--authfile", dockerConfigFile,
		"--karg", "$ignition_firstboot",
		"--karg", defaultIgnitionPlatformId,
		"--image", o.installerConfig.CoreosImage)
	if err != nil {
		return errors.Wrapf(err, "failed to deploy container image %s to stateroot: %s", o.installerConfig.CoreosImage, out)
	}

	out, err = o.ExecPrivilegeCommand(liveLogger, "ostree", "admin", "finalize-staged")
	if err != nil {
		return errors.Wrapf(err, "failed to finalize staged deployment: %s", out)
	}

	return nil
}

func (o *ops) applyInstallKargs(liveLogger io.Writer, installerArgs []string) error {
	commandArgs := []string{"kargs", "--os", installStateRootName}

	lastArgIndex := len(installerArgs) - 1
	for i, arg := range installerArgs {
		if arg == "--append-karg" && i < lastArgIndex {
			commandArgs = append(commandArgs, "--append", installerArgs[i+1])
			continue
		}
		if arg == "--delete-karg" && i < lastArgIndex {
			commandArgs = append(commandArgs, "--delete-if-present", installerArgs[i+1])
			continue
		}
	}

	if len(commandArgs) == 3 {
		return nil
	}

	o.log.Info("Modifying install kernel args")

	out, err := o.ExecPrivilegeCommand(liveLogger, "rpm-ostree", commandArgs...)
	if err != nil {
		return errors.Wrapf(err, "failed to apply kargs modifications: %s", out)
	}

	out, err = o.ExecPrivilegeCommand(liveLogger, "ostree", "admin", "finalize-staged")
	if err != nil {
		return errors.Wrapf(err, "failed to finalize staged deployment after kargs application: %s", out)
	}

	return nil
}

func (o *ops) configureFirstBootNetworking(liveLogger io.Writer, installerArgs []string) error {
	if !slices.Contains(installerArgs, "--copy-network") && !slices.Contains(installerArgs, "-n") {
		return nil
	}

	o.log.Info("copying network files from /etc/NetworkManager/system-connections to /boot/coreos-firstboot-network")
	out, err := o.ExecPrivilegeCommand(liveLogger, "mkdir", "/boot/coreos-firstboot-network")
	if err != nil {
		return errors.Wrapf(err, "failed to create firstboot network directory: %s", out)
	}

	out, err = o.ExecPrivilegeCommand(liveLogger, "rsync", "-av", "/etc/NetworkManager/system-connections/", "/boot/coreos-firstboot-network/")
	if err != nil {
		return errors.Wrapf(err, "failed to copy network files to firstboot network directory: %s", out)
	}

	return nil
}

func (o *ops) configureFirstBootIgnition(liveLogger io.Writer, ignitionPath string) error {
	out, err := o.ExecPrivilegeCommand(liveLogger, "mkdir", "/boot/ignition")
	if err != nil {
		return errors.Wrapf(err, "failed to create ignition directory: %s", out)
	}

	out, err = o.ExecPrivilegeCommand(liveLogger, "cp", ignitionPath, "/boot/ignition/config.ign")
	if err != nil {
		return errors.Wrapf(err, "failed to copy ignition file: %s", out)
	}

	out, err = o.ExecPrivilegeCommand(liveLogger, "touch", "/boot/ignition.firstboot")
	if err != nil {
		return errors.Wrapf(err, "failed to write ignition marker file: %s", out)
	}

	return nil
}

func (o *ops) WriteImageToExistingRoot(liveLogger io.Writer, ignitionPath string, installerArgs []string) error {
	if err := o.remountFilesystems(liveLogger); err != nil {
		return err
	}

	if err := o.removeNodeImage(liveLogger); err != nil {
		return errors.Wrap(err, "failed to remove node image")
	}

	if err := o.installToNewStateroot(liveLogger); err != nil {
		return err
	}

	if err := o.applyInstallKargs(liveLogger, installerArgs); err != nil {
		return err
	}

	if err := o.configureFirstBootNetworking(liveLogger, installerArgs); err != nil {
		return err
	}

	return o.configureFirstBootIgnition(liveLogger, ignitionPath)
}

func (o *ops) WriteImageToDisk(liveLogger io.Writer, ignitionPath string, device string, extraArgs []string) error {
	allArgs := installerArgs(ignitionPath, device, extraArgs)
	o.log.Infof("Writing image and ignition to disk with arguments: %v", allArgs)

	installerExecutable := coreosInstallerExecutable
	if o.installerConfig.DryRunEnabled {
		// In dry run, we use an executable called dry-installer rather than coreos-installer.
		// This executable is expected to pretend to be doing coreos-installer stuff and print fake
		// progress. It's up to the dry-mode user to make sure such executable is available in PATH
		installerExecutable = dryRunCoreosInstallerExecutable
	}

	_, err := o.ExecPrivilegeCommand(liveLogger, installerExecutable, allArgs...)
	return err
}

func (o *ops) EvaluateDiskSymlink(device string) string {
	// Overcome https://github.com/coreos/coreos-installer/issues/512 bug.
	// coreos-installer has a bug where when a disk has busy partitions, it will
	// print a confusing error message if that disk doesn't have a `/dev/*` style path.
	// The service may give us paths that don't have the `/dev/*` path format but instead
	// are symlinks to the actual `/dev/*` path. e.g. `/dev/disk/by-id/wwn-*`.
	// To fix the bug we simply resolve the symlink and pass the resolved link to coreos-installer.
	linkTarget, err := filepath.EvalSymlinks(device)
	if err != nil {
		o.log.Warnf("Failed to filepath.EvalSymlinks(%s): %s. Continuing with %s anyway.",
			device, err.Error(), device)
	} else {
		o.log.Infof("Resolving installation device %s symlink to %s ", device, linkTarget)
		device = linkTarget
	}
	return device
}

func (o *ops) FormatDisk(disk string) error {
	if o.installerConfig.DryRunEnabled {
		return nil
	}

	o.log.Infof("Formatting disk %s", disk)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "dd", "if=/dev/zero", fmt.Sprintf("of=%s", disk), "bs=512", "count=1")
	if err != nil {
		o.log.Errorf("Failed to format disk %s, err: %s", disk, err)
		return err
	}
	return nil
}

func (o *ops) Reboot(delay string) error {
	o.log.Info("Rebooting node")
	_, err := o.ExecPrivilegeCommand(o.logWriter, "shutdown", "-r", delay, "'Installation completed, server is going to reboot.'")
	if err != nil {
		o.log.Errorf("Failed to reboot node, err: %s", err)
		return err
	}
	return nil
}

func (o *ops) SetBootOrder(device string) error {
	if o.installerConfig.DryRunEnabled {
		return nil
	}

	o.log.Infof("SetBootOrder, runtime.GOARCH: %s, device: %s", runtime.GOARCH, device)
	_, err := o.ExecPrivilegeCommand(nil, "test", "-f", "/usr/sbin/bootlist")
	if err == nil {
		_, err = o.ExecPrivilegeCommand(o.logWriter, "bootlist", "-m", "normal", "-o", device)
		if err != nil {
			o.log.WithError(err).Errorf("Failed to set boot disk with bootlist. Skipping...")
		}
		return nil
	}

	_, err = o.ExecPrivilegeCommand(nil, "test", "-d", "/sys/firmware/efi")
	if err != nil {
		o.log.Info("setting the boot order on BIOS systems is not supported. Skipping...")
		return nil
	}

	o.log.Info("Setting efibootmgr to boot from disk")
	var efiDirname string
	var findError error
	//Retry find partitions due to delayed update on device-mappers
	err = utils.WaitForPredicate(20*time.Second, 5*time.Second, func() bool {
		efiDirname, findError = o.findEfiDirectory(device)
		return findError == nil
	})
	if err != nil {
		errMessage := "failed to find EFI directory: " + findError.Error()
		o.log.WithError(err).Error(errMessage)
		return err
	}
	efiFilepath := o.getEfiFilePath(efiDirname)
	// efi-system is installed onto partition 2
	out, err := o.ExecPrivilegeCommand(o.logWriter, "efibootmgr", "-v", "-d", device, "-p", "2", "-c", "-L", "Red Hat Enterprise Linux", "-l", efiFilepath)
	if err != nil {
		o.log.Errorf("Failed to set efibootmgr to boot from disk %s, err: %s", device, err)
		return err
	}
	o.handleDuplicateEntries(out, efiFilepath)
	_, err = o.ExecPrivilegeCommand(o.logWriter, "efibootmgr", "-l", efiFilepath)
	if err != nil {
		o.log.WithError(err).Errorf("Failed to show current boot order with efibootmgr")
	}
	return nil
}

func (o *ops) handleDuplicateEntries(output, efiFilepath string) {
	r := regexp.MustCompile(`Boot(.*) has same label Red Hat Enterprise Linux`)
	for _, line := range strings.Split(output, "\n") {
		DupBootEntry := r.FindStringSubmatch(line)
		if len(DupBootEntry) > 0 {
			o.log.Infof("Found duplicate value in boot manager: %s", line)
			o.deleteBootEntry(DupBootEntry[len(DupBootEntry)-1], efiFilepath)
		}
	}
}

func (o *ops) deleteBootEntry(bootNum, efiFilepath string) {
	o.log.Infof("Removing boot entry number %s", bootNum)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "efibootmgr", "-v", "--delete-bootnum", "--bootnum", bootNum, "-l", efiFilepath)
	if err != nil {
		o.log.Errorf("Failed to delete duplicate Red Hat Enterprise Linux label %s, err: %s", bootNum, err)
	}
}

func (o *ops) getEfiFilePath(efiDirname string) string {
	var efiFileName string
	switch runtime.GOARCH {
	case "arm64":
		efiFileName = "shimaa64.efi"
	default:
		efiFileName = "shimx64.efi"
	}
	o.log.Infof("Using EFI file '%s' for GOARCH '%s'", efiFileName, runtime.GOARCH)
	return fmt.Sprintf("\\EFI\\%s\\%s", efiDirname, efiFileName)
}

func (o *ops) findEfiDirectory(device string) (string, error) {
	var (
		out string
		err error
	)
	// Find the actual path for partition 2 using lsblk
	partition2Path, err := o.getPartitionPathFromLsblk(device, "2")
	if err != nil {
		return "", errors.Wrap(err, "failed to find partition 2 for EFI directory")
	}
	if err = utils.Retry(3, 5*time.Second, o.log, func() (err error) {
		_, err = o.ExecPrivilegeCommand(nil, "mount", partition2Path, "/mnt")
		return
	}); err != nil {
		return "", errors.Wrap(err, "failed to mount efi device")
	}
	defer func() {
		_, _ = o.ExecPrivilegeCommand(nil, "umount", "/mnt")
	}()
	out, err = o.ExecPrivilegeCommand(nil, "ls", "-1", "/mnt/EFI")
	if err != nil {
		return "", errors.Wrap(err, "failed to read efi top directory")
	}
	fnames := strings.Split(strings.TrimSpace(out), "\n")
	for _, dirName := range []string{"redhat", "centos"} {
		if funk.ContainsString(fnames, dirName) {
			return dirName, nil
		}
	}
	return "", errors.New("failed to find efi boot entry directory")
}

func (o *ops) ExtractFromIgnition(ignitionPath string, fileToExtract string) error {
	if o.installerConfig.DryRunEnabled {
		return nil
	}

	o.log.Infof("Getting data from %s", ignitionPath)
	ignitionData, err := os.ReadFile(ignitionPath)
	if err != nil {
		o.log.Errorf("Error occurred while trying to read %s : %e", ignitionPath, err)
		return err
	}
	extractedContent, err := utils.GetFileContentFromIgnition(ignitionData, fileToExtract)
	if err != nil {
		o.log.Error("Failed to parse ignition")
		return err
	}

	tmpFile := "/opt/extracted_from_ignition.json"
	o.log.Infof("Writing extracted content to tmp file %s", tmpFile)
	// #nosec
	err = os.WriteFile(tmpFile, extractedContent, 0644)
	if err != nil {
		o.log.Errorf("Error occurred while writing extracted content to %s", tmpFile)
		return err
	}

	o.log.Infof("Moving %s to %s", tmpFile, fileToExtract)
	dir := filepath.Dir(fileToExtract)
	_, err = o.ExecPrivilegeCommand(o.logWriter, "mkdir", "-p", filepath.Dir(fileToExtract))
	if err != nil {
		o.log.Errorf("Failed to create directory %s ", dir)
		return err
	}
	_, err = o.ExecPrivilegeCommand(o.logWriter, "mv", tmpFile, fileToExtract)
	if err != nil {
		o.log.Errorf("Error occurred while moving %s to %s", tmpFile, fileToExtract)
		return err
	}
	return nil
}

func (o *ops) PrepareController() error {
	// Do not prepare controller files in dry mode
	if o.installerConfig.DryRunEnabled {
		return nil
	}
	if err := o.renderControllerCm(); err != nil {
		return err
	}

	if err := o.renderControllerSecret(); err != nil {
		return err
	}

	if err := o.renderControllerPod(); err != nil {
		return err
	}

	// Copy deploy files to manifestsFolder
	files, err := utils.FindFiles(controllerDeployFolder, utils.W_FILEONLY, "*.yaml")
	if err != nil {
		o.log.Errorf("Error occurred while trying to get list of files from %s : %e", controllerDeployFolder, err)
		return err
	}
	for _, file := range files {
		err := utils.CopyFile(file, filepath.Join(manifestsFolder, filepath.Base(file)))
		if err != nil {
			o.log.Errorf("Failed to copy %s to %s. error :%e", file, manifestsFolder, err)
			return err
		}
	}
	return nil
}

func (o *ops) renderControllerCm() error {
	var params = map[string]interface{}{
		"InventoryUrl":         o.installerConfig.URL,
		"ClusterId":            o.installerConfig.ClusterID,
		"SkipCertVerification": strconv.FormatBool(o.installerConfig.SkipCertVerification),
		"CACertPath":           o.installerConfig.CACertPath,
		"ControlPlaneCount":    strconv.Itoa(o.installerConfig.ControlPlaneCount),
		"CheckCVO":             o.installerConfig.CheckClusterVersion,
		"MustGatherImage":      o.installerConfig.MustGatherImage,
	}

	return o.renderDeploymentFiles(filepath.Join(controllerDeployFolder, controllerDeployCmTemplate),
		params, renderedControllerCm)
}

func (o *ops) renderControllerSecret() error {
	var params = map[string]interface{}{
		"PullSecretToken": o.installerConfig.PullSecretToken,
	}

	return o.renderDeploymentFiles(filepath.Join(controllerDeployFolder, controllerDeploySecretTemplate),
		params, renderedControllerSecret)
}

func (o *ops) renderControllerPod() error {
	var params = map[string]interface{}{
		"ControllerImage":  o.installerConfig.ControllerImage,
		"CACertPath":       o.installerConfig.CACertPath,
		"OpenshiftVersion": o.installerConfig.OpenshiftVersion,
		"NotifyNumReboots": o.installerConfig.NotifyNumReboots,
	}

	if o.installerConfig.ServiceIPs != "" {
		params["ServiceIPs"] = strings.Split(o.installerConfig.ServiceIPs, ",")
	}

	if o.installerConfig.ControlPlaneCount == 1 {
		params["SNO"] = true
	}

	return o.renderDeploymentFiles(filepath.Join(controllerDeployFolder, controllerDeployPodTemplate),
		params, renderedControllerPod)
}

func (o *ops) renderDeploymentFiles(srcTemplate string, params map[string]interface{}, dest string) error {
	templateData, err := os.ReadFile(srcTemplate)
	if err != nil {
		o.log.Errorf("Error occurred while trying to read %s : %e", srcTemplate, err)
		return err
	}
	o.log.Infof("Filling template file %s", srcTemplate)
	tmpl := template.Must(template.New("assisted-controller").Parse(string(templateData)))
	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, params); err != nil {
		o.log.Errorf("Failed to render controller template: %e", err)
		return err
	}

	if err = o.Mkdir(manifestsFolder); err != nil {
		o.log.Errorf("Failed to create manifests dir: %e", err)
		return err
	}

	renderedControllerYaml := filepath.Join(manifestsFolder, dest)
	o.log.Infof("Writing rendered data to %s", renderedControllerYaml)
	// #nosec
	if err = os.WriteFile(renderedControllerYaml, buf.Bytes(), 0644); err != nil {
		o.log.Errorf("Error occurred while trying to write rendered data to %s : %e", renderedControllerYaml, err)
		return err
	}
	return nil
}

func (o *ops) GetMCSLogs() (string, error) {
	if o.installerConfig.DryRunEnabled {
		mcsLogs := ""
		for _, clusterHost := range o.installerConfig.ParsedClusterHosts {
			// Add IP access log for each IP, this is how the installer determines which node has downloaded the ignition
			if !o.DryRebootHappened(clusterHost.RebootMarkerPath) {
				// Host didn't even reboot yet, don't pretend it fetched the ignition
				continue
			}
			mcsLogs += fmt.Sprintf("%s.(Ignition)\n", clusterHost.Ip)
		}
		return mcsLogs, nil
	}

	files, err := utils.FindFiles("/var/log/containers/", utils.W_FILEONLY, "*machine-config-server*.log")
	if err != nil {
		o.log.WithError(err).Errorf("Error occurred while trying to get list of files from %s", "/var/log/containers/")
		return "", err
	}
	if len(files) < 1 {
		o.log.Warnf("MCS log file not found")
		return "", err
	}
	// There is theoretical option in case of static pod restart that there can be more than one file
	// we never saw it and it was decided not to handle it here
	logs, err := os.ReadFile(files[0])
	if err != nil {
		o.log.Errorf("Error occurred while trying to read %s : %e", files[0], err)
		return "", err
	}

	return string(logs), nil
}

// This function runs a container that implements the logs_sender command.
// Any change to the assisted-service API used by logs_sender (e.g. UploadLogs)
// must be reflected here (input parameters, etc.)
func (o *ops) UploadInstallationLogs(isBootstrap bool) (string, error) {
	command := "podman"

	args := []string{"run", "--rm", "--privileged", "--net=host", "--pid=host", "-v",
		"/run/systemd/journal/socket:/run/systemd/journal/socket",
		"-v", "/var/log:/var/log"}

	if o.installerConfig.CACertPath != "" {
		args = append(args, "-v", fmt.Sprintf("%[1]s:%[1]s", o.installerConfig.CACertPath))
	}

	args = append(args, "--env", "PULL_SECRET_TOKEN")

	args = append(args, o.installerConfig.AgentImage, "logs_sender",
		"-cluster-id", o.installerConfig.ClusterID, "-url", o.installerConfig.URL,
		"-host-id", o.installerConfig.HostID, "-infra-env-id", o.installerConfig.InfraEnvID,
		fmt.Sprintf("-insecure=%s", strconv.FormatBool(o.installerConfig.SkipCertVerification)),
		fmt.Sprintf("-bootstrap=%s", strconv.FormatBool(isBootstrap)))

	if o.installerConfig.CACertPath != "" {
		args = append(args, fmt.Sprintf("-cacert=%s", o.installerConfig.CACertPath))
	}

	// Pass token via command-specific environment to avoid exposure in /proc/PID/cmdline
	env := []string{fmt.Sprintf("PULL_SECRET_TOKEN=%s", o.installerConfig.PullSecretToken)}
	return o.executor.ExecCommandWithOptions(o.logWriter, command, args, execute.WithPrivilege(), execute.WithEnv(env))
}

// Sometimes we will need to reload container files from host
// For example /etc/resolv.conf, it can't be changed with Z flag but is updated by bootkube.sh
// and we need this update for dns resolve of kubeapi
func (o *ops) ReloadHostFile(filepath string) error {
	if o.installerConfig.DryRunEnabled {
		return nil
	}

	o.log.Infof("Reloading %s", filepath)
	output, err := o.ExecPrivilegeCommand(o.logWriter, "cat", filepath)
	if err != nil {
		o.log.Errorf("Failed to read %s on the host", filepath)
		return err
	}
	f, err := os.OpenFile(filepath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	defer func() {
		_ = f.Close()
	}()
	if err != nil {
		o.log.Errorf("Failed to open local %s", filepath)
		return err
	}
	_, err = f.WriteString(output)
	if err != nil {
		o.log.Errorf("Failed to write host %s data to local", filepath)
		return err
	}
	return nil
}

func (o *ops) CreateOpenshiftSshManifest(filePath, tmpl, sshPubKeyPath string) error {
	if o.installerConfig.DryRunEnabled {
		return nil
	}

	o.log.Info("Create an openshift manifets for SSH public key")
	sshPublicKey, err := o.ExecPrivilegeCommand(o.logWriter, "cat", sshPubKeyPath)
	if err != nil {
		o.log.WithError(err).Errorf("Failed to read SSH pub key from %s", sshPubKeyPath)
		return err
	}
	f, err := os.Create(filePath)
	if err != nil {
		o.log.WithError(err).Errorf("Failed to create %s", filePath)
		return err
	}
	defer f.Close()
	t := template.Must(template.New("openshift SSH manifest").Parse(tmpl))
	sshConfig := struct {
		SshPubKey string
	}{sshPublicKey}
	if err := t.Execute(f, sshConfig); err != nil {
		o.log.WithError(err).Error("Failed to execute template")
		return err
	}
	return nil
}

func (o *ops) GetNumberOfReboots(ctx context.Context, nodeName, kubeconfigPath string) (int, error) {
	out, err := o.executor.ExecCommandWithOptions(o.logWriter, "oc",
		[]string{
			"--kubeconfig",
			kubeconfigPath,
			"debug",
			fmt.Sprintf("node/%s", nodeName),
			"--",
			"chroot",
			"/host",
			"last",
			"reboot",
		},
		execute.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	lines := strings.Split(out, "\n")
	numReboots := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "reboot ") {
			numReboots++
		}
	}
	return numReboots, nil
}

// validateImageReference validates image format and rejects shell metacharacters
func validateImageReference(image string) error {
	if image == "" {
		return errors.New("image reference cannot be empty")
	}

	dangerousChars := ";|&$`<>(){}[]'\"\\!\n\r\t"
	for _, char := range dangerousChars {
		if strings.ContainsRune(image, char) {
			return errors.Errorf("image reference contains invalid character: %c", char)
		}
	}

	for _, r := range image {
		if unicode.IsControl(r) {
			return errors.Errorf("image reference contains control character")
		}
	}

	if !imageReferencePattern.MatchString(image) {
		return errors.Errorf("image reference has invalid format")
	}

	return nil
}

func (o *ops) GetMustGatherLogs(workDir, kubeconfigPath string, images ...string) (string, error) {
	args := []string{"--kubeconfig=" + kubeconfigPath, "adm", "must-gather"}
	for _, img := range images {
		if err := validateImageReference(img); err != nil {
			return "", errors.Wrapf(err, "invalid must-gather image reference: %s", img)
		}
		args = append(args, "--image="+img)
	}

	output, err := o.executor.ExecCommandWithOptions(o.logWriter, "oc", args, execute.WithDir(workDir))
	if err != nil {
		return "", err
	}
	o.log.Info(output)

	//find the directory of logs which is the output of the command
	//this is a temp directory so we have to find it by its prefix
	files, err := utils.FindFiles(workDir, utils.W_DIRONLY, "must-gather*")
	if err != nil {
		o.log.WithError(err).Errorf("Failed to read must-gather working dir %s\n", workDir)
		return "", err
	}

	if len(files) == 0 {
		lerr := fmt.Errorf("Failed to find must-gather output")
		o.log.Error(lerr)
		return "", lerr
	}
	logsDir := filepath.Base(files[0])

	tarPath := path.Join(workDir, MustGatherFileName)
	tarArgs := []string{"-czf", tarPath, "-C", workDir, logsDir}
	_, err = o.executor.ExecCommand(o.logWriter, "tar", tarArgs...)
	if err != nil {
		o.log.WithError(err).Errorf("Failed to tar must-gather logs\n")
		return "", err
	}
	return tarPath, nil
}

func (o *ops) CreateRandomHostname(hostname string) error {
	o.log.Infof("Setting hostname to %s", hostname)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "hostnamectl", "set-hostname", hostname)
	return err
}

func (o *ops) GetHostname() (string, error) {
	return os.Hostname()
}

func (o *ops) CreateManifests(kubeconfig string, content []byte) error {
	// Create temp file, where we store the content to be create by oc command:
	file, err := os.CreateTemp("", "operator-manifest")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	// Write the content to the temporary file:
	// #nosec
	if err = os.WriteFile(file.Name(), content, 0644); err != nil {
		return err
	}

	// Run oc command that creates the custom manifest:
	output, err := o.executor.ExecCommand(o.logWriter, "oc", "--kubeconfig="+kubeconfig, "apply", "-f", file.Name())
	if err != nil {
		return err
	}
	o.log.Infof("Applying custom manifest file %s succeed %s", file.Name(), output)

	return nil
}

// DryRebootHappened checks if a reboot happened according to a particular reboot marker path
// The dry run installer creates this file on "Reboot" (instead of actually rebooting)
// We use this function to check whether the given node in the cluster have already rebooted
func (o *ops) DryRebootHappened(markerPath string) bool {
	return o.FileExists(markerPath)
}

// FileExists checks if a file exists
func (o *ops) FileExists(path string) bool {
	_, err := o.ExecPrivilegeCommand(nil, "stat", path)

	return err == nil
}

// ExecPrivilegeCommand execute a command in the host environment via nsenter
func (o *ops) ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error) {
	return o.executor.ExecCommandWithOptions(liveLogger, command, args, execute.WithPrivilege())
}

func (o *ops) ReadFile(filePath string) ([]byte, error) {
	return os.ReadFile(filePath)
}

func installerArgs(ignitionPath string, device string, extra []string) []string {
	allArgs := []string{"install", "--insecure", "-i", ignitionPath}
	if extra != nil {
		allArgs = append(allArgs, extra...)
	}
	return append(allArgs, device)
}

func (o *ops) getPointedIgnitionAndCA(ignitionPath string) (string, string, error) {
	b, err := os.ReadFile(ignitionPath)
	if err != nil {
		return "", "", errors.Wrapf(err, "failed to read ignition file %s", ignitionPath)
	}
	conf, _, err := config_latest.ParseCompatibleVersion(b)
	if err != nil {
		return "", "", errors.Wrapf(err, "failed to parse ignition file %s", ignitionPath)
	}
	source := ""
	for i := range conf.Ignition.Config.Merge {
		r := &conf.Ignition.Config.Merge[i]
		if r.Source != nil {
			source = swag.StringValue(r.Source)
			if source != "" {
				break
			}
		}
	}
	if source == "" {
		return "", "", errors.Errorf("source URL not found for ignition %s", ignitionPath)
	}
	ca := ""
	for i := range conf.Ignition.Security.TLS.CertificateAuthorities {
		r := &conf.Ignition.Security.TLS.CertificateAuthorities[i]
		if r.Source != nil {
			d, err := dataurl.DecodeString(swag.StringValue(r.Source))
			if err != nil {
				return "", "", err
			}
			ca = string(d.Data)
			break
		}
	}
	return source, ca, nil
}

func (o *ops) getEmbeddedIgnition(source string) ([]byte, error) {
	d, err := dataurl.DecodeString(source)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode dataurl")
	}
	r, err := gzip.NewReader(bytes.NewReader(d.Data))
	if err != nil {
		return nil, errors.Wrap(err, "getEmbeddedIgnition: failed to decompress")
	}
	var out bytes.Buffer
	if _, err = io.CopyN(&out, r, math.MaxInt64); err != nil && err != io.EOF {
		return nil, errors.Wrap(err, "getEmbeddedIgnition: failed to copy")
	}
	return out.Bytes(), nil
}

func (o *ops) getIgnitionFromBoostrap(source, ca string) ([]byte, error) {
	parsedURL, err := url.Parse(source)
	if err != nil || parsedURL == nil {
		return nil, errors.Wrapf(err, "failed to parse ignition source URL - %s", source)
	}

	client := &http.Client{}
	if parsedURL.Scheme == schemeHTTPS && ca == "" {
		return nil, fmt.Errorf("ignition endpoint %s was specified with HTTPS, but CA certificate is empty", source)
	}

	if parsedURL.Scheme == schemeHTTPS {
		caCertPool := x509.NewCertPool()
		if ok := caCertPool.AppendCertsFromPEM([]byte(ca)); !ok {
			return nil, errors.New("failed to parse CA certificate")
		}
		o.log.Info("using HTTPS with given CA certificate to get ignition from bootstrap")
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    caCertPool,
			},
		}
		client.Transport = tr
	}

	o.log.Infof("Getting ignition from %s", source)
	req, err := http.NewRequest("GET", source, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", ignitionContent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get ignition from %s", source)
	}

	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to copy ignition from %s", source)
	}

	return buf.Bytes(), nil
}

func (o *ops) getMCSIgnition(source, ca string) ([]byte, error) {
	if strings.HasPrefix(source, "data:") {
		return o.getEmbeddedIgnition(source)
	} else {
		return o.getIgnitionFromBoostrap(source, ca)
	}
}

func (o *ops) GetEncapsulatedMC(ignitionPath string) (*mcfgv1.MachineConfig, error) {
	source, ca, err := o.getPointedIgnitionAndCA(ignitionPath)
	if err != nil {
		return nil, err
	}
	ignBytes, err := o.getMCSIgnition(source, ca)
	if err != nil {
		return nil, err
	}
	var ignition struct {
		Storage struct {
			Files []struct {
				Path     string
				Contents struct {
					Source string
				}
			}
		}
	}
	err = json.Unmarshal(ignBytes, &ignition)
	if err != nil {
		return nil, err
	}
	for _, f := range ignition.Storage.Files {
		if f.Path == encapsulatedMachineConfigFile {
			d, err := dataurl.DecodeString(f.Contents.Source)
			if err != nil {
				return nil, err
			}
			var machineConfig mcfgv1.MachineConfig

			if err = json.Unmarshal(d.Data, &machineConfig); err != nil {
				return nil, err
			}
			return &machineConfig, nil
		}
	}
	return nil, errors.New("failed to get encapsulated machine config from ignition")
}

func (o *ops) ignitionPlatformId() string {
	output, err := o.ExecPrivilegeCommand(nil, "cat", "/proc/cmdline")
	if err != nil {
		o.log.WithError(err).Warningf("failed to read /proc/cmdline. Defaulting to metal")
		return defaultIgnitionPlatformId
	}
	splits := strings.Split(output, " ")
	id, found := funk.FindString(splits, func(s string) bool { return strings.HasPrefix(s, "ignition.platform.id=") })
	if !found {
		o.log.WithError(err).Warningf("ignition platform id not present in /proc/cmdline. Defaulting to metal")
		return defaultIgnitionPlatformId
	}
	return id
}

func stripDev(device string) string {
	return strings.Replace(device, "/dev/", "", 1)
}

// makePartitionDevicePath constructs the partition device path for use in sfdisk scripts.
// Handles different device naming conventions:
// - /dev/sda + partition 5 = /dev/sda5 (traditional SCSI/SATA)
// - /dev/nvme0n1 + partition 5 = /dev/nvme0n1p5 (NVMe - ends with digit, needs 'p')
// - /dev/mmcblk0 + partition 5 = /dev/mmcblk0p5 (MMC - ends with digit, needs 'p')
func makePartitionDevicePath(device string, partitionNumber int) string {
	// Check if device name ends with a digit
	// For such devices (nvme0n1, mmcblk0, loop0), we need a 'p' separator
	deviceName := device
	if len(deviceName) > 0 && deviceName[len(deviceName)-1] >= '0' && deviceName[len(deviceName)-1] <= '9' {
		return fmt.Sprintf("%sp%d", device, partitionNumber)
	}
	// For traditional devices (sda, vda, xvda), just append the number
	return fmt.Sprintf("%s%d", device, partitionNumber)
}

// getPartitionPathFromLsblk uses lsblk to find the actual partition device path
// for a given device and partition number. This handles all device types including
// device mapper devices correctly.
func (o *ops) getPartitionPathFromLsblk(device, partitionNumber string) (string, error) {
	type node struct {
		Name     string  `json:"name"`
		Size     int64   `json:"size"`
		Type     string  `json:"type"`
		Children []*node `json:"children"`
	}
	var disks struct {
		Blockdevices []*node `json:"blockdevices"`
	}

	ret, err := o.ExecPrivilegeCommand(nil, "lsblk", "--bytes", "--json", device)
	if err != nil {
		return "", errors.Wrap(err, "failed to run lsblk command")
	}
	if err = json.Unmarshal([]byte(ret), &disks); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal lsblk output")
	}

	if len(disks.Blockdevices) == 0 {
		return "", errors.Errorf("no block device information returned for %s", device)
	}
	diskNode := disks.Blockdevices[0] // lsblk with device filter returns only that device
	diskType := diskNode.Type

	if len(diskNode.Children) == 0 {
		return "", errors.Errorf("device %s has no partitions", device)
	}

	// Convert partition number to integer for indexing
	partNum, err := strconv.Atoi(partitionNumber)
	if err != nil {
		return "", errors.Errorf("invalid partition number %s", partitionNumber)
	}

	// Partition numbers are 1-based, but array indices are 0-based
	if partNum < 1 || partNum > len(diskNode.Children) {
		return "", errors.Errorf("partition %s not found on device %s", partitionNumber, device)
	}

	partitionName := diskNode.Children[partNum-1].Name
	var devicePath string
	switch diskType {
	case "mpath":
		devicePath = "/dev/mapper/" + partitionName
	default:
		devicePath = "/dev/" + partitionName

	}
	return devicePath, nil
}

func (o *ops) calculateFreePercent(device string, requiredSize *int64) (int64, error) {
	type node struct {
		Name     string
		Size     int64
		Children []*node
	}
	var disks struct {
		Blockdevices []*node
	}
	var (
		diskNode, partitionNode *node
	)
	ret, err := o.ExecPrivilegeCommand(nil, "lsblk", "--bytes", "--json", device)
	if err != nil {
		return 0, errors.Wrap(err, "failed to run lsblk command")
	}
	if err = json.Unmarshal([]byte(ret), &disks); err != nil {
		return 0, errors.Wrap(err, "failed to unmarshal lsblk output")
	}
	if len(disks.Blockdevices) == 0 {
		return 0, errors.Errorf("no block device information returned for %s", device)
	}
	diskNode = disks.Blockdevices[0] // lsblk with device filter returns only that device
	// Find partition 4 (1-based indexing, so array index 3)
	if len(diskNode.Children) < 4 {
		return 0, errors.Errorf("device %s does not have 4 partitions", device)
	}
	var usedSize int64
	funk.ForEach(diskNode.Children, func(n *node) { usedSize += n.Size })

	if requiredSize == nil {
		partitionNode = diskNode.Children[3] // partition 4 is at index 3
		requiredSize = &partitionNode.Size
	}

	// The assumption is that the extra space needed for image overwrite is not more than the existing partition size.  So
	// the partition size will be doubled, and the rest will remain as free space.
	totalRequiredSize := usedSize + *requiredSize
	if totalRequiredSize < diskNode.Size {
		return ((diskNode.Size - totalRequiredSize) * 100) / diskNode.Size, nil
	}
	return 0, nil
}

// copyNetworkRequested reports whether the installation was asked to propagate the live
// environment's NetworkManager configuration to the installed system. Note that the args
// OverwriteOsImage receives have been reduced to kernel arguments, so the original installer args
// have to be read from the config.
func (o *ops) copyNetworkRequested() bool {
	args := o.installerConfig.InstallerArgs
	return slices.Contains(args, "--copy-network") || slices.Contains(args, "-n")
}

// networkKeyfileDir returns the directory coreos-installer was told to copy NetworkManager
// keyfiles from, so that we copy the same files it did. --network-dir overrides the default.
func (o *ops) networkKeyfileDir() string {
	args := o.installerConfig.InstallerArgs
	for i, arg := range args {
		if arg == networkDirArg && i+1 < len(args) {
			return args[i+1]
		}
		if dir, found := strings.CutPrefix(arg, networkDirArg+"="); found {
			return dir
		}
	}
	return networkManagerKeyfileDir
}

// iscsiInstall reports whether the installation disk is reached over iSCSI, which
// assisted-service marks by appending iBFT kernel arguments.
func (o *ops) iscsiInstall() bool {
	for _, arg := range o.installerConfig.InstallerArgs {
		if strings.Contains(arg, "rd.iscsi.firmware") || strings.HasSuffix(arg, ":ibft") {
			return true
		}
	}
	return false
}

func (o *ops) OverwriteOsImage(osImage, device string, extraArgs []string) error {
	// On iSCSI the boot partition lives on the SAN and is unreachable until the network is up,
	// which is why coreos-rescue-misplaced-network is disabled there and why coreos-installer
	// 0.27.0 moved the firstboot network config into the initramfs in the first place. Replacing
	// the deployment would discard that initramfs and leave the node with no way to configure
	// its network, so skip the optimisation and accept the extra MCO reboot instead.
	if o.copyNetworkRequested() && o.iscsiInstall() {
		o.log.Infof("iSCSI installation with --copy-network - skip overwrite image so that the " +
			"firstboot network configuration embedded in the initramfs is preserved")
		return nil
	}

	type cmd struct {
		command string
		args    []string
	}

	makecmd := func(commad string, args ...string) *cmd {
		return &cmd{command: commad, args: args}
	}
	freePercent, err := o.calculateFreePercent(device, nil)
	if err != nil {
		return err
	}
	var growpartcmd *cmd
	if freePercent > 0 {
		growpartcmd = makecmd("growpart", fmt.Sprintf("--free-percent=%d", freePercent), device, "4")
	} else {
		growpartcmd = makecmd("growpart", device, "4")
	}
	ret, err := o.ExecPrivilegeCommand(nil, "uname", "-m")
	if err == nil && ret == "s390x" {
		o.log.Infof("Running on s390x archtiecture - skip overwrite image.")
		return nil
	}

	partition4, err := o.getPartitionPathFromLsblk(device, "4")
	if err != nil {
		return errors.Wrapf(err, "failed to find partition 4 for device %s", device)
	}
	partition3, err := o.getPartitionPathFromLsblk(device, "3")
	if err != nil {
		return errors.Wrapf(err, "failed to find partition 3 for device %s", device)
	}

	cmds := []*cmd{
		makecmd("mount", partition4, "/mnt"),
		makecmd("mount", partition3, "/mnt/boot"),
		growpartcmd,
		makecmd("xfs_growfs", "/mnt"),

		// On 4.14 ostree command fails if selinux is not disabled
		// TODO: find a way to avoid disabling selinux
		makecmd("setenforce", "0"),
		makecmd("ostree",
			append(append([]string{}, "container",
				"image",
				"deploy",
				"--sysroot",
				"/mnt",
				"--authfile",
				dockerConfigFile,
				"--imgref",
				"ostree-unverified-registry:"+osImage,
				"--karg",
				o.ignitionPlatformId(),
				"--karg",
				"$ignition_firstboot",
				"--stateroot",
				"rhcos"), extraArgs...)...,
		),
	}

	// coreos-installer 0.27.0 and later embed the firstboot network configuration into the
	// initramfs of the ostree deployment that exists at install time, which the deploy above has
	// just replaced, so that copy is now gone. Place the keyfiles at the root of the boot
	// partition instead, which is outside any deployment and is where
	// coreos-rescue-misplaced-network looks for them on firstboot. Older coreos-installer
	// versions populate that directory themselves from the same source, so the copy is a no-op
	// there rather than being skipped: rsync is idempotent, which keeps it correct if an earlier
	// attempt failed partway and is retried. Symlinks are dereferenced and subdirectories
	// skipped, so that the same set of files coreos-installer would have embedded ends up on
	// the boot partition, and so that nothing resolvable only in the live environment is left
	// behind as a dangling link for the rescue service to trip over.
	//
	// Mark the configuration as deliberately placed. coreos-rescue-misplaced-network otherwise
	// assumes anything in that directory was left by an outdated coreos-installer and raises a
	// motd telling the operator to reinstall, which would not help here. The marker is a sibling
	// of the directory rather than a file inside it, so that it is not copied into
	// /run/NetworkManager/system-connections along with the keyfiles. It is written whenever
	// --copy-network was requested, including when an older coreos-installer populated the
	// directory itself, since the advice is equally unhelpful in that case.
	if o.copyNetworkRequested() {
		cmds = append(cmds,
			makecmd("rsync", "-avL", "--exclude=*/",
				o.networkKeyfileDir()+"/", firstBootNetworkDirOnBoot+"/"),
			makecmd("touch", firstBootNetworkSentinelOnBoot),
		)
	}

	cmds = append(cmds,
		makecmd("fsfreeze", "--freeze", "/mnt/boot"),
		makecmd("umount", "/mnt/boot"),
		makecmd("fsfreeze", "--freeze", "/mnt"),
		makecmd("umount", "/mnt"),
	)
	for i := range cmds {
		c := cmds[i]
		o.log.Infof("Running %s with args %+v", c.command, c.args)
		if err := utils.Retry(3, 5*time.Second, o.log, func() (ret error) {
			_, ret = o.ExecPrivilegeCommand(nil, c.command, c.args...)
			return
		}); err != nil {
			return errors.Wrapf(err, "failed to run %s with args %+v", c.command, c.args)
		}
	}
	return nil
}

func (o *ops) getRegistryDataSize(dataPath string) (int64, error) {
	// Get size of dataPath directory using du command
	// du -sb outputs: "size_bytes path"
	out, err := o.ExecPrivilegeCommand(nil, "du", "-sb", dataPath)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to get size of %s", dataPath)
	}

	// Parse the output: "size_bytes path" -> extract just the number
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, errors.Errorf("invalid du output for %s: %s", dataPath, out)
	}

	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse size from du output: %s", out)
	}

	return size, nil
}

// TEMPORARY: This function should be REMOVED once MCO provides the IRI registry
// mount unit in the master machineconfig.
//
// Migration path (3 phases):
// Phase 1 (now): AI adds mount unit to ignition (this code)
// Phase 2: MCO adds mount unit to master machineconfig (AI detects and skips)
// Phase 3: Remove this function from AI (MCO is authoritative source)
//
// This function is idempotent: if the mount unit already exists (e.g., from MCO),
// it skips adding it again.
func (o *ops) addRegistryMountUnitToIgnitionTemporary(ignitionPath string) error {
	// Parse existing ignition config
	ign := ignition.NewIgnition()
	config, err := ign.ParseIgnitionFile(ignitionPath)
	if err != nil {
		return errors.Wrapf(err, "failed to parse ignition file %s", ignitionPath)
	}

	// Check if mount unit already exists (idempotency check)
	// This allows smooth migration: MCO can add the mount unit and AI will skip it
	mountUnitName := "var-lib-iri\\x2dregistry.mount"
	for _, unit := range config.Systemd.Units {
		if unit.Name == mountUnitName {
			o.log.Infof("IRI registry mount unit '%s' already present in ignition, skipping (likely from MCO)", mountUnitName)
			return nil
		}
	}

	o.log.Info("Adding IRI registry mount unit to ignition (temporary workaround until MCO provides it)")

	// Create systemd mount unit content
	// Note: The mount unit name MUST match the Where= path with systemd escaping rules:
	// - '/' becomes '-'
	// - '-' in the original path becomes '\x2d'
	// Path /var/lib/iri-registry -> var-lib-iri\x2dregistry.mount
	mountUnitContent := `[Unit]
Description=Mount IRI Registry Partition
DefaultDependencies=no
Conflicts=umount.target
Before=local-fs.target umount.target iri-registry.service

[Mount]
What=/dev/disk/by-partlabel/iri-registry
Where=/var/lib/iri-registry
Type=xfs
Options=defaults

[Install]
WantedBy=local-fs.target
`

	// Create the mount unit
	// Name uses \\x2d (double backslash in Go string) -> becomes \x2d in ignition
	enabled := true
	mountUnit := types.Unit{
		Name:     "var-lib-iri\\x2dregistry.mount",
		Enabled:  &enabled,
		Contents: &mountUnitContent,
	}

	// Add mount unit to systemd units
	config.Systemd.Units = append(config.Systemd.Units, mountUnit)

	// Validate the modified config
	report := validate.ValidateWithContext(*config, nil)
	if report.IsFatal() {
		return errors.Errorf("modified ignition config is invalid: %s", report.String())
	}

	// Write the modified ignition back
	if err := ign.WriteIgnitionFile(ignitionPath, config); err != nil {
		return errors.Wrapf(err, "failed to write modified ignition file %s", ignitionPath)
	}

	o.log.Info("Successfully added IRI registry mount unit to ignition")
	return nil
}

// verifyDiskSpaceForIRI checks if the device has enough free space for the IRI partition
func (o *ops) verifyDiskSpaceForIRI(device string) error {
	type node struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var disks struct {
		Blockdevices []*node `json:"blockdevices"`
	}

	ret, err := o.ExecPrivilegeCommand(nil, "lsblk", "--bytes", "--json", device)
	if err != nil {
		return errors.Wrap(err, "failed to run lsblk command")
	}
	if err = json.Unmarshal([]byte(ret), &disks); err != nil {
		return errors.Wrap(err, "failed to unmarshal lsblk output")
	}

	if len(disks.Blockdevices) == 0 {
		return errors.Errorf("no block device information returned for %s", device)
	}

	totalDiskSize := disks.Blockdevices[0].Size

	// Use default partition config
	partConfig := defaultIRIPartitionConfig

	// Calculate required size (partition + reserved space)
	requiredSize := int64(partConfig.sizeGB) * 1024 * 1024 * 1024
	requiredSize += int64(partConfig.reservedGB) * 1024 * 1024 * 1024

	// We need space for existing partitions + IRI partition + reserved space
	// A rough check: total disk should be significantly larger
	// to account for partitions 1-4 and some free space
	minDiskSize := requiredSize + (20 * 1024 * 1024 * 1024) // IRI + reserved + 20GB for other partitions

	if totalDiskSize < minDiskSize {
		return errors.Errorf("device %s is too small for IRI registry partition (total: %d bytes, requires at least %d bytes for %dGB IRI partition + %dGB reserved + existing partitions)",
			device, totalDiskSize, minDiskSize, partConfig.sizeGB, partConfig.reservedGB)
	}

	o.log.Infof("Disk %s has sufficient space: %d bytes (required: %d bytes for %dGB partition + %dGB reserved)",
		device, totalDiskSize, minDiskSize, partConfig.sizeGB, partConfig.reservedGB)
	return nil
}

// createIRIRegistryPartition creates partition 5 at the end of the disk for IRI registry.
//
// This function executes the following commands:
//
//  1. sfdisk -d /dev/sda
//     Dumps the current partition table to get the GPT last-lba (last usable sector).
//     GPT reserves sectors at the end of the disk for the backup partition table,
//     so we can't use the full disk size.
//
// 2. Calculate partition geometry:
//
//   - Partition size: 150GB = 314572800 sectors (512 bytes/sector)
//
//   - Start sector: last-lba - partition_size + 1
//     This places the partition at the very end of the usable disk space.
//
//     3. (sfdisk -d /dev/sda; echo "/dev/sda5 : start=X, size=Y, type=GUID") | sfdisk --no-reread /dev/sda
//     Creates partition 5 by:
//
//   - Dumping existing partition table
//
//   - Appending new partition 5 definition
//
//   - Writing combined table back to disk
//     Uses --no-reread because disk is in use during installation (partitions 1-4 mounted).
//     GPT type GUID: 0FC63DAF-8483-4772-8E79-3D69D8477DE4 = Linux filesystem
//
//     4. partx -u /dev/sda
//     Updates the kernel's in-memory partition table to recognize the new partition 5.
//     Used instead of partprobe because the disk is actively in use.
//
//     5. udevadm settle
//     Waits for udev to finish processing device events, ensuring /dev/sda5 and
//     /dev/disk/by-partlabel/iri-registry device nodes are created before we proceed.
func (o *ops) createIRIRegistryPartition(device string) error {
	o.log.Infof("Creating IRI registry partition on device %s", device)

	// Step 1: Get partition table dump to extract last-lba
	// Command: sfdisk -d /dev/sda
	dump, err := o.ExecPrivilegeCommand(nil, "sfdisk", "-d", device)
	if err != nil {
		return errors.Wrapf(err, "failed to dump partition table for %s", device)
	}

	// Step 2: Extract last-lba and sector-size from dump
	// GPT reserves sectors at end for backup partition table, so we must use last-lba
	// instead of total disk size to avoid "sector beyond last-lba" errors
	var lastLBA int64
	var sectorSize int64 = 512 // default fallback
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "last-lba:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				lastLBA, err = strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return errors.Wrap(err, "failed to parse last-lba")
				}
			}
		} else if strings.HasPrefix(line, "sector-size:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				sectorSize, err = strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return errors.Wrap(err, "failed to parse sector-size")
				}
			}
		}
	}
	if lastLBA == 0 {
		return errors.Errorf("could not find last-lba in partition table for %s", device)
	}

	// Step 3: Calculate partition geometry in sectors
	// Use default partition config (future: may come from installer config)
	partConfig := defaultIRIPartitionConfig

	partitionSizeBytes := int64(partConfig.sizeGB) * 1024 * 1024 * 1024
	partitionSizeSectors := partitionSizeBytes / sectorSize

	// Calculate reserved space (space to leave unallocated after partition)
	reservedSpaceBytes := int64(partConfig.reservedGB) * 1024 * 1024 * 1024
	reservedSpaceSectors := reservedSpaceBytes / sectorSize

	// Leave margin for GPT backup table (2048 sectors = 1MB)
	// This prevents "Secondary partition table overlaps" error from coreos-gpt-setup
	// The GPT backup table needs space at the end of disk, we leave 1MB before last-lba
	gptBackupMargin := int64(2048)

	// Calculate start sector: last-lba - partition_size - reserved_space - gpt_margin + 1
	// This ensures partition ends at (last-lba - gpt_margin), leaving space for GPT backup
	startSector := lastLBA - partitionSizeSectors - reservedSpaceSectors - gptBackupMargin + 1

	o.log.Infof("Creating %dGB partition (last-lba: %d, start: %d, size: %d sectors, reserved: %dGB)",
		partConfig.sizeGB, lastLBA, startSector, partitionSizeSectors, partConfig.reservedGB)

	// Step 4: Create partition using sfdisk dump + append approach
	// Command: (sfdisk -d /dev/sda; echo "/dev/sda5 : start=X, size=Y, type=GUID, name=LABEL") | sfdisk --no-reread /dev/sda
	// GPT type GUID: 0FC63DAF-8483-4772-8E79-3D69D8477DE4 = Linux filesystem
	// name sets the partition label (creates /dev/disk/by-partlabel/iri-registry symlink)
	// Use makePartitionDevicePath to handle different device types (nvme0n1p5, sda5, etc.)
	partitionDevicePath := makePartitionDevicePath(device, 5)
	newPartLine := fmt.Sprintf("%s : start=%d, size=%d, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"%s\"",
		partitionDevicePath, startSector, partitionSizeSectors, iriRegistryPartitionLabel)
	sfdiskCmd := fmt.Sprintf("(sfdisk -d %s; echo '%s') | sfdisk --no-reread %s", device, newPartLine, device)

	o.log.Infof("Running: %s", sfdiskCmd)
	_, err = o.ExecPrivilegeCommand(o.logWriter, "sh", "-c", sfdiskCmd)
	if err != nil {
		return errors.Wrapf(err, "failed to create partition 5 on device %s", device)
	}

	// Step 5: Update kernel partition table
	// Command: partx -u /dev/sda
	// Disk is in use (partitions 1-4 mounted), so we use partx instead of partprobe
	o.log.Infof("Updating kernel partition table with partx")
	_, err = o.ExecPrivilegeCommand(o.logWriter, "partx", "-u", device)
	if err != nil {
		return errors.Wrapf(err, "failed to update kernel partition table for %s", device)
	}

	// Wait for udev to finish processing device events
	o.log.Info("Waiting for udev to settle")
	_, err = o.ExecPrivilegeCommand(o.logWriter, "udevadm", "settle")
	if err != nil {
		return errors.Wrapf(err, "failed to wait for udev to settle after creating partition on %s", device)
	}

	// Get the partition 5 device path
	partition5, err := o.getPartitionPathFromLsblk(device, "5")
	if err != nil {
		return errors.Wrapf(err, "failed to find partition 5 on device %s after creation", device)
	}

	o.log.Infof("Partition 5 created at %s, formatting with XFS", partition5)

	// Format partition 5 with XFS
	_, err = o.ExecPrivilegeCommand(o.logWriter, "mkfs.xfs", "-L", iriRegistryPartitionLabel, partition5)
	if err != nil {
		return errors.Wrapf(err, "failed to format partition %s with XFS", partition5)
	}

	o.log.Infof("Successfully created and formatted IRI registry partition at %s", partition5)
	return nil
}

// copyRegistryDataToPartition mounts partition 5 and copies registry data to it
func (o *ops) copyRegistryDataToPartition(liveLogger io.Writer, device string) error {
	o.log.Info("Copying registry data to IRI partition")

	// Get partition 5 path
	partition5, err := o.getPartitionPathFromLsblk(device, "5")
	if err != nil {
		return errors.Wrapf(err, "failed to find partition 5 on device %s", device)
	}

	// Create temporary mount point
	if err := o.Mkdir(iriRegistryTempMount); err != nil {
		return errors.Wrapf(err, "failed to create temporary mount point %s", iriRegistryTempMount)
	}

	// Mount partition 5
	o.log.Infof("Mounting %s to %s", partition5, iriRegistryTempMount)
	_, err = o.ExecPrivilegeCommand(o.logWriter, "mount", partition5, iriRegistryTempMount)
	if err != nil {
		return errors.Wrapf(err, "failed to mount %s to %s", partition5, iriRegistryTempMount)
	}

	// Ensure unmount on exit
	defer func() {
		o.log.Infof("Unmounting %s", iriRegistryTempMount)
		if _, unmountErr := o.ExecPrivilegeCommand(o.logWriter, "umount", iriRegistryTempMount); unmountErr != nil {
			o.log.WithError(unmountErr).Warnf("Failed to unmount %s", iriRegistryTempMount)
		}
	}()

	// Copy registry data using rsync with progress reporting
	// Source: /mnt/agentdata/  (registryDataDirOnMnt)
	// Destination: /mnt/iri-registry/
	rsyncCmd := fmt.Sprintf("rsync -ah --info=progress2 %s/ %s/", registryDataDirOnMnt, iriRegistryTempMount)

	o.log.Infof("Running rsync to copy registry data")
	_, err = o.ExecPrivilegeCommand(liveLogger, "sh", "-c", rsyncCmd)
	if err != nil {
		return errors.Wrapf(err, "failed to copy registry data from %s to %s", registryDataDirOnMnt, iriRegistryTempMount)
	}

	o.log.Info("Successfully copied registry data to IRI partition")
	return nil
}

// PrepareIRIRegistryIgnition adds the IRI registry mount unit to ignition if not already present.
// This must be called BEFORE writeImageToDisk() so that coreos-installer
// embeds the modified ignition with the mount unit into the disk image.
//
// TEMPORARY: This function is idempotent and will be removed once MCO provides
// the mount unit in the master machineconfig (3-phase migration):
// Phase 1 (now): AI adds mount unit
// Phase 2: MCO adds mount unit (AI detects and skips)
// Phase 3: Remove this function from AI
func (o *ops) PrepareIRIRegistryIgnition(ignitionPath string) error {
	// Check if IRI registry setup is applicable
	if !o.FileExists("/run/media/iso/registry") || o.installerConfig.Role != string(models.HostRoleMaster) {
		o.log.Info("Skipping IRI registry ignition preparation (not applicable)")
		return nil
	}

	o.log.Info("Preparing IRI registry mount unit in ignition")

	// Add systemd mount unit to ignition
	// This must happen BEFORE coreos-installer writes the image to disk
	if err := o.addRegistryMountUnitToIgnitionTemporary(ignitionPath); err != nil {
		return err
	}

	o.log.Info("Successfully prepared IRI registry ignition")
	return nil
}

// SetupIRIRegistryPartition creates partition 5 and copies IRI registry data.
// This must be called AFTER writeImageToDisk() because:
// 1. coreos-installer creates partitions 1-4 during writeImageToDisk
// 2. We create partition 5 at the end of the disk after partitions 1-4 exist
//
// The function performs the following steps:
// 1. Verifies disk has sufficient space (150GB minimum + 20GB overhead)
// 2. Creates partition 5 at the END of the disk (preserves root auto-expansion)
// 3. Formats partition 5 with XFS filesystem
// 4. Copies registry data from /run/media/iso/registry to partition 5
func (o *ops) SetupIRIRegistryPartition(device string, liveLogger io.Writer, updateProgress func(models.HostStage, string)) error {
	// Check if IRI registry setup is applicable
	if !o.FileExists("/run/media/iso/registry") || o.installerConfig.Role != string(models.HostRoleMaster) {
		o.log.Info("Skipping IRI registry partition setup (not applicable)")
		return nil
	}

	o.log.Info("Starting IRI registry partition setup")

	// Step 1: Verify disk space and create partition 5
	updateProgress(models.HostStageCopyingRegistryDataToDisk, "Creating IRI registry partition")
	o.log.Infof("Verifying disk space and creating partition 5 on %s", device)

	if err := o.verifyDiskSpaceForIRI(device); err != nil {
		return err
	}

	if err := o.createIRIRegistryPartition(device); err != nil {
		return err
	}

	// Step 2: Copy registry data to partition 5
	updateProgress(models.HostStageCopyingRegistryDataToDisk, "Copying registry data to partition")
	o.log.Info("Copying registry data to partition 5")

	if err := o.copyRegistryDataToPartition(liveLogger, device); err != nil {
		return err
	}

	o.log.Info("IRI registry partition setup completed successfully")
	return nil
}

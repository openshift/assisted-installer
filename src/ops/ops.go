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
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"

	config_latest "github.com/coreos/ignition/v2/config/v3_2"
	"github.com/go-openapi/swag"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/thoas/go-funk"
	"github.com/vincent-petithory/dataurl"

	"github.com/openshift/assisted-service/models"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/openshift/assisted-installer/src/utils"
)

const (
	coreosInstallerExecutable       = "coreos-installer"
	dryRunCoreosInstallerExecutable = "dry-installer"
	encapsulatedMachineConfigFile   = "/etc/ignition-machine-config-encapsulated.json"
	defaultIgnitionPlatformId       = "ignition.platform.id=metal"
	ignitionContent                 = "application/vnd.coreos.ignition+json; version=3.4.0"
)

//go:generate mockgen -source=ops.go -package=ops -destination=mock_ops.go
type Ops interface {
	Mkdir(dirName string) error
	WriteImageToDisk(liveLogger io.Writer, ignitionPath string, device string, extraArgs []string) error
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
	ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	ReadFile(filePath string) ([]byte, error)
	GetEncapsulatedMC(ignitionPath string) (*mcfgv1.MachineConfig, error)
	OverwriteOsImage(osImage, device string, extraArgs []string) error
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
	efiDirname, err := o.findEfiDirectory(device)
	if err != nil {
		o.log.WithError(err).Error("failed to find EFI directory")
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
	if _, err = o.ExecPrivilegeCommand(nil, "mount", partitionForDevice(device, "2"), "/mnt"); err != nil {
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
		"HaMode":               o.installerConfig.HighAvailabilityMode,
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
	}

	if o.installerConfig.ServiceIPs != "" {
		params["ServiceIPs"] = strings.Split(o.installerConfig.ServiceIPs, ",")
	}

	if o.installerConfig.HighAvailabilityMode == models.ClusterHighAvailabilityModeNone {
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

// This function actually runs container that imeplements logs_sender command
// Any change to the assisted-service API that is used by the logs_sender command
// ( for example UploadLogs), must be reflected here (input parameters, etc'),
// if needed
func (o *ops) UploadInstallationLogs(isBootstrap bool) (string, error) {
	command := "podman"

	args := []string{"run", "--rm", "--privileged", "--net=host", "--pid=host", "-v",
		"/run/systemd/journal/socket:/run/systemd/journal/socket",
		"-v", "/var/log:/var/log"}

	if o.installerConfig.CACertPath != "" {
		args = append(args, "-v", fmt.Sprintf("%[1]s:%[1]s", o.installerConfig.CACertPath))
	}

	args = append(args, o.installerConfig.AgentImage, "logs_sender",
		"-cluster-id", o.installerConfig.ClusterID, "-url", o.installerConfig.URL,
		"-host-id", o.installerConfig.HostID, "-infra-env-id", o.installerConfig.InfraEnvID,
		"-pull-secret-token", o.installerConfig.PullSecretToken,
		fmt.Sprintf("-insecure=%s", strconv.FormatBool(o.installerConfig.SkipCertVerification)),
		fmt.Sprintf("-bootstrap=%s", strconv.FormatBool(isBootstrap)))

	if o.installerConfig.CACertPath != "" {
		args = append(args, fmt.Sprintf("-cacert=%s", o.installerConfig.CACertPath))
	}
	return o.ExecPrivilegeCommand(o.logWriter, command, args...)
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
	out, err := o.executor.ExecCommandWithContext(ctx, o.logWriter, "oc",
		"--kubeconfig",
		kubeconfigPath,
		"debug",
		fmt.Sprintf("node/%s", nodeName),
		"--",
		"chroot",
		"/host",
		"last",
		"reboot")
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

func (o *ops) GetMustGatherLogs(workDir, kubeconfigPath string, images ...string) (string, error) {
	//invoke oc adm must-gather command in the working directory
	var imageOption string = ""
	for _, img := range images {
		imageOption = imageOption + fmt.Sprintf(" --image=%s", img)
	}

	command := fmt.Sprintf("cd %s && oc --kubeconfig=%s adm must-gather%s", workDir, kubeconfigPath, imageOption)
	output, err := o.executor.ExecCommand(o.logWriter, "bash", "-c", command)
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
		o.log.Errorf(lerr.Error())
		return "", lerr
	}
	logsDir := filepath.Base(files[0])

	//tar the log directory and return the path to the tarball
	command = fmt.Sprintf("cd %s && tar zcf %s %s", workDir, MustGatherFileName, logsDir)
	_, err = o.executor.ExecCommand(o.logWriter, "bash", "-c", command)
	if err != nil {
		o.log.WithError(err).Errorf("Failed to tar must-gather logs\n")
		return "", err
	}
	return path.Join(workDir, MustGatherFileName), nil
}

func (o *ops) CreateRandomHostname(hostname string) error {
	command := fmt.Sprintf("echo %s > /etc/hostname", hostname)
	o.log.Infof("create random hostname with command %s", command)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "bash", "-c", command)
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
	command := fmt.Sprintf("oc --kubeconfig=%s apply -f %s", kubeconfig, file.Name())
	output, err := o.executor.ExecCommand(o.logWriter, "bash", "-c", command)
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
	_, err := o.ExecPrivilegeCommand(nil, "stat", markerPath)

	return err == nil
}

// ExecPrivilegeCommand execute a command in the host environment via nsenter
func (o *ops) ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error) {
	// nsenter is used here to launch processes inside the container in a way that makes said processes feel
	// and behave as if they're running on the host directly rather than inside the container
	commandBase := "nsenter"

	arguments := []string{
		"--target", "1",
		// Entering the cgroup namespace is not required for podman on CoreOS (where the
		// agent typically runs), but it's needed on some Fedora versions and
		// some other systemd based systems. Those systems are used to run dry-mode
		// agents for load testing. If this flag is not used, Podman will sometimes
		// have trouble creating a systemd cgroup slice for new containers.
		"--cgroup",
		// The mount namespace is required for podman to access the host's container
		// storage
		"--mount",
		// TODO: Document why we need the IPC namespace
		"--ipc",
		"--pid",
		"--",
		command,
	}

	arguments = append(arguments, args...)
	return o.executor.ExecCommand(liveLogger, commandBase, arguments...)
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
	if ca == "" {
		return nil, errors.New("CA cert is empty")
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM([]byte(ca))
	tr := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    caCertPool,
	}}
	client := http.Client{Transport: tr}
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

func partitionNameForDeviceName(deviceName, partitionNumber string) string {
	var format string
	switch {
	case strings.HasPrefix(deviceName, "nvme"):
		format = "%sp%s"
	case strings.HasPrefix(deviceName, "mmcblk"):
		format = "%sP%s"
	default:
		format = "%s%s"
	}
	return fmt.Sprintf(format, deviceName, partitionNumber)
}

func partitionForDevice(device, partitionNumber string) string {
	return "/dev/" + partitionNameForDeviceName(stripDev(device), partitionNumber)
}

func (o *ops) calculateFreePercent(device string) (int64, error) {
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
		ok                      bool
	)
	ret, err := o.ExecPrivilegeCommand(nil, "lsblk", "-b", "-J")
	if err != nil {
		return 0, errors.Wrap(err, "failed to run lsblk command")
	}
	if err = json.Unmarshal([]byte(ret), &disks); err != nil {
		return 0, errors.Wrap(err, "failed to unmarshal lsblk output")
	}
	deviceName := stripDev(device)
	diskNode, ok = funk.Find(disks.Blockdevices, func(n *node) bool { return deviceName == n.Name }).(*node)
	if !ok {
		return 0, errors.Errorf("failed to find device is %s in lsblk output", device)
	}
	partitionName := partitionNameForDeviceName(diskNode.Name, "4")
	partitionNode, ok = funk.Find(diskNode.Children, func(n *node) bool { return partitionName == n.Name }).(*node)
	if !ok {
		return 0, errors.Errorf("failed to find partition node %s in lsblk output", device)
	}
	var usedSize int64
	funk.ForEach(diskNode.Children, func(n *node) { usedSize += n.Size })

	// The assumption is that the extra space needed for image overwrite is not more than the existing partition size.  So
	// the partition size will be doubled, and the rest will remain as free space.
	totalRequiredSize := usedSize + partitionNode.Size
	if totalRequiredSize < diskNode.Size {
		return ((diskNode.Size - totalRequiredSize) * 100) / diskNode.Size, nil
	}
	return 0, nil
}

func (o *ops) OverwriteOsImage(osImage, device string, extraArgs []string) error {
	type cmd struct {
		command string
		args    []string
	}

	makecmd := func(commad string, args ...string) *cmd {
		return &cmd{command: commad, args: args}
	}
	freePercent, err := o.calculateFreePercent(device)
	if err != nil {
		return err
	}
	var growpartcmd *cmd
	if freePercent > 0 {
		growpartcmd = makecmd("growpart", fmt.Sprintf("--free-percent=%d", freePercent), device, "4")
	} else {
		growpartcmd = makecmd("growpart", device, "4")
	}
	cmds := []*cmd{
		makecmd("mount", partitionForDevice(device, "4"), "/mnt"),
		makecmd("mount", partitionForDevice(device, "3"), "/mnt/boot"),
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
				"/root/.docker/config.json",
				"--imgref",
				"ostree-unverified-registry:"+osImage,
				"--karg",
				o.ignitionPlatformId(),
				"--karg",
				"$ignition_firstboot",
				"--stateroot",
				"rhcos"), extraArgs...)...,
		),
		makecmd("fsfreeze", "--freeze", "/mnt/boot"),
		makecmd("umount", "/mnt/boot"),
		makecmd("fsfreeze", "--freeze", "/mnt"),
		makecmd("umount", "/mnt"),
	}
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

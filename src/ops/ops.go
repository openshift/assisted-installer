package ops

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"text/template"

	"github.com/openshift/assisted-installer/src/ops/execute"

	"io/ioutil"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/utils"
)

const (
	coreosInstallerExecutable       = "coreos-installer"
	dryRunCoreosInstallerExecutable = "dry-installer"
)

//go:generate mockgen -source=ops.go -package=ops -destination=mock_ops.go
type Ops interface {
	Mkdir(dirName string) error
	WriteImageToDisk(ignitionPath string, device string, progressReporter inventory_client.InventoryClient, extra []string) error
	Reboot() error
	SetBootOrder(device string) error
	ExtractFromIgnition(ignitionPath string, fileToExtract string) error
	SystemctlAction(action string, args ...string) error
	PrepareController() error
	GetVGByPV(pvName string) (string, error)
	RemoveVG(vgName string) error
	RemoveLV(lvName, vgName string) error
	RemovePV(pvName string) error
	Wipefs(device string) error
	IsRaidMember(device string) bool
	GetRaidDevices(device string) ([]string, error)
	CleanRaidMembership(device string) error
	GetMCSLogs() (string, error)
	UploadInstallationLogs(isBootstrap bool) (string, error)
	ReloadHostFile(filepath string) error
	CreateOpenshiftSshManifest(filePath, template, sshPubKeyPath string) error
	GetMustGatherLogs(workDir, kubeconfigPath string, images ...string) (string, error)
	CreateRandomHostname(hostname string) error
	GetHostname() (string, error)
	EvaluateDiskSymlink(string) string
	FormatDisk(string) error
	CreateManifests(string, []byte) error
	DryRebootHappened(markerPath string) bool
	ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error)
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

func (o *ops) WriteImageToDisk(ignitionPath string, device string, progressReporter inventory_client.InventoryClient, extraArgs []string) error {
	allArgs := installerArgs(ignitionPath, device, extraArgs)
	o.log.Infof("Writing image and ignition to disk with arguments: %v", allArgs)

	installerExecutable := coreosInstallerExecutable
	if o.installerConfig.DryRunEnabled {
		// In dry run, we use an executable called dry-installer rather than coreos-installer.
		// This executable is expected to pretend to be doing coreos-installer stuff and print fake
		// progress. It's up to the dry-mode user to make sure such executable is available in PATH
		installerExecutable = dryRunCoreosInstallerExecutable
	}

	_, err := o.ExecPrivilegeCommand(NewCoreosInstallerLogWriter(o.log, progressReporter, o.installerConfig.InfraEnvID, o.installerConfig.HostID),
		installerExecutable, allArgs...)
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

func (o *ops) Reboot() error {
	o.log.Info("Rebooting node")
	_, err := o.ExecPrivilegeCommand(o.logWriter, "shutdown", "-r", "+1", "'Installation completed, server is going to reboot.'")
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

	_, err := o.ExecPrivilegeCommand(nil, "test", "-d", "/sys/firmware/efi")
	if err != nil {
		o.log.Info("setting the boot order on BIOS systems is not supported. Skipping...")
		return nil
	}

	o.log.Info("Setting efibootmgr to boot from disk")

	// efi-system is installed onto partition 2
	out, err := o.ExecPrivilegeCommand(o.logWriter, "efibootmgr", "-v", "-d", device, "-p", "2", "-c", "-L", "Red Hat Enterprise Linux", "-l", o.getEfiFilePath())
	if err != nil {
		o.log.Errorf("Failed to set efibootmgr to boot from disk %s, err: %s", device, err)
		return err
	}
	o.handleDuplicateEntries(out)
	return nil
}

func (o *ops) handleDuplicateEntries(output string) {
	r := regexp.MustCompile(`Boot(.*) has same label Red Hat Enterprise Linux`)
	for _, line := range strings.Split(output, "\n") {
		DupBootEntry := r.FindStringSubmatch(line)
		if len(DupBootEntry) > 0 {
			o.log.Infof("Found duplicate value in boot manager: %s", line)
			o.deleteBootEntry(DupBootEntry[len(DupBootEntry)-1])
		}
	}
}

func (o *ops) deleteBootEntry(bootNum string) {
	o.log.Infof("Removing boot entry number %s", bootNum)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "efibootmgr", "-v", "--delete-bootnum", "--bootnum", bootNum, "-l", o.getEfiFilePath())
	if err != nil {
		o.log.Errorf("Failed to delete duplicate Red Hat Enterprise Linux label %s, err: %s", bootNum, err)
	}
}

func (o *ops) getEfiFilePath() string {
	var efiFileName string
	switch runtime.GOARCH {
	case "arm64":
		efiFileName = "shimaa64.efi"
	default:
		efiFileName = "shimx64.efi"
	}
	o.log.Infof("Using EFI file '%s' for GOARCH '%s'", efiFileName, runtime.GOARCH)
	return fmt.Sprintf("\\EFI\\redhat\\%s", efiFileName)
}

func (o *ops) ExtractFromIgnition(ignitionPath string, fileToExtract string) error {
	if o.installerConfig.DryRunEnabled {
		return nil
	}

	o.log.Infof("Getting data from %s", ignitionPath)
	ignitionData, err := ioutil.ReadFile(ignitionPath)
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
	err = ioutil.WriteFile(tmpFile, extractedContent, 0644)
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

	return o.renderDeploymentFiles(filepath.Join(controllerDeployFolder, controllerDeployPodTemplate),
		params, renderedControllerPod)
}

func (o *ops) renderDeploymentFiles(srcTemplate string, params map[string]interface{}, dest string) error {
	templateData, err := ioutil.ReadFile(srcTemplate)
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
	if err = ioutil.WriteFile(renderedControllerYaml, buf.Bytes(), 0644); err != nil {
		o.log.Errorf("Error occurred while trying to write rendered data to %s : %e", renderedControllerYaml, err)
		return err
	}
	return nil
}

func (o *ops) GetVGByPV(pvName string) (string, error) {
	output, err := o.ExecPrivilegeCommand(o.logWriter, "vgs", "--noheadings", "-o", "vg_name,pv_name")
	if err != nil {
		o.log.Errorf("Failed to list VGs in the system")
		return "", err
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		res := strings.Fields(line)
		if len(res) < 2 {
			continue
		}

		if strings.Contains(res[1], pvName) {
			return res[0], nil
		}
	}
	return "", nil
}

func (o *ops) RemoveVG(vgName string) error {
	output, err := o.ExecPrivilegeCommand(o.logWriter, "vgremove", vgName, "-y")
	if err != nil {
		o.log.Errorf("Failed to remove VG %s, output %s, error %s", vgName, output, err)
	}
	return err
}

func (o *ops) RemoveLV(lvName, vgName string) error {
	output, err := o.ExecPrivilegeCommand(o.logWriter, "lvremove", fmt.Sprintf("/dev/%s/%s", vgName, lvName), "-y")
	if err != nil {
		o.log.Errorf("Failed to remove LVM %s, output %s, error %s", fmt.Sprintf("/dev/%s/%s", vgName, lvName), output, err)
	}
	return err
}

func (o *ops) RemovePV(pvName string) error {
	output, err := o.ExecPrivilegeCommand(o.logWriter, "pvremove", pvName, "-y", "-ff")
	if err != nil {
		o.log.Errorf("Failed to remove PV %s, output %s, error %s", pvName, output, err)
	}
	return err
}

func (o *ops) Wipefs(device string) error {
	_, err := o.ExecPrivilegeCommand(o.logWriter, "wipefs", "--all", "--force", device)

	if err != nil {
		_, err = o.ExecPrivilegeCommand(o.logWriter, "wipefs", "--all", device)
	}

	return err
}

func (o *ops) IsRaidMember(device string) bool {
	raidDevices, err := o.getRaidDevices2Members()

	if err != nil {
		o.log.WithError(err).Errorf("Error occurred while trying to get list of raid devices - continue without cleaning")
		return false
	}

	// The device itself or one of its partitions
	expression, _ := regexp.Compile(device + "[\\d]*")

	for _, raidArrayMembers := range raidDevices {
		for _, raidMember := range raidArrayMembers {
			if expression.MatchString(raidMember) {
				return true
			}
		}
	}

	return false
}

func (o *ops) CleanRaidMembership(device string) error {
	raidDevices, err := o.getRaidDevices2Members()

	if err != nil {
		return err
	}

	for raidDeviceName, raidArrayMembers := range raidDevices {
		err = o.removeDeviceFromRaidArray(device, raidDeviceName, raidArrayMembers)

		if err != nil {
			return err
		}
	}

	return nil
}

func (o *ops) GetRaidDevices(deviceName string) ([]string, error) {
	raidDevices, err := o.getRaidDevices2Members()
	var result []string

	if err != nil {
		return result, err
	}

	for raidDeviceName, raidArrayMembers := range raidDevices {
		expression, _ := regexp.Compile(deviceName + "[\\d]*")

		for _, raidMember := range raidArrayMembers {
			// A partition or the device itself is part of the raid array.
			if expression.MatchString(raidMember) {
				result = append(result, raidDeviceName)
				break
			}
		}
	}

	return result, nil
}

func (o *ops) getRaidDevices2Members() (map[string][]string, error) {
	output, err := o.ExecPrivilegeCommand(o.logWriter, "mdadm", "-v", "--query", "--detail", "--scan")

	if err != nil {
		return nil, err
	}

	lines := strings.Split(output, "\n")
	result := make(map[string][]string)

	/*
		The output pattern is:
		ARRAY /dev/md0 level=raid1 num-devices=2 metadata=1.2 name=0 UUID=77e1b6f2:56530ebd:38bd6808:17fd01c4
		   devices=/dev/vda2,/dev/vda3
		ARRAY /dev/md1 level=raid1 num-devices=1 metadata=1.2 name=1 UUID=aad7aca9:81db82f3:2f1fedb1:f89ddb43
		   devices=/dev/vda1
	*/
	for i := 0; i < len(lines); {
		if !strings.Contains(lines[i], "ARRAY") {
			i++
			continue
		}

		fields := strings.Fields(lines[i])
		raidDeviceName := fields[1]
		i++

		// Ensuring that we have at least two lines per device.
		if len(lines) == i {
			break
		}

		raidArrayMembersStr := strings.TrimSpace(lines[i])
		prefix := "devices="

		if !strings.HasPrefix(raidArrayMembersStr, prefix) {
			continue
		}

		raidArrayMembersStr = raidArrayMembersStr[len(prefix):]
		result[raidDeviceName] = strings.Split(raidArrayMembersStr, ",")
		i++
	}

	return result, nil
}

func (o *ops) removeDeviceFromRaidArray(deviceName string, raidDeviceName string, raidArrayMembers []string) error {
	raidStopped := false

	expression, _ := regexp.Compile(deviceName + "[\\d]*")

	for _, raidMember := range raidArrayMembers {
		// A partition or the device itself is part of the raid array.
		if expression.MatchString(raidMember) {
			// Stop the raid device.
			if !raidStopped {
				o.log.Info("Stopping raid device: " + raidDeviceName)
				_, err := o.ExecPrivilegeCommand(o.logWriter, "mdadm", "--stop", raidDeviceName)

				if err != nil {
					return err
				}

				raidStopped = true
			}

			// Clean the raid superblock from the device
			o.log.Infof("Cleaning raid member %s superblock", raidMember)
			_, err := o.ExecPrivilegeCommand(o.logWriter, "mdadm", "--zero-superblock", raidMember)

			if err != nil {
				return err
			}
		}
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
	logs, err := ioutil.ReadFile(files[0])
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
	file, err := ioutil.TempFile("", "operator-manifest")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	// Write the content to the temporary file:
	if err = ioutil.WriteFile(file.Name(), content, 0644); err != nil {
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

func installerArgs(ignitionPath string, device string, extra []string) []string {
	allArgs := []string{"install", "--insecure", "-i", ignitionPath}
	if extra != nil {
		allArgs = append(allArgs, extra...)
	}
	return append(allArgs, device)
}

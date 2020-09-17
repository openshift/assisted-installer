package ops

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/template"

	"github.com/openshift/assisted-installer/src/inventory_client"

	"github.com/openshift/assisted-installer/src/config"

	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/openshift/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

//go:generate mockgen -source=ops.go -package=ops -destination=mock_ops.go
type Ops interface {
	ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	Mkdir(dirName string) error
	WriteImageToDisk(ignitionPath string, device string, image string, progressReporter inventory_client.InventoryClient) error
	Reboot() error
	ExtractFromIgnition(ignitionPath string, fileToExtract string) error
	SetFileInIgnition(ignitionPath, filePath, contents string, mode int) error
	SystemctlAction(action string, args ...string) error
	PrepareController() error
	GetVGByPV(pvName string) (string, error)
	RemoveVG(vgName string) error
	RemoveLV(lvName, vgName string) error
	RemovePV(pvName string) error
	GetMCSLogs() (string, error)
	UploadInstallationLogs(isBootstrap bool) (string, error)
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
)

type ops struct {
	log       *logrus.Logger
	logWriter *utils.LogWriter
	cmdEnv    []string
}

// NewOps return a new ops interface
func NewOps(logger *logrus.Logger, proxySet bool) Ops {
	cmdEnv := os.Environ()
	if proxySet && (config.GlobalConfig.HTTPProxy != "" || config.GlobalConfig.HTTPSProxy != "") {
		if config.GlobalConfig.HTTPProxy != "" {
			cmdEnv = append(cmdEnv, fmt.Sprintf("HTTP_PROXY=%s", config.GlobalConfig.HTTPProxy))
		}
		if config.GlobalConfig.HTTPSProxy != "" {
			cmdEnv = append(cmdEnv, fmt.Sprintf("HTTPS_PROXY=%s", config.GlobalConfig.HTTPSProxy))
		}
		if config.GlobalConfig.NoProxy != "" {
			cmdEnv = append(cmdEnv, fmt.Sprintf("NO_PROXY=%s", config.GlobalConfig.NoProxy))
		}
	}
	return &ops{logger, utils.NewLogWriter(logger), cmdEnv}
}

// ExecPrivilegeCommand execute a command in the host environment via nsenter
func (o *ops) ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error) {
	commandBase := "nsenter"
	arguments := []string{"-t", "1", "-m", "-i", "--", command}
	arguments = append(arguments, args...)
	return o.ExecCommand(liveLogger, commandBase, arguments...)
}

// ExecCommand executes command.
func (o *ops) ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error) {

	var stdoutBuf bytes.Buffer
	cmd := exec.Command(command, args...)
	if liveLogger != nil {
		cmd.Stdout = io.MultiWriter(liveLogger, &stdoutBuf)
		cmd.Stderr = io.MultiWriter(liveLogger, &stdoutBuf)
	} else {
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stdoutBuf
	}
	cmd.Env = o.cmdEnv
	err := cmd.Run()
	output := strings.TrimSpace(stdoutBuf.String())
	if err != nil {

		// Get all lines from Error message
		errorIndex := strings.Index(output, "Error")
		// if Error not found return all output
		if errorIndex > -1 {
			output = output[errorIndex:]
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				o.log.Debugf("failed executing %s %v, env vars %v, out: %s, with status %d",
					command, args, output, cmd.Env, status.ExitStatus())

				return output, fmt.Errorf("failed executing %s %v, env vars %v, error %s. Output %s", command, args, cmd.Env, exitErr, output)
			}
		}
		return output, fmt.Errorf("failed executing %s %v, env vars %v, error %s. Output %s", command, args, cmd.Env, err, output)
	}
	o.log.Debug("Command executed:", " command", command, " arguments", args, "env vars", cmd.Env, "output", output)
	return output, err
}

func (o *ops) Mkdir(dirName string) error {
	o.log.Infof("Creating directory: %s", dirName)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "mkdir", "-p", dirName)
	return err
}

func (o *ops) SystemctlAction(action string, args ...string) error {
	o.log.Infof("Running systemctl %s %s", action, args)
	_, err := o.ExecPrivilegeCommand(o.logWriter, "systemctl", append([]string{action}, args...)...)
	if err != nil {
		o.log.Errorf("Failed to executing systemctl %s %s", action, args)
	}
	return err
}

func (o *ops) WriteImageToDisk(ignitionPath string, device string, image string, progressReporter inventory_client.InventoryClient) error {
	o.log.Info("Writing image and ignition to disk")
	_, err := o.ExecPrivilegeCommand(NewCoreosInstallerLogWriter(o.log, progressReporter, config.GlobalConfig.HostID),
		"coreos-installer", "install", "--image-url", image, "--insecure", "-i", ignitionPath, device)
	return err
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

func (o *ops) ExtractFromIgnition(ignitionPath string, fileToExtract string) error {
	o.log.Infof("Getting pull secret from %s", ignitionPath)
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

func (o *ops) SetFileInIgnition(ignitionPath, filePath, contents string, mode int) error {
	o.log.Infof("Setting  file %s, mode %d in ignition %s", filePath, mode, ignitionPath)
	ignitionData, err := ioutil.ReadFile(ignitionPath)
	if err != nil {
		o.log.Errorf("Error occurred while trying to read %s : %e", ignitionPath, err)
		return err
	}

	newIgnitionData, err := utils.SetFileInIgnition(ignitionData, filePath, contents, mode)
	if err != nil {
		o.log.Errorf("Failed to set new file data %s into ignition data", filePath)
		return err
	}

	err = ioutil.WriteFile(ignitionPath, newIgnitionData, os.ModePerm)
	if err != nil {
		o.log.Errorf("Failed to write new ignition to %s", ignitionPath)
		return err
	}

	return nil
}

func (o *ops) PrepareController() error {

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
	files, err := utils.GetListOfFilesFromFolder(controllerDeployFolder, "*.yaml")
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
	var params = map[string]string{
		"InventoryUrl":         config.GlobalConfig.URL,
		"ClusterId":            config.GlobalConfig.ClusterID,
		"SkipCertVerification": strconv.FormatBool(config.GlobalConfig.SkipCertVerification),
		"CACertPath":           config.GlobalConfig.CACertPath,
	}

	return o.renderDeploymentFiles(filepath.Join(controllerDeployFolder, controllerDeployCmTemplate),
		params, renderedControllerCm)
}

func (o *ops) renderControllerSecret() error {
	var params = map[string]string{
		"PullSecretToken": config.GlobalConfig.PullSecretToken,
	}

	return o.renderDeploymentFiles(filepath.Join(controllerDeployFolder, controllerDeploySecretTemplate),
		params, renderedControllerSecret)
}

func (o *ops) renderControllerPod() error {
	var params = map[string]string{
		"ControllerImage": config.GlobalConfig.ControllerImage,
	}

	return o.renderDeploymentFiles(filepath.Join(controllerDeployFolder, controllerDeployPodTemplate),
		params, renderedControllerPod)
}

func (o *ops) renderDeploymentFiles(srcTemplate string, params map[string]string, dest string) error {
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
	output, err := o.ExecPrivilegeCommand(o.logWriter, "pvremove", pvName, "-y")
	if err != nil {
		o.log.Errorf("Failed to remove PV %s, output %s, error %s", pvName, output, err)
	}
	return err
}

func (o *ops) GetMCSLogs() (string, error) {

	files, err := utils.GetListOfFilesFromFolder("/var/log/containers/", "*machine-config-server*.log")
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

func (o *ops) UploadInstallationLogs(isBootstrap bool) (string, error) {
	command := "podman"
	args := []string{"run", "--rm", "--privileged", "-v", "/run/systemd/journal/socket:/run/systemd/journal/socket",
		"-v", "/var/log:/var/log", config.GlobalConfig.AgentImage, "logs_sender",
		"-cluster-id", config.GlobalConfig.ClusterID, "-url", config.GlobalConfig.URL,
		"-host-id", config.GlobalConfig.HostID,
		"-pull-secret-token", config.GlobalConfig.PullSecretToken,
		fmt.Sprintf("-insecure=%s", strconv.FormatBool(config.GlobalConfig.SkipCertVerification)),
		fmt.Sprintf("-bootstrap=%s", strconv.FormatBool(isBootstrap)),
	}

	if config.GlobalConfig.CACertPath != "" {
		args = append(args, fmt.Sprintf("-cacert=%s", config.GlobalConfig.CACertPath))
	}
	return o.ExecPrivilegeCommand(o.logWriter, command, args...)
}

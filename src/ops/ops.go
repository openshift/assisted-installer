package ops

import (
	"fmt"

	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/eranco74/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

//go:generate mockgen -source=ops.go -package=ops -destination=mock_ops.go
type Ops interface {
	ExecPrivilegeCommand(command string, args ...string) (string, error)
	ExecCommand(command string, args ...string) (string, error)
	Mkdir(dirName string) error
	WriteImageToDisk(ignitionPath string, device string) error
	Reboot() error
	ExtractFromIgnition(ignitionPath string, fileToExtract string) error
}

type ops struct {
	log *logrus.Logger
}

// NewOps return a new ops interface
func NewOps(logger *logrus.Logger) Ops {
	return &ops{logger}
}

// ExecPrivilegeCommand execute a command in the host environment via nsenter
func (o *ops) ExecPrivilegeCommand(command string, args ...string) (string, error) {
	commandBase := "nsenter"
	arguments := []string{"-t", "1", "-m", "--", command}
	arguments = append(arguments, args...)
	return o.ExecCommand(commandBase, arguments...)
}

// ExecCommand executes command.
func (o *ops) ExecCommand(command string, args ...string) (string, error) {
	out, err := exec.Command(command, args...).CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				o.log.Debugf("failed executing %s %v, out: %s, with status %d",
					command, args, output, status.ExitStatus())
				return output, fmt.Errorf("failed executing %s %v , error %s", command, args, exitErr)
			}
		}
		return output, fmt.Errorf("failed executing %s %v , error %s", command, args, err)
	}
	o.log.Debug("Command executed:", " command", command, " arguments", args, " output", output)
	return output, err
}

func (o *ops) Mkdir(dirName string) error {
	o.log.Infof("Creating directory: %s", dirName)
	_, err := o.ExecPrivilegeCommand("mkdir", "-p", dirName)
	return err
}

func (o *ops) WriteImageToDisk(ignitionPath string, device string) error {
	o.log.Info("Writing image and ignition to disk")
	out, err := o.ExecPrivilegeCommand("coreos-installer", "install", "--image-url",
		"https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/4.4/latest/rhcos-4.4.3-x86_64-metal.x86_64.raw.gz",
		"--insecure", "-i", ignitionPath, device)
	o.log.Info(out)

	return err
}
func (o *ops) Reboot() error {
	o.log.Info("Rebooting node")
	_, err := o.ExecPrivilegeCommand("shutdown", "-r", "+1", "'Installation completed, server is going to reboot.'")
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
	err = ioutil.WriteFile(tmpFile, extractedContent, 0644)
	if err != nil {
		o.log.Errorf("Error occurred while writing extracted content to %s", tmpFile)
		return err
	}

	o.log.Infof("Moving %s to %s", tmpFile, fileToExtract)
	dir := filepath.Dir(fileToExtract)
	_, err = o.ExecPrivilegeCommand("mkdir", "-p", filepath.Dir(fileToExtract))
	if err != nil {
		o.log.Errorf("Failed to create directory %s ", dir)
		return err
	}
	_, err = o.ExecPrivilegeCommand("mv", tmpFile, fileToExtract)
	if err != nil {
		o.log.Errorf("Error occurred while moving %s to %s", tmpFile, fileToExtract)
		return err
	}
	return nil
}

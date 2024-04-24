package execute

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/openshift/assisted-installer/src/config"
	"github.com/sirupsen/logrus"
)

//go:generate mockgen -source=execute.go -package=execute -destination=mock_execute.go
type Execute interface {
	ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	ExecCommandWithContext(ctx context.Context, liveLogger io.Writer, command string, args ...string) (string, error)
	Execute(command string, args ...string) (string, error)
}

type executor struct {
	cmdEnv          []string
	log             *logrus.Logger
	installerConfig *config.Config
}

func NewExecutor(installerConfig *config.Config, logger *logrus.Logger, proxySet bool) Execute {
	cmdEnv := os.Environ()
	if proxySet && (installerConfig.HTTPProxy != "" || installerConfig.HTTPSProxy != "") {
		if installerConfig.HTTPProxy != "" {
			cmdEnv = append(cmdEnv, fmt.Sprintf("HTTP_PROXY=%s", installerConfig.HTTPProxy))
		}
		if installerConfig.HTTPSProxy != "" {
			cmdEnv = append(cmdEnv, fmt.Sprintf("HTTPS_PROXY=%s", installerConfig.HTTPSProxy))
		}
		if installerConfig.NoProxy != "" {
			cmdEnv = append(cmdEnv, fmt.Sprintf("NO_PROXY=%s", installerConfig.NoProxy))
		}
	}
	return &executor{cmdEnv: cmdEnv, log: logger, installerConfig: installerConfig}
}

func (e *executor) execCommand(liveLogger io.Writer, cmd *exec.Cmd) (string, error) {
	var stdoutBuf bytes.Buffer

	if liveLogger != nil {
		cmd.Stdout = io.MultiWriter(liveLogger, &stdoutBuf)
		cmd.Stderr = io.MultiWriter(liveLogger, &stdoutBuf)
	} else {
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stdoutBuf
	}
	cmd.Env = e.cmdEnv
	err := cmd.Run()
	output := strings.TrimSpace(stdoutBuf.String())
	if err != nil {

		// Get all lines from Error message
		errorIndex := strings.Index(output, "Error")
		// if Error not found return all output
		if errorIndex > -1 {
			output = output[errorIndex:]
		}

		execErr := &ExecCommandError{
			Command:         cmd.Path,
			Args:            cmd.Args[1:],
			Env:             cmd.Env,
			ExitErr:         err,
			Output:          output,
			PullSecretToken: e.installerConfig.PullSecretToken,
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				execErr.WaitStatus = status.ExitStatus()
			}
		}
		if liveLogger != nil {
			//If the caller didn't provide liveLogger the log isn't interesting and might spam
			e.log.Info(execErr.DetailedError())
		}
		return output, execErr
	}
	e.log.Debug("Command executed:", " command", cmd.Path, " arguments", removePullSecret(cmd.Args[1:], e.installerConfig.PullSecretToken), "env vars",
		removePullSecret(cmd.Env, e.installerConfig.PullSecretToken), "output", output)
	return output, err
}

func (e *executor) ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error) {
	return e.execCommand(liveLogger, exec.Command(command, args...))
}

func (e *executor) ExecCommandWithContext(ctx context.Context, liveLogger io.Writer, command string, args ...string) (string, error) {
	return e.execCommand(liveLogger, exec.CommandContext(ctx, command, args...))
}

type ExecCommandError struct {
	Command         string
	Args            []string
	Env             []string
	ExitErr         error
	Output          string
	WaitStatus      int
	PullSecretToken string
}

func (e *ExecCommandError) Error() string {
	lastOutput := e.Output
	if len(e.Output) > 200 {
		lastOutput = "... " + e.Output[len(e.Output)-200:]
	}
	return fmt.Sprintf("failed executing %s %v, Error %s, LastOutput \"%s\"", e.Command, removePullSecret(e.Args, e.PullSecretToken), e.ExitErr, lastOutput)
}

func (e *ExecCommandError) DetailedError() string {
	return fmt.Sprintf("failed executing %s %v, env vars %v, error %s, waitStatus %d, Output \"%s\"", e.Command, removePullSecret(e.Args, e.PullSecretToken), removePullSecret(e.Env, e.PullSecretToken), e.ExitErr, e.WaitStatus, e.Output)
}

func removePullSecret(s []string, pullSecretToken string) []string {
	if pullSecretToken == "" {
		return s
	}

	return strings.Split(strings.ReplaceAll(strings.Join(s, " "), pullSecretToken, "<SECRET>"), " ")
}

// Execute execute a command in the host environment via nsenter
func (e *executor) Execute(command string, args ...string) (string, error) {
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
	return e.ExecCommand(e.log.Writer(), commandBase, arguments...)
}

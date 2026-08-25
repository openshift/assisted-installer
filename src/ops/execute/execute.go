package execute

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/openshift/assisted-installer/src/config"
	"github.com/sirupsen/logrus"
)

//go:generate mockgen -source=execute.go -package=execute -destination=mock_execute.go
type Execute interface {
	ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	ExecCommandWithOptions(liveLogger io.Writer, command string, args []string, opts ...CommandOption) (string, error)
	Execute(command string, args ...string) (string, error)
}

type executor struct {
	cmdEnv          []string
	log             *logrus.Logger
	installerConfig *config.Config
}

// commandConfig holds command configuration that options can modify before
// exec.Cmd is created. This ensures options never see or mutate the executor.
type commandConfig struct {
	command string
	args    []string
	env     []string
	dir     string
	ctx     context.Context
}

// CommandOption is a functional option for configuring command execution
type CommandOption func(*commandConfig)

// WithEnv adds environment variables to the command
func WithEnv(env []string) CommandOption {
	return func(c *commandConfig) {
		c.env = append(c.env, env...)
	}
}

// WithDir sets the working directory for the command
func WithDir(dir string) CommandOption {
	return func(c *commandConfig) {
		c.dir = dir
	}
}

// WithContext sets a context for the command, enabling cancellation and timeouts.
func WithContext(ctx context.Context) CommandOption {
	return func(c *commandConfig) {
		c.ctx = ctx
	}
}

// WithPrivilege wraps the command in nsenter to execute it in the host
// environment rather than inside the container.
func WithPrivilege() CommandOption {
	return func(c *commandConfig) {
		c.args = append([]string{
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
			c.command,
		}, c.args...)
		c.command = "nsenter"
	}
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
	cmd.Env = slices.Concat(e.cmdEnv, cmd.Env)
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
			Command: cmd.Path,
			Args:    cmd.Args[1:],
			Env:     cmd.Env,
			ExitErr: err,
			Output:  output,
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
	e.log.Debug("Command executed:", " command", cmd.Path, " arguments", cmd.Args[1:], "env vars",
		redactSensitiveEnv(cmd.Env), "output", output)
	return output, err
}

func (e *executor) ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error) {
	return e.execCommand(liveLogger, exec.Command(command, args...))
}

func (e *executor) ExecCommandWithOptions(liveLogger io.Writer, command string, args []string, opts ...CommandOption) (string, error) {
	cfg := &commandConfig{command: command, args: args}
	for _, opt := range opts {
		opt(cfg)
	}
	cmd := newCmd(cfg.ctx, cfg.command, cfg.args...)
	cmd.Env = append(cmd.Env, cfg.env...)
	cmd.Dir = cfg.dir
	return e.execCommand(liveLogger, cmd)
}

func newCmd(ctx context.Context, command string, args ...string) *exec.Cmd {
	if ctx != nil {
		return exec.CommandContext(ctx, command, args...)
	}
	return exec.Command(command, args...)
}

type ExecCommandError struct {
	Command    string
	Args       []string
	Env        []string
	ExitErr    error
	Output     string
	WaitStatus int
}

func (e *ExecCommandError) Error() string {
	lastOutput := e.Output
	if len(e.Output) > 200 {
		lastOutput = "... " + e.Output[len(e.Output)-200:]
	}
	return fmt.Sprintf("failed executing %s %v, Error %s, LastOutput \"%s\"", e.Command, e.Args, e.ExitErr, lastOutput)
}

func (e *ExecCommandError) DetailedError() string {
	return fmt.Sprintf("failed executing %s %v, env vars %v, error %s, waitStatus %d, Output \"%s\"", e.Command, e.Args, redactSensitiveEnv(e.Env), e.ExitErr, e.WaitStatus, e.Output)
}

// redactSensitiveEnv redacts environment variables that contain secrets
func redactSensitiveEnv(env []string) []string {
	if len(env) == 0 {
		return env
	}

	sensitivePatterns := []string{
		"TOKEN", "KEY", "SECRET", "PASSWORD", "PASS", "PWD",
		"AUTH", "CREDENTIAL", "CRED", "CERT", "PRIVATE",
	}

	redacted := make([]string, len(env))
	for i, e := range env {
		if idx := strings.Index(e, "="); idx > 0 {
			name := strings.ToUpper(e[:idx])
			isSensitive := false
			for _, pattern := range sensitivePatterns {
				if strings.Contains(name, pattern) {
					isSensitive = true
					break
				}
			}
			if isSensitive {
				redacted[i] = e[:idx] + "=<REDACTED>"
			} else {
				redacted[i] = e
			}
		} else {
			redacted[i] = e
		}
	}
	return redacted
}

// Execute execute a command in the host environment via nsenter
func (e *executor) Execute(command string, args ...string) (string, error) {
	return e.ExecCommandWithOptions(e.log.Writer(), command, args, WithPrivilege())
}

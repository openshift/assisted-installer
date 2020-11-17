package exec

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"

	"github.com/openshift/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

type CmdContext struct {
	Out io.Writer
	Env []string
	Dir string
}

//go:generate mockgen -source=cmdexec.go -package=exec -destination=mock_cmdexec.go
type Excecutor interface {
	ExecPrivilegeCommand(out io.Writer, command string, args ...string) (string, error)
	ExecCommand(out io.Writer, command string, args ...string) (string, error)

	ExecPrivilegeCommandWithContext(cmdctx *CmdContext, command string, args ...string) (string, error)
	ExecCommandWithContext(cmdctx *CmdContext, command string, args ...string) (string, error)
}

type ExcecutorRunner struct {
	log       *logrus.Logger
	logWriter *utils.LogWriter
	cmdEnv    []string
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
	return fmt.Sprintf("failed executing %s %v, env vars %v, error %s, waitStatus %d, Output \"%s\"", e.Command, e.Args, e.Env, e.ExitErr, e.WaitStatus, e.Output)
}

func NewExecutor(log *logrus.Logger, logWriter *utils.LogWriter, cmdEnv []string) *ExcecutorRunner {
	return &ExcecutorRunner{log, logWriter, cmdEnv}
}

// ExecPrivilegeCommand execute a command in the host environment via nsenter
func (runner ExcecutorRunner) ExecPrivilegeCommand(liveLogger io.Writer, command string, args ...string) (string, error) {
	cmdctx := &CmdContext{Out: liveLogger, Env: runner.cmdEnv}
	return runner.ExecPrivilegeCommandWithContext(cmdctx, command, args...)
}

func (runner ExcecutorRunner) ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error) {
	cmdctx := &CmdContext{Out: liveLogger, Env: runner.cmdEnv}
	return runner.ExecCommandWithContext(cmdctx, command, args...)
}

func (runner ExcecutorRunner) ExecPrivilegeCommandWithContext(cmdctx *CmdContext, command string, args ...string) (string, error) {
	commandBase := "nsenter"
	arguments := []string{"-t", "1", "-m", "-i", "--", command}
	arguments = append(arguments, args...)
	return runner.ExecCommandWithContext(cmdctx, commandBase, arguments...)
}

// ExecCommand executes command.
func (runner ExcecutorRunner) ExecCommandWithContext(cmdctx *CmdContext, command string, args ...string) (string, error) {

	var stdoutBuf bytes.Buffer
	cmd := exec.Command(command, args...)
	if cmdctx.Out != nil {
		cmd.Stdout = io.MultiWriter(cmdctx.Out, &stdoutBuf)
		cmd.Stderr = io.MultiWriter(cmdctx.Out, &stdoutBuf)
	} else {
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stdoutBuf
	}
	//if Dir is empty, command runs in the calling process's current directory
	//see https://golang.org/pkg/os/exec/#Cmd
	cmd.Dir = cmdctx.Dir
	//Env specifies the environment of the process. If Env is nil
	//the new process uses the current process's environment.
	//see https://golang.org/pkg/os/exec/#Cmd
	cmd.Env = cmdctx.Env
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
			Command: command,
			Args:    args,
			Env:     cmd.Env,
			ExitErr: err,
			Output:  output,
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				execErr.WaitStatus = status.ExitStatus()
			}
		}
		runner.log.Info(execErr.DetailedError())
		return output, execErr
	}
	runner.log.Debug("Command executed:", " command", command, " arguments", args, "env vars", cmd.Env, "output", output)
	return output, err
}

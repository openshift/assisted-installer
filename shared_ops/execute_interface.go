package shared_ops

import (
	"context"
	"io"
)

//go:generate mockgen -source=execute_interface.go -package=shared_ops -destination=mock_execute_interface.go
type Execute interface {
	ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	ExecCommandWithContext(ctx context.Context, liveLogger io.Writer, command string, args ...string) (string, error)
	Execute(command string, args ...string) (string, error)
}

package disk_ops

import (
	"context"
	"io"
)

//go:generate mockgen -source=execute.go -package=execute -destination=mock_execute.go
type Execute interface {
	ExecCommand(liveLogger io.Writer, command string, args ...string) (string, error)
	ExecCommandWithContext(ctx context.Context, liveLogger io.Writer, command string, args ...string) (string, error)
}

package shared_ops

//go:generate mockgen -source=execute_interface.go -package=shared_ops -destination=mock_execute_interface.go
type Execute interface {
	Execute(command string, args ...string) (string, error)
}

package journald

import (
	"net"
	"syscall"
	"unsafe"
)

func sockaddr(addr *net.UnixAddr) (unsafe.Pointer, uint8) {
	sa := syscall.RawSockaddrUnix{Family: syscall.AF_UNIX}
	name := addr.Name
	n := len(name)

	for i := 0; i < n; i++ {
		sa.Path[i] = int8(name[i])
	}
	return unsafe.Pointer(&sa), byte(2 + n + 1) // length is family (uint16), name, NUL.
}

// +build !go1.9

package journald

import (
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Go implementation of sendmsg (UnixConn.WriteMsgUnix) is not suitable
// for systemd's Journal in case of sending file descriptors. It always
// sends a dummy data byte, but journal does not expect to get it:
//
// n = recvmsg(fd, &msghdr, MSG_DONTWAIT|MSG_CMSG_CLOEXEC);
// ...
// if (n > 0 && n_fds == 0)
//     server_process_native_message(...);
// else if (n == 0 && n_fds == 1)
//     server_process_native_file(...);
// else if (n_fds > 0)
//     log_warning("Got too many file descriptors via native socket. Ignoring.");
//
// So we get a warning message (the last line) instead of the new log
// entry.
//
func writeMsgUnix(c *net.UnixConn, oob []byte, addr *net.UnixAddr) (oobn int, err error) {
	ptr, salen := sockaddr(addr)

	var msg syscall.Msghdr
	msg.Name = (*byte)(ptr)
	msg.Namelen = uint32(salen)
	msg.Control = (*byte)(unsafe.Pointer(&oob[0]))
	msg.SetControllen(len(oob))

	f, err := c.File()
	if err != nil {
		return 0, err
	}
	defer f.Close()

	_, n, errno := syscall.Syscall(unix.SYS_SENDMSG, f.Fd(), uintptr(unsafe.Pointer(&msg)), 0)
	if errno != 0 {
		return int(n), errno
	}

	return int(n), nil
}

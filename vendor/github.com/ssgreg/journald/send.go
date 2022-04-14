// Copyright 2017 Grigory Zubankov. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.
//

package journald

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
)

// Priority is the numeric message priority value as known from BSD syslog
type Priority int

// Priority values
const (
	PriorityEmerg Priority = iota
	PriorityAlert
	PriorityCrit
	PriorityErr
	PriorityWarning
	PriorityNotice
	PriorityInfo
	PriorityDebug
)

var (
	addr = &net.UnixAddr{Name: "/run/systemd/journal/socket", Net: "unixgram"}

	// DefaultJournal is the default journal and is used by Print and Send.
	DefaultJournal = &Journal{}
)

// IsNotExist checks if the system journal is not exist.
func IsNotExist() bool {
	_, err := os.Stat(addr.Name)
	return os.IsNotExist(err)
}

// Journal keeps a connection to the system journal
type Journal struct {
	once    sync.Once
	conn    *net.UnixConn
	connErr error

	// NormalizeFieldNameFn is a hook that allows client to change
	// fields names just before sending if set.
	//
	// Default value is nil.
	// string.ToUpper is a good example of the hook usage.
	NormalizeFieldNameFn func(string) string

	// TestModeEnabled allows Journal to do nothing. All messages are
	// discarding.
	TestModeEnabled bool
}

// Print may be used to submit simple, plain text log entries to the
// system journal. The first argument is a priority value. This is
// followed by a format string and its parameters.
func (j *Journal) Print(p Priority, format string, a ...interface{}) error {
	return j.Send(fmt.Sprintf(format, a...), p, nil)
}

// WriteMsg writes the given bytes to the systemd journal's socket.
// The caller is in charge of correct data format.
func (j *Journal) WriteMsg(data []byte) error {
	if j.TestModeEnabled {
		return nil
	}

	c, err := j.journalConn()
	if err != nil {
		return err
	}

	_, _, err = c.WriteMsgUnix(data, nil, addr)
	if err == nil {
		return nil
	}
	errno := toErrno(err)
	if errno != syscall.EMSGSIZE && errno != syscall.ENOBUFS {
		return err
	}

	// Message doesn't fit...

	// TODO: user memfd when it will be available in go:
	// http://man7.org/linux/man-pages/man2/memfd_create.2.html

	f, err := openTempFileUnlinkable()
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	if err != nil {
		return err
	}
	_, err = writeMsgUnix(c, syscall.UnixRights(int(f.Fd())), addr)

	return err

}

// Send may be used to submit structured log entries to the system
// journal. It takes a map of fields with names and values.
//
// The field names must be in uppercase and consist only of characters,
// numbers and underscores, and may not begin with an underscore. All
// fields that do not follow this syntax will be ignored. The value can
// be of any size and format. A variable may be assigned more than one
// value per entry.
//
// A number of well known fields are defined, see:
// http://0pointer.de/public/systemd-man/systemd.journal-fields.html
//
func (j *Journal) Send(msg string, p Priority, fields map[string]interface{}) error {
	return j.WriteMsg(j.marshal(msg, p, fields))
}

// Close closes the underlying connection.
func (j *Journal) Close() error {
	if j.connErr != nil {
		return j.connErr
	}
	if j.conn == nil {
		return nil
	}

	return j.conn.Close()
}

func (j *Journal) journalConn() (*net.UnixConn, error) {
	j.once.Do(func() {
		// File from UNIX socket. This is the only way to create a
		// connect-less socket. Both Dial and ListenUnixgram do extra
		// work it terms of connection.
		//
		// This UNIX socket is needed only to make a Conn on it's base.
		// No reason to switch it to nonblocking mode and set additional
		// flags. FileConn will do such things for us for Conn socket.
		fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
		if err != nil {
			j.connErr = err
			return
		}
		f := os.NewFile(uintptr(fd), "base UNIX socket")
		defer f.Close()

		fc, err := net.FileConn(f)
		if err != nil {
			j.connErr = err
			return
		}
		uc, ok := fc.(*net.UnixConn)
		if !ok {
			fc.Close()
			j.connErr = errors.New("not a UNIX connection")
			return
		}
		uc.SetWriteBuffer(8 * 1024 * 1024)
		j.conn = uc
	})

	return j.conn, j.connErr
}

// Print may be used to submit simple, plain text log entries to the
// system journal. The first argument is a priority value. This is
// followed by a format string and its parameters.
//
// Print is a wrapper around DefaultJournal.Print.
func Print(p Priority, format string, a ...interface{}) error {
	return DefaultJournal.Print(p, format, a...)
}

// Send may be used to submit structured log entries to the system
// journal. It takes a map of fields with names and values.
//
// The field names must be in uppercase and consist only of characters,
// numbers and underscores, and may not begin with an underscore. All
// fields that do not follow this syntax will be ignored. The value can
// be of any size and format. A variable may be assigned more than one
// value per entry.
//
// A number of well known fields are defined, see:
// http://0pointer.de/public/systemd-man/systemd.journal-fields.html
//
// Send is a wrapper around DefaultJournal.Send.
func Send(msg string, p Priority, fields map[string]interface{}) error {
	return DefaultJournal.Send(msg, p, fields)
}

func toErrno(err error) syscall.Errno {
	switch e := err.(type) {
	case *net.OpError:
		switch e := e.Err.(type) {
		case *os.SyscallError:
			return e.Err.(syscall.Errno)
		}
	}

	return 0
}

func (j *Journal) marshal(msg string, p Priority, fields map[string]interface{}) []byte {
	bb := new(bytes.Buffer)
	j.writeField(bb, "PRIORITY", strconv.Itoa(int(p)))
	j.writeField(bb, "MESSAGE", msg)
	for k, v := range fields {
		j.writeField(bb, k, v)
	}

	return bb.Bytes()
}

func (j *Journal) writeField(w io.Writer, name string, value interface{}) {
	w.Write([]byte(j.normalizeFieldName(name)))
	dv := valueToBytes(value)
	if bytes.ContainsRune(dv, '\n') {
		// According to the format, if the value includes a newline
		// need to write the field name, plus a newline, then the
		// size (64bit LE), the field data and a final newline.

		w.Write([]byte{'\n'})
		binary.Write(w, binary.LittleEndian, uint64(len(dv)))
	} else {
		w.Write([]byte{'='})
	}
	w.Write(dv)
	w.Write([]byte{'\n'})
}

func (j *Journal) normalizeFieldName(s string) string {
	if j.NormalizeFieldNameFn != nil {
		return j.NormalizeFieldNameFn(s)
	}
	return s
}

func valueToBytes(value interface{}) []byte {
	switch rv := value.(type) {
	case string:
		return []byte(rv)
	case []byte:
		return rv
	default:
		return []byte(fmt.Sprint(value))
	}
}

func openTempFileUnlinkable() (*os.File, error) {
	f, err := ioutil.TempFile("/dev/shm/", "journald-send-tmp")
	if err != nil {
		return nil, err
	}
	// The file will be deleted just after the systemd will close passed fd.
	err = syscall.Unlink(f.Name())
	if err != nil {
		return nil, err
	}

	return f, nil
}

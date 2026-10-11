//go:build linux || darwin || freebsd

package ftp

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// setOOBInline keeps TCP urgent data in the normal stream. Clients that
// send ABOR (or its Telnet Synch) as urgent data, such as Python's ftplib,
// would otherwise lose the urgent byte and leave a half line behind.
func setOOBInline(conn net.Conn) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_OOBINLINE, 1) // #nosec G115 -- fd fits int
	})
}

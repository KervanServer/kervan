//go:build !(linux || darwin || freebsd)

package ftp

import "net"

func setOOBInline(net.Conn) {}

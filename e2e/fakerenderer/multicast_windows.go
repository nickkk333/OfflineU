//go:build windows

package main

import (
	"net"
	"syscall"
)

// enableMulticastLoopback turns IP_MULTICAST_LOOP back on for an SSDP socket.
//
// net.ListenMulticastUDP deliberately clears that option on every listener it
// opens ("to disable loopback of multicast packets"), and it offers no Control
// hook to put it back. On Windows the option is a receive-side switch: with it
// cleared, a socket does not see multicast datagrams that the same host sent.
// The E2E server and this fake renderer live on one machine, so the server's
// M-SEARCH never showed up and discovery always came back empty. Re-enabling the
// option costs nothing on a real network and makes the local cast test work.
func enableMulticastLoopback(connection *net.UDPConn) error {
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
	}); err != nil {
		return err
	}
	return sockErr
}

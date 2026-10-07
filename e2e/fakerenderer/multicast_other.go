//go:build !windows

package main

import "net"

// enableMulticastLoopback is a no-op outside Windows.
//
// Only Windows treats IP_MULTICAST_LOOP as a receive-side switch, so only
// Windows loses the locally sent M-SEARCH that net.ListenMulticastUDP clears
// the option for. Everywhere else the loopback is decided by the sending
// socket, and a listener still receives what the same host sent.
func enableMulticastLoopback(connection *net.UDPConn) error {
	return nil
}

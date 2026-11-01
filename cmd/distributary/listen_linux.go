//go:build linux

package main

import (
	"context"
	"net"
	"syscall"
)

// listen opens the TCP listener. If mss > 0 it caps the TCP segment size of
// every accepted connection (they inherit it from the listening socket).
//
// Why: the professor's bandwidth_2mbit.sh shapes loopback with a 4 KB token
// bucket, while loopback's MTU is 64 KB. Any TCP segment over about 4 KB is
// then dropped on every retransmission and the connection freezes. That hits
// a 16 KB HISTORY, and bursts of small frames that TCP merges into one
// segment. With a 1400-byte cap, both were measured to go through.
func listen(addr string, mss int) (net.Listener, error) {
	lc := net.ListenConfig{}
	if mss > 0 {
		lc.Control = func(_, _ string, c syscall.RawConn) error {
			var sockErr error
			err := c.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_MAXSEG, mss)
			})
			if err != nil {
				return err
			}
			return sockErr
		}
	}
	return lc.Listen(context.Background(), "tcp", addr)
}

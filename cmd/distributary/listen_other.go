//go:build !linux

package main

import "net"

// listen opens the TCP listener. The MSS cap (see listen_linux.go) only
// matters for the tc/netem experiments, which run on Linux, so other systems
// ignore it. This file exists so the command still builds on a laptop.
func listen(addr string, _ int) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

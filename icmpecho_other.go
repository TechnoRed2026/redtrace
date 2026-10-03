//go:build !windows

package main

import (
	"net"
	"time"
)

const nativeEchoAvailable = false

func nativeEcho(dest net.IP, ttl int, timeout time.Duration) (net.IP, uint32, time.Duration, bool) {
	return nil, 0, 0, false
}

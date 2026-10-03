//go:build windows

package main

import "syscall"

func setTTLFd(fd uintptr, ttl int) error {
	// IP_TTL = 4
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, 4, ttl)
}

//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

func termInit() (func(), error) {
	fd := os.Stdin.Fd()
	var old syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&old)); err != nil {
		return nil, err
	}
	n := old
	n.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	n.Iflag &^= syscall.IXON | syscall.ICRNL | syscall.INLCR | syscall.ISTRIP | syscall.BRKINT
	n.Cc[syscall.VMIN] = 1
	n.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TCSETS, unsafe.Pointer(&n)); err != nil {
		return nil, err
	}
	return func() { _ = ioctl(fd, syscall.TCSETS, unsafe.Pointer(&old)) }, nil
}

func termSize() (int, int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(os.Stdout.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 {
		return 120, 30
	}
	return int(ws.Col), int(ws.Row)
}

func relaunchElevated(args []string) bool { return false }

const adminHint = "run with sudo (or: sudo setcap cap_net_raw+ep ./redtrace)"

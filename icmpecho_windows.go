//go:build windows

package main

import (
	"encoding/binary"
	"net"
	"syscall"
	"time"
	"unsafe"
)

// Windows' own traceroute API (iphlpapi IcmpSendEcho) - this is what tracert.exe uses.
// It reliably reports "TTL expired in transit" with the router address, even when raw
// sockets do not receive ICMP error messages.

var (
	iph              = syscall.NewLazyDLL("iphlpapi.dll")
	pIcmpCreateFile  = iph.NewProc("IcmpCreateFile")
	pIcmpCloseHandle = iph.NewProc("IcmpCloseHandle")
	pIcmpSendEcho    = iph.NewProc("IcmpSendEcho")
)

const nativeEchoAvailable = true

type ipOptionInfo struct {
	Ttl, Tos, Flags, OptionsSize uint8
	OptionsData                  uintptr
}

func nativeEcho(dest net.IP, ttl int, timeout time.Duration) (net.IP, uint32, time.Duration, bool) {
	v4 := dest.To4()
	if v4 == nil {
		return nil, 0, 0, false
	}
	h, _, _ := pIcmpCreateFile.Call()
	if h == 0 || h == ^uintptr(0) {
		return nil, 0, 0, false
	}
	defer pIcmpCloseHandle.Call(h)

	addr := uint32(v4[0]) | uint32(v4[1])<<8 | uint32(v4[2])<<16 | uint32(v4[3])<<24
	data := []byte("redtrace-echo-0123456789abcdef!!")
	opt := ipOptionInfo{Ttl: uint8(ttl)}
	reply := make([]byte, 512)
	start := time.Now()
	n, _, _ := pIcmpSendEcho.Call(h, uintptr(addr),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(&opt)),
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)),
		uintptr(timeout/time.Millisecond))
	rtt := time.Since(start)
	if n == 0 {
		return nil, 0, rtt, false
	}
	from := net.IPv4(reply[0], reply[1], reply[2], reply[3])
	status := binary.LittleEndian.Uint32(reply[4:8])
	return from, status, rtt, true
}

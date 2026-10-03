//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	k32                 = syscall.NewLazyDLL("kernel32.dll")
	pGetConsoleMode     = k32.NewProc("GetConsoleMode")
	pSetConsoleMode     = k32.NewProc("SetConsoleMode")
	pGetConsoleOutputCP = k32.NewProc("GetConsoleOutputCP")
	pSetConsoleOutputCP = k32.NewProc("SetConsoleOutputCP")
	pGetCSBI            = k32.NewProc("GetConsoleScreenBufferInfo")
)

type coord struct{ X, Y int16 }
type smallRect struct{ Left, Top, Right, Bottom int16 }
type csbi struct {
	Size    coord
	Cursor  coord
	Attr    uint16
	Window  smallRect
	MaxSize coord
}

func termInit() (func(), error) {
	hin, hout := os.Stdin.Fd(), os.Stdout.Fd()
	var inMode, outMode uint32
	if r, _, err := pGetConsoleMode.Call(hin, uintptr(unsafe.Pointer(&inMode))); r == 0 {
		return nil, err
	}
	if r, _, err := pGetConsoleMode.Call(hout, uintptr(unsafe.Pointer(&outMode))); r == 0 {
		return nil, err
	}
	oldCP, _, _ := pGetConsoleOutputCP.Call()

	// input: no echo / line mode / ctrl-c processing / quick-edit, enable VT input
	newIn := inMode &^ (0x0001 | 0x0002 | 0x0004 | 0x0040)
	newIn |= 0x0200 | 0x0080
	// output: processed output + VT sequences
	newOut := outMode | 0x0001 | 0x0004
	if r, _, err := pSetConsoleMode.Call(hin, uintptr(newIn)); r == 0 {
		return nil, err
	}
	if r, _, err := pSetConsoleMode.Call(hout, uintptr(newOut)); r == 0 {
		pSetConsoleMode.Call(hin, uintptr(inMode))
		return nil, err
	}
	pSetConsoleOutputCP.Call(65001)
	return func() {
		pSetConsoleMode.Call(hin, uintptr(inMode))
		pSetConsoleMode.Call(hout, uintptr(outMode))
		if oldCP != 0 {
			pSetConsoleOutputCP.Call(oldCP)
		}
	}, nil
}

func termSize() (int, int) {
	var info csbi
	r, _, _ := pGetCSBI.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 120, 30
	}
	w := int(info.Window.Right-info.Window.Left) + 1
	h := int(info.Window.Bottom-info.Window.Top) + 1
	if w < 1 || h < 1 {
		return 120, 30
	}
	return w, h
}

// relaunchElevated re-runs this exe through UAC ("runas") – raw ICMP sockets need admin on Windows.
func relaunchElevated(args []string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cwd, _ := os.Getwd()
	parts := make([]string, 0, len(args)+1)
	for _, a := range args {
		parts = append(parts, syscall.EscapeArg(a))
	}
	parts = append(parts, "--elevated")
	sh := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(strings.Join(parts, " "))
	dir, _ := syscall.UTF16PtrFromString(cwd)
	r, _, _ := sh.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), uintptr(unsafe.Pointer(dir)), 1)
	return r > 32
}

const adminHint = "run this program as Administrator"

//go:build linux || darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// windowColumns asks the terminal f is open on how many columns wide it
// is. It reports false when f is no terminal.
func windowColumns(f *os.File) (int, bool) {
	var ws struct{ rows, cols, xpixels, ypixels uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	return int(ws.cols), errno == 0
}

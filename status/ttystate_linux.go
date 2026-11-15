package main

import (
	"os"
	"syscall"
	"unsafe"
)

// ttyState is the controlling terminal's settings, saved so they can be
// put back after a program that changes them is stopped before it could.
type ttyState struct {
	f *os.File
	t syscall.Termios
}

// ttyPath is the controlling terminal. Tests name a pty of their own.
var ttyPath = "/dev/tty"

// saveTerminal saves the controlling terminal's settings, or returns nil
// when there is no terminal to read.
func saveTerminal() *ttyState {
	f, err := os.OpenFile(ttyPath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil
	}
	s := &ttyState{f: f}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&s.t))); errno != 0 {
		f.Close()
		return nil
	}
	return s
}

// restore puts the saved settings back when the terminal's differ from
// them. Settings left as they were are not written again, so a process in
// the background is not stopped for writing them.
func (s *ttyState) restore() {
	if s == nil {
		return
	}
	var now syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, s.f.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&now))); errno == 0 && now == s.t {
		return
	}
	syscall.Syscall(syscall.SYS_IOCTL, s.f.Fd(), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&s.t)))
}

// release lets the terminal go.
func (s *ttyState) release() {
	if s != nil {
		s.f.Close()
	}
}

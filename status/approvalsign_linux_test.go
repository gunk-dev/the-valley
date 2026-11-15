package main

import (
	"io"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func init() { extraFakePIV["echo-off"] = fakeEchoOff }

// fakeEchoOff turns echo off on the terminal VALLEY_TEST_FAKE_PIV_TTY
// names, as the helper does while it reads the PIN, says it is waiting,
// and waits to be killed.
func fakeEchoOff() int {
	f, err := os.OpenFile(os.Getenv("VALLEY_TEST_FAKE_PIV_TTY"), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return 1
	}
	t, err := termios(f)
	if err != nil {
		return 1
	}
	t.Lflag &^= syscall.ECHO
	if ioctl(f, syscall.TCSETS, unsafe.Pointer(&t)) != nil || !waitingFor(make(chan os.Signal, 1)) {
		return 1
	}
	io.Copy(io.Discard, os.Stdin)
	return 1
}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

func termios(f *os.File) (syscall.Termios, error) {
	var t syscall.Termios
	return t, ioctl(f, syscall.TCGETS, unsafe.Pointer(&t))
}

// A PIV signer's helper killed while echo is off, as it is while the
// helper reads the PIN, leaves the terminal as it was once approve has
// refused: approve puts back the settings it saved before the helper
// started. The terminal is a pty of the test's own, so its settings
// really change.
func TestAKilledPIVSignerLeavesTheTerminalAsItWas(t *testing.T) {
	saved, savedTTY := pivSigner, ttyPath
	pivSigner = os.Args[0]
	t.Cleanup(func() { pivSigner, ttyPath = saved, savedTTY })
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}
	defer master.Close()
	var n uint32
	var unlock int32
	if ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)) != nil || ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)) != nil {
		t.Fatal("the pty did not open")
	}
	ttyPath = "/dev/pts/" + strconv.Itoa(int(n))
	pty, err := os.OpenFile(ttyPath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	before, err := termios(pty)
	if err != nil || before.Lflag&syscall.ECHO == 0 {
		t.Fatalf("the pty does not echo to begin with: %v", err)
	}

	w := newSignWorld(t, nil)
	t.Setenv("VALLEY_TEST_FAKE_PIV_TTY", ttyPath)
	ended := make(chan int, 1)
	pid := w.helperWaiting("echo-off", func() {
		ended <- w.approve("--head", w.head, "--approval-piv", w.id)
	}, ended)
	if during, err := termios(pty); err != nil || during.Lflag&syscall.ECHO != 0 {
		t.Fatalf("the stand-in did not turn echo off: %v", err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-ended:
		w.nothingPublished(status, "approval not signed")
	case <-time.After(time.Minute):
		t.Fatal("approve did not end once its helper was killed")
	}
	if after, err := termios(pty); err != nil || after != before {
		t.Errorf("the terminal was left with other settings than it had: echo %v, %v", after.Lflag&syscall.ECHO != 0, err)
	}
}

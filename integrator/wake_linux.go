//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// wakeSupported says whether watch can be given --woken-by here.
const wakeSupported = true

// watchOpened, when set, runs once watchDir has opened and locked the
// directory and before it watches it. Tests set it to replace the
// directory there.
var watchOpened func()

// inotifyWatch watches one wake directory for a created or moved-in due.
//
// It holds the directory open, and everything it does goes through that
// descriptor: the inotify watch is on the directory it holds, and due is
// looked for and removed there. So the watch and the marks it consumes are
// always one directory's, whatever the path comes to name. A goroutine
// reads the inotify descriptor through the runtime's poller, so closing the
// file ends the read.
type inotifyWatch struct {
	path   string
	dir    *os.File // the directory, held open and locked
	held   string   // the directory as /proc/self/fd names the one held
	f      *os.File // the inotify descriptor
	dirWd  int32
	parent int32 // the watch on the parent, for the directory's own name
	note   chan struct{}
	end    chan error
}

// watchDir watches dir. It opens the directory and takes the consumer's
// lock on it, so a second controller given the same directory does not
// consume its marks. It watches the directory it opened, through
// /proc/self/fd, and the parent for the directory's name, so a directory
// removed, moved or replaced at the path ends the watch at once, even
// though the descriptor it holds keeps the old directory alive. Then it
// checks that the path still names the directory it holds. A directory
// replaced before the watch was in place fails that check, and the watch
// is not made. Every descriptor a failure leaves open is closed.
func watchDir(path string) (watcher, error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	w := &inotifyWatch{
		path: path,
		dir:  dir,
		held: fmt.Sprintf("/proc/self/fd/%d", dir.Fd()),
		note: make(chan struct{}, 1),
		end:  make(chan error, 1),
	}
	fail := func(err error) (watcher, error) {
		if w.f != nil {
			w.f.Close()
		}
		dir.Close()
		return nil, err
	}
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fail(errors.New("another controller consumes its marks"))
		}
		return fail(os.NewSyscallError("flock", err))
	}
	if watchOpened != nil {
		watchOpened()
	}
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return fail(os.NewSyscallError("inotify_init1", err))
	}
	w.f = os.NewFile(uintptr(fd), "inotify")
	wd, err := syscall.InotifyAddWatch(fd, w.held, syscall.IN_CREATE|syscall.IN_MOVED_TO|syscall.IN_ONLYDIR|
		syscall.IN_DELETE_SELF|syscall.IN_MOVE_SELF)
	if err != nil {
		return fail(os.NewSyscallError("inotify_add_watch", err))
	}
	w.dirWd = int32(wd)
	wd, err = syscall.InotifyAddWatch(fd, filepath.Dir(path), syscall.IN_CREATE|syscall.IN_DELETE|
		syscall.IN_MOVED_FROM|syscall.IN_MOVED_TO|syscall.IN_ONLYDIR|syscall.IN_DELETE_SELF|syscall.IN_MOVE_SELF)
	if err != nil {
		return fail(fmt.Errorf("watching its parent: %w", os.NewSyscallError("inotify_add_watch", err)))
	}
	w.parent = int32(wd)
	if !w.same() {
		return fail(fmt.Errorf("another directory took its place while it was being watched"))
	}
	go w.read()
	return w, nil
}

// same says whether the path still names the directory the watch holds.
func (w *inotifyWatch) same() bool {
	held, err := w.dir.Stat()
	if err != nil {
		return false
	}
	now, err := os.Stat(w.path)
	return err == nil && os.SameFile(held, now)
}

func (w *inotifyWatch) events() <-chan struct{} { return w.note }
func (w *inotifyWatch) failed() <-chan error    { return w.end }

func (w *inotifyWatch) close() {
	w.f.Close()
	w.dir.Close()
}

// removeDue removes due from the directory the watch holds.
func (w *inotifyWatch) removeDue() error {
	err := os.Remove(filepath.Join(w.held, "due"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// hasDue says whether due is in the directory the watch holds.
func (w *inotifyWatch) hasDue() bool {
	_, err := os.Lstat(filepath.Join(w.held, "due"))
	return err == nil
}

func (w *inotifyWatch) notify() {
	select {
	case w.note <- struct{}{}:
	default:
	}
}

func (w *inotifyWatch) read() {
	base := filepath.Base(w.path)
	buf := make([]byte, 64*(syscall.SizeofInotifyEvent+syscall.NAME_MAX+1))
	for {
		n, err := w.f.Read(buf)
		if err != nil {
			w.end <- fmt.Errorf("reading its events: %w", err)
			return
		}
		for off := 0; off+syscall.SizeofInotifyEvent <= n; {
			wd := int32(binary.NativeEndian.Uint32(buf[off:]))
			mask := binary.NativeEndian.Uint32(buf[off+4:])
			size := int(binary.NativeEndian.Uint32(buf[off+12:]))
			name := string(trimNUL(buf[off+syscall.SizeofInotifyEvent : min(n, off+syscall.SizeofInotifyEvent+size)]))
			off += syscall.SizeofInotifyEvent + size
			self := uint32(syscall.IN_IGNORED | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF | syscall.IN_UNMOUNT)
			switch {
			case mask&syscall.IN_Q_OVERFLOW != 0:
				// Events were dropped, and one may have been the
				// directory's replacement, so the path is checked. due is
				// on disk, so a look at it is all any other was worth.
				if !w.same() {
					w.end <- errGone
					return
				}
				w.notify()
			case mask&self != 0:
				w.end <- errGone
				return
			case wd == w.parent:
				if name == base {
					w.end <- errGone
					return
				}
			case wd == w.dirWd && name == "due" && mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0:
				w.notify()
			}
		}
	}
}

func trimNUL(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}

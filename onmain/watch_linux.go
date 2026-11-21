//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// inotifyWatch watches what git's files ref storage changes when main
// moves: refs/heads, where main's loose ref is created, replaced by a
// rename or deleted, and the git directory, where packed-refs is replaced
// by a rename, as git pack-refs and the deletion of a packed ref do. Every
// other name in those directories, the locks git takes included, is
// ignored.
//
// A systemd path unit could not stand in for this: PathChanged= holds no
// watch while the unit it started runs, so a move during a run would be
// seen only at the next move. The bridge turns these moves into the
// request file, which a path unit's PathExists= does follow.
//
// The watches stay on the directories they were made on. So a watched
// directory deleted or moved away, or refs or refs/heads created, renamed
// or deleted in its parent, fails the watch, as does an overflow of the
// kernel's queue, which may have dropped any of those. The bridge then
// sets the watch up again. refs and refs/heads are each watched only while
// they exist, as in a repository whose refs are all packed. Each one's
// parent is watched first, so one that appears after its own watch was
// refused for its absence shows in its parent's watch, which fails, and the
// next watch is made on it. A goroutine reads the inotify descriptor
// through the runtime's poller, so closing it ends the read.
type inotifyWatch struct {
	f      *os.File
	gitWd  int32
	refsWd int32 // -1 while refs is absent
	headWd int32 // -1 while refs/heads is absent
	note   chan struct{}
	end    chan error
}

func watchRefs(gitDir string) (watcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, os.NewSyscallError("inotify_init1", err)
	}
	w := &inotifyWatch{
		f:    os.NewFile(uintptr(fd), "inotify"),
		note: make(chan struct{}, 1),
		end:  make(chan error, 1),
	}
	const mask = syscall.IN_CREATE | syscall.IN_MOVED_TO | syscall.IN_MOVED_FROM | syscall.IN_DELETE |
		syscall.IN_CLOSE_WRITE | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF | syscall.IN_ONLYDIR
	add := func(dir string) (int32, error) {
		wd, err := syscall.InotifyAddWatch(fd, dir, mask)
		if err != nil {
			return -1, fmt.Errorf("%s: %w", dir, os.NewSyscallError("inotify_add_watch", err))
		}
		return int32(wd), nil
	}
	w.refsWd, w.headWd = -1, -1
	w.gitWd, err = add(gitDir)
	if err == nil {
		w.refsWd, err = add(filepath.Join(gitDir, "refs"))
	}
	if err == nil {
		w.headWd, err = add(filepath.Join(gitDir, "refs", "heads"))
	}
	if errors.Is(err, syscall.ENOENT) && w.gitWd >= 0 {
		err = nil
	}
	if err != nil {
		w.f.Close()
		return nil, err
	}
	go w.read()
	return w, nil
}

func (w *inotifyWatch) events() <-chan struct{} { return w.note }
func (w *inotifyWatch) failed() <-chan error    { return w.end }
func (w *inotifyWatch) close()                  { w.f.Close() }

func (w *inotifyWatch) read() {
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
				w.end <- errDropped
				return
			case mask&self != 0:
				w.end <- errors.New("a watched directory was deleted or moved")
				return
			case wd == w.gitWd && name == "refs":
				w.end <- errors.New("the refs directory appeared, went or was replaced")
				return
			case wd == w.refsWd && name == "heads":
				w.end <- errors.New("refs/heads appeared, went or was replaced")
				return
			case wd == w.gitWd && name == "packed-refs", wd == w.headWd && name == "main":
				select {
				case w.note <- struct{}{}:
				default:
				}
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

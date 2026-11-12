//go:build unix

package rebase

import "syscall"

// entryFlags open a cache entry without following a symlink, and without
// waiting for a writer, as opening a FIFO to read otherwise does.
const entryFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockState makes the state directory if it is missing and locks it for
// one pass, and returns what lets the lock go. The lock is flock's, on the
// directory's lock file. It is let go when the pass ends however it ends,
// since the kernel lets it go with the last descriptor of the file, and a
// second pass that asks for it while one holds it fails at once.
func lockState(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, lockFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another pass is using the state directory %s: it holds the lock on %s until it ends", dir, path)
		}
		return nil, fmt.Errorf("could not lock the state directory %s: %w", dir, err)
	}
	return func() { f.Close() }, nil
}

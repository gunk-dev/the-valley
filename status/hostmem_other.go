//go:build !linux

package main

import "errors"

// hostMemory is not read where there are no cgroups to find a limit in,
// so a pass there finds no evaluation budget unless one is given.
func hostMemory() (int64, error) {
	return 0, errors.New("only read on Linux")
}

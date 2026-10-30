//go:build !linux

// Package nodump makes a process non-dumpable, so the secrets it holds in
// memory stay in it alone. The publisher's link command holds a new
// link's secret, and valley-web with readers holds the links readers
// post and their sessions' tokens.
package nodump

import "errors"

// Set refuses outside Linux, where this package cannot make a process
// non-dumpable, so a program that needs it holds no secret there.
func Set() error {
	return errors.New("making the process non-dumpable needs Linux")
}

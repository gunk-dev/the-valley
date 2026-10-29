//go:build !linux

package main

import "errors"

// noCoreDump refuses outside Linux, where the publisher does not make its
// process non-dumpable, so no link's secret is made there.
func noCoreDump() error {
	return errors.New("making the process non-dumpable needs Linux")
}

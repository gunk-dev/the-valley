//go:build !unix

package main

import "errors"

// lockState refuses where there is no flock: a pass that cannot lock its
// state directory does not run.
func lockState(dir string) (func(), error) {
	return nil, errors.New("the restacker locks its state directory with flock, which this system does not have")
}

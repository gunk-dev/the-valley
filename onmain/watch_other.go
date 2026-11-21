//go:build !linux

package main

import "errors"

// watchRefs fails where there is no inotify: the bridge runs on Linux
// hosts alone.
func watchRefs(string) (watcher, error) {
	return nil, errors.New("onmain watches ref storage with inotify, which only Linux has")
}

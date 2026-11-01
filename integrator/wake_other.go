//go:build !linux

package main

import "errors"

// wakeSupported says whether watch can be given --woken-by here. Watching a
// wake directory needs Linux's inotify. Anywhere else, watch passes at its
// interval, as it does without the flag.
const wakeSupported = false

func watchDir(string) (watcher, error) {
	return nil, errors.New("watching a wake directory needs Linux's inotify")
}

//go:build !linux && !darwin

package main

import "os"

// windowColumns takes stdout for no terminal where it cannot ask one.
func windowColumns(*os.File) (int, bool) { return 0, false }

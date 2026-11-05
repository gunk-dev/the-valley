package main

import "syscall"

// hostMemory is the host's memory, in bytes.
func hostMemory() (int64, error) {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0, err
	}
	return int64(info.Totalram) * int64(info.Unit), nil
}

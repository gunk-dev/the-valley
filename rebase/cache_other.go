//go:build !unix

package rebase

// entryFlags are none where syscall has no O_NOFOLLOW. read still takes
// only a regular file.
const entryFlags = 0

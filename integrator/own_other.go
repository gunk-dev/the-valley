//go:build !unix

package main

// Where there is no flock, a controller does not lock the repository it
// serves, as before the lock existed.

type repoLock struct{}

func ownRepo(string) (*repoLock, error) { return nil, nil }

func (r *repoLock) check() error { return nil }
func (r *repoLock) Close() error { return nil }

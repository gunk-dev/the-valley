package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The state directory holds the scratch repository a pass reads origin
// into, and what a pass keeps between passes. Two passes on one directory
// would share the scratch repository and race each other's state, so a
// pass locks the directory from start to end (lockState). And what a pass
// keeps is about one repository's target, read and written as one
// principal, so the directory belongs to those three, the first a pass
// used it for (bindState).

// The files in a state directory beside the scratch repository.
const (
	lockFile    = "lock"         // what a pass holds a lock on while it runs
	bindingFile = "binding.json" // the repository, target and principal the directory belongs to
)

// binding is what a state directory belongs to: the repository as --repo
// gave it, the target, and the principal.
type binding struct {
	Repository string `json:"repository"`
	Target     string `json:"target"`
	Principal  string `json:"principal"`
}

// bindState binds the state directory to b the first time a pass uses it,
// and afterwards refuses a pass for anything else. It runs under the lock.
func bindState(dir string, b binding) error {
	path := filepath.Join(dir, bindingFile)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var was binding
		if err := json.Unmarshal(data, &was); err != nil {
			return fmt.Errorf("%s does not read, so whose state %s holds is not known: %v", path, dir, err)
		}
		if was != b {
			return fmt.Errorf("the state directory %s belongs to %s on %s as %s, and this pass is for %s on %s as %s: give each its own --state",
				dir, was.Target, was.Repository, was.Principal, b.Target, b.Repository, b.Principal)
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("reading what the state directory %s belongs to: %w", dir, err)
	}
	data, err = json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, bindingFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(append(data, '\n'))
	if serr := tmp.Sync(); err == nil {
		err = serr
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing what the state directory %s belongs to: %w", dir, err)
	}
	return os.Rename(tmp.Name(), path)
}

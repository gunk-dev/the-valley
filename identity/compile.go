package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func compile(args []string) error {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	repo := fs.String("repo", "", "the bare instance repository carrying the registry")
	ref := fs.String("ref", "refs/heads/main", "the ref whose tip is read")
	dir := fs.String("dir", "identity", "the registry's directory in that tip")
	schema := fs.String("schema", os.Getenv("VALLEY_IDENTITY_SCHEMA"), "the identity schema")
	knownSigners := fs.String("known-signers", "", "where the verifier keys are written")
	authorizedKeys := fs.String("authorized-keys", "", "where the tagged authorized_keys is written")
	now := fs.String("now", "", "the day expiry is judged against")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, required := range []struct{ flag, value string }{
		{"repo", *repo},
		{"schema", *schema},
		{"known-signers", *knownSigners},
		{"authorized-keys", *authorizedKeys},
	} {
		if required.value == "" {
			return fmt.Errorf("--%s is required", required.flag)
		}
	}

	// The day expiry is judged against. A flag rather than the clock alone,
	// so a compilation can be reproduced and a check can pin one.
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if *now != "" {
		parsed, err := time.Parse(dateLayout, *now)
		if err != nil {
			return fmt.Errorf("--now %q is not a calendar day", *now)
		}
		day = parsed
	}

	r, commit, err := readRegistry(*repo, *ref, *dir, *schema)
	if err != nil {
		return err
	}
	a, err := render(r, day)
	if err != nil {
		return fmt.Errorf("the registry at %s does not compile: %w", commit, err)
	}

	// The omissions, before the artifacts are written: an entry that has
	// aged out is the compiler doing its job, and it is not a silent one.
	for _, note := range a.notes {
		fmt.Fprintf(os.Stderr, "identity: %s\n", note)
	}

	signersWritten, err := writeArtifact(*knownSigners, a.knownSigners)
	if err != nil {
		return err
	}
	keysWritten, err := writeArtifact(*authorizedKeys, a.authorizedKeys)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "identity: %s at %s: %d verifier key(s)%s, %d authorized key(s)%s\n",
		*ref, commit, a.signers, changed(signersWritten), a.authorized, changed(keysWritten))
	return nil
}

func changed(written bool) string {
	if written {
		return " written"
	}
	return " unchanged"
}

// writeArtifact replaces one file by rename, and only when its content
// differs. Both artifacts are rendered before either is written, so the
// only way to see one of them newer than the other is a crash between the
// two renames — and neither can ever be seen half-written.
func writeArtifact(path string, content []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return false, nil
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(content); err != nil {
		f.Close()
		return false, err
	}
	// sshd reads the authorized_keys file and refuses one anybody but its
	// owner can write, so the mode is stated rather than left to CreateTemp.
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(f.Name(), path)
}

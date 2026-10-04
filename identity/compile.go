package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	grantsFile := fs.String("grants", "", "where the grants the pre-receive hook checks are written")
	declaredKeys := fs.String("declared-keys", "", "the authorized_keys lines the host declares by hand")
	now := fs.String("now", "", "the day expiry is judged against")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, required := range []struct{ flag, value string }{
		{"repo", *repo},
		{"schema", *schema},
		{"known-signers", *knownSigners},
		{"authorized-keys", *authorizedKeys},
		{"grants", *grantsFile},
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

	var declared map[string]string
	if *declaredKeys != "" {
		var err error
		if declared, err = readDeclaredKeys(*declaredKeys); err != nil {
			return err
		}
	}
	r, commit, err := readRegistry(*repo, *ref, *dir, *schema)
	if err != nil {
		return err
	}
	a, err := render(r, day, declared)
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
	grantsWritten, err := writeArtifact(*grantsFile, a.grants)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "identity: %s at %s: %d verifier key(s)%s, %d authorized key(s)%s, %d grant(s)%s\n",
		*ref, commit, a.signers, changed(signersWritten), a.authorized, changed(keysWritten),
		a.granted, changed(grantsWritten))
	return nil
}

// readDeclaredKeys reads the authorized_keys lines the host declares by
// hand, and maps each key to the principal its entry is tagged with.
func readDeclaredKeys(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	declared := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		blob := keyBlob(line)
		if blob == "" {
			continue
		}
		tag := ""
		if m := principalTag.FindStringSubmatch(line); m != nil {
			tag = m[1]
		}
		declared[blob] = tag
	}
	return declared, nil
}

// principalTag is the option a tagged entry carries.
var principalTag = regexp.MustCompile(`environment="` + principalEnv + `=([^"]*)"`)

// keyBlob is the base64 key in an authorized_keys line: the field after the
// first one naming a key type.
func keyBlob(line string) string {
	fields := strings.Fields(line)
	for i, f := range fields[:max(len(fields)-1, 0)] {
		if strings.HasPrefix(f, "ssh-") || strings.HasPrefix(f, "ecdsa-") || strings.HasPrefix(f, "sk-") {
			return fields[i+1]
		}
	}
	return ""
}

func changed(written bool) string {
	if written {
		return " written"
	}
	return " unchanged"
}

// writeArtifact replaces one file by rename, and only when its content
// differs. Every artifact is rendered before any is written, so the only
// way to see one of them newer than another is a crash between two renames
// — and none can ever be seen half-written.
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

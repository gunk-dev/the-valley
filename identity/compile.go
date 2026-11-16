package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"the-valley/approval"
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
	approvalSignersFile := fs.String("approval-signers", "", "where the verifier keys of every approval key are written")
	approvalHardwareFile := fs.String("approval-signers-hardware", "", "where the verifier keys of the hardware approval keys are written")
	declaredKeys := fs.String("declared-keys", "", "the authorized_keys lines the host declares by hand")
	now := fs.String("now", "", "the day expiry and certificate validity are judged against")
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
	// A full ref name is read by that exact name. Any other name, such as
	// main, git would resolve by trying several refs in turn, and a pusher
	// can make one of them.
	if !strings.HasPrefix(*ref, "refs/") {
		return fmt.Errorf("--ref %s is not a full ref name; name it whole, as refs/heads/main", *ref)
	}

	// The day expiry is judged against. A flag rather than the clock alone,
	// so a compilation can be reproduced and a check can pin one.
	c := compilation{
		repo: *repo, ref: *ref, dir: *dir, schema: *schema,
		knownSigners: *knownSigners, authorizedKeys: *authorizedKeys, grants: *grantsFile,
		approvalSigners: *approvalSignersFile, approvalHardware: *approvalHardwareFile,
		today: func() time.Time { return time.Now().UTC().Truncate(24 * time.Hour) },
	}
	// A retry budget, not a count the inputs imply: room for the ref to
	// move once, and the day to turn once unless --now pins it, while a
	// run compiles, with a last compilation for the reading after them.
	rounds := 3
	if *now != "" {
		parsed, err := time.Parse(dateLayout, *now)
		if err != nil {
			return fmt.Errorf("--now %q is not a calendar day", *now)
		}
		c.today = func() time.Time { return parsed }
		rounds = 2
	}

	if *declaredKeys != "" {
		var err error
		if c.declared, err = readDeclaredKeys(*declaredKeys); err != nil {
			return err
		}
	}
	return converge(c.ref, rounds, c.read, c.compileAt)
}

// reading is what a compilation depends on that can change while it runs:
// the object the ref holds, and the day expiry is judged against.
type reading struct {
	oid string
	day time.Time
}

func (r reading) equal(o reading) bool {
	return r.oid == o.oid && r.day.Equal(o.day)
}

// converge compiles what read returns, reads again, and compiles again
// until a reading matches the one compiled, so a ref moved or a day turned
// while a run compiles is compiled by that run. What starts a run cannot
// be relied on to start another for such a change: systemd holds no watch
// on the ref while the compiler runs, and does not see afterwards that the
// ref was replaced, and a start requested while the compiler runs is
// merged into that run. A change made after the last reading waits for
// whatever starts the next run.
//
// A compilation that fails ends the run only when the reading after it
// matches what it compiled. If the ref or the day changed meanwhile, the
// failure is said and the run compiles again, so a landing that repairs a
// registry while the broken one compiles is compiled by that run.
//
// rounds is the retry budget. A run whose inputs still change after that
// many compilations fails, so the unit shows it. Its artifacts keep what
// its compilations wrote: each is replaced whole, but a write that fails
// part way leaves the ones before it from the newer registry.
func converge(ref string, rounds int, read func() (reading, error), compileAt func(reading) error) error {
	at, err := read()
	if err != nil {
		return err
	}
	for round := 1; ; round++ {
		failed := compileAt(at)
		again, err := read()
		if err != nil {
			return errors.Join(failed, err)
		}
		if again.equal(at) {
			return failed
		}
		if round == rounds {
			return errors.Join(failed, fmt.Errorf("%s kept moving while the registry was compiled: after %d compilations it holds %s", ref, rounds, again.oid))
		}
		if failed != nil {
			fmt.Fprintf(os.Stderr, "identity: %v\n", failed)
		}
		if again.oid != at.oid {
			fmt.Fprintf(os.Stderr, "identity: %s moved to %s while %s was compiled, so the registry is compiled again\n", ref, again.oid, at.oid)
		} else {
			fmt.Fprintf(os.Stderr, "identity: the day turned to %s while the registry was compiled, so it is compiled again\n", again.day.Format(dateLayout))
		}
		at = again
	}
}

// compilation is where one run reads the registry and writes its
// artifacts.
type compilation struct {
	repo, ref, dir, schema               string
	knownSigners, authorizedKeys, grants string
	approvalSigners, approvalHardware    string
	declared                             map[string]string
	today                                func() time.Time
}

// read resolves the ref by its exact name, and reads the day.
func (c compilation) read() (reading, error) {
	oid, ok, err := resolveRef(c.repo, c.ref)
	if err != nil {
		return reading{}, fmt.Errorf("could not read %s in %s: %w", c.ref, c.repo, err)
	} else if !ok {
		return reading{}, fmt.Errorf("%s has no %s", c.repo, c.ref)
	}
	return reading{oid: oid, day: c.today()}, nil
}

// compileAt renders the registry the reading's object holds, as of the
// reading's day, and writes every artifact whose bytes changed.
func (c compilation) compileAt(at reading) error {
	r, commit, err := readRegistry(c.repo, c.ref, at.oid, c.dir, c.schema)
	if err != nil {
		return err
	}
	a, err := render(r, at.day, c.declared)
	if err != nil {
		return fmt.Errorf("the registry at %s does not compile: %w", commit, err)
	}

	approvers, hardware, err := stampApproval(a, approval.Authority{Ref: c.ref, Commit: commit, Day: at.day})
	if err != nil {
		return err
	}

	// The omissions, before the artifacts are written: an entry that has
	// aged out is the compiler doing its job, and it is not a silent one.
	for _, note := range a.notes {
		fmt.Fprintf(os.Stderr, "identity: %s\n", note)
	}

	signersWritten, err := writeArtifact(c.knownSigners, a.knownSigners)
	if err != nil {
		return err
	}
	keysWritten, err := writeArtifact(c.authorizedKeys, a.authorizedKeys)
	if err != nil {
		return err
	}
	grantsWritten, err := writeArtifact(c.grants, a.grants)
	if err != nil {
		return err
	}

	// The approval files are written only where the caller names them.
	// Every render computes both, so a registry whose approval keys do not
	// compile fails the whole compilation either way.
	approval := ""
	for _, f := range []struct {
		path    string
		content []byte
		count   int
		what    string
	}{
		{c.approvalSigners, approvers, a.approvers, "approval signer(s)"},
		{c.approvalHardware, hardware, a.hardware, "hardware approval signer(s)"},
	} {
		if f.path == "" {
			continue
		}
		written, err := writeArtifact(f.path, f.content)
		if err != nil {
			return err
		}
		approval += fmt.Sprintf(", %d %s%s", f.count, f.what, changed(written))
	}

	fmt.Fprintf(os.Stderr, "identity: %s at %s: %d verifier key(s)%s, %d authorized key(s)%s, %d grant(s)%s%s\n",
		c.ref, commit, a.signers, changed(signersWritten), a.authorized, changed(keysWritten),
		a.granted, changed(grantsWritten), approval)
	return nil
}

// stampApproval is the two approval signers files as they are written:
// each opens with the authority stamp (approval.Authority), naming the ref
// the registry was read at, the commit that ref resolved to, and the day
// expiry was judged on, and then holds the rendered file byte for byte.
// The commit is the one every document was read from, so the stamp names
// exactly what the keys below it render, even when the ref has moved
// since.
func stampApproval(a artifacts, source approval.Authority) (approvers, hardware []byte, err error) {
	stamp, err := source.Stamp()
	if err != nil {
		return nil, nil, fmt.Errorf("the approval signers files cannot be stamped: %w", err)
	}
	return append([]byte(stamp), a.approvalSigners...), append([]byte(stamp), a.approvalHardware...), nil
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

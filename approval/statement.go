// Package approval is an approval's signed statement: what an approver's
// key signs to say that one recorded head of a change may land on a
// target, how the statement is written, signed and sealed in a note, and
// how a reader verifies it against the approval keys it holds.
//
// A statement is written in the attestation's line form: the statement
// type on the first line, then one key, one space and one value per line,
// in a fixed order, with no escapes and a closing newline.
//
//	the-valley/approval/v1
//	instance 5f0c…
//	project app
//	target main
//	change nxvpwlkmqtsrzyoukklnpmqrstuvwxyz
//	version 2
//	head git-sha1:2222…
//	record git-sha1:3333…
//	principal operator
//	issued 2026-11-06T00:43:26Z
//
// The issue time is for the record. A statement carries no expiry: whether
// an approval still lands is decided by what a landing checks, never by a
// clock.
//
// The package depends on the standard library and this repository alone.
package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"the-valley/change"
	"the-valley/note"
)

const (
	// Type is the first line of every approval statement. The signature
	// covers the text alone, so this line is what keeps an approval key's
	// signature over an attestation, or anything else, from reading as an
	// approval.
	Type = "the-valley/approval/v1"

	// MaxStatement is the most bytes a statement may hold. It is the bound
	// the hardware signer is held to, so a statement is refused before any
	// device is asked to sign it.
	MaxStatement = 1024

	// MaxNote is the most bytes an approval note may hold: a statement and
	// one signature line named by its principal, which the statement
	// already holds.
	MaxNote = 2 * MaxStatement

	// TimeLayout is how the issue time is written: UTC, to the second.
	TimeLayout = "2006-01-02T15:04:05Z"

	// MaxProject is the most bytes in a project name, as the registry
	// schema bounds it.
	MaxProject = 64

	instanceType = "valley-instance-v1"
)

// keys is the written form's line order, after the type line.
var keys = []string{"instance", "project", "target", "change", "version", "head", "record", "principal", "issued"}

// name is a project's or a principal's name, as the schemas spell one.
var name = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Statement is what an approver signs.
type Statement struct {
	// Instance is the instance's identity, InstanceID of its repository.
	Instance string
	// Project and Target are where the change lands.
	Project string
	Target  string
	// Change and Version are the change and the version of it whose head
	// was approved; Head is that head commit, as git-sha1:<hex>.
	Change  string
	Version int
	Head    string
	// Record is the change-record commit the approver read.
	Record string
	// Principal is who approves: the name their approval key is listed
	// under.
	Principal string
	// Issued is when the statement was made, to the second.
	Issued time.Time
}

// Encode writes the statement in its written form, after holding every
// field to what the form allows.
func (s Statement) Encode() ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(Type + "\n")
	for i, v := range s.values() {
		b.WriteString(keys[i] + " " + v + "\n")
	}
	if b.Len() > MaxStatement {
		return nil, fmt.Errorf("the statement is %d bytes; an approval statement holds at most %d, the most the signers support", b.Len(), MaxStatement)
	}
	return b.Bytes(), nil
}

func (s Statement) values() []string {
	return []string{s.Instance, s.Project, s.Target, s.Change, strconv.Itoa(s.Version),
		s.Head, s.Record, s.Principal, s.Issued.UTC().Format(TimeLayout)}
}

func (s Statement) check() error {
	switch {
	case !isHex(s.Instance, sha256.Size*2):
		return fmt.Errorf("the instance %q is not 64 lowercase hex digits", s.Instance)
	case !name.MatchString(s.Project) || len(s.Project) > MaxProject:
		return fmt.Errorf("the project %q is not a project name of at most %d bytes", s.Project, MaxProject)
	case !change.ValidBranch(s.Target):
		return fmt.Errorf("the target %q is not a branch name", s.Target)
	case !change.ValidID(s.Change):
		return fmt.Errorf("the change %q is not a change ID: one is %d letters from k to z", s.Change, change.IDLength)
	case s.Version < 1 || s.Version > change.MaxVersions:
		return fmt.Errorf("the version %d is outside 1 to %d", s.Version, change.MaxVersions)
	case !isCommit(s.Head):
		return fmt.Errorf("the head %q is not %s and 40 lowercase hex digits", s.Head, change.HeadPrefix)
	case !isCommit(s.Record):
		return fmt.Errorf("the record %q is not %s and 40 lowercase hex digits", s.Record, change.HeadPrefix)
	case !name.MatchString(s.Principal):
		return fmt.Errorf("the principal %q is not a principal name", s.Principal)
	case s.Issued.IsZero():
		return errors.New("the statement has no issue time")
	case s.Issued.Nanosecond() != 0 || s.Issued.UTC().Year() < 1 || s.Issued.UTC().Year() > 9999:
		return fmt.Errorf("the issue time %s is not a whole second from year 1 to 9999", s.Issued)
	}
	return nil
}

// Parse reads a statement's written form. Text is accepted only when it is
// exactly what Encode writes for the statement it says: every line in its
// place, no line missing, unknown or repeated, and every value in its one
// spelling.
func Parse(text []byte) (Statement, error) {
	if len(text) > MaxStatement {
		return Statement{}, fmt.Errorf("the statement is %d bytes; an approval statement holds at most %d", len(text), MaxStatement)
	}
	if err := note.CheckText(text); err != nil {
		return Statement{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	if lines[0] != Type {
		return Statement{}, fmt.Errorf("is not an approval: its text opens with %q, not %q", lines[0], Type)
	}
	if len(lines) != 1+len(keys) {
		return Statement{}, fmt.Errorf("holds %d lines; an approval statement holds %d", len(lines), 1+len(keys))
	}
	v := make([]string, len(keys))
	for i, key := range keys {
		k, value, ok := strings.Cut(lines[i+1], " ")
		if !ok || k != key {
			return Statement{}, fmt.Errorf("line %d is %q, where the %s line belongs", i+2, lines[i+1], key)
		}
		v[i] = value
	}
	version, err := strconv.Atoi(v[4])
	if err != nil {
		return Statement{}, fmt.Errorf("the version %q is not a number", v[4])
	}
	issued, err := time.Parse(TimeLayout, v[8])
	if err != nil {
		return Statement{}, fmt.Errorf("the issue time %q is not written %s", v[8], TimeLayout)
	}
	s := Statement{Instance: v[0], Project: v[1], Target: v[2], Change: v[3], Version: version,
		Head: v[5], Record: v[6], Principal: v[7], Issued: issued}
	written, err := s.Encode()
	if err != nil {
		return Statement{}, err
	}
	if !bytes.Equal(written, text) {
		return Statement{}, errors.New("is not in the statement's one written form")
	}
	return s, nil
}

// InstanceID is an instance's identity, derived from its repository's
// history: SHA-256 over the line valley-instance-v1, the line
// object-format <format>, and one line root <id> for each root commit of
// the instance repository's integrated tip, sorted bytewise. Repositories
// sharing that history share the identity.
func InstanceID(format string, roots []string) (string, error) {
	size := map[string]int{"sha1": 40, "sha256": 64}[format]
	if size == 0 {
		return "", fmt.Errorf("the object format %q is not sha1 or sha256", format)
	}
	if len(roots) == 0 {
		return "", errors.New("an instance identity needs at least one root commit")
	}
	sorted := slices.Clone(roots)
	slices.Sort(sorted)
	var b strings.Builder
	b.WriteString(instanceType + "\nobject-format " + format + "\n")
	for i, r := range sorted {
		if !isHex(r, size) {
			return "", fmt.Errorf("the root %q is not a %s object ID", r, format)
		}
		if i > 0 && r == sorted[i-1] {
			return "", fmt.Errorf("the root %s is listed twice", r)
		}
		b.WriteString("root " + r + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

func isCommit(s string) bool {
	id, ok := strings.CutPrefix(s, change.HeadPrefix)
	return ok && isHex(id, 40)
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

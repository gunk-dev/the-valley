// Package change is the change record: the versions of one change, as the
// valley command writes them and the valley's push hook guards them.
//
// A change is one piece of work aimed at a target branch, with a stable
// ID that survives rebases. Each time its author marks a new head ready,
// the change gains a version. The record lists every version in order,
// says which one is active, and says whether the change is ready or
// withdrawn.
//
// A record lives at refs/the-valley/changes/<target>/<change ID>. The ref
// points at a record commit whose tree holds one file, record.json, in the
// schema valley-change-record/v1:
//
//	{
//	  "schema": "valley-change-record/v1",
//	  "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
//	  "target": "main",
//	  "owner": "klaus-env",
//	  "state": "ready",
//	  "seq": 1,
//	  "versions": [
//	    {"n": 1, "head": "git-sha1:<40 hex>", "title": "the head's subject",
//	     "branch": "topic", "uploader": "klaus-env"}
//	  ],
//	  "active": 1
//	}
//
// Reading is strict. The record must be valid UTF-8, name no key twice in
// any object, hold no field the schema does not, and hold nothing after
// the object. Every field has exactly the type above, and every integer is
// in range. This package is the one reading of a record: the valley
// command reads the record it is about to extend with it, and the hook
// reads both sides of every pushed update with it, so a record one of them
// accepts is a record the other accepts.
//
// The package depends on the standard library alone, like everything that
// uses it.
package change

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Schema names the record format.
const Schema = "valley-change-record/v1"

// The states a change can be in.
const (
	Ready     = "ready"
	Withdrawn = "withdrawn"
)

// The bounds a record is held to.
const (
	MaxSeq      = 100000 // record updates in a change's life
	MaxVersions = 1000   // versions of one change
	MaxTitle    = 200    // bytes in a version's title
)

// HeadPrefix opens a version's head: the head is named by its hash
// function and then its object ID.
const HeadPrefix = "git-sha1:"

// Record is one change's record.
type Record struct {
	Schema   string    `json:"schema"`
	ChangeID string    `json:"change_id"`
	Target   string    `json:"target"`
	Owner    string    `json:"owner"`
	State    string    `json:"state"`
	Seq      int       `json:"seq"`
	Versions []Version `json:"versions"`
	Active   int       `json:"active"`
}

// Version is one head a change was marked ready at.
type Version struct {
	N        int    `json:"n"`
	Head     string `json:"head"`
	Title    string `json:"title"`
	Branch   string `json:"branch"`
	Uploader string `json:"uploader"`
}

// ActiveVersion is the version Active names. A record that parsed always
// has one.
func (r *Record) ActiveVersion() Version {
	return r.Versions[r.Active-1]
}

// Last is the newest version.
func (r *Record) Last() Version {
	return r.Versions[len(r.Versions)-1]
}

// Parse reads record.json, strictly.
func Parse(data []byte) (*Record, error) {
	v, err := readJSON(data)
	if err != nil {
		return nil, err
	}
	o, err := fieldsOf(v, "the record", "schema", "change_id", "target", "owner", "state", "seq", "versions", "active")
	if err != nil {
		return nil, err
	}
	r := &Record{}
	for _, f := range []struct {
		name string
		into *string
	}{
		{"schema", &r.Schema}, {"change_id", &r.ChangeID}, {"target", &r.Target},
		{"owner", &r.Owner}, {"state", &r.State},
	} {
		if *f.into, err = stringOf(o[f.name], f.name); err != nil {
			return nil, err
		}
	}
	if r.Seq, err = integerOf(o["seq"], "seq", MaxSeq); err != nil {
		return nil, err
	}
	if r.Active, err = integerOf(o["active"], "active", MaxVersions); err != nil {
		return nil, err
	}
	versions := o["versions"]
	if versions.kind != kindArray {
		return nil, fmt.Errorf("its versions is %s, where an array is expected", versions.kind)
	}
	if len(versions.items) > MaxVersions {
		return nil, fmt.Errorf("it holds %d versions, more than the %d a change may have", len(versions.items), MaxVersions)
	}
	for i, item := range versions.items {
		where := fmt.Sprintf("versions[%d]", i)
		vo, err := fieldsOf(item, where, "n", "head", "title", "branch", "uploader")
		if err != nil {
			return nil, err
		}
		var ver Version
		if ver.N, err = integerOf(vo["n"], where+".n", MaxVersions); err != nil {
			return nil, err
		}
		for _, f := range []struct {
			name string
			into *string
		}{
			{"head", &ver.Head}, {"title", &ver.Title}, {"branch", &ver.Branch}, {"uploader", &ver.Uploader},
		} {
			if *f.into, err = stringOf(vo[f.name], where+"."+f.name); err != nil {
				return nil, err
			}
		}
		r.Versions = append(r.Versions, ver)
	}
	if err := r.Check(); err != nil {
		return nil, err
	}
	return r, nil
}

// Check holds a record to everything the schema says beyond the types of
// its fields: the formats of its strings, the ranges of its integers, and
// versions numbered 1, 2, 3 with no gap.
func (r *Record) Check() error {
	switch {
	case r.Schema != Schema:
		return fmt.Errorf("its schema is %q, where %q is expected", r.Schema, Schema)
	case !ValidID(r.ChangeID):
		return fmt.Errorf("its change_id %q is not a change ID: 32 letters from k to z", r.ChangeID)
	case !ValidBranch(r.Target):
		return fmt.Errorf("its target %q is not a branch name", r.Target)
	case !ValidPrincipal(r.Owner):
		return fmt.Errorf("its owner %q is not a principal name", r.Owner)
	case r.State != Ready && r.State != Withdrawn:
		return fmt.Errorf("its state is %q, where %q or %q is expected", r.State, Ready, Withdrawn)
	case r.Seq < 1 || r.Seq > MaxSeq:
		return fmt.Errorf("its seq is %d, outside 1 to %d", r.Seq, MaxSeq)
	case len(r.Versions) == 0:
		return errors.New("it holds no version, and a change has at least one")
	case len(r.Versions) > MaxVersions:
		return fmt.Errorf("it holds %d versions, more than the %d a change may have", len(r.Versions), MaxVersions)
	case r.Active < 1 || r.Active > len(r.Versions):
		return fmt.Errorf("its active is %d, which names none of its %d versions", r.Active, len(r.Versions))
	}
	for i, v := range r.Versions {
		where := fmt.Sprintf("versions[%d]", i)
		switch {
		case v.N != i+1:
			return fmt.Errorf("its %s.n is %d, and versions are numbered 1, 2, 3 in order with no gap", where, v.N)
		case !ValidHead(v.Head):
			return fmt.Errorf("its %s.head %q is not %s and 40 lowercase hex digits", where, v.Head, HeadPrefix)
		case !ValidTitle(v.Title):
			return fmt.Errorf("its %s.title is not 1 to %d bytes free of control characters", where, MaxTitle)
		case v.Branch != "" && !ValidBranch(v.Branch):
			return fmt.Errorf("its %s.branch %q is not a branch name", where, v.Branch)
		case !ValidPrincipal(v.Uploader):
			return fmt.Errorf("its %s.uploader %q is not a principal name", where, v.Uploader)
		}
	}
	return nil
}

// Encode writes a record as record.json. It refuses a record Parse would
// refuse, so what it writes always reads back as the same record.
func Encode(r *Record) ([]byte, error) {
	if err := r.Check(); err != nil {
		return nil, fmt.Errorf("the record cannot be written: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fieldsOf is v's fields, when v is an object holding exactly the fields
// named, no more and no fewer.
func fieldsOf(v *value, what string, names ...string) (map[string]*value, error) {
	if v.kind != kindObject {
		return nil, fmt.Errorf("%s is %s, where an object is expected", what, v.kind)
	}
	for _, key := range v.keys {
		known := false
		for _, n := range names {
			known = known || key == n
		}
		if !known {
			return nil, fmt.Errorf("%s holds the field %q, which the schema does not have", what, key)
		}
	}
	for _, n := range names {
		if _, ok := v.fields[n]; !ok {
			return nil, fmt.Errorf("%s has no field %q", what, n)
		}
	}
	return v.fields, nil
}

func stringOf(v *value, name string) (string, error) {
	if v.kind != kindString {
		return "", fmt.Errorf("its %s is %s, where a string is expected", name, v.kind)
	}
	return v.text, nil
}

// integerOf reads an integer from 1 to max. Only a plain run of digits is
// an integer: 3.0 and 3e0 are numbers of another type.
func integerOf(v *value, name string, max int) (int, error) {
	if v.kind != kindNumber {
		return 0, fmt.Errorf("its %s is %s, where an integer is expected", name, v.kind)
	}
	for _, c := range []byte(v.text) {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("its %s is %s, where an integer is expected", name, v.text)
		}
	}
	n, err := strconv.Atoi(v.text)
	if err != nil || n < 1 || n > max {
		return 0, fmt.Errorf("its %s is %s, outside 1 to %d", name, v.text, max)
	}
	return n, nil
}

// principalName is the shape of a principal's name (#PrincipalName in
// schema/valley.cue).
var principalName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidPrincipal reports whether s is a principal's name.
func ValidPrincipal(s string) bool {
	return principalName.MatchString(s)
}

// ValidHead reports whether s names a head the way a version does.
func ValidHead(s string) bool {
	if len(s) != len(HeadPrefix)+40 || s[:len(HeadPrefix)] != HeadPrefix {
		return false
	}
	for _, c := range []byte(s[len(HeadPrefix):]) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ValidTitle reports whether s may be a version's title: UTF-8, 1 to
// MaxTitle bytes, holding no control character.
func ValidTitle(s string) bool {
	if len(s) < 1 || len(s) > MaxTitle || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Title makes a version's title from a commit's subject. Each control
// character becomes a space, and bytes that are not UTF-8 become U+FFFD.
// The result is cut to MaxTitle bytes at a character boundary. A subject
// with nothing left is titled "(no subject)".
func Title(subject string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(subject, "�") {
		if unicode.IsControl(r) {
			r = ' '
		}
		if b.Len()+utf8.RuneLen(r) > MaxTitle {
			break
		}
		b.WriteRune(r)
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		return t
	}
	return "(no subject)"
}

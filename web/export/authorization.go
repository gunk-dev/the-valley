package export

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"the-valley/note"
)

const (
	// AuthorizationType is the first line of every authorization's text.
	AuthorizationType = "the-valley/web-authorization/v1"

	// MaxAuthorization is the most bytes of a signed authorization a
	// receiver reads, and a server reads of the stored one.
	MaxAuthorization = 256 << 10

	// Anyone is the reader every visitor is, signed in or not.
	Anyone = "anyone"
)

// Authorization is the signed statement of who may read which project at
// one destination. The publisher's policy path alone writes it, with the
// same key as content manifests, and the receiver stores the newest one it
// admitted. A server serves a project only while the stored authorization
// names it, by name and publication identity, with a reader the request
// is. Content manifests carry nothing of it, so no content upload can
// grant or restore access.
//
// Its text is the type line and then one record per line, each a keyword
// and its fields separated by single spaces:
//
//	the-valley/web-authorization/v1
//	instance valley-host
//	destination example.org
//	sequence 42
//	checked 2026-10-29T01:03:00Z
//	project app 9da6809f038df92138c131cd16bed76d
//	reader app anyone
//
// The sequence is the publisher's own counter, independent of any clock:
// each authorization it signs has a sequence greater than every one before.
// A receiver admits an authorization by its sequence alone, never by its
// age. Checked is when the publisher last finished a policy check in full;
// pages show it, and nothing decides access by it. A project has one or
// more reader records.
//
// An authorization may carry a basis record, basis <sequence>, after the
// checked record. It names the authorization the receiver must hold for
// this one to be stored: the publisher writes it when a project keeps
// access it could not check again, which it keeps only from the
// authorization the receiver last said it stored. A receiver that holds
// another refuses it as stale-basis, so access kept from a stale record
// never reaches the edge.
type Authorization struct {
	Instance    string
	Destination string
	Sequence    int64
	Checked     time.Time
	// Basis is the sequence of the authorization the receiver must hold,
	// or 0 for none.
	Basis    int64
	Projects []AuthorizedProject
}

// AuthorizedProject is one project an authorization names: its name, its
// publication identity, and its effective readers, sorted.
type AuthorizedProject struct {
	Name     string
	Identity string
	Readers  []string
}

// Text is the authorization's text: the type line, the instance,
// destination, sequence and checked records, then each project's records
// in name order, its readers sorted.
func (a *Authorization) Text() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\ninstance %s\ndestination %s\nsequence %d\nchecked %s\n",
		AuthorizationType, a.Instance, a.Destination, a.Sequence, a.Checked.UTC().Format(time.RFC3339))
	if a.Basis > 0 {
		fmt.Fprintf(&b, "basis %d\n", a.Basis)
	}
	projects := append([]AuthorizedProject{}, a.Projects...)
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	for _, p := range projects {
		fmt.Fprintf(&b, "project %s %s\n", p.Name, p.Identity)
		readers := append([]string{}, p.Readers...)
		sort.Strings(readers)
		for _, r := range readers {
			fmt.Fprintf(&b, "reader %s %s\n", p.Name, r)
		}
	}
	return b.Bytes()
}

// Sign is the authorization signed as a note by the publisher's export
// key. It is checked against ParseAuthorization first, so the publisher
// never signs what a receiver would refuse to read.
func (a *Authorization) Sign(name string, key ed25519.PrivateKey) ([]byte, error) {
	text := a.Text()
	if _, err := ParseAuthorization(text); err != nil {
		return nil, err
	}
	return note.Sign(text, name, key)
}

// Allows reports whether the authorization lets reader read the project
// called name with the publication identity given.
func (a *Authorization) Allows(reader, name, identity string) bool {
	if a == nil {
		return false
	}
	for _, p := range a.Projects {
		if p.Name == name && p.Identity == identity {
			for _, r := range p.Readers {
				if r == reader {
					return true
				}
			}
		}
	}
	return false
}

// Names reports whether the authorization names the project called name
// with the publication identity given, for any reader.
func (a *Authorization) Names(name, identity string) bool {
	p, ok := a.Project(name)
	return ok && p.Identity == identity
}

// Project is the authorization's project called name.
func (a *Authorization) Project(name string) (AuthorizedProject, bool) {
	if a == nil {
		return AuthorizedProject{}, false
	}
	for _, p := range a.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return AuthorizedProject{}, false
}

// AuthorizationAdmission is what a receiver admits an authorization
// against: the pinned verifier keys, the instance and destination it is
// configured with, and the sequence of the authorization it stores. It
// holds no clock: an authorization is never refused for its age.
type AuthorizationAdmission struct {
	Verifiers   []note.VerifierKey
	Instance    string
	Destination string
	HighWater   int64
}

// VerifyAuthorization admits a signed authorization, in this order, each
// step its own:
//
//  1. read at most MaxAuthorization bytes;
//  2. open the note with the pinned verifier keys;
//  3. require the type line, then read the records;
//  4. require the configured instance and destination;
//  5. require a sequence greater than the stored authorization's;
//  6. require a basis, when it has one, equal to the stored
//     authorization's sequence.
//
// There is no step for its age or its checked time: a valid narrowing
// above the stored sequence is admitted however old it is, and however far
// the clocks disagree. Every refusal is an *Error.
func VerifyAuthorization(r io.Reader, a AuthorizationAdmission) (*Authorization, error) {
	signed, err := io.ReadAll(io.LimitReader(r, MaxAuthorization+1))
	if err != nil {
		return nil, refuse(CodeUnreadable, "the authorization could not be read: %v", err)
	}
	if len(signed) > MaxAuthorization {
		return nil, refuse(CodeTooLarge, "an authorization is at most %d bytes", MaxAuthorization)
	}
	text, _, err := note.Open(signed, a.Verifiers)
	if err != nil {
		return nil, refuse(CodeBadSignature, "the authorization does not open under the pinned key: %v", err)
	}
	auth, err := ParseAuthorization(text)
	if err != nil {
		return nil, err
	}
	if auth.Instance != a.Instance {
		return nil, refuse(CodeWrongInstance, "the authorization is from instance %s, and this receiver takes %s", auth.Instance, a.Instance)
	}
	if auth.Destination != a.Destination {
		return nil, refuse(CodeWrongDestination, "the authorization is for %s, and this receiver is %s", auth.Destination, a.Destination)
	}
	if err := auth.admissible(a.HighWater); err != nil {
		return nil, err
	}
	return auth, nil
}

// admissible is steps 5 and 6 of VerifyAuthorization against the stored
// authorization's sequence, hw. Store.Authorize takes them again under its
// lock.
func (a *Authorization) admissible(hw int64) error {
	if a.Sequence <= hw {
		e := refuse(CodeReplayed, "the authorization's sequence %d is not greater than %d, the stored authorization's", a.Sequence, hw)
		e.HighWater = hw
		return e
	}
	if a.Basis > 0 && a.Basis != hw {
		e := refuse(CodeStaleBasis, "the authorization keeps access from authorization %d, and this receiver holds %d", a.Basis, hw)
		e.HighWater = hw
		return e
	}
	return nil
}

// ReadStoredAuthorization reads an authorization a receiver stored: the
// signed note it admitted. The receiver verified the signature before it
// stored it, so this reads the text without verifying it again, and holds
// it to ParseAuthorization.
func ReadStoredAuthorization(signed []byte) (*Authorization, error) {
	if len(signed) > MaxAuthorization {
		return nil, fmt.Errorf("the stored authorization is larger than %d bytes", MaxAuthorization)
	}
	text, _, err := note.Split(signed)
	if err != nil {
		return nil, fmt.Errorf("the stored authorization is not a signed note: %w", err)
	}
	return ParseAuthorization(text)
}

// ParseAuthorization reads an authorization's text, step 3 of
// VerifyAuthorization. The first line is exactly AuthorizationType. Each
// record after it is a keyword and exactly its fields. The instance,
// destination, sequence and checked records appear once each, a project at
// most once, and every project has one or more readers, each named once.
// A reader record names a project that has a project record. Records may
// come in any order; Text writes them in one.
func ParseAuthorization(text []byte) (*Authorization, error) {
	bad := func(format string, args ...any) error { return refuse(CodeBadManifest, format, args...) }
	if len(text) == 0 || text[len(text)-1] != '\n' {
		return nil, bad("an authorization's text ends with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	if lines[0] != AuthorizationType {
		return nil, bad("the authorization opens with %q, not %q", lines[0], AuthorizationType)
	}
	arity := map[string]int{"instance": 1, "destination": 1, "sequence": 1, "checked": 1, "basis": 1, "project": 2, "reader": 2}
	a := &Authorization{}
	seen := map[string]bool{}
	projects := map[string]*AuthorizedProject{}
	readers := map[string]map[string]bool{}
	for i, line := range lines[1:] {
		n := i + 2
		fields := strings.Split(line, " ")
		key, args := fields[0], fields[1:]
		want, known := arity[key]
		switch {
		case !known:
			return nil, bad("line %d is %q; no record opens with %q", n, line, key)
		case len(args) != want:
			return nil, bad("line %d is %q; a %s record has %d fields", n, line, key, want)
		}
		for _, f := range args {
			if f == "" {
				return nil, bad("line %d is %q; fields are separated by one space", n, line)
			}
		}
		switch key {
		case "instance", "destination", "sequence", "checked", "basis":
			if seen[key] {
				return nil, bad("line %d is a second %s record", n, key)
			}
			seen[key] = true
		}
		switch key {
		case "instance", "destination":
			if !ValidName(args[0]) {
				return nil, bad("line %d names %s %q, which is not a name", n, key, args[0])
			}
			if key == "instance" {
				a.Instance = args[0]
			} else {
				a.Destination = args[0]
			}
		case "sequence":
			seq, ok := decimal(args[0])
			if !ok || seq == 0 {
				return nil, bad("line %d gives sequence %q; a sequence is a positive decimal", n, args[0])
			}
			a.Sequence = seq
		case "basis":
			basis, ok := decimal(args[0])
			if !ok || basis == 0 {
				return nil, bad("line %d gives basis %q; a basis is a positive decimal", n, args[0])
			}
			a.Basis = basis
		case "checked":
			at, err := time.Parse(time.RFC3339, args[0])
			if err != nil || at.UTC().Format(time.RFC3339) != args[0] {
				return nil, bad("line %d gives checked %q; it is a time in RFC 3339, in UTC, to the second", n, args[0])
			}
			a.Checked = at.UTC()
		case "project":
			name, id := args[0], args[1]
			if !ValidName(name) {
				return nil, bad("line %d names project %q, which is not a project name", n, name)
			}
			if projects[name] != nil {
				return nil, bad("line %d is a second project record for %s", n, name)
			}
			if !ValidIdentity(id) {
				return nil, bad("line %d gives %s identity %q, which is not 32 lowercase hex digits", n, name, id)
			}
			projects[name] = &AuthorizedProject{Name: name, Identity: id}
		case "reader":
			name, reader := args[0], args[1]
			if !ValidName(name) || !ValidName(reader) {
				return nil, bad("line %d is %q; a reader record names a project and a principal", n, line)
			}
			if readers[name][reader] {
				return nil, bad("line %d names reader %s of %s a second time", n, reader, name)
			}
			if readers[name] == nil {
				readers[name] = map[string]bool{}
			}
			readers[name][reader] = true
		}
	}
	for _, key := range []string{"instance", "destination", "sequence", "checked"} {
		if !seen[key] {
			return nil, bad("the authorization has no %s record", key)
		}
	}
	if a.Basis >= a.Sequence {
		return nil, bad("the authorization's basis %d is not less than its sequence %d", a.Basis, a.Sequence)
	}
	for name, rs := range readers {
		p := projects[name]
		if p == nil {
			return nil, bad("the authorization has a reader of %s and no project record for it", name)
		}
		for r := range rs {
			p.Readers = append(p.Readers, r)
		}
		sort.Strings(p.Readers)
	}
	var names []string
	for name, p := range projects {
		if len(p.Readers) == 0 {
			return nil, bad("project %s has no reader record", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a.Projects = append(a.Projects, *projects[name])
	}
	return a, nil
}

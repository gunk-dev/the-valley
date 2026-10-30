package export

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"the-valley/note"
)

const (
	// AuthorizationType is the first line of every authorization's text.
	AuthorizationType = "the-valley/web-authorization/v1"

	// MaxAuthorization is the most bytes of a signed authorization a
	// receiver reads, and a server reads of the stored one. Sign signs
	// nothing larger.
	MaxAuthorization = 256 << 10

	// FallbackRoom is the room a publisher leaves under MaxAuthorization
	// when it signs an authorization in full, so that a fallback narrowed
	// from it (Narrow) fits with nothing more dropped. The fallback's
	// records are some of that authorization's, under the same instance
	// and destination. Only two of its records can be longer: its
	// sequence, by at most 18 digits, and a basis record it may add, at
	// most 26 bytes. The room is no guarantee: a receiver may hold an
	// authorization signed without it. So a publisher measures every
	// fallback, and restricts one over the limit further (Fit).
	FallbackRoom = 64

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
//	binding lk-0123456789abcdef alice 3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7
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
//
// A binding record carries one active read link the host issued: its
// credential ID, the principal it identifies and its verifier (Binding).
// A credential with no binding is revoked. The authorization never
// carries a link's secret.
type Authorization struct {
	Instance    string
	Destination string
	Sequence    int64
	Checked     time.Time
	// Basis is the sequence of the authorization the receiver must hold,
	// or 0 for none.
	Basis    int64
	Projects []AuthorizedProject
	// Bindings are the active read links, sorted by ID.
	Bindings []Binding
}

// Binding is one active read link an authorization carries. ID is the
// credential's ID, lk- and 16 lowercase hex digits. Principal is the one
// registry principal it identifies, never anyone. Verifier is the
// lowercase hex SHA-256 of the link's 32 secret bytes.
type Binding struct {
	ID        string
	Principal string
	Verifier  string
}

// MaxBindings is the most bindings an authorization carries, which keeps
// it well inside MaxAuthorization.
const MaxBindings = 1000

// ValidLinkID reports whether s is a read link's credential ID: lk- and 16
// lowercase hex digits.
func ValidLinkID(s string) bool {
	return len(s) == 19 && strings.HasPrefix(s, "lk-") && lowerHex(s[3:])
}

// ValidVerifier reports whether s is a link verifier: 64 lowercase hex
// digits.
func ValidVerifier(s string) bool {
	return len(s) == 64 && lowerHex(s)
}

func lowerHex(s string) bool {
	for _, c := range []byte(s) {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return s != ""
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
// in name order, its readers sorted, then the bindings in ID order.
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
	bindings := append([]Binding{}, a.Bindings...)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	for _, l := range bindings {
		fmt.Fprintf(&b, "binding %s %s %s\n", l.ID, l.Principal, l.Verifier)
	}
	return b.Bytes()
}

// Sign is the authorization signed as a note by the publisher's export
// key, called name. It is checked against ParseAuthorization and its size
// against MaxAuthorization first, so the publisher never signs what a
// receiver would refuse to read. One too large is refused with
// CodeTooLarge.
func (a *Authorization) Sign(name string, key ed25519.PrivateKey) ([]byte, error) {
	text := a.Text()
	if _, err := ParseAuthorization(text); err != nil {
		return nil, err
	}
	if n := note.SignedSize(text, name); n > MaxAuthorization {
		return nil, refuse(CodeTooLarge, "authorization %d would be %d bytes signed, and a receiver reads at most %d", a.Sequence, n, MaxAuthorization)
	}
	return note.Sign(text, name, key)
}

// SignedSize is how many bytes Sign writes for the authorization signed by
// the key called name.
func (a *Authorization) SignedSize(name string) int {
	return note.SignedSize(a.Text(), name)
}

// Narrow is the bounded fallback a publisher sends when cur is too large
// to sign: prev, the last authorization the receiver acknowledged, less
// every access cur does not give. It keeps each of prev's projects that
// cur names under the same publication identity, with those of its
// readers cur names for it, and each of prev's bindings that cur carries
// unchanged. It takes cur's instance, destination, sequence, checked time
// and basis. It adds nothing prev does not give, so it is never larger
// than prev but for its sequence and basis records (FallbackRoom). A nil
// prev gives an authorization that names no project and carries no
// binding.
func Narrow(prev, cur *Authorization) *Authorization {
	n := &Authorization{Instance: cur.Instance, Destination: cur.Destination,
		Sequence: cur.Sequence, Checked: cur.Checked, Basis: cur.Basis}
	if prev == nil {
		return n
	}
	for _, old := range prev.Projects {
		now, ok := cur.Project(old.Name)
		if !ok || now.Identity != old.Identity {
			continue
		}
		kept := AuthorizedProject{Name: old.Name, Identity: old.Identity}
		for _, r := range old.Readers {
			if slices.Contains(now.Readers, r) {
				kept.Readers = append(kept.Readers, r)
			}
		}
		if len(kept.Readers) > 0 {
			n.Projects = append(n.Projects, kept)
		}
	}
	for _, b := range prev.Bindings {
		if slices.Contains(cur.Bindings, b) {
			n.Bindings = append(n.Bindings, b)
		}
	}
	return n
}

// Fit is a restricted until it is at most limit bytes signed by the key
// called name. A publisher sends it in place of a bounded fallback that is
// itself over the limit. onBasis names the projects a keeps only on its
// basis, as when their policy could not be read. Fit returns a when a
// fits. Otherwise it drops access one step at a time, in this order, and
// stops at the first step after which the authorization, measured, fits:
//
//  1. each project kept on the basis, whole, in name order;
//  2. each reader of the other projects, one at a time: the reader that
//     sorts last, of the project with the most readers left, the first
//     by name of those with as many, so a project goes with its last
//     reader;
//  3. each binding, in ID order.
//
// The basis record stays only while a project kept on it does. Every step
// only removes access, so Fit allows nothing a does not. After the last
// step it grants nothing: no project, no binding and no basis, a few
// hundred bytes for any instance and destination name, which always fits
// within MaxAuthorization.
func Fit(a *Authorization, onBasis []string, name string, limit int) *Authorization {
	if a.SignedSize(name) <= limit {
		return a
	}
	// A cut is one step: a project, whole, when reader is empty; one
	// reader of a project; or one binding, by its ID.
	type cut struct{ project, reader, binding string }
	projects := slices.Clone(a.Projects)
	slices.SortFunc(projects, func(x, y AuthorizedProject) int { return strings.Compare(x.Name, y.Name) })
	var cuts []cut
	for _, p := range projects {
		if slices.Contains(onBasis, p.Name) {
			cuts = append(cuts, cut{project: p.Name})
		}
	}
	// A project's reader at index i goes when the project has i+1 readers
	// left, so the readers go in descending index; the projects are in
	// name order, and the stable sort keeps them so among equal indexes.
	type indexed struct {
		cut
		i int
	}
	var readers []indexed
	for _, p := range projects {
		if slices.Contains(onBasis, p.Name) {
			continue
		}
		for i, r := range slices.Sorted(slices.Values(p.Readers)) {
			readers = append(readers, indexed{cut{project: p.Name, reader: r}, i})
		}
	}
	slices.SortStableFunc(readers, func(x, y indexed) int { return y.i - x.i })
	for _, r := range readers {
		cuts = append(cuts, r.cut)
	}
	bindings := slices.Clone(a.Bindings)
	slices.SortFunc(bindings, func(x, y Binding) int { return strings.Compare(x.ID, y.ID) })
	for _, b := range bindings {
		cuts = append(cuts, cut{binding: b.ID})
	}

	// without is a less its first k cuts. Each cut only removes records,
	// so its size falls as k grows, and the first k that fits is found
	// by measuring a few of them.
	without := func(k int) *Authorization {
		gone := map[cut]bool{}
		for _, c := range cuts[:k] {
			gone[c] = true
		}
		n := &Authorization{Instance: a.Instance, Destination: a.Destination, Sequence: a.Sequence, Checked: a.Checked}
		for _, p := range a.Projects {
			if gone[cut{project: p.Name}] {
				continue
			}
			kept := AuthorizedProject{Name: p.Name, Identity: p.Identity}
			for _, r := range p.Readers {
				if !gone[cut{project: p.Name, reader: r}] {
					kept.Readers = append(kept.Readers, r)
				}
			}
			if len(kept.Readers) == 0 {
				continue
			}
			n.Projects = append(n.Projects, kept)
			if slices.Contains(onBasis, p.Name) {
				n.Basis = a.Basis
			}
		}
		for _, b := range a.Bindings {
			if !gone[cut{binding: b.ID}] {
				n.Bindings = append(n.Bindings, b)
			}
		}
		return n
	}
	k := sort.Search(len(cuts)+1, func(k int) bool { return without(k).SignedSize(name) <= limit })
	return without(min(k, len(cuts)))
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
// A reader record names a project that has a project record. A binding
// names each credential ID once, at most MaxBindings of them. Records may
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
	arity := map[string]int{"instance": 1, "destination": 1, "sequence": 1, "checked": 1, "basis": 1, "project": 2, "reader": 2, "binding": 3}
	a := &Authorization{}
	seen := map[string]bool{}
	projects := map[string]*AuthorizedProject{}
	readers := map[string]map[string]bool{}
	bindings := map[string]bool{}
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
		case "binding":
			id, principal, verifier := args[0], args[1], args[2]
			switch {
			case !ValidLinkID(id):
				return nil, bad("line %d gives credential ID %q, which is not lk- and 16 lowercase hex digits", n, id)
			case !ValidName(principal) || principal == Anyone:
				return nil, bad("line %d binds %s to %q, which is not a principal", n, id, principal)
			case !ValidVerifier(verifier):
				return nil, bad("line %d gives %s the verifier %q, which is not 64 lowercase hex digits", n, id, verifier)
			case bindings[id]:
				return nil, bad("line %d binds %s a second time", n, id)
			case len(bindings) == MaxBindings:
				return nil, bad("line %d is a binding past the most an authorization carries, %d", n, MaxBindings)
			}
			bindings[id] = true
			a.Bindings = append(a.Bindings, Binding{ID: id, Principal: principal, Verifier: verifier})
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
	sort.Slice(a.Bindings, func(i, j int) bool { return a.Bindings[i].ID < a.Bindings[j].ID })
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

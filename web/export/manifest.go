package export

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"the-valley/note"
)

const (
	// ManifestType is the first line of every manifest's text.
	ManifestType = "the-valley/web-export/v1"

	// MaxManifest is the most bytes of a signed manifest a receiver reads.
	MaxManifest = 64 << 10

	// ExportFile is export.json's name, in a manifest and in a generation.
	ExportFile = "export.json"

	// MaxAge and MaxAhead bound a manifest's signing time, its sequence,
	// against the receiver's clock: no older than three days, and no more
	// than a minute ahead. They bound how long a replayed manifest can be
	// admitted after the receiver loses its high-water mark. They do not
	// expire an export that was accepted.
	MaxAge   = 72 * time.Hour
	MaxAhead = time.Minute
)

// Body is one upload body a manifest declares: its name, the SHA-256 of
// its bytes in hex, and its size in bytes. export.json's name is
// ExportFile, and a bundle's is its project's.
type Body struct {
	Name   string
	SHA256 string
	Size   int64
}

// BodyOf is the body data makes under name.
func BodyOf(name string, data []byte) Body {
	sum := sha256.Sum256(data)
	return Body{Name: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
}

// FileBody is the body the file at path makes under name, read once.
func FileBody(name, path string) (Body, error) {
	f, err := os.Open(path)
	if err != nil {
		return Body{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Body{}, err
	}
	return Body{Name: name, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

// Manifest is the signed statement an upload is admitted by. It names the
// valley instance that signed it and the destination it is for, its
// sequence, export.json's hash and size, and for each project its
// visibility, its graph and source tips, and its bundle's hash and size.
//
// Its text is the type line and then one record per line, each a keyword
// and its fields separated by single spaces:
//
//	the-valley/web-export/v1
//	instance valley-host
//	destination example.org
//	sequence 1792929600000000000
//	file export.json <sha256> <size>
//	project app public <graph-tip> <source-tip>
//	bundle app <sha256> <size>
//
// The sequence is the signing time in Unix nanoseconds. A project with no
// plan graph has - for its graph tip. Every project has one bundle.
type Manifest struct {
	Instance    string
	Destination string
	Sequence    int64
	File        Body
	Projects    []ManifestProject
}

// ManifestProject is one project's two records.
type ManifestProject struct {
	Name       string
	Visibility Visibility
	// GraphTip is "" for a project with no plan graph.
	GraphTip  string
	SourceTip string
	Bundle    Body
}

// NewManifest is the manifest for an export: its bytes, exportJSON, and
// each project's bundle, by project name.
func NewManifest(instance, destination string, sequence int64, e *Export, exportJSON []byte, bundles map[string]Body) (*Manifest, error) {
	m := &Manifest{Instance: instance, Destination: destination, Sequence: sequence,
		File: BodyOf(ExportFile, exportJSON)}
	for _, p := range e.Projects {
		b, ok := bundles[p.Name]
		if !ok {
			return nil, fmt.Errorf("project %s has no bundle", p.Name)
		}
		b.Name = p.Name
		m.Projects = append(m.Projects, ManifestProject{Name: p.Name, Visibility: p.Visibility,
			GraphTip: p.GraphTip, SourceTip: p.SourceTip, Bundle: b})
	}
	if len(bundles) != len(m.Projects) {
		return nil, fmt.Errorf("%d bundles for %d projects", len(bundles), len(m.Projects))
	}
	if _, err := ParseManifest(m.Text()); err != nil {
		return nil, err
	}
	return m, nil
}

// Text is the manifest's text: the type line, then the instance,
// destination, sequence and file records, then each project's two records
// in name order.
func (m *Manifest) Text() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\ninstance %s\ndestination %s\nsequence %d\n", ManifestType, m.Instance, m.Destination, m.Sequence)
	fmt.Fprintf(&b, "file %s %s %d\n", m.File.Name, m.File.SHA256, m.File.Size)
	projects := append([]ManifestProject{}, m.Projects...)
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	for _, p := range projects {
		graph := p.GraphTip
		if graph == "" {
			graph = "-"
		}
		fmt.Fprintf(&b, "project %s %s %s %s\n", p.Name, p.Visibility, graph, p.SourceTip)
		fmt.Fprintf(&b, "bundle %s %s %d\n", p.Bundle.Name, p.Bundle.SHA256, p.Bundle.Size)
	}
	return b.Bytes()
}

// Sign is the manifest signed as a note by the publisher's export key.
func (m *Manifest) Sign(name string, key ed25519.PrivateKey) ([]byte, error) {
	return note.Sign(m.Text(), name, key)
}

// SignedAt is the manifest's signing time: its sequence.
func (m *Manifest) SignedAt() time.Time { return time.Unix(0, m.Sequence).UTC() }

// Bodies are every body the manifest declares: export.json, then each
// bundle in project order.
func (m *Manifest) Bodies() []Body {
	bodies := []Body{m.File}
	for _, p := range m.Projects {
		bodies = append(bodies, p.Bundle)
	}
	return bodies
}

// CheckSizes holds the bodies the manifest declares to a receiver's caps:
// each body at most maxBody bytes, and every body together at most
// maxUpload. A body declared twice, under one SHA-256, is staged once and
// counted once. A refusal is too-large.
func (m *Manifest) CheckSizes(maxBody, maxUpload int64) error {
	var total int64
	counted := map[string]bool{}
	for _, b := range m.Bodies() {
		if b.Size > maxBody {
			return refuse(CodeTooLarge, "%s declares %d bytes, more than this receiver's cap of %d on one body", b.Name, b.Size, maxBody)
		}
		if counted[b.SHA256] {
			continue
		}
		counted[b.SHA256] = true
		// Compared before it is added, so the sum never overflows.
		if b.Size > maxUpload-total {
			return refuse(CodeTooLarge, "the manifest declares more than this receiver's cap of %d bytes of bodies on one upload", maxUpload)
		}
		total += b.Size
	}
	return nil
}

// Project is the manifest's project called name.
func (m *Manifest) Project(name string) (ManifestProject, bool) {
	for _, p := range m.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return ManifestProject{}, false
}

// Admission is what a receiver admits a manifest against: the pinned
// verifier keys, the instance and destination it is configured with, the
// greatest sequence it has accepted, and its clock.
type Admission struct {
	Verifiers   []note.VerifierKey
	Instance    string
	Destination string
	HighWater   int64
	Now         time.Time
}

// VerifyManifest admits a signed manifest, in this order, each step its
// own:
//
//  1. read at most MaxManifest bytes;
//  2. open the note with the pinned verifier keys;
//  3. require the type line, then read the records;
//  4. require the configured instance and destination;
//  5. require a sequence greater than the high-water mark;
//  6. require a signing time no older than MaxAge and no more than
//     MaxAhead ahead.
//
// Every refusal is an *Error, whose Code the upload's reply carries.
func VerifyManifest(r io.Reader, a Admission) (*Manifest, error) {
	signed, err := readManifest(r)
	if err != nil {
		return nil, err
	}
	text, err := openManifest(signed, a.Verifiers)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(text)
	if err != nil {
		return nil, err
	}
	if err := checkDestination(m, a); err != nil {
		return nil, err
	}
	if err := checkSequence(m, a.HighWater); err != nil {
		return nil, err
	}
	if err := checkSigningTime(m, a.Now); err != nil {
		return nil, err
	}
	return m, nil
}

// readManifest is step 1: at most MaxManifest bytes.
func readManifest(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxManifest+1))
	if err != nil {
		return nil, refuse(CodeUnreadable, "the manifest could not be read: %v", err)
	}
	if len(b) > MaxManifest {
		return nil, refuse(CodeTooLarge, "a manifest is at most %d bytes", MaxManifest)
	}
	return b, nil
}

// openManifest is step 2: the note opens under a pinned key.
func openManifest(signed []byte, verifiers []note.VerifierKey) ([]byte, error) {
	text, _, err := note.Open(signed, verifiers)
	if err != nil {
		return nil, refuse(CodeBadSignature, "the manifest does not open under the pinned key: %v", err)
	}
	return text, nil
}

// checkDestination is step 4.
func checkDestination(m *Manifest, a Admission) error {
	if m.Instance != a.Instance {
		return refuse(CodeWrongInstance, "the manifest is from instance %s, and this receiver takes %s", m.Instance, a.Instance)
	}
	if m.Destination != a.Destination {
		return refuse(CodeWrongDestination, "the manifest is for %s, and this receiver is %s", m.Destination, a.Destination)
	}
	return nil
}

// checkSequence is step 5. A manifest at the high-water mark is a replay,
// and one below it is older than what was accepted.
func checkSequence(m *Manifest, highWater int64) error {
	if m.Sequence <= highWater {
		e := refuse(CodeReplayed, "the manifest's sequence %d is not greater than %d, the greatest accepted", m.Sequence, highWater)
		e.HighWater = highWater
		return e
	}
	return nil
}

// checkSigningTime is step 6.
func checkSigningTime(m *Manifest, now time.Time) error {
	at := m.SignedAt()
	switch {
	case now.Sub(at) > MaxAge:
		return refuse(CodeOutOfWindow, "the manifest was signed at %s, more than %s before %s", at.Format(time.RFC3339), MaxAge, now.UTC().Format(time.RFC3339))
	case at.Sub(now) > MaxAhead:
		return refuse(CodeOutOfWindow, "the manifest was signed at %s, more than %s after %s", at.Format(time.RFC3339), MaxAhead, now.UTC().Format(time.RFC3339))
	}
	return nil
}

// ParseManifest reads a manifest's text, step 3 of VerifyManifest. The
// first line is exactly ManifestType. Each record after it is a keyword and
// exactly its fields. The instance, destination, sequence and file records
// appear once each, a project at most once, and each project has exactly
// one bundle record. Names are project names, which hold no path character
// and are never a dot segment. Records may come in any order; Text writes
// them in one.
func ParseManifest(text []byte) (*Manifest, error) {
	bad := func(format string, args ...any) error { return refuse(CodeBadManifest, format, args...) }
	if len(text) == 0 || text[len(text)-1] != '\n' {
		return nil, bad("a manifest's text ends with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	if lines[0] != ManifestType {
		return nil, bad("the manifest opens with %q, not %q", lines[0], ManifestType)
	}
	arity := map[string]int{"instance": 1, "destination": 1, "sequence": 1, "file": 3, "project": 4, "bundle": 3}
	m := &Manifest{}
	seen := map[string]bool{}
	projects := map[string]*ManifestProject{}
	bundles := map[string]Body{}
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
		case "instance", "destination", "sequence", "file":
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
				m.Instance = args[0]
			} else {
				m.Destination = args[0]
			}
		case "sequence":
			seq, ok := decimal(args[0])
			if !ok || seq == 0 {
				return nil, bad("line %d gives sequence %q; a sequence is a positive decimal", n, args[0])
			}
			m.Sequence = seq
		case "file":
			if args[0] != ExportFile {
				return nil, bad("line %d declares file %q; the only file is %s", n, args[0], ExportFile)
			}
			b, err := body(args)
			if err != nil {
				return nil, bad("line %d: %v", n, err)
			}
			m.File = b
		case "project":
			name, vis, graph, source := args[0], Visibility(args[1]), args[2], args[3]
			if !ValidName(name) {
				return nil, bad("line %d names project %q, which is not a project name", n, name)
			}
			if projects[name] != nil {
				return nil, bad("line %d is a second project record for %s", n, name)
			}
			if vis != Public {
				return nil, bad("line %d gives %s visibility %q; v1 exports public projects alone", n, name, vis)
			}
			if graph == "-" {
				graph = ""
			} else if !ValidObjectID(graph) {
				return nil, bad("line %d gives %s graph tip %q, which is not an object ID or -", n, name, graph)
			}
			if !ValidObjectID(source) {
				return nil, bad("line %d gives %s source tip %q, which is not an object ID", n, name, source)
			}
			projects[name] = &ManifestProject{Name: name, Visibility: vis, GraphTip: graph, SourceTip: source}
		case "bundle":
			if !ValidName(args[0]) {
				return nil, bad("line %d names bundle %q, which is not a project name", n, args[0])
			}
			if _, twice := bundles[args[0]]; twice {
				return nil, bad("line %d is a second bundle record for %s", n, args[0])
			}
			b, err := body(args)
			if err != nil {
				return nil, bad("line %d: %v", n, err)
			}
			bundles[args[0]] = b
		}
	}
	for _, key := range []string{"instance", "destination", "sequence", "file"} {
		if !seen[key] {
			return nil, bad("the manifest has no %s record", key)
		}
	}
	for name, b := range bundles {
		p := projects[name]
		if p == nil {
			return nil, bad("the manifest has a bundle for %s and no project record for it", name)
		}
		p.Bundle = b
	}
	var names []string
	for name, p := range projects {
		if p.Bundle.Name == "" {
			return nil, bad("project %s has no bundle record", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m.Projects = append(m.Projects, *projects[name])
	}
	sizes := map[string]int64{}
	for _, b := range m.Bodies() {
		if size, ok := sizes[b.SHA256]; ok && size != b.Size {
			return nil, bad("two bodies with SHA-256 %s declare sizes %d and %d", b.SHA256, size, b.Size)
		}
		sizes[b.SHA256] = b.Size
	}
	return m, nil
}

// body reads a file or bundle record's name, hash and size.
func body(args []string) (Body, error) {
	name, sum, written := args[0], args[1], args[2]
	if len(sum) != 64 || !isHex(sum) {
		return Body{}, fmt.Errorf("%q is not a SHA-256 in lowercase hex", sum)
	}
	size, ok := decimal(written)
	if !ok || size == 0 {
		return Body{}, fmt.Errorf("size %q is not a positive decimal", written)
	}
	return Body{Name: name, SHA256: sum, Size: size}, nil
}

// decimal reads a non-negative int64 written in decimal, with no sign and
// no leading zero.
func decimal(s string) (int64, bool) {
	if s == "" || len(s) > 1 && s[0] == '0' || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// CheckExport holds export.json to the manifest: its bytes have the
// declared hash and size, it is a valid export, and it names the same
// projects with the same visibility and tips. It returns the export.
func (m *Manifest) CheckExport(data []byte) (*Export, error) {
	if got := BodyOf(ExportFile, data); got != m.File {
		return nil, refuse(CodeHashMismatch, "export.json has SHA-256 %s and %d bytes; the manifest declares %s and %d", got.SHA256, got.Size, m.File.SHA256, m.File.Size)
	}
	e, err := Decode(data)
	if err != nil {
		return nil, refuse(CodeBadExport, "%v", err)
	}
	if len(e.Projects) != len(m.Projects) {
		return nil, refuse(CodeBadExport, "export.json holds %d projects, and the manifest %d", len(e.Projects), len(m.Projects))
	}
	for i, p := range e.Projects {
		mp := m.Projects[i]
		switch {
		case p.Name != mp.Name:
			return nil, refuse(CodeBadExport, "export.json holds project %s where the manifest has %s", p.Name, mp.Name)
		case p.Visibility != mp.Visibility:
			return nil, refuse(CodeBadExport, "project %s is %s in export.json and %s in the manifest", p.Name, p.Visibility, mp.Visibility)
		case p.GraphTip != mp.GraphTip:
			return nil, refuse(CodeBadExport, "project %s has graph tip %q in export.json and %q in the manifest", p.Name, p.GraphTip, mp.GraphTip)
		case p.SourceTip != mp.SourceTip:
			return nil, refuse(CodeBadExport, "project %s has source tip %s in export.json and %s in the manifest", p.Name, p.SourceTip, mp.SourceTip)
		}
	}
	return e, nil
}

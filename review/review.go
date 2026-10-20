// Package review is the review record, the-valley/review/v1: a reviewer's
// judgment of one version of a change, and the history that keeps every
// judgment of the change.
//
// A review says whether its reviewer approves the version it read or
// requests changes to it. It carries a summary, and notes anchored to the
// files the reviewer read. Human and agent reviewers write the same record;
// an agent's adds the provenance of its run. A review may instead withdraw
// an earlier review of the same change, named by its ID. Nothing else takes
// a judgment back: a later approval does not clear an earlier request for
// changes.
//
// A change's reviews live at refs/the-valley/reviews/<target>/<change ID>.
// The ref points at the newest review commit, whose tree holds one file,
// review.json:
//
//	{
//	  "schema": "the-valley/review/v1",
//	  "review_id": "<32 lowercase hex digits>",
//	  "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
//	  "target": "main",
//	  "review": "operator",
//	  "reviewer": "klaus-env",
//	  "verdict": "request-changes",
//	  "withdraws": null,
//	  "binding": {
//	    "record": "git-sha1:<the change record commit read>",
//	    "version": 2,
//	    "head": "git-sha1:<version 2's head>",
//	    "target_tip": "git-sha1:<the target tip judged against>",
//	    "base": "git-sha1:<the merge base of the tip and the head>",
//	    "delta": "<the SHA-256 of the valley-delta-v1 encoding from base to head>"
//	  },
//	  "summary": "Split the second paragraph.",
//	  "notes": [
//	    {"side": "after", "path": "docs/guide.md", "blob": "git-sha1:<the file read>",
//	     "lines": {"start": 12, "end": 14}, "body": "This sentence carries two ideas."}
//	  ],
//	  "provenance": null
//	}
//
// The binding says exactly what the reviewer read (binding.go), and anyone
// holding the objects can recompute it. The history keeps every review in
// order (history.go).
//
// Reading is as strict as a change record's: valid UTF-8, no key twice in
// any object, no field the schema does not have, every field of exactly
// its type, every string and list within its bound, and nothing after the
// object. A review is at most MaxRecord bytes.
//
// The package depends on the standard library, the change record
// (../change) and valley-delta-v1 (../delta), and on git for reading and
// writing objects.
package review

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"the-valley/change"
)

// Schema names the record format.
const Schema = "the-valley/review/v1"

// The verdicts a review gives. approve and request-changes judge the
// version the binding names. withdraw judges nothing: it takes back the
// earlier review it names.
const (
	Approve        = "approve"
	RequestChanges = "request-changes"
	Withdraw       = "withdraw"
)

// The sides of the comparison a note is anchored to: the merge base the
// version was compared from, or the version's head.
const (
	Before = "before"
	After  = "after"
)

// The bounds a review is held to.
const (
	MaxRecord   = 64 << 10      // bytes in review.json
	MaxName     = 64            // bytes in a review's name
	MaxSummary  = 8 << 10       // bytes in a summary
	MaxNotes    = 256           // notes in one review
	MaxNoteBody = 8 << 10       // bytes in a note's body
	MaxPath     = 4095          // bytes in a note's path, Linux's PATH_MAX less its NUL
	MaxLine     = 1 << 24       // the highest line a note may name
	MaxRunText  = 256           // bytes in a provenance's harness or model
	MaxTokens   = 1000000000000 // tokens a provenance may report, input or output
)

// IDLength is the length of a review ID: 32 lowercase hex digits.
const IDLength = 32

// Record is one review.
type Record struct {
	Schema     string      `json:"schema"`
	ReviewID   string      `json:"review_id"`
	ChangeID   string      `json:"change_id"`
	Target     string      `json:"target"`
	Review     string      `json:"review"`
	Reviewer   string      `json:"reviewer"`
	Verdict    string      `json:"verdict"`
	Withdraws  *string     `json:"withdraws"`
	Binding    *Binding    `json:"binding"`
	Summary    string      `json:"summary"`
	Notes      []Note      `json:"notes"`
	Provenance *Provenance `json:"provenance"`
}

// Binding is the version a review judged and what the reviewer read of
// it: the change record commit that names the version, the version's
// number and head, the target tip it was judged against, the merge base
// of that tip and the head, and the digest of the valley-delta-v1 delta
// from the base to the head. The commits are git-sha1:<40 hex>, and the
// digest is 64 lowercase hex digits.
type Binding struct {
	Record    string `json:"record"`
	Version   int    `json:"version"`
	Head      string `json:"head"`
	TargetTip string `json:"target_tip"`
	Base      string `json:"base"`
	Delta     string `json:"delta"`
}

// Note is one piece of feedback, anchored to a file the reviewer read: its
// side, its path, the blob the path held on that side, and an optional
// range of lines, numbered from 1. A note on deleted lines is anchored to
// the before side. A note on a whole file has no lines.
type Note struct {
	Side  string `json:"side"`
	Path  string `json:"path"`
	Blob  string `json:"blob"`
	Lines *Lines `json:"lines"`
	Body  string `json:"body"`
}

// Lines is a range of lines, from Start to End inclusive, numbered from 1.
type Lines struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Provenance is how an agent reviewer's judgment was produced: the
// harness that ran it and its version, the model, the SHA-256 of the
// generation settings and of the manifest of inputs, both in lowercase
// hex, and the tokens the run reported using, nil where it reported none.
// A human's review has none.
type Provenance struct {
	Harness      string `json:"harness"`
	Model        string `json:"model"`
	Settings     string `json:"settings"`
	Inputs       string `json:"inputs"`
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
}

// Judges reports whether the review judges a version, which every review
// but a withdrawal does.
func (r *Record) Judges() bool {
	return r.Verdict == Approve || r.Verdict == RequestChanges
}

// NewID makes a review ID: 16 random bytes in lowercase hex. A client
// makes one when it first writes a review and keeps it through every
// retry of publishing it.
func NewID() (string, error) {
	var raw [IDLength / 2]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// Parse reads review.json, strictly.
func Parse(data []byte) (*Record, error) {
	if len(data) > MaxRecord {
		return nil, fmt.Errorf("it is %d bytes, more than the %d a review may be", len(data), MaxRecord)
	}
	v, err := readJSON(data)
	if err != nil {
		return nil, err
	}
	o, err := fieldsOf(v, "the review", "schema", "review_id", "change_id", "target", "review", "reviewer",
		"verdict", "withdraws", "binding", "summary", "notes", "provenance")
	if err != nil {
		return nil, err
	}
	r := &Record{}
	for _, f := range []struct {
		name string
		into *string
	}{
		{"schema", &r.Schema}, {"review_id", &r.ReviewID}, {"change_id", &r.ChangeID}, {"target", &r.Target},
		{"review", &r.Review}, {"reviewer", &r.Reviewer}, {"verdict", &r.Verdict}, {"summary", &r.Summary},
	} {
		if *f.into, err = stringOf(o[f.name], f.name); err != nil {
			return nil, err
		}
	}
	if r.Withdraws, err = optionalString(o["withdraws"], "withdraws"); err != nil {
		return nil, err
	}
	if r.Binding, err = bindingOf(o["binding"]); err != nil {
		return nil, err
	}
	if r.Notes, err = notesOf(o["notes"]); err != nil {
		return nil, err
	}
	if r.Provenance, err = provenanceOf(o["provenance"]); err != nil {
		return nil, err
	}
	if err := r.Check(); err != nil {
		return nil, err
	}
	return r, nil
}

func bindingOf(v *value) (*Binding, error) {
	if isNull(v) {
		return nil, nil
	}
	o, err := fieldsOf(v, "its binding", "record", "version", "head", "target_tip", "base", "delta")
	if err != nil {
		return nil, err
	}
	b := &Binding{}
	for _, f := range []struct {
		name string
		into *string
	}{
		{"record", &b.Record}, {"head", &b.Head}, {"target_tip", &b.TargetTip}, {"base", &b.Base}, {"delta", &b.Delta},
	} {
		if *f.into, err = stringOf(o[f.name], "binding."+f.name); err != nil {
			return nil, err
		}
	}
	n, err := integerOf(o["version"], "binding.version", 1, change.MaxVersions)
	if err != nil {
		return nil, err
	}
	b.Version = int(n)
	return b, nil
}

func notesOf(v *value) ([]Note, error) {
	if v.kind != kindArray {
		return nil, fmt.Errorf("its notes is %s, where an array is expected", v.kind)
	}
	if len(v.items) > MaxNotes {
		return nil, fmt.Errorf("it holds %d notes, more than the %d a review may", len(v.items), MaxNotes)
	}
	notes := []Note{}
	for i, item := range v.items {
		where := fmt.Sprintf("notes[%d]", i)
		o, err := fieldsOf(item, where, "side", "path", "blob", "lines", "body")
		if err != nil {
			return nil, err
		}
		var n Note
		for _, f := range []struct {
			name string
			into *string
		}{
			{"side", &n.Side}, {"path", &n.Path}, {"blob", &n.Blob}, {"body", &n.Body},
		} {
			if *f.into, err = stringOf(o[f.name], where+"."+f.name); err != nil {
				return nil, err
			}
		}
		if lines := o["lines"]; !isNull(lines) {
			lo, err := fieldsOf(lines, where+".lines", "start", "end")
			if err != nil {
				return nil, err
			}
			start, err := integerOf(lo["start"], where+".lines.start", 1, MaxLine)
			if err != nil {
				return nil, err
			}
			end, err := integerOf(lo["end"], where+".lines.end", 1, MaxLine)
			if err != nil {
				return nil, err
			}
			n.Lines = &Lines{Start: int(start), End: int(end)}
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func provenanceOf(v *value) (*Provenance, error) {
	if isNull(v) {
		return nil, nil
	}
	o, err := fieldsOf(v, "its provenance", "harness", "model", "settings", "inputs", "input_tokens", "output_tokens")
	if err != nil {
		return nil, err
	}
	p := &Provenance{}
	for _, f := range []struct {
		name string
		into *string
	}{
		{"harness", &p.Harness}, {"model", &p.Model}, {"settings", &p.Settings}, {"inputs", &p.Inputs},
	} {
		if *f.into, err = stringOf(o[f.name], "provenance."+f.name); err != nil {
			return nil, err
		}
	}
	for _, f := range []struct {
		name string
		into **int64
	}{
		{"input_tokens", &p.InputTokens}, {"output_tokens", &p.OutputTokens},
	} {
		if isNull(o[f.name]) {
			continue
		}
		n, err := integerOf(o[f.name], "provenance."+f.name, 0, MaxTokens)
		if err != nil {
			return nil, err
		}
		*f.into = &n
	}
	return p, nil
}

// Check holds a review to everything the schema says beyond the types of
// its fields: the formats and bounds of its strings and lists, and what
// its fields say about one another. A review that judges a version has a
// binding and withdraws nothing. A withdrawal names the review it takes
// back, which is not itself, and has no binding and no notes. Every review
// has a summary.
func (r *Record) Check() error {
	switch {
	case r.Schema != Schema:
		return fmt.Errorf("its schema is %q, where %q is expected", r.Schema, Schema)
	case !ValidID(r.ReviewID):
		return fmt.Errorf("its review_id %q is not a review ID: %d lowercase hex digits", r.ReviewID, IDLength)
	case !change.ValidID(r.ChangeID):
		return fmt.Errorf("its change_id %q is not a change ID: 32 letters from k to z", r.ChangeID)
	case !change.ValidBranch(r.Target) || !utf8.ValidString(r.Target):
		return fmt.Errorf("its target %q is not a branch name in UTF-8", r.Target)
	case !ValidName(r.Review):
		return fmt.Errorf("its review %q is not a review's name: 1 to %d lowercase letters, digits and dashes, opening with a letter", r.Review, MaxName)
	case !change.ValidPrincipal(r.Reviewer):
		return fmt.Errorf("its reviewer %q is not a principal name", r.Reviewer)
	case !validText(r.Summary, MaxSummary):
		return fmt.Errorf("its summary is not 1 to %d bytes free of control characters but newline and tab", MaxSummary)
	case len(r.Notes) > MaxNotes:
		return fmt.Errorf("it holds %d notes, more than the %d a review may", len(r.Notes), MaxNotes)
	}
	switch r.Verdict {
	case Approve, RequestChanges:
		switch {
		case r.Withdraws != nil:
			return fmt.Errorf("its verdict is %s, and only a withdrawal names a review in withdraws", r.Verdict)
		case r.Binding == nil:
			return fmt.Errorf("its verdict is %s, and a review that judges a version has a binding", r.Verdict)
		}
		if err := r.Binding.check(); err != nil {
			return err
		}
	case Withdraw:
		switch {
		case r.Withdraws == nil || !ValidID(*r.Withdraws):
			return fmt.Errorf("it is a withdrawal, and its withdraws is not a review ID: %d lowercase hex digits", IDLength)
		case *r.Withdraws == r.ReviewID:
			return errors.New("it is a withdrawal of itself, and a withdrawal takes back an earlier review")
		case r.Binding != nil:
			return errors.New("it is a withdrawal, and a withdrawal judges no version, so it has no binding")
		case len(r.Notes) > 0:
			return errors.New("it is a withdrawal, and a withdrawal has no notes: its reason is its summary")
		}
	default:
		return fmt.Errorf("its verdict is %q, where %q, %q or %q is expected", r.Verdict, Approve, RequestChanges, Withdraw)
	}
	for i, n := range r.Notes {
		if err := n.check(fmt.Sprintf("notes[%d]", i)); err != nil {
			return err
		}
	}
	if p := r.Provenance; p != nil {
		switch {
		case !validLine(p.Harness, MaxRunText):
			return fmt.Errorf("its provenance.harness is not 1 to %d bytes free of control characters", MaxRunText)
		case !validLine(p.Model, MaxRunText):
			return fmt.Errorf("its provenance.model is not 1 to %d bytes free of control characters", MaxRunText)
		case !validDigest(p.Settings):
			return fmt.Errorf("its provenance.settings %q is not a SHA-256 in 64 lowercase hex digits", p.Settings)
		case !validDigest(p.Inputs):
			return fmt.Errorf("its provenance.inputs %q is not a SHA-256 in 64 lowercase hex digits", p.Inputs)
		case p.InputTokens != nil && (*p.InputTokens < 0 || *p.InputTokens > MaxTokens):
			return fmt.Errorf("its provenance.input_tokens is %d, outside 0 to %d", *p.InputTokens, int64(MaxTokens))
		case p.OutputTokens != nil && (*p.OutputTokens < 0 || *p.OutputTokens > MaxTokens):
			return fmt.Errorf("its provenance.output_tokens is %d, outside 0 to %d", *p.OutputTokens, int64(MaxTokens))
		}
	}
	return nil
}

func (b *Binding) check() error {
	for _, f := range []struct{ name, value string }{
		{"record", b.Record}, {"head", b.Head}, {"target_tip", b.TargetTip}, {"base", b.Base},
	} {
		if !change.ValidHead(f.value) {
			return fmt.Errorf("its binding.%s %q is not %s and 40 lowercase hex digits", f.name, f.value, change.HeadPrefix)
		}
	}
	switch {
	case b.Version < 1 || b.Version > change.MaxVersions:
		return fmt.Errorf("its binding.version is %d, outside 1 to %d", b.Version, change.MaxVersions)
	case !validDigest(b.Delta):
		return fmt.Errorf("its binding.delta %q is not a SHA-256 in 64 lowercase hex digits", b.Delta)
	}
	return nil
}

func (n Note) check(where string) error {
	switch {
	case n.Side != Before && n.Side != After:
		return fmt.Errorf("its %s.side is %q, where %q or %q is expected", where, n.Side, Before, After)
	case !change.ValidHead(n.Blob):
		return fmt.Errorf("its %s.blob %q is not %s and 40 lowercase hex digits", where, n.Blob, change.HeadPrefix)
	case n.Lines != nil && (n.Lines.Start < 1 || n.Lines.End > MaxLine || n.Lines.Start > n.Lines.End):
		return fmt.Errorf("its %s.lines run from %d to %d, and lines run forward from 1 to at most %d", where, n.Lines.Start, n.Lines.End, MaxLine)
	case !validText(n.Body, MaxNoteBody):
		return fmt.Errorf("its %s.body is not 1 to %d bytes free of control characters but newline and tab", where, MaxNoteBody)
	}
	if err := checkPath(n.Path); err != nil {
		return fmt.Errorf("its %s.path %w", where, err)
	}
	return nil
}

// checkPath refuses a note's path that names no file a tree could hold
// and a checkout could make: an empty one, one longer than MaxPath, one
// with an empty component, a component that is . or .., .git in any case,
// or longer than 255 bytes, and one holding a control character, which no
// line of git's batch reading can carry. A file whose path is not UTF-8
// takes no note; the summary can speak of it.
func checkPath(p string) error {
	switch {
	case p == "" || len(p) > MaxPath:
		return fmt.Errorf("is not 1 to %d bytes", MaxPath)
	case strings.ContainsFunc(p, unicode.IsControl):
		return errors.New("holds a control character")
	}
	for _, name := range strings.Split(p, "/") {
		switch {
		case name == "":
			return fmt.Errorf("%q has an empty component", p)
		case name == "." || name == "..":
			return fmt.Errorf("%q has the component %s", p, name)
		case strings.EqualFold(name, ".git"):
			return fmt.Errorf("%q has the component %s, which git keeps for itself", p, name)
		case len(name) > 255:
			return fmt.Errorf("%q has a component longer than Linux's limit of 255 bytes", p)
		}
	}
	return nil
}

// Encode writes a review as review.json. It refuses a review Parse would
// refuse, so what it writes always reads back as the same review.
func Encode(r *Record) ([]byte, error) {
	if err := r.Check(); err != nil {
		return nil, fmt.Errorf("the review cannot be written: %w", err)
	}
	out := *r
	if out.Notes == nil {
		out.Notes = []Note{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&out); err != nil {
		return nil, err
	}
	if buf.Len() > MaxRecord {
		return nil, fmt.Errorf("the review cannot be written: it takes %d bytes, more than the %d a review may", buf.Len(), MaxRecord)
	}
	return buf.Bytes(), nil
}

// ValidID reports whether s is a review ID.
func ValidID(s string) bool {
	return len(s) == IDLength && lowerHex(s)
}

// reviewName is the shape of a review's name, such as docs-style or
// operator.
var reviewName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidName reports whether s is a review's name.
func ValidName(s string) bool {
	return len(s) <= MaxName && reviewName.MatchString(s)
}

func validDigest(s string) bool {
	return len(s) == 64 && lowerHex(s)
}

func lowerHex(s string) bool {
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validText reports whether s is 1 to max bytes of UTF-8 holding no
// control character but newline and tab: a summary's or a note's prose.
func validText(s string, max int) bool {
	if len(s) < 1 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

// validLine reports whether s is 1 to max bytes of UTF-8 holding no
// control character at all.
func validLine(s string, max int) bool {
	if len(s) < 1 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
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

func isNull(v *value) bool {
	return v.kind == kindLiteral && v.text == "null"
}

// optionalString reads a string or null.
func optionalString(v *value, name string) (*string, error) {
	if isNull(v) {
		return nil, nil
	}
	s, err := stringOf(v, name)
	if err != nil {
		return nil, fmt.Errorf("its %s is %s, where a string or null is expected", name, v.kind)
	}
	return &s, nil
}

// integerOf reads an integer from min to max. Only a plain run of digits
// is an integer: 3.0, 3e0 and -1 are numbers of another type.
func integerOf(v *value, name string, min, max int64) (int64, error) {
	if v.kind != kindNumber {
		return 0, fmt.Errorf("its %s is %s, where an integer is expected", name, v.kind)
	}
	for _, c := range []byte(v.text) {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("its %s is %s, where an integer is expected", name, v.text)
		}
	}
	n, err := strconv.ParseInt(v.text, 10, 64)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("its %s is %s, outside %d to %d", name, v.text, min, max)
	}
	return n, nil
}

// Package telemetry holds the valley's diagnostic records: what a
// component was doing, said by the code that was doing it.
//
// A record is an observation and nothing more. It is never signed, never
// stored at a ref and never read to decide anything. Evidence, requests
// and outcomes stay where they are, and keep their meaning.
//
// Records come only from producer code, through a stream (store.go) or a
// report (protocol.go). A line a check prints is never read as a record,
// whatever it looks like: a check author can print anything.
package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Format names the record and wire format these types describe.
const Format = "valley-telemetry/v1"

// Record is one observation.
type Record struct {
	// Stream and Seq place the record: the stream that holds it and its
	// number there. Both are set when a stream stores the record.
	Stream string `json:"stream,omitempty"`
	Seq    int64  `json:"seq,omitempty"`

	Type string `json:"type"`
	// Time is the producer's clock when it made the record, in UTC. A
	// clock on another machine can disagree with this host's, so two
	// machines' times are never subtracted.
	Time string `json:"time"`

	// What the record is about. Run names one invocation of a producer
	// (a worker pass, an attest run, an approval), and every record that
	// invocation makes carries it.
	Run     string `json:"run,omitempty"`
	Project string `json:"project,omitempty"`
	Change  string `json:"change,omitempty"`
	Version int    `json:"version,omitempty"`
	Head    string `json:"head,omitempty"`
	Tree    string `json:"tree,omitempty"`
	Check   string `json:"check,omitempty"`

	Data     map[string]any `json:"data,omitempty"`
	Producer *Producer      `json:"producer,omitempty"`

	// Set by the collector on a reported record, from the session that
	// delivered it, and never taken from the sender. ClientID is the
	// sender's own name for the record, which makes a retried report
	// harmless.
	Principal string `json:"principal,omitempty"`
	Received  string `json:"received,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
}

// Producer is the program that made a record, as it observed itself: its
// executable, its version when it knows one, and its process. A version
// a program cannot observe is left empty and reads as unknown.
type Producer struct {
	Name    string `json:"name"`
	Exe     string `json:"exe,omitempty"`
	Version string `json:"version,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

// Self is the running program as a producer named name.
func Self(name, version string) *Producer {
	p := &Producer{Name: name, Version: version, PID: os.Getpid()}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		p.Exe = exe
	}
	return p
}

// The record types. A stream and a report take these and no other.
var recordTypes = map[string]bool{
	// The check worker (status/worker.go).
	"pass.start": true, "pass.end": true, "pass.interrupted": true,
	"change.seen": true, "batch.start": true, "batch.end": true,
	"backoff": true, "publish.start": true, "publish.end": true,
	// attest run (attest/run.go), on the host or a workstation.
	"run.start": true, "run.end": true, "eval.start": true, "eval.end": true,
	"check.queued": true, "check.build-start": true, "check.build-end": true,
	"check.start": true, "check.result": true, "evidence.stored": true,
	// A piece of a check's output, which a workstation sends from its
	// spool's logs (Untrusted).
	"check.output": true,
	// valley approve (bin/valley), on a workstation.
	"approve.start": true, "approve.policy": true, "approve.coverage": true,
	"approve.checks": true, "approve.request": true, "approve.end": true,
	// One valley invocation, on a workstation (status/command.go).
	"command": true,
	// The integrator (integrator/timing.go).
	"decision": true,
	// The collector, about a report.
	"report.gap": true,
}

// ClientType reports whether a report may carry records of type t: what a workstation or a
// remote component says about its own runs.
func ClientType(t string) bool {
	if t == "command" {
		return true
	}
	for _, prefix := range []string{"approve.", "run.", "eval.", "check.", "evidence."} {
		if strings.HasPrefix(t, prefix) {
			return recordTypes[t]
		}
	}
	return false
}

// Bounds on one record. The text of a check.output record may be longer
// than another string, up to OutputChunk before it is made safe to show.
const (
	MaxRecordBytes = 8 << 10
	maxDataKeys    = 32
	maxString      = 512
	maxList        = 64
	OutputChunk    = 1024
)

// Untrusted reports whether a record carries a check's own output, which a
// reader sees only when it asks for logs.
func (r *Record) Untrusted() bool { return r.Type == "check.output" }

var (
	nameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,127}$`)
	hexRE   = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	treeRE  = regexp.MustCompile(`^[a-z0-9-]{1,16}:[0-9a-f]{16,128}$|^[0-9a-f]{16,128}$`)
	keyRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	runRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	epochRE = regexp.MustCompile(`^[0-9a-f]{16}$`)
	stampRE = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d{1,9})?Z$`)
)

// Validate holds a record to its shape: a known type, a UTC time, names
// and digests in their alphabets, and data of plain keys holding short
// strings, numbers, booleans or short lists of strings, within
// MaxRecordBytes in all.
func (r *Record) Validate() error {
	if !recordTypes[r.Type] {
		return fmt.Errorf("unknown record type %q", clip(r.Type))
	}
	if !stampRE.MatchString(r.Time) {
		return fmt.Errorf("time %q is not a UTC timestamp", clip(r.Time))
	}
	for field, v := range map[string]string{"project": r.Project, "change": r.Change, "check": r.Check} {
		if v != "" && !nameRE.MatchString(v) {
			return fmt.Errorf("%s %q is not a name", field, clip(v))
		}
	}
	if r.Run != "" && !runRE.MatchString(r.Run) {
		return fmt.Errorf("run %q is not a run ID", clip(r.Run))
	}
	if r.Head != "" && !hexRE.MatchString(r.Head) {
		return fmt.Errorf("head %q is not an object ID", clip(r.Head))
	}
	if r.Tree != "" && !treeRE.MatchString(r.Tree) {
		return fmt.Errorf("tree %q is not a tree digest", clip(r.Tree))
	}
	if r.Version < 0 || r.Version > 1<<20 {
		return errors.New("version out of range")
	}
	if len(r.Data) > maxDataKeys {
		return fmt.Errorf("%d data keys; at most %d", len(r.Data), maxDataKeys)
	}
	for k, v := range r.Data {
		if !keyRE.MatchString(k) {
			return fmt.Errorf("data key %q is not a plain name", clip(k))
		}
		if s, ok := v.(string); ok && k == "text" && r.Untrusted() && len(s) <= 6*OutputChunk && utf8.ValidString(s) {
			continue
		}
		if err := plainValue(v); err != nil {
			return fmt.Errorf("data %s: %w", k, err)
		}
	}
	if p := r.Producer; p != nil {
		if !nameRE.MatchString(p.Name) || len(p.Exe) > maxString || len(p.Version) > 64 || !utf8.ValidString(p.Exe+p.Version) {
			return errors.New("producer is not a name, an executable and a version")
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > MaxRecordBytes {
		return fmt.Errorf("record is %d bytes; at most %d", len(data), MaxRecordBytes)
	}
	return nil
}

func plainValue(v any) error {
	switch v := v.(type) {
	case nil, bool, float64, int, int64:
		return nil
	case string:
		if len(v) > maxString || !utf8.ValidString(v) {
			return errors.New("string too long or not UTF-8")
		}
		return nil
	case []any:
		if len(v) > maxList {
			return errors.New("list too long")
		}
		for _, e := range v {
			if s, ok := e.(string); !ok || len(s) > 128 || !utf8.ValidString(s) {
				return errors.New("a list holds short strings only")
			}
		}
		return nil
	case []string:
		if len(v) > maxList {
			return errors.New("list too long")
		}
		for _, s := range v {
			if len(s) > 128 || !utf8.ValidString(s) {
				return errors.New("a list holds short strings only")
			}
		}
		return nil
	}
	return fmt.Errorf("a %T is not a plain value", v)
}

// clip shortens what an error quotes back.
func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// Stamp is a time as records write it: UTC, to the millisecond.
func Stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// DecodeRecord reads one record as a sender wrote it. A field the format
// does not have is refused, not ignored.
func DecodeRecord(line []byte) (Record, error) {
	var r Record
	d := json.NewDecoder(bytes.NewReader(line))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if d.More() {
		return r, errors.New("trailing data after the record")
	}
	for k, v := range r.Data {
		if n, ok := v.(json.Number); ok {
			f, err := n.Float64()
			if err != nil {
				return r, fmt.Errorf("data %s: %w", k, err)
			}
			r.Data[k] = f
		}
	}
	return r, nil
}

// Sanitize makes untrusted text safe to show: invalid UTF-8 is replaced,
// and every control character but newline and tab, escape included, is
// written as a visible \x.. sequence, so a log line cannot move a
// terminal's cursor, change its colours or end a quoted field.
func Sanitize(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			b.WriteString(`\ufffd`)
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

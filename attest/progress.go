package main

// Progress: what a run says about itself while it runs, with --telemetry.
//
// A timing (timing.go) is printed when a check is reported, and checks
// are reported in the order given, so a slow first check holds back the
// lines of every check after it. Progress records are not held back.
// Each is stored in the telemetry stream (../telemetry) when the thing it
// records happens: the shared evaluation, a check queued for a build
// slot, its build starting and ending, and its result. So a reader sees a
// fast second check finish while a slow first one still builds.
//
// With telemetry, a nix build is run with nix's structured log
// (--log-format internal-json -L), which carries the builder's output
// line by line as it is written. That output goes to the check's log in
// the stream, bounded, as it arrives. nix's own messages are kept as the
// check's log the terminal shows on a failure, as without telemetry, and
// go to the stream's log too. The builder's lines are told apart by nix's
// framing, never by what a line says, so a check cannot write a record.
//
// Like a timing, progress is never evidence, and storing it never fails
// or holds up a run (telemetry.Emitter).

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"the-valley/telemetry"
)

// observer stores a run's progress. A nil observer stores nothing.
type observer struct {
	tel   *telemetry.Emitter
	mu    sync.Mutex
	names map[string]string // a derivation's log is named for the first check that builds it
	logs  map[string]logged
}

type logged struct {
	bytes     int64
	truncated bool
	cached    bool // nix built and fetched nothing (nixLog.ran)
}

func newObserver(tel *telemetry.Emitter) *observer {
	if tel == nil {
		return nil
	}
	return &observer{tel: tel, names: map[string]string{}, logs: map[string]logged{}}
}

func (o *observer) emit(typ, check string, data map[string]any) {
	if o == nil {
		return
	}
	o.tel.Emit(typ, telemetry.Record{Check: check}, data)
}

// name says which check's log a derivation's build writes.
func (o *observer) name(drv, check string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if _, ok := o.names[drv]; !ok {
		o.names[drv] = check
	}
	o.mu.Unlock()
}

// logOf is what was logged of a derivation's build.
func (o *observer) logOf(drv string) logged {
	if o == nil {
		return logged{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.logs[drv]
}

// nixBuild builds one derivation as runPure does, and streams its log.
func (o *observer) nixBuild(tree, drv string) (string, string, error) {
	o.mu.Lock()
	name := o.names[drv]
	o.mu.Unlock()
	log := o.tel.Log(name)
	cmd := exec.Command("nix", "build", "--no-link", "--print-out-paths", "--log-format", "internal-json", "-L", drv+"^*")
	cmd.Dir = tree
	var out bytes.Buffer
	stderr := &nixLog{log: log}
	cmd.Stdout = &out
	cmd.Stderr = stderr
	err := cmd.Run()
	stderr.Write([]byte("\n")) // whatever was left without a newline
	written, truncated := log.Close()
	o.mu.Lock()
	o.logs[drv] = logged{written, truncated, err == nil && !stderr.ran}
	o.mu.Unlock()
	return out.String(), stderr.text.String(), err
}

// nixLog reads nix's structured log as it is written. The builder's
// lines go to the check's log alone. nix's messages, and its activities
// at the level it prints by default, go to the text a failed check shows
// and to the log. A line that is not nix's framing is kept as text, as
// nix printed it. ran says nix started a build or a substitution: a
// build that started neither found its outputs already in the store.
type nixLog struct {
	log     io.Writer
	pending []byte
	text    bytes.Buffer
	ran     bool
}

// The types of nix's activities that make an output: building a
// derivation and fetching a path from a substituter.
const (
	nixActBuild      = 105
	nixActSubstitute = 108
)

// ansi matches the colour sequences nix writes into its messages.
var ansi = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

func (l *nixLog) Write(p []byte) (int, error) {
	l.pending = append(l.pending, p...)
	for {
		i := bytes.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		l.line(string(l.pending[:i]))
		l.pending = l.pending[i+1:]
	}
	return len(p), nil
}

func (l *nixLog) line(line string) {
	if line == "" {
		return
	}
	payload, framed := strings.CutPrefix(line, "@nix ")
	if !framed {
		l.text.WriteString(line + "\n")
		l.log.Write([]byte(line + "\n"))
		return
	}
	var m struct {
		Action string `json:"action"`
		Type   int    `json:"type"`
		Level  int    `json:"level"`
		Msg    string `json:"msg"`
		Text   string `json:"text"`
		Fields []any  `json:"fields"`
	}
	if json.Unmarshal([]byte(payload), &m) != nil {
		return
	}
	if m.Action == "start" && (m.Type == nixActBuild || m.Type == nixActSubstitute) {
		l.ran = true
	}
	switch {
	case m.Action == "result" && m.Type == 101 && len(m.Fields) > 0: // a line of build output
		if s, ok := m.Fields[0].(string); ok {
			l.log.Write([]byte(s + "\n"))
		}
	case m.Action == "msg":
		msg := ansi.ReplaceAllString(m.Msg, "") + "\n"
		l.text.WriteString(msg)
		l.log.Write([]byte(msg))
	case m.Action == "start" && m.Text != "" && m.Level <= 3:
		l.text.WriteString(m.Text + "\n")
	}
}

// buildData is what a check's build-end and result records say about
// its build.
func buildData(b *drvBuild, wait time.Duration, began, ended time.Time, lg logged) map[string]any {
	d := map[string]any{
		"build_ms":     b.took.Milliseconds(),
		"slot_wait_ms": wait.Milliseconds(),
		"build_began":  telemetry.Stamp(began),
		"build_ended":  telemetry.Stamp(ended),
		"log_bytes":    lg.bytes,
		"cached":       lg.cached,
	}
	if lg.truncated {
		d["log_truncated"] = true
	}
	if b.err != nil {
		d["build_error"] = clipError(b.err.Error())
	}
	return d
}

func clipError(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

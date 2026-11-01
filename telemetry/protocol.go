package telemetry

// The telemetry command, as a session sends it over ssh, and the request
// the git user's shell passes to the collector for it.
//
// The command is a closed grammar: the word valley-telemetry, an
// operation, and the flags that operation takes, each written --flag=value
// and separated by single spaces. There is no quoting, no path, no unit
// name and no pattern in it. Anything else is refused whole, and the
// shell never hands a refused command to git-shell or to anything else.
//
//	valley-telemetry sources
//	valley-telemetry snapshot
//	valley-telemetry read [--source=NAME] [--since=CURSOR] [--limit=N] [--logs]
//	valley-telemetry follow [--source=NAME] [--since=CURSOR] [--seconds=N] [--logs]
//	valley-telemetry report
//
// report is the one operation that sends records: they follow on standard
// input, one JSON line each. Every other operation only reads.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CommandName is the first word of every telemetry command.
const CommandName = "valley-telemetry"

// The operations, and the grant each needs.
const (
	OpSources  = "sources"
	OpSnapshot = "snapshot"
	OpRead     = "read"
	OpFollow   = "follow"
	OpReport   = "report"

	// GrantObserve reads every source on the host: shared logs span every
	// project, so reading them is a host-wide grant, apart from any
	// project's read consent. GrantReport sends records about the
	// sender's own runs.
	GrantObserve = "observe"
	GrantReport  = "report"
)

// Limits the collector holds every request to, whatever it asks for.
const (
	DefaultReadLimit     = 200
	MaxReadLimit         = 1000
	DefaultFollowSeconds = 60
	MaxFollowSeconds     = 300
	MaxResponseBytes     = 4 << 20
	MaxReportBytes       = 1 << 20
	MaxReportRecords     = 1000
	LogTailBytes         = 64 << 10
	MaxCommandBytes      = 4096
)

// Request is what the shell passes to the collector: the operation and
// its flags as the command gave them, and the principal the shell derived
// from the key sshd authenticated. Nothing in the command names it.
type Request struct {
	V         int    `json:"v"`
	Principal string `json:"principal"`
	Op        string `json:"op"`
	Source    string `json:"source,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Seconds   int    `json:"seconds,omitempty"`
	Logs      bool   `json:"logs,omitempty"`
}

// Grant is the grant the request's operation needs.
func (r Request) Grant() string {
	if r.Op == OpReport {
		return GrantReport
	}
	return GrantObserve
}

var (
	sourceRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{0,95}$`)
	cursorRE    = regexp.MustCompile(`^[a-z0-9._@-]{1,96}=[0-9a-f]{16}:[0-9]{1,18}$`)
	principalRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
)

// ValidPrincipal reports whether s is a principal name, as the push
// policy reads one.
func ValidPrincipal(s string) bool { return principalRE.MatchString(s) }

// ValidSource reports whether s is a source or stream name.
func ValidSource(s string) bool { return sourceRE.MatchString(s) }

// IsCommand reports whether a session's command is meant as a telemetry
// command: anything that starts with the command's name. Such a command
// is the telemetry interface's to accept or refuse, and nothing else's.
func IsCommand(cmd string) bool { return strings.HasPrefix(cmd, CommandName) }

var opFlags = map[string]map[string]bool{
	OpSources:  {},
	OpSnapshot: {},
	OpRead:     {"source": true, "since": true, "limit": true, "logs": true},
	OpFollow:   {"source": true, "since": true, "seconds": true, "logs": true},
	OpReport:   {},
}

// ParseCommand reads a telemetry command. principal is not part of it.
func ParseCommand(cmd string) (Request, error) {
	r := Request{V: 1}
	if len(cmd) > MaxCommandBytes {
		return r, errors.New("the command is too long")
	}
	for i := 0; i < len(cmd); i++ {
		if cmd[i] < 0x21 && cmd[i] != ' ' || cmd[i] > 0x7e {
			return r, errors.New("the command holds a character it may not")
		}
	}
	words := strings.Split(cmd, " ")
	if len(words) < 2 || words[0] != CommandName {
		return r, fmt.Errorf("usage: %s sources|snapshot|read|follow|report [--flag=value]...", CommandName)
	}
	r.Op = words[1]
	allowed, ok := opFlags[r.Op]
	if !ok {
		return r, fmt.Errorf("%q is not a telemetry operation", clip(r.Op))
	}
	seen := map[string]bool{}
	for _, w := range words[2:] {
		if !strings.HasPrefix(w, "--") {
			return r, fmt.Errorf("%q is not a flag", clip(w))
		}
		name, value, hasValue := strings.Cut(w[2:], "=")
		if !allowed[name] {
			return r, fmt.Errorf("%s takes no --%s", r.Op, clip(name))
		}
		if seen[name] {
			return r, fmt.Errorf("--%s is given twice", name)
		}
		seen[name] = true
		if name == "logs" {
			if hasValue {
				return r, errors.New("--logs takes no value")
			}
			r.Logs = true
			continue
		}
		if !hasValue || value == "" {
			return r, fmt.Errorf("--%s needs a value", name)
		}
		switch name {
		case "source":
			if !ValidSource(value) {
				return r, fmt.Errorf("%q is not a source name", clip(value))
			}
			r.Source = value
		case "since":
			if _, err := ParseCursor(value); err != nil {
				return r, err
			}
			r.Cursor = value
		case "limit", "seconds":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 1_000_000 || strconv.Itoa(n) != value {
				return r, fmt.Errorf("--%s=%s is not a positive number", name, clip(value))
			}
			if name == "limit" {
				r.Limit = n
			} else {
				r.Seconds = n
			}
		}
	}
	return r, nil
}

// Command is the command that asks for r: the inverse of ParseCommand.
func (r Request) Command() string {
	words := []string{CommandName, r.Op}
	if r.Source != "" {
		words = append(words, "--source="+r.Source)
	}
	if r.Cursor != "" {
		words = append(words, "--since="+r.Cursor)
	}
	if r.Limit > 0 {
		words = append(words, "--limit="+strconv.Itoa(r.Limit))
	}
	if r.Seconds > 0 {
		words = append(words, "--seconds="+strconv.Itoa(r.Seconds))
	}
	if r.Logs {
		words = append(words, "--logs")
	}
	return strings.Join(words, " ")
}

// Check holds a request the collector received to what ParseCommand
// could have produced, and clamps its limits. The collector checks every
// request again, whatever passed it on.
func (r *Request) Check() error {
	if r.V != 1 {
		return fmt.Errorf("request version %d is not 1", r.V)
	}
	if !ValidPrincipal(r.Principal) {
		return errors.New("the request names no principal")
	}
	parsed, err := ParseCommand(r.Command())
	if err != nil {
		return err
	}
	parsed.Principal = r.Principal
	if parsed != *r {
		return errors.New("the request is not one a command asks for")
	}
	if r.Limit <= 0 {
		r.Limit = DefaultReadLimit
	}
	r.Limit = min(r.Limit, MaxReadLimit)
	if r.Seconds <= 0 {
		r.Seconds = DefaultFollowSeconds
	}
	r.Seconds = min(r.Seconds, MaxFollowSeconds)
	return nil
}

// DecodeRequest reads the one line the shell sends.
func DecodeRequest(line []byte) (Request, error) {
	var r Request
	d := json.NewDecoder(strings.NewReader(string(line)))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, fmt.Errorf("the request does not read: %w", err)
	}
	return r, nil
}

// A cursor says how far a reader has read: for each stream, its epoch and
// the number of the last record read, as stream=epoch:seq separated by
// commas. A stream made again has another epoch, and a cursor naming the
// old one reads it from its start, with the reset said.
type Cursor map[string]Position

// Position is how far one stream has been read.
type Position struct {
	Epoch string
	Seq   int64
}

// ParseCursor reads a cursor.
func ParseCursor(s string) (Cursor, error) {
	c := Cursor{}
	if s == "" {
		return c, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) > 64 {
		return nil, errors.New("the cursor names too many streams")
	}
	for _, p := range parts {
		if !cursorRE.MatchString(p) {
			return nil, fmt.Errorf("%q is not a cursor", clip(p))
		}
		name, rest, _ := strings.Cut(p, "=")
		epoch, seq, _ := strings.Cut(rest, ":")
		n, err := strconv.ParseInt(seq, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a cursor", clip(p))
		}
		if _, dup := c[name]; dup {
			return nil, fmt.Errorf("the cursor names %s twice", name)
		}
		c[name] = Position{epoch, n}
	}
	return c, nil
}

func (c Cursor) String() string {
	names := make([]string, 0, len(c))
	for n := range c {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s=%s:%d", n, c[n].Epoch, c[n].Seq)
	}
	return strings.Join(parts, ",")
}

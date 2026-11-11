package main

// valley telemetry: reading a valley host's diagnostics from a session,
// and the workstation's half of them.
//
// The reading verbs (sources, snapshot, read, follow) run one telemetry
// command on the host over ssh, as the git user, with the key the remote
// already uses (../telemetry/protocol.go). The host's shell derives the
// principal from that key and holds it to the observe grant. The remote
// is the checkout's origin, or the one --remote names: a remote's name,
// an ssh URL, or user@host:path. Its URL is read, never run, so no
// configured ssh command takes part. The answer is JSON lines, which
// --json prints as they come and which are otherwise rendered as text.
// An error, or an answer that never ended, exits 1: it never reads as
// nothing to report.
//
// The workstation's records: each approval attempt starts a run (start),
// which binds the run to the destination its checkout's telemetry remote
// names at that moment. The run's records (emit), and the progress attest
// records of the checks it runs, go into that destination's own spool,
// owner-only, under $XDG_STATE_HOME/valley/telemetry/spools/. A spool
// names its destination in a file written once, and flush sends a spool
// to that destination and nowhere else, whichever checkout or remote the
// flush runs from. Each spool has its own acknowledgement, lock and log
// offsets, so one host's acknowledgement never stands for another's.
//
// flush sends what a spool holds and its host has not acknowledged, with
// the report command, and remembers how far the host acknowledged. A
// record the host already holds is not stored twice. Each spool is
// bounded like any stream, and loses its oldest records first; the host
// records the gap. At most maxSpools spools exist at once. Nothing in this
// ever fails or holds up an approval: approve runs flush in the
// background, and a flush that fails leaves the spool for the next.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"the-valley/telemetry"
)

const telemetryUsage = `usage: valley telemetry [--remote <remote>] [--json] sources
       valley telemetry [--remote <remote>] [--json] snapshot
       valley telemetry [--remote <remote>] [--json] read [--source <name>] [--since <cursor>] [--limit <n>] [--logs]
       valley telemetry [--remote <remote>] [--json] follow [--source <name>] [--since <cursor>] [--seconds <n>] [--logs]
       valley telemetry flush [--remote <remote>]`

func runTelemetry(g git, args []string, stdout, stderr io.Writer) error {
	remote, asJSON := "", false
	for len(args) > 0 {
		switch {
		case args[0] == "--json":
			asJSON, args = true, args[1:]
		case args[0] == "--remote" && len(args) > 1:
			remote, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "--remote="):
			remote, args = strings.TrimPrefix(args[0], "--remote="), args[1:]
		default:
			goto verb
		}
	}
verb:
	if len(args) == 0 {
		return failure(telemetryUsage)
	}
	switch args[0] {
	case "start":
		// A new run, bound to its destination: the run ID, the spool of
		// that destination for approve and the attest it runs to record
		// into, and the project the destination's path names.
		dest, err := telemetryRemote(g, remote)
		if err != nil {
			return failure(err.Error())
		}
		dir, err := bindSpool(dest)
		if err != nil {
			return failure(err.Error())
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", telemetry.NewRunID(), dir, dest.project())
		return nil
	case "emit":
		return runEmit(args[1:])
	case "command":
		// The command record of a verb bin/valley runs itself, as it
		// exits: --since <µs> --version <stamp> --status <n> -- <verb> [args].
		// It exits 0 whatever becomes of the record.
		var since, version, status string
		rest := args[1:]
		for len(rest) > 1 && rest[0] != "--" {
			switch rest[0] {
			case "--since":
				since = rest[1]
			case "--version":
				version = rest[1]
			case "--status":
				status = rest[1]
			}
			rest = rest[2:]
		}
		if len(rest) > 0 && rest[0] == "--" {
			n, err := strconv.Atoi(status)
			if err == nil {
				invocationOf(rest[1:], since, version).record(g, n, false)
			}
		}
		return nil
	case "flush":
		return runFlush(g, remote, args[1:], stderr)
	case telemetry.OpSources, telemetry.OpSnapshot, telemetry.OpRead, telemetry.OpFollow:
	default:
		return failure(telemetryUsage)
	}
	req := telemetry.Request{V: 1, Op: args[0]}
	fs := flag.NewFlagSet("telemetry", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&req.Source, "source", "", "")
	fs.StringVar(&req.Cursor, "since", "", "")
	fs.IntVar(&req.Limit, "limit", 0, "")
	fs.IntVar(&req.Seconds, "seconds", 0, "")
	fs.BoolVar(&req.Logs, "logs", false, "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return failure(telemetryUsage)
	}
	cmd := req.Command()
	if _, err := telemetry.ParseCommand(cmd); err != nil {
		return failure(err.Error())
	}
	dest, err := telemetryRemote(g, remote)
	if err != nil {
		return failure(err.Error())
	}
	timeout := 60 * time.Second
	if req.Op == telemetry.OpFollow {
		timeout = time.Duration(max(req.Seconds, telemetry.DefaultFollowSeconds)+60) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ssh := sshCommand(ctx, dest, cmd)
	out, err := ssh.StdoutPipe()
	if err != nil {
		return err
	}
	var sshErr bytes.Buffer
	ssh.Stderr = &sshErr
	if err := ssh.Start(); err != nil {
		return failure(fmt.Sprintf("could not run ssh: %v", err))
	}
	r := &renderer{w: stdout, json: asJSON}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), telemetry.MaxResponseBytes)
	for sc.Scan() {
		r.line(sc.Bytes())
	}
	waitErr := ssh.Wait()
	switch {
	case r.err != "":
		return failure("the host refused: " + r.err)
	case !r.ended:
		why := strings.TrimSpace(telemetry.Sanitize(sshErr.String()))
		if why == "" && waitErr != nil {
			why = waitErr.Error()
		}
		return failure(fmt.Sprintf("the telemetry answer from %s did not end; nothing above is complete. %s", dest.host, why))
	}
	return nil
}

// sshDest is where a telemetry command goes.
type sshDest struct {
	host string // [user@]host, which ssh resolves through its own config
	port string
	path string // the repository's path on the host, where the remote names one
}

// identity names a destination as the remote spelled it:
// ssh://[user@]host[:port]. Two spellings of one host are two
// destinations: an alias in ssh's configuration can stand for another
// host, port or key, and nothing here can tell.
func (d sshDest) identity() string {
	if d.port != "" {
		return "ssh://" + d.host + ":" + d.port
	}
	return "ssh://" + d.host
}

// project is the project a destination's path names: its last element,
// less .git, or nothing.
func (d sshDest) project() string {
	name := strings.TrimSuffix(path.Base(strings.TrimRight(d.path, "/")), ".git")
	if projectName.MatchString(name) {
		return name
	}
	return ""
}

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// destination reads an identity back.
func destination(identity string) (sshDest, error) {
	u, err := url.Parse(identity)
	if err != nil || u.Scheme != "ssh" || u.Path != "" || u.RawQuery != "" {
		return sshDest{}, fmt.Errorf("%q names no ssh destination", identity)
	}
	d := sshDest{host: u.Hostname(), port: u.Port()}
	if u.User != nil {
		d.host = u.User.Username() + "@" + d.host
	}
	if !sshHost.MatchString(d.host) || d.identity() != identity {
		return sshDest{}, fmt.Errorf("%q names no ssh destination", identity)
	}
	return d, nil
}

var (
	scpRemote = regexp.MustCompile(`^((?:[A-Za-z0-9._-]+@)?[A-Za-z0-9][A-Za-z0-9.-]*):[^:]`)
	sshHost   = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?[A-Za-z0-9][A-Za-z0-9.-]*$`)
)

// telemetryRemote resolves --remote, or the checkout's telemetry remote
// (valley.telemetry.remote, else origin), to an ssh destination. A remote
// that is not reached over ssh is refused.
func telemetryRemote(g git, remote string) (sshDest, error) {
	return remoteDest(func(key string) (string, bool) {
		v, err := g.output(true, "config", "--get", key)
		return strings.TrimSpace(v), err == nil
	}, remote)
}

// remoteDest is telemetryRemote with the checkout's configuration read
// from config.
func remoteDest(config func(key string) (string, bool), remote string) (sshDest, error) {
	if remote == "" {
		if r, ok := config("valley.telemetry.remote"); ok && r != "" {
			remote = r
		} else {
			remote = "origin"
		}
	}
	location := remote
	if !strings.Contains(remote, ":") {
		u, ok := config("remote." + remote + ".url")
		if !ok || u == "" {
			return sshDest{}, fmt.Errorf("no remote %s to read telemetry from; name one with --remote", remote)
		}
		location = u
	}
	if strings.Contains(location, "://") {
		u, err := url.Parse(location)
		if err != nil || u.Scheme != "ssh" || u.Hostname() == "" {
			return sshDest{}, fmt.Errorf("%s is not an ssh remote; telemetry is read over ssh", remote)
		}
		host := u.Hostname()
		if u.User != nil {
			host = u.User.Username() + "@" + host
		}
		if !sshHost.MatchString(host) {
			return sshDest{}, fmt.Errorf("%s does not name an ssh host", remote)
		}
		return sshDest{host: host, port: u.Port(), path: u.Path}, nil
	}
	if m := scpRemote.FindStringSubmatch(location); m != nil {
		return sshDest{host: m[1], path: location[len(m[1])+1:]}, nil
	}
	if sshHost.MatchString(location) {
		return sshDest{host: location}, nil
	}
	return sshDest{}, fmt.Errorf("%s is not an ssh remote; telemetry is read over ssh", remote)
}

// sshCommand runs one command on dest. The command is built from parsed
// flags, never from text the caller wrote.
func sshCommand(ctx context.Context, dest sshDest, command string) *exec.Cmd {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15"}
	if dest.port != "" {
		args = append(args, "-p", dest.port)
	}
	args = append(args, "--", dest.host, command)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// renderer prints answer lines: as they are with --json, or as text.
// Every string from the host is made safe to show again here.
type renderer struct {
	w     io.Writer
	json  bool
	ended bool
	err   string
}

func (r *renderer) line(raw []byte) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		r.err = "an answer line is not JSON"
		return
	}
	kind, _ := m["kind"].(string)
	switch kind {
	case "error":
		r.err = telemetry.Sanitize(fmt.Sprint(m["error"]))
	case "end":
		r.ended = true
	}
	if r.json {
		fmt.Fprintf(r.w, "%s\n", raw)
		return
	}
	s := func(k string) string {
		v, ok := m[k]
		if !ok || v == nil {
			return ""
		}
		return telemetry.Sanitize(fmt.Sprint(v))
	}
	switch kind {
	case "header":
		fmt.Fprintf(r.w, "%s at %s (%s)\n", s("host"), s("server_time"), s("format"))
	case "source":
		fmt.Fprintf(r.w, "\n%-20s %-11s %s\n", s("name"), s("state"), s("detail"))
		if d := s("description"); d != "" {
			fmt.Fprintf(r.w, "  %s; %s\n", d, s("location"))
		}
		if units, ok := m["unit_states"].([]any); ok {
			for _, u := range units {
				r.unit(u)
			}
		}
		if st, ok := m["stream"].(map[string]any); ok {
			fmt.Fprintf(r.w, "  stream %v: records %v..%v, newest %v at %v\n", st["name"], num(st["first"]), num(st["last"]), st["last_type"], st["last_time"])
		}
		if j := s("journal_since"); j != "" {
			fmt.Fprintf(r.w, "  journal reaches back to %s\n", j)
		}
		if c, ok := m["configured"].(map[string]any); ok {
			for _, k := range sortedKeysAny(c) {
				fmt.Fprintf(r.w, "  configured %s: %s\n", k, telemetry.Sanitize(fmt.Sprint(c[k])))
			}
		}
		if miss, ok := m["not_instrumented"].([]any); ok {
			for _, x := range miss {
				fmt.Fprintf(r.w, "  not instrumented: %s\n", telemetry.Sanitize(fmt.Sprint(x)))
			}
		}
		for _, key := range []string{"open_runs", "latest_runs"} {
			if runs, ok := m[key].([]any); ok {
				for _, x := range runs {
					run, _ := x.(map[string]any)
					label := "run"
					if key == "open_runs" {
						label = "open"
					}
					fmt.Fprintf(r.w, "  %s %v %v %v..%v last %v %v\n", label, run["run"], run["producer"], run["first"], run["last"], run["last_type"], checksOf(run["checks"]))
				}
			}
		}
		if st, ok := m["exported_state"]; ok {
			b, _ := json.MarshalIndent(st, "  ", "  ")
			fmt.Fprintf(r.w, "  exported state:\n  %s\n", telemetry.Sanitize(string(b)))
		}
	case "record":
		if s("type") == "check.output" {
			d, _ := m["data"].(map[string]any)
			fmt.Fprintf(r.w, "--- untrusted output: %s#%s run %s check %s at byte %s\n%s\n", s("stream"), num(m["seq"]), s("run"), s("check"),
				num(d["offset"]), strings.TrimRight(telemetry.Sanitize(fmt.Sprint(d["text"])), "\n"))
			return
		}
		fmt.Fprintf(r.w, "%s %s#%s %s%s\n", s("time"), s("stream"), num(m["seq"]), s("type"), recordDetail(m))
	case "gap":
		fmt.Fprintf(r.w, "! gap in %s: %s %s %s %s\n", s("stream"), s("reason"), num(m["from"]), num(m["to"]), s("detail"))
	case "lifecycle":
		fmt.Fprintf(r.w, "%s %s [systemd] %s\n", s("time"), s("unit"), s("message"))
	case "log":
		where := s("unit")
		if where == "" {
			where = fmt.Sprintf("%s run %s check %s, bytes %s..%s of %s", s("stream"), s("run"), s("check"), num(m["from"]), num(m["to"]), num(m["size"]))
		}
		fmt.Fprintf(r.w, "--- untrusted output: %s %s\n%s\n", where, s("time"), strings.TrimRight(s("text"), "\n"))
	case "unavailable":
		fmt.Fprintf(r.w, "! %s unavailable: %s\n", s("what"), s("error"))
	case "ack":
		fmt.Fprintf(r.w, "reported: %s accepted, %s already held, %s refused\n", num(m["accepted"]), num(m["duplicates"]), num(m["rejected"]))
	case "end":
		if c := s("cursor"); c != "" {
			fmt.Fprintf(r.w, "\ncursor %s\n", c)
		}
		if t, _ := m["truncated"].(bool); t {
			fmt.Fprintln(r.w, "the answer was cut short at its size bound; read on from the cursor")
		}
	}
}

func (r *renderer) unit(x any) {
	u, _ := x.(map[string]any)
	g := func(k string) string { return telemetry.Sanitize(fmt.Sprint(u[k])) }
	line := fmt.Sprintf("  %s: %s/%s", g("Id"), g("ActiveState"), g("SubState"))
	if res := g("Result"); res != "" && res != "<nil>" {
		line += " result " + res
	}
	if st := g("ExecMainStatus"); st != "" && st != "0" && st != "<nil>" {
		line += " status " + st
	}
	if n := g("NRestarts"); n != "" && n != "0" && n != "<nil>" {
		line += " restarts " + n
	}
	if t := g("StateChangeTimestamp"); t != "" && t != "<nil>" {
		line += " since " + t
	}
	fmt.Fprintln(r.w, line)
}

func num(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatInt(int64(f), 10)
	}
	return telemetry.Sanitize(fmt.Sprint(v))
}

func sortedKeysAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func checksOf(v any) string {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return ""
	}
	var parts []string
	for _, k := range sortedKeysAny(m) {
		parts = append(parts, telemetry.Sanitize(fmt.Sprintf("%s=%v", k, m[k])))
	}
	return "checks " + strings.Join(parts, " ")
}

// recordDetail is what a record is about, and its data, on one line.
func recordDetail(m map[string]any) string {
	var b strings.Builder
	for _, k := range []string{"principal", "project", "change", "version", "head", "check", "run"} {
		if v, ok := m[k]; ok && v != nil && v != "" {
			s := num(v)
			if k == "head" && len(s) > 12 {
				s = s[:12]
			}
			fmt.Fprintf(&b, " %s=%s", k, s)
		}
	}
	if d, ok := m["data"].(map[string]any); ok {
		for _, k := range sortedKeysAny(d) {
			fmt.Fprintf(&b, " %s=%s", k, num(d[k]))
		}
	}
	if rcv, ok := m["received"].(string); ok && rcv != "" {
		fmt.Fprintf(&b, " (received %s)", telemetry.Sanitize(rcv))
	}
	return b.String()
}

// The spools: $XDG_STATE_HOME/valley/telemetry/spools/<key>, one for each
// destination, where an unset or relative XDG_STATE_HOME means
// ~/.local/state, and key is the start of the SHA-256 of the destination's
// identity. A spool's destination file names the identity; it is written
// once, and a spool without one is no spool. The spool of earlier CLIs,
// .../telemetry/spool, named no destination: it is kept as it is and
// never sent.
func spoolsRoot() (string, error) {
	state, err := stateDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(state), "telemetry", "spools"), nil
}

func legacySpool() (string, error) {
	state, err := stateDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(state), "telemetry", "spool"), nil
}

// maxSpools is how many destinations' spools exist at once. Each spool is
// a stream of at most 8 MiB, with the logs of its newest 16 runs, so the
// spools together stay bounded too.
const maxSpools = 4

// idleAfter is how long a spool whose records were all acknowledged stays
// before it may be removed to make room for another destination's.
const idleAfter = time.Hour

func mkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// bindSpool returns the spool of dest, making it, and its destination
// file, when there is none. Making one when maxSpools exist first removes
// those idle for idleAfter with nothing left to send, and fails when that
// leaves no room.
func bindSpool(dest sshDest) (string, error) {
	root, err := spoolsRoot()
	if err != nil {
		return "", err
	}
	if err := mkdirPrivate(root); err != nil {
		return "", err
	}
	id := dest.identity()
	sum := sha256.Sum256([]byte(id))
	dir := filepath.Join(root, hex.EncodeToString(sum[:8]))
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := makeRoom(root); err != nil {
			return "", err
		}
	}
	if err := mkdirPrivate(dir); err != nil {
		return "", err
	}
	named, err := spoolDestination(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeDestination(dir, id); err != nil {
			return "", err
		}
		named, err = spoolDestination(dir)
	}
	if err != nil {
		return "", err
	}
	if named.identity() != id {
		return "", fmt.Errorf("the spool %s is bound to %s, not %s", dir, named.identity(), id)
	}
	return dir, nil
}

// writeDestination names a spool's destination, once: the file is written
// whole under another name and linked into place, and of two runs making
// it at once, one names it.
func writeDestination(dir, id string) error {
	tmp, err := os.CreateTemp(dir, ".destination-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(id + "\n")
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), filepath.Join(dir, "destination")); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}

// spoolDestination is the destination a spool is bound to.
func spoolDestination(dir string) (sshDest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "destination"))
	if err != nil {
		return sshDest{}, err
	}
	return destination(strings.TrimSpace(string(data)))
}

// spools lists the bound spools.
func spools() []string {
	root, err := spoolsRoot()
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(root)
	var dirs []string
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if _, err := spoolDestination(dir); e.IsDir() && err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// unsent is how many of a spool's records its host has not acknowledged,
// and when the spool last stored one.
func unsent(dir string) (int64, time.Time) {
	var last time.Time
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && info.ModTime().After(last) {
				last = info.ModTime()
			}
		}
	}
	rng, err := (&telemetry.Stream{Dir: dir}).Range()
	if err != nil {
		return 0, last
	}
	return max(rng.Last-ackedOf(dir, rng.Epoch), 0), last
}

// ackedOf is the number of the newest record a spool's host acknowledged.
func ackedOf(dir, epoch string) int64 {
	data, err := os.ReadFile(filepath.Join(dir, "acked"))
	if err != nil {
		return 0
	}
	e, seq, ok := strings.Cut(strings.TrimSpace(string(data)), ":")
	if !ok || e != epoch {
		return 0
	}
	n, _ := strconv.ParseInt(seq, 10, 64)
	return n
}

func makeRoom(root string) error {
	dirs := spools()
	for _, dir := range dirs {
		if len(dirs) < maxSpools {
			return nil
		}
		if n, last := unsent(dir); n == 0 && time.Since(last) > idleAfter {
			os.RemoveAll(dir)
			dirs = spools()
		}
	}
	if len(spools()) >= maxSpools {
		return fmt.Errorf("the telemetry spools of %d other destinations still hold records to send or are in use; this run is not recorded", maxSpools)
	}
	return nil
}

// runEmit stores one record of the workstation's own in the spool. It
// never fails the caller: a record that cannot be stored is dropped, and
// emit exits 0 all the same.
func runEmit(args []string) error {
	fs := flag.NewFlagSet("emit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var r telemetry.Record
	var typ, run, version, exe string
	data := map[string]any{}
	var spool string
	fs.StringVar(&typ, "type", "", "")
	fs.StringVar(&run, "run", "", "")
	fs.StringVar(&spool, "spool", "", "")
	fs.StringVar(&version, "producer-version", "", "")
	fs.StringVar(&exe, "producer-exe", "", "")
	fs.StringVar(&r.Project, "project", "", "")
	fs.StringVar(&r.Change, "change", "", "")
	fs.IntVar(&r.Version, "version", 0, "")
	fs.StringVar(&r.Head, "head", "", "")
	fs.StringVar(&r.Tree, "tree", "", "")
	fs.StringVar(&r.Check, "check", "", "")
	fs.Func("data", "key=value", func(kv string) error {
		k, v, _ := strings.Cut(kv, "=")
		data[k] = v
		return nil
	})
	fs.Func("number", "key=n", func(kv string) error {
		k, v, _ := strings.Cut(kv, "=")
		n, err := strconv.ParseInt(v, 10, 64)
		data[k] = n
		return err
	})
	fs.Func("list", "key=a,b", func(kv string) error {
		k, v, _ := strings.Cut(kv, "=")
		list := []string{}
		if v != "" {
			list = strings.Split(v, ",")
		}
		data[k] = list
		return nil
	})
	if err := fs.Parse(args); err != nil || typ == "" || run == "" || spool == "" {
		return failure("usage: valley-go telemetry emit --spool <dir> --type <type> --run <id> [--project --change --version --head --tree --check] [--data k=v] [--number k=n] [--list k=a,b]")
	}
	// Only a spool start bound stores a record: one that names no
	// destination would have nowhere to go.
	dir := filepath.Clean(spool)
	if _, err := spoolDestination(dir); err != nil {
		return nil
	}
	p := telemetry.Self("valley", version)
	if exe != "" {
		p.Exe = exe
	}
	telemetry.NewEmitter(dir, p, run).Emit(typ, r, data)
	return nil
}

// Flushing.
const (
	flushBatch    = 500
	flushDeadline = 20 * time.Second
	watchEvery    = 2 * time.Second
	watchLongest  = 12 * time.Hour
)

// runFlush sends every spool's unacknowledged records, each to the
// destination it is bound to, or with --remote only the spool bound to the
// destination that remote names. With --watch PID, it sends every
// watchEvery while that process lives, and once more after it ends.
func runFlush(g git, remote string, args []string, stderr io.Writer) error {
	watch := 0
	if len(args) == 2 && args[0] == "--watch" {
		n, err := strconv.Atoi(args[1])
		if err != nil || n <= 0 {
			return failure("usage: valley telemetry flush [--watch <pid>]")
		}
		watch = n
	} else if len(args) != 0 {
		return failure("usage: valley telemetry flush [--watch <pid>]")
	}
	if watch != 0 {
		end := time.Now().Add(watchLongest)
		for time.Now().Before(end) {
			alive := syscall.Kill(watch, 0) == nil
			for _, dir := range spools() {
				flushSpool(dir, false)
			}
			if !alive {
				return nil
			}
			time.Sleep(watchEvery)
		}
		return nil
	}
	dirs := spools()
	if remote != "" {
		dest, err := telemetryRemote(g, remote)
		if err != nil {
			return failure(err.Error())
		}
		var only []string
		for _, dir := range dirs {
			if d, _ := spoolDestination(dir); d.identity() == dest.identity() {
				only = append(only, dir)
			}
		}
		dirs = only
		if len(dirs) == 0 {
			fmt.Fprintf(stderr, "valley: nothing is spooled for %s\n", dest.identity())
		}
	}
	var failed []string
	for _, dir := range dirs {
		d, _ := spoolDestination(dir)
		n, err := flushSpool(dir, true)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		fmt.Fprintf(stderr, "valley: %d records sent to %s\n", n, d.identity())
	}
	if legacy, err := legacySpool(); err == nil {
		if rng, err := (&telemetry.Stream{Dir: legacy}).Range(); err == nil && rng.Last >= rng.First {
			fmt.Fprintf(stderr, "valley: %s holds records from before each spool named its destination; they are kept and never sent. Remove it to drop them.\n", legacy)
		}
	}
	if len(failed) > 0 {
		return failure(strings.Join(failed, "; "))
	}
	return nil
}

// flushSpool sends what the spool holds after what the host acknowledged,
// a batch at a time, to the destination it is bound to, and returns how
// many records it sent. Only one flush
// runs at a time. A background flush that finds another running leaves the
// work to it; one asked for by hand (wait) waits for it, up to
// flushDeadline, and then sends what is left itself.
func flushSpool(dir string, wait bool) (int, error) {
	st := &telemetry.Stream{Dir: dir}
	rng, err := st.Range()
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "flush.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	for waited := time.Duration(0); syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil; waited += 100 * time.Millisecond {
		if !wait {
			return 0, nil
		}
		if waited > flushDeadline {
			return 0, errors.New("another flush has held the spool for too long")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The destination is the spool's own, never the checkout's or the
	// caller's.
	dest, err := spoolDestination(dir)
	if err != nil {
		return 0, err
	}
	spoolOutput(st)
	ackPath := filepath.Join(dir, "acked")
	acked := ackedOf(dir, rng.Epoch)
	sent := 0
	for {
		recs, _, err := st.Read(acked, flushBatch, telemetry.MaxReportBytes/2)
		if err != nil || len(recs) == 0 {
			return sent, err
		}
		var body bytes.Buffer
		for _, r := range recs {
			r.ClientID = fmt.Sprintf("%s:%d", rng.Epoch, r.Seq)
			r.Seq, r.Stream = 0, ""
			line, _ := json.Marshal(r)
			body.Write(append(line, '\n'))
		}
		ctx, cancel := context.WithTimeout(context.Background(), flushDeadline)
		cmd := sshCommand(ctx, dest, telemetry.Request{V: 1, Op: telemetry.OpReport}.Command())
		cmd.Stdin = &body
		out, err := cmd.Output()
		cancel()
		if err != nil {
			return sent, fmt.Errorf("the report to %s did not complete: %v", dest.identity(), err)
		}
		got := int64(-1)
		for _, line := range strings.Split(string(out), "\n") {
			var m struct {
				Kind  string           `json:"kind"`
				Error string           `json:"error"`
				Acked map[string]int64 `json:"acked"`
			}
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if m.Kind == "error" {
				return sent, fmt.Errorf("the host refused the report: %s", telemetry.Sanitize(m.Error))
			}
			if m.Kind == "ack" {
				got = m.Acked[rng.Epoch]
			}
		}
		if got < recs[len(recs)-1].Seq {
			return sent, fmt.Errorf("the host acknowledged %d of the spool, not %d", got, recs[len(recs)-1].Seq)
		}
		sent += len(recs)
		acked = got
		if err := os.WriteFile(ackPath+".new", []byte(fmt.Sprintf("%s:%d\n", rng.Epoch, acked)), 0o600); err == nil {
			os.Rename(ackPath+".new", ackPath)
		}
	}
}

// outputPerFlush bounds how much check output one flush turns into
// records.
const outputPerFlush = 256 << 10

// spoolOutput turns what the spool's check logs gained since the last
// flush into check.output records in the spool, so a check's output
// reaches the host while it runs. Each record holds at most
// telemetry.OutputChunk bytes of it, made safe to show; the host serves
// them only to a reader asking for logs, marked untrusted.
func spoolOutput(st *telemetry.Stream) {
	sentPath := filepath.Join(st.Dir, "output-sent.json")
	sent := map[string]int64{}
	if data, err := os.ReadFile(sentPath); err == nil {
		json.Unmarshal(data, &sent)
	}
	budget := int64(outputPerFlush)
	now := map[string]int64{}
	for _, l := range st.Logs() {
		key := l.Run + "/" + l.Check
		from := sent[key]
		now[key] = from
		if l.Size <= from || budget <= 0 {
			continue
		}
		f, err := os.Open(l.Path)
		if err != nil {
			continue
		}
		buf := make([]byte, min(l.Size-from, budget))
		n, _ := f.ReadAt(buf, from)
		f.Close()
		buf = buf[:n]
		e := telemetry.NewEmitter(st.Dir, telemetry.Self("attest", ""), l.Run)
		for len(buf) > 0 {
			chunk := buf[:min(len(buf), telemetry.OutputChunk)]
			if i := bytes.LastIndexByte(chunk, '\n'); i > 0 && len(chunk) < len(buf) {
				chunk = chunk[:i+1]
			}
			e.Emit("check.output", telemetry.Record{Check: l.Check}, map[string]any{"offset": from, "text": telemetry.Sanitize(string(chunk))})
			from += int64(len(chunk))
			budget -= int64(len(chunk))
			buf = buf[len(chunk):]
		}
		now[key] = from
	}
	if data, err := json.Marshal(now); err == nil {
		if os.WriteFile(sentPath+".new", data, 0o600) == nil {
			os.Rename(sentPath+".new", sentPath)
		}
	}
}

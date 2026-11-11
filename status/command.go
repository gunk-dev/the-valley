package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"the-valley/telemetry"
)

// A valley invocation's command record (valley help telemetry): its verb,
// the names of the flags it was given, its exit status, its wall time,
// and the time and processes each phase of it took. It carries no
// argument's value, since a report goes to the host and a project may be
// public. Each public verb records its own, once it has run.

// phaseClock is where this process's wall time goes: to the phase current
// when it passes, and to "other" outside every phase. A phase's time
// leaves out the phases entered from it. Work on another goroutine counts
// toward whichever phase is current, so the times are a reading guide,
// not a profile.
type phaseClock struct {
	mu      sync.Mutex
	began   time.Time
	current string
	since   time.Time
	spent   map[string]time.Duration
	procs   map[string]int64
}

var phases = &phaseClock{began: time.Now(), current: "other", since: time.Now(),
	spent: map[string]time.Duration{}, procs: map[string]int64{}}

// phase enters the phase name, and returns what leaves it for the phase
// it was entered from.
func phase(name string) (end func()) {
	c := phases
	c.mu.Lock()
	defer c.mu.Unlock()
	from := c.switchTo(name)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.switchTo(from)
	}
}

func (c *phaseClock) switchTo(name string) (from string) {
	now := time.Now()
	c.spent[c.current] += now.Sub(c.since)
	from, c.current, c.since = c.current, name, now
	return from
}

// processStarted counts a process toward the current phase.
func processStarted() {
	phases.mu.Lock()
	phases.procs[phases.current]++
	phases.mu.Unlock()
}

// invocation is the valley command a process records: the arguments it
// was run with, when it started, and the valley's stamp.
type invocation struct {
	args    []string
	since   time.Time
	version string
}

// commandOf is the invocation a process run with args records, started
// when the process did, or nil for one that records nothing: help, a
// verb's help, --version, an unknown or internal verb, and the background
// flusher, telemetry flush --watch, which sends the records and makes
// none.
func commandOf(args []string) *invocation {
	switch {
	case len(args) == 0 || !publicVerb(args[0]):
		return nil
	case len(args) == 2 && (args[1] == "-h" || args[1] == "--help"):
		return nil
	case len(args) > 2 && args[0] == "telemetry" && args[1] == "flush" && args[2] == "--watch":
		return nil
	}
	return &invocation{args: args, since: phases.began, version: versionStamp}
}

// record stores the invocation's command record in the spool, when the
// repository g names has valley.telemetry on in g's environment, and
// starts a flusher to send it. The record is the project's g's
// configuration names. Telemetry off costs no process and writes nothing.
// Like every workstation record it is best effort: whatever goes wrong
// costs this record, and never the command's output or exit status.
func (inv *invocation) record(g git, status int, timed bool) {
	if inv == nil || !telemetryMentioned(g) {
		return
	}
	elapsed := time.Since(inv.since)
	data := inv.data(status, elapsed, timed)
	g.stderr = io.Discard
	listed, err := g.output(true, "config", "--get-regexp", `^(valley\.telemetry(\.remote)?|remote\..*\.url)$`)
	if err != nil {
		return
	}
	settings := map[string]string{}
	for _, line := range strings.Split(listed, "\n") {
		key, value, given := strings.Cut(line, " ")
		if !given {
			value = "true" // a key with no value is a true boolean
		}
		// git prints the section and the name in lower case, and the
		// subsection, such as a remote's name, as it is.
		settings[key] = value
	}
	switch strings.ToLower(settings["valley.telemetry"]) {
	case "true", "yes", "on", "1":
	default:
		return
	}
	dest, err := remoteDest(func(key string) (string, bool) {
		v, ok := settings[key]
		return strings.TrimSpace(v), ok
	}, "")
	if err != nil {
		return
	}
	dir, err := bindSpool(dest)
	if err != nil {
		return
	}
	version := inv.version
	if version == "" {
		version = "unstamped"
	}
	// No executable: its path can name the user's home.
	p := telemetry.Self("valley", version)
	p.Exe = ""
	telemetry.NewEmitter(dir, p, telemetry.NewRunID()).
		Emit("command", telemetry.Record{Project: dest.project()}, data)
	startFlusher()
}

// data is the record's data: the verb, its subverb for knot and
// telemetry, the flag names given, the exit status, the wall time since
// the command started, and, when this process timed the command, each
// phase's milliseconds and processes started.
func (inv *invocation) data(status int, elapsed time.Duration, timed bool) map[string]any {
	verb, sub, flags := commandWords(inv.args)
	d := map[string]any{"verb": verb, "flags": flags, "status": int64(status), "elapsed_ms": elapsed.Milliseconds()}
	if sub != "" {
		d["subverb"] = sub
	}
	if !timed {
		return d
	}
	c := phases
	c.mu.Lock()
	defer c.mu.Unlock()
	c.switchTo(c.current)
	for name, t := range c.spent {
		d[name+"_ms"] = t.Milliseconds()
		d[name+"_procs"] = c.procs[name]
	}
	return d
}

// knownFlags is every flag a public verb takes: each word the help text
// and the usage texts write as a flag, and -h. A word that looks like a
// flag and is not one may be an argument's value, so it is left out.
var knownFlags = func() map[string]bool {
	flagRE := regexp.MustCompile(`(?:^|[\s\[|(])(--?[a-z][a-z0-9-]*)`)
	text := helpText("") + knotUsage + telemetryUsage + " -h"
	for verb := range helpTopics {
		text += helpText(verb)
	}
	known := map[string]bool{}
	for _, m := range flagRE.FindAllStringSubmatch(text, -1) {
		known[m[1]] = true
	}
	return known
}()

// The subverbs a record names: knot's verbs and telemetry's operations.
var (
	knotVerbs = func() map[string]bool {
		known := map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)^  ([a-z]+) `).FindAllStringSubmatch(knotUsage, -1) {
			known[m[1]] = true
		}
		return known
	}()
	telemetryOps = map[string]bool{"sources": true, "snapshot": true, "read": true, "follow": true,
		"flush": true, "start": true, "emit": true}
)

// commandWords is an invocation's verb, its subverb, and the names of the
// known flags it was given, each once, in the order given. Nothing after
// -- is read, since everything there is a value.
func commandWords(args []string) (verb, sub string, flags []string) {
	verb, flags = args[0], []string{}
	if !publicVerb(verb) {
		verb = "unknown"
	}
	for _, arg := range args[1:] {
		if arg == "--" {
			break
		}
		name, _, _ := strings.Cut(arg, "=")
		switch {
		case knownFlags[name] && strings.HasPrefix(name, "-"):
			if !slices.Contains(flags, name) {
				flags = append(flags, name)
			}
		case sub == "" && verb == "knot" && knotVerbs[arg], sub == "" && verb == "telemetry" && telemetryOps[arg]:
			sub = arg
		}
	}
	return verb, sub, flags
}

// telemetryMentioned reports whether a configuration file git reads in
// g's repository and environment may set valley.telemetry, without
// starting git: it is false only when none of them mentions telemetry or
// includes another file. So a checkout with telemetry off starts no
// process to find that out, and git itself answers whenever the files
// could say yes.
func telemetryMentioned(g git) bool {
	for _, v := range []string{"GIT_DIR", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_SYSTEM", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		if envValue(g.env, v) != "" {
			return true
		}
	}
	home := envValue(g.env, "HOME")
	xdg := envValue(g.env, "XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	files := []string{"/etc/gitconfig", filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config")}
	dirs, found := gitDirs(g.dir)
	if !found {
		return true
	}
	for _, dir := range dirs {
		files = append(files, filepath.Join(dir, "config"), filepath.Join(dir, "config.worktree"))
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		data = bytes.ToLower(data)
		if bytes.Contains(data, []byte("telemetry")) || bytes.Contains(data, []byte("include")) {
			return true
		}
	}
	return false
}

// envValue is the value env gives name, the last one given winning as it
// does for a process started with env, or "".
func envValue(env []string, name string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			value = v
		}
	}
	return value
}

// gitDirs is the repository's git directory at or above from, the working
// directory when from is "", and, for a linked worktree, its common
// directory.
func gitDirs(from string) ([]string, bool) {
	dir, err := filepath.Abs(from)
	if err != nil {
		return nil, false
	}
	for {
		dotgit := filepath.Join(dir, ".git")
		if st, err := os.Stat(dotgit); err == nil {
			if st.IsDir() {
				return []string{dotgit}, true
			}
			data, err := os.ReadFile(dotgit)
			linked, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
			if err != nil || !ok {
				return nil, false
			}
			if !filepath.IsAbs(linked) {
				linked = filepath.Join(dir, linked)
			}
			dirs := []string{linked}
			if common, err := os.ReadFile(filepath.Join(linked, "commondir")); err == nil {
				c := strings.TrimSpace(string(common))
				if !filepath.IsAbs(c) {
					c = filepath.Join(linked, c)
				}
				dirs = append(dirs, c)
			}
			return dirs, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, false
		}
		dir = parent
	}
}

// startFlusher sends the spools in the background, in a session of its
// own, so the command neither waits for the host nor takes it down on
// exit. One process starts one flusher.
func startFlusher() {
	flusherOnce.Do(flushInBackground)
}

var flusherOnce sync.Once

func flushInBackground() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "telemetry", "flush", "--watch", strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

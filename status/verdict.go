package main

// verdict prints what approve and review's prompt say (bin/valley), from
// the facts bin/valley already holds. An approval ends in one verdict
// line, the change and what happened to it, with one indented line under
// it only when the person must act:
//
//	✓ nqyw v3  approved · checks passed · landing on main
//	◇ nqyw v3  approved · waiting for 2 checks (no worker building)
//	  run: valley approve --build-here nqyw
//	⊘ nqyw v3  not approved · change is a draft
//	  run: valley ready nqyw
//
// The glyphs are plan's (plan.go): ✓ settled, ◐ under way, ◇ waiting on
// nothing that will move by itself, ⊘ refused, and ? unknown. An approval
// goes to stdout, a refusal to stderr. On a terminal the glyph has its
// colour and the change's name is bold; in a pipe, or with NO_COLOR set,
// the text is the same without them. A verdict never says a change landed:
// approve files a request and does not watch it land.
//
// checking runs the checks approve builds here, as one line redrawn in
// place on a terminal and as a start line in a pipe, and keeps the
// builder's output to itself unless a check fails. prompt is review's
// prompt: a header of clauses about the change, and the keys it takes.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const verdictUsage = `usage: valley-go verdict approved|refused|unknown --name <name> [--version <n>] [<fact>]...
       valley-go verdict checking --name <name> [--version <n>] [--of <n>] [--passed <n>] [-v] --check <name>... -- <command>...
       valley-go verdict prompt <clause>... -- <key>...
facts: --required <n> --passed <n> --took <duration> --waiting <check> --worker
       --failed <check> --why <obstruction> --run <command> --see <command>
       --then <text> --target <branch>`

// The SGR colours of the verdict glyphs, as plan shows its states.
const (
	colourDone    = "32"
	colourWorking = "33"
	colourRefused = "31"
	colourWaiting = "36"
)

// verdict is the facts one outcome of approve is told from.
type verdict struct {
	outcome  string // approved, refused or unknown
	name     string // a change's handle, or a branch
	version  string
	target   string
	required int // the checks the policy requires; -1 when not given
	passed   int // how many of them have passed; -1 when not known
	took     string
	waiting  []string // required checks no evidence is published for yet
	worker   bool     // a check worker builds the waiting checks
	failed   []string
	why      string // what stops the approval, or what is not known
	run      string // the command the person runs next
	see      string // where the person reads more
	then     string // what the person does next, when it is no command
}

func runVerdict(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, verdictUsage)
		return 2
	}
	if args[0] == "prompt" {
		header, keys, _ := cutArgs(args[1:])
		fmt.Fprint(stdout, promptLines(terminalOf(stdout), header, keys))
		return 0
	}
	v := verdict{outcome: args[0], target: "main", required: -1, passed: -1}
	var checks, command []string
	verbose := false
	args, command, _ = cutArgs(args[1:])
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "--worker" || flag == "-v" {
			v.worker = v.worker || flag == "--worker"
			verbose = verbose || flag == "-v"
			continue
		}
		if i+1 == len(args) {
			fmt.Fprintln(stderr, verdictUsage)
			return 2
		}
		i++
		value := args[i]
		var err error
		switch flag {
		case "--name":
			v.name = value
		case "--version":
			v.version = value
		case "--target":
			v.target = value
		case "--required", "--of":
			v.required, err = strconv.Atoi(value)
		case "--passed":
			v.passed, err = strconv.Atoi(value)
		case "--took":
			v.took = value
		case "--waiting":
			v.waiting = append(v.waiting, value)
		case "--failed":
			v.failed = append(v.failed, value)
		case "--check":
			checks = append(checks, value)
		case "--why":
			v.why = value
		case "--run":
			v.run = value
		case "--see":
			v.see = value
		case "--then":
			v.then = value
		default:
			err = fmt.Errorf("unknown flag %s", flag)
		}
		if err != nil {
			fmt.Fprintf(stderr, "valley: %v\n%s\n", err, verdictUsage)
			return 2
		}
	}
	if v.name == "" {
		fmt.Fprintln(stderr, verdictUsage)
		return 2
	}
	switch v.outcome {
	case "approved", "unknown":
		fmt.Fprint(stdout, v.lines(terminalOf(stdout)))
	case "refused":
		fmt.Fprint(stderr, v.lines(terminalOf(stderr)))
		return 1
	case "checking":
		if len(command) == 0 || len(checks) == 0 {
			fmt.Fprintln(stderr, verdictUsage)
			return 2
		}
		return v.checking(checks, command, verbose, droppable(stdout), droppable(stderr))
	default:
		fmt.Fprintln(stderr, verdictUsage)
		return 2
	}
	return 0
}

// droppable is w, when it is a file, through a duplicate of its
// descriptor. Go ends a process whose write to its own standard output or
// error finds no reader; through a duplicate the write fails with EPIPE
// and nothing more, so an approval goes on when nobody reads what its
// checks print, as attest run's own output does.
func droppable(w io.Writer) io.Writer {
	f, ok := w.(*os.File)
	if !ok {
		return w
	}
	syscall.ForkLock.RLock()
	fd, err := syscall.Dup(int(f.Fd()))
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return w
	}
	return os.NewFile(uintptr(fd), f.Name())
}

// cutArgs splits args at the first "--".
func cutArgs(args []string) (before, after []string, found bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}

// lines is the verdict: its line, and the action line under it when there
// is one.
func (v verdict) lines(t terminal) string {
	var glyph, colour string
	var clauses []string
	action := v.action(t)
	switch v.outcome {
	case "refused":
		glyph, colour = "⊘", colourRefused
		why := v.why
		if len(v.failed) > 0 {
			why = fmt.Sprintf("%d of %s failed", len(v.failed), countOf(v.required, "check"))
			if v.required < 0 {
				why = countOf(len(v.failed), "check") + " failed"
			}
			action = t.tint(colourRefused, "✗") + " " + strings.Join(v.failed, ", ") + " — see: valley status " + v.name
		}
		clauses = []string{"not approved", why}
	case "unknown":
		glyph = "?"
		why := v.why
		if why == "" {
			why = "state unknown"
		}
		clauses = []string{"approved", why, "see: valley status " + v.name}
		action = ""
	case "approved":
		clauses = []string{"approved"}
		switch {
		case v.required == 0:
			glyph, colour = "✓", colourDone
			clauses = append(clauses, "no checks required", "landing on "+v.target)
		case len(v.waiting) == 0:
			glyph, colour = "✓", colourDone
			passed := "checks passed"
			if v.took != "" && v.required > 0 {
				passed = countOf(v.required, "check") + " passed in " + v.took
			}
			clauses = append(clauses, passed, "landing on "+v.target)
		case v.worker:
			glyph, colour = "◐", colourWorking
			if v.passed >= 0 && v.required > 0 {
				pass := "pass"
				if len(v.waiting) == 1 {
					pass = "passes"
				}
				clauses = append(clauses, fmt.Sprintf("lands when %s %s (%d/%d passed)", countOf(len(v.waiting), "check"), pass, v.passed, v.required))
			} else {
				clauses = append(clauses, "waiting for "+countOf(len(v.waiting), "check"))
			}
		default:
			glyph, colour = "◇", colourWaiting
			clauses = append(clauses, "waiting for "+countOf(len(v.waiting), "check")+" (no worker building)")
		}
	}
	line := t.tint(colour, glyph) + " " + t.strong(v.label()) + "  " + strings.Join(clauses, " · ") + "\n"
	if action != "" {
		line += "  " + action + "\n"
	}
	return line
}

// action is the line under a verdict that says what the person does next.
func (v verdict) action(t terminal) string {
	switch {
	case v.run != "":
		return "run: " + v.run
	case v.see != "":
		return "see: " + v.see
	}
	return v.then
}

// label is the change as a verdict names it: its handle and version, or
// a branch's name.
func (v verdict) label() string {
	if v.version == "" {
		return v.name
	}
	return v.name + " v" + v.version
}

// countOf is n of a thing, as "1 check" or "3 checks".
func countOf(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return strconv.Itoa(n) + " " + thing + "s"
}

// promptLines is review's prompt: the header, its first clause the
// change in bold, and the keys, each with its letter in bold on a
// terminal, left open for the answer.
func promptLines(t terminal, header, keys []string) string {
	if len(header) > 0 {
		header[0] = t.strong(header[0])
	}
	shown := make([]string, len(keys))
	for i, k := range keys {
		if open, rest, ok := strings.Cut(k, "["); ok {
			if letter, after, ok := strings.Cut(rest, "]"); ok {
				k = open + "[" + t.strong(letter) + "]" + after
			}
		}
		shown[i] = k
	}
	return strings.Join(header, " · ") + "\n" + strings.Join(shown, "  ") + ": "
}

// logTail is how many lines of a failed check's log are shown, and
// logKept how many of the last lines of the run's output are kept to find
// them in. attest run prints a failed check's log after every check has
// ended, so the logs are at the end of its output.
const (
	logTail = 20
	logKept = 2000
)

// checking runs command, attest run over the checks named, in order. On a
// terminal one line says which check is building and for how long, and is
// redrawn in place; in a pipe one line says the checks started. The
// command's own output is kept back, unless verbose, and when a check
// fails the end of its log is shown and the refusal follows. On success
// nothing more is printed: approve has not filed yet, and its verdict
// follows. The status is the command's.
func (v verdict) checking(checks, command []string, verbose bool, stdout, stderr io.Writer) int {
	t := terminalOf(stdout)
	began := time.Now()
	of := v.required
	if of < len(checks) {
		of = len(checks)
	}
	done := v.passed
	if done < 0 {
		done = of - len(checks)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	var mu sync.Mutex
	var output []string
	reported, failed := 0, []string(nil)
	line := func() string {
		mu.Lock()
		defer mu.Unlock()
		current := ""
		if reported < len(checks) {
			current = " · " + checks[reported]
		}
		return fmt.Sprintf("%s %s  checking %d/%d%s (%s)…", t.tint(colourWorking, "◐"), t.strong(v.label()),
			min(done+reported+1, of), of, current, time.Since(began).Round(time.Second))
	}
	if verbose || !t.live {
		fmt.Fprintf(stdout, "%s %s  checking %s: %s\n", t.tint(colourWorking, "◐"), t.strong(v.label()),
			countOf(len(checks), "check"), strings.Join(checks, ", "))
	}
	// Both streams are read line by line, kept for a failure's log, and
	// passed through as they come when verbose. A check's result is the
	// line attest run prints on stdout as each check ends.
	var cleanup []func()
	var wg sync.WaitGroup
	scan := func(echo io.Writer) io.Writer {
		r, w := io.Pipe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := bufio.NewScanner(r)
			s.Buffer(make([]byte, 64*1024), 1024*1024)
			for s.Scan() {
				text := s.Text()
				mu.Lock()
				output = append(output, text)
				if len(output) > 2*logKept {
					output = append(output[:0], output[len(output)-logKept:]...)
				}
				if f := strings.Fields(text); len(f) == 4 && f[0] == "check" {
					reported++
					if f[3] != "passed" {
						failed = append(failed, f[1])
					}
				}
				if verbose {
					fmt.Fprintln(echo, text)
				}
				mu.Unlock()
			}
			io.Copy(io.Discard, r)
		}()
		cleanup = append(cleanup, func() { w.Close() })
		return w
	}
	cmd.Stdout, cmd.Stderr = scan(stdout), scan(stderr)
	cleanup = append(cleanup, wg.Wait)
	if t.live && !verbose {
		// The clock in the line moves once a second; what it shows
		// otherwise changes only as the command reports a check.
		stop := make(chan struct{})
		ticked := make(chan struct{})
		go func() {
			defer close(ticked)
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				fmt.Fprint(stdout, "\r\x1b[K"+t.fit(line(), ""))
				select {
				case <-stop:
					return
				case <-tick.C:
				}
			}
		}()
		cleanup = append([]func(){func() { close(stop); <-ticked; fmt.Fprint(stdout, "\r\x1b[K") }}, cleanup...)
	}
	err := cmd.Run()
	for _, f := range cleanup {
		f()
	}
	if err == nil {
		return 0
	}
	status := 1
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() > 0 {
		status = exit.ExitCode()
	}
	if !verbose {
		fmt.Fprint(stderr, failedLogs(output, failed))
	}
	r := v
	r.outcome, r.failed = "refused", failed
	if len(failed) == 0 {
		r.why, r.see = "the checks did not run", "the output above"
		if !verbose {
			r.run = "valley approve -v " + v.name
		}
	}
	fmt.Fprint(stderr, r.lines(terminalOf(stderr)))
	return status
}

// failedLogs is the end of each failed check's log, as attest run prints
// a failed check's log after a "--- <check> ---" line, or the end of the
// whole output when no check's log is in it.
func failedLogs(output, failed []string) string {
	var b strings.Builder
	shown := false
	for _, name := range failed {
		start := -1
		for i, l := range output {
			if l == "--- "+name+" ---" {
				start = i + 1
			}
		}
		if start < 0 {
			continue
		}
		end := start
		for end < len(output) && !strings.HasPrefix(output[end], "--- ") {
			end++
		}
		fmt.Fprintf(&b, "--- %s ---\n", name)
		for _, l := range output[max(start, end-logTail):end] {
			b.WriteString(l + "\n")
		}
		shown = true
	}
	if !shown {
		for _, l := range output[max(0, len(output)-logTail):] {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

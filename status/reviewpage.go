package main

// What review shows of a change: the page, set against the change's
// approved version; the pager it is read in; the notes the reviewer
// inserts into it; and the checks line above the prompt.
//
// The page is set against the change's approved version: the greatest
// version below it whose head a request or an outcome on origin names
// (compare.go). none has no approved version to compare with, so review
// pages the whole diff, under any note compare gives, such as an approved
// version it could not compare. clean has the approved version's file
// delta: review prints that in a few lines, with the landings between the
// two bases, and pages nothing. changed pages the files whose change
// differs, with the landings that edited them, the range-diff when a
// commit was edited, and the shown version's diff of those files. A note
// taken beside the range-diff is unanchored, since its hunks carry no line
// numbers; the last diff is in git's ordinary form, so a note taken there
// keeps its file and line. [v]iew full diff pages the whole diff at every
// prompt. The comparison files no request and skips no check.
//
// The page is a file rather than a pipe, so less's v key has something to
// hand the editor: editing it is how notes are written. Whatever comes
// back with lines inserted is read as notes: no marker, no syntax, no
// mode. An inserted line is a note because it was not there before, and
// it is anchored to the file and the new-side line number of the diff
// line above it, which is the pair a reviewer would otherwise copy out by
// hand. Notes are kept in $XDG_STATE_HOME/valley/notes, since they are
// neither config nor cache, and losing them loses judgment.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// showReview shows a change alone. A change that is not ready says so
// first, before anything else shown of it, and that notice becomes the
// heading, so the change's knots follow it.
func (r *review) showReview(branch string) {
	head := r.changeHead(branch)
	// [a]pprove approves exactly the head shown here (bind).
	if r.c.id == "" {
		r.approveHead = head
	}
	held := notReady(r.c.handle, r.c.version, r.c.state, r.c.owner)
	r.newScratch()
	kind, compared := r.compareShown(r.c.id, r.c.version, head)
	r.showCompared(branch, head, held, r.doesOf(r.c.id), kind, compared, func(w io.Writer) { r.fullDiff(w, branch, head) })
	r.checksClause = r.checksLine(r.c.id, false)
}

// viewFull is [v]iew full diff for a change alone: its whole diff paged,
// and the notes taken in it kept.
func (r *review) viewFull(branch string) {
	head := r.changeHead(branch)
	dir := r.newScratch()
	var b bytes.Buffer
	r.fullDiff(&b, branch, head)
	r.writePage(dir, b.Bytes())
	r.pageBuffer(branch, r.short(head))
}

// compareShown sets the version shown against the change's approved
// version, against main's tip as the fetch left it. It returns none,
// clean or changed, and the text to show. A plain branch is none, and so
// is a comparison that could not be made, with a warning.
func (r *review) compareShown(id, version, head string) (kind, text string) {
	if id == "" {
		return compareNone, ""
	}
	var out bytes.Buffer
	compared := false
	if tip := r.ref("refs/remotes/origin/main"); tip != "" {
		args := []string{"--tip", tip, id, version, head}
		if r.verbose {
			args = append([]string{"-v"}, args...)
		}
		if err := runCompare(r.g, args, &out); err != nil {
			fmt.Fprintf(r.stderr, "valley: %s\n", err)
		} else {
			compared = true
		}
	}
	if !compared {
		fmt.Fprintf(r.stderr, "valley: warning: could not compare v%s of change %s with its approved version; reviewing it in full\n", version, id)
		return compareNone, ""
	}
	kind, text, _ = strings.Cut(out.String(), "\n")
	return kind, text
}

// showCompared shows what compareShown found, under the heading when
// there is one. A clean restack is printed, and nothing is paged. A
// changed version pages the comparison. Otherwise the writer writes the
// whole diff, and it is paged. Notes taken in a page are kept under key,
// against head.
//
// Knots follow the heading when there is one. Otherwise they follow the
// first line of the comparison or the whole diff, which names the change.
func (r *review) showCompared(key, head, heading, knots, kind, compared string, writer func(io.Writer)) {
	sha := r.short(head)
	var body bytes.Buffer
	body.WriteString(compared)
	if kind == compareNone {
		writer(&body)
	}
	var page bytes.Buffer
	if heading != "" {
		page.WriteString(heading + "\n")
		if knots != "" {
			page.WriteString(knots + "\n")
		}
		page.Write(body.Bytes())
	} else if body.Len() > 0 {
		first, rest, _ := strings.Cut(body.String(), "\n")
		page.WriteString(first + "\n")
		if knots != "" {
			page.WriteString(knots + "\n")
		}
		page.WriteString(rest)
	}
	r.mu.Lock()
	dir := r.scratch
	r.mu.Unlock()
	r.writePage(dir, page.Bytes())
	if kind == compareClean {
		f, err := os.Open(filepath.Join(dir, "review.diff"))
		if err == nil {
			runStyle(nil, f, r.stdout)
			f.Close()
		}
		r.dropScratch()
		return
	}
	r.pageBuffer(key, sha)
}

// writePage writes the page to review.diff in dir.
func (r *review) writePage(dir string, page []byte) {
	if err := os.WriteFile(filepath.Join(dir, "review.diff"), page, 0o600); err != nil {
		r.die(fmt.Sprintf("could not write the page: %v", err))
	}
}

// fullDiff writes the whole diff of a change alone: its log and its diff
// against origin/main. main is read by its exact name, since git would
// take a tag named origin/main for it. A main that cannot be read ends the
// review, as a page it cannot write does (gitTo).
func (r *review) fullDiff(w io.Writer, branch, head string) {
	sha := r.short(head)
	tip := r.ref("refs/remotes/origin/main")
	if tip == "" {
		r.exit(1)
	}
	if r.c.head != "" {
		fmt.Fprintf(w, "change %s v%s at %s, on branch %s, log against origin/main:\n\n", r.c.handle, r.c.version, sha, branch)
	} else {
		fmt.Fprintf(w, "branch %s, log against origin/main:\n\n", branch)
	}
	r.commitLog(w, tip+".."+head)
	r.gitTo(w, "--no-pager", "diff", tip+"..."+head)
}

// commitLog writes each commit in the range, newest first: its short hash
// and subject, its body indented four spaces, and a blank line. The whole
// message is shown because a change lands with its head commit's. The
// indent keeps a body line from reading as a diff header, to the notes'
// anchors and to the diff pager alike.
func (r *review) commitLog(w io.Writer, revs string) {
	r.gitTo(w, "--no-pager", "log", "--format=%h %s%n%w(0,4,4)%b", revs)
}

// gitTo runs git with its output written to w. A git that fails ends the
// review with git's status, having said why: the page would be incomplete,
// and no answer is asked about a page the operator could not read whole.
func (r *review) gitTo(w io.Writer, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = r.g.env, w, r.stderr
	processStarted()
	if err := cmd.Run(); err != nil {
		r.exit(exitCode(err))
	}
}

// pageBuffer pages the page written to the scratch directory, keeps the
// notes taken in it under key, against the commit shown, and removes it.
func (r *review) pageBuffer(key, sha string) {
	r.mu.Lock()
	dir := r.scratch
	r.mu.Unlock()
	page := filepath.Join(dir, "review.diff")
	orig := filepath.Join(dir, "review.orig")
	if data, err := os.ReadFile(page); err == nil {
		if err := os.WriteFile(orig, data, 0o600); err != nil {
			r.die(fmt.Sprintf("could not keep the page: %v", err))
		}
	}
	r.page(page)
	r.collectNotes(key, sha, orig, page)
	r.dropScratch()
}

// diffPager is the pager git would use for a diff: pager.diff first, as
// per-command pager configurations set it and leave core.pager alone,
// then git var GIT_PAGER (core.pager, GIT_PAGER, PAGER, less). pager.diff
// may legally be a boolean; true, false and an empty value are taken as
// unset. The value is a shell command line.
func (r *review) diffPager() string {
	pager, _ := r.g.output(false, "config", "--get", "pager.diff")
	switch pager {
	case "", "true", "false":
		pager, _ = r.g.output(false, "var", "GIT_PAGER")
	}
	return pager
}

// pageEdit is what less's v key runs (LESSEDIT): the editor less's %E
// names, VISUAL, then EDITOR, then vi, each a shell command line, on the
// raw page, and then the rendering again, so the view less reopens shows
// the notes in place. Paths reach it through the environment, since
// LESSEDIT is a prompt string in which % ? . : and \ are special.
const pageEdit = `eval "${VISUAL:-${EDITOR:-vi}} \"\$@\" \"\$VALLEY_PAGE_RAW\""
      sh -c "$VALLEY_PAGE_RENDER" < "$VALLEY_PAGE_RAW" > "$VALLEY_PAGE_SHOWN"`

// page shows file to the operator. Off a terminal it is printed.
//
// The configured diff pager is the renderer, run once as a filter into a
// sibling file that less pages, with style (style.go) after it, so the
// pager never reads style's escapes and the raw page has none. v runs
// pageEdit. What this costs is the line number: v opens the editor at
// +%lm, a line of the rendered view, so a filter that restructures the
// diff, as delta's default rendering does, opens it near the cursor
// rather than on it. A structure-preserving filter (pager.diff = `delta
// --color-only`) makes it exact. No anchor depends on it: a note is
// anchored by where it was inserted, not by where the editor opened.
//
// A pager that is less already keeps its options, and style alone renders.
// Without less there is no editor key, and the pager reads the page
// through a pipe.
func (r *review) page(file string) {
	if _, tty := windowColumns(os.Stdout); !tty {
		if data, err := os.ReadFile(file); err == nil {
			r.stdout.Write(data)
		}
		return
	}
	shown := strings.TrimSuffix(file, ".diff") + ".shown"
	pager, view := r.diffPager(), "less"
	exe, exeErr := os.Executable()
	first := pager
	if i := strings.IndexAny(pager, " \t\n\v\f\r"); i >= 0 {
		first = pager[:i]
	}
	if first == "less" || strings.HasSuffix(first, "/less") {
		if exeErr != nil {
			r.shell(pager+` "$1"`, nil, nil, nil, file)
			return
		}
		view, pager = pager, "cat"
	}
	if _, err := exec.LookPath("less"); err != nil {
		in, err := os.Open(file)
		if err == nil {
			r.shell(pager, in, nil, nil)
			in.Close()
		}
		return
	}
	if exeErr == nil {
		pager += " | " + shellQuote(exe) + " style --paged"
	}
	in, err := os.Open(file)
	if err != nil {
		return
	}
	out, err := os.Create(shown)
	if err == nil {
		r.shell(pager, in, out, nil)
		out.Close()
	}
	in.Close()
	// -R because the filter's colour is the whole point of running it. No
	// LESSOPEN, since the filter has run and less answers v on a file read
	// through one with a warning, then runs the editor on whatever key comes
	// next, q included; and so no LESSCLOSE.
	env := []string{}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "LESSOPEN", "LESSCLOSE", "LESSEDIT", "VALLEY_PAGE_RAW", "VALLEY_PAGE_SHOWN", "VALLEY_PAGE_RENDER", "VALLEY_PAGE_EDIT":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "VALLEY_PAGE_RAW="+file, "VALLEY_PAGE_SHOWN="+shown, "VALLEY_PAGE_RENDER="+pager,
		"VALLEY_PAGE_EDIT="+pageEdit, `LESSEDIT=sh -c "$VALLEY_PAGE_EDIT" sh ?lm+%lm.`)
	r.shell(view+` -R -- "$1"`, nil, nil, env, shown)
}

// shell runs a shell command line in the foreground, with the arguments
// given as $1 and on, stdin and stdout the terminal's unless given.
func (r *review) shell(line string, stdin io.Reader, stdout io.Writer, env []string, args ...string) {
	cmd := exec.Command("sh", append([]string{"-c", line, "sh"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, r.stdout, r.stderr
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.Env = env
	r.inForeground(func() { _ = cmd.Run() })
}

// anchor is where a line of the page sits: the file it belongs to, and
// the new-side line number. No path is above every file (the log); line 0
// is a file header, above every hunk.
type anchor struct {
	path string
	line int
}

// diffHeader is a diff's file header, its b/ side the name a rename will
// have.
var diffHeader = regexp.MustCompile(`^diff --git a/(.*) b/(.*)$`)

// pageLines are the lines of data that end in a newline.
func pageLines(data []byte) []string {
	s := string(data)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.Split(s[:i], "\n")
	}
	return nil
}

// leadingNumber is the number s starts with, or 0.
func leadingNumber(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

// anchors is the anchor of every line of the page, by line, built in one
// pass so that anchoring a note is a lookup.
func anchors(page []byte) []anchor {
	var all []anchor
	path, at, next, inHunk := "", 0, 0, false
	for _, line := range pageLines(page) {
		// Header lines are only headers outside a hunk: inside one, every
		// line carries a +/-/space prefix, so a diff of a diff cannot be
		// misread.
		if inHunk {
			switch {
			case line == "" || line[0] == ' ' || line[0] == '+':
				at = next // this line's own new-side number
				next++
			case line[0] == '-' || line[0] == '\\':
				// a removal or the no-newline marker: no new-side line
			default:
				inHunk = false
			}
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(line, "diff --git "):
				path = ""
				if m := diffHeader.FindStringSubmatch(line); m != nil {
					path = m[2]
				}
				at = 0
			case strings.HasPrefix(line, "@@ "):
				_, rest, _ := strings.Cut(line, "+") // -12,7 +34,9 @@ -> 34,9 @@
				at = leadingNumber(rest)
				next, inHunk = at, true
			}
		}
		all = append(all, anchor{path, at})
	}
	return all
}

// anchorOf is the anchor of a note inserted after page line n, as it is
// written down.
func anchorOf(all []anchor, n int) string {
	if n < 1 || n > len(all) || all[n-1].path == "" {
		return "(unanchored)"
	}
	if a := all[n-1]; a.line != 0 {
		return fmt.Sprintf("%s:%d", a.path, a.line)
	}
	return all[n-1].path
}

// notesIn is the notes in an edited page, one `anchor: text` line each.
// git says where the reviewer's lines landed: a hunk that removes nothing
// is an insertion, and the line it was inserted after is the anchor. A
// hunk that removes something is an edit of the diff itself, which
// anchors to nothing, since the line it would name is the line it
// changed, so it is dropped with a count.
func (r *review) notesIn(orig, edited string) []string {
	page, _ := os.ReadFile(orig)
	all := anchors(page)
	cmd := exec.Command("git", "--no-pager", "diff", "--no-ext-diff", "--no-index", "-U0", "--", orig, edited)
	cmd.Env, cmd.Stderr = r.g.env, r.stderr
	processStarted()
	out, _ := cmd.Output()
	var notes []string
	at, insert, touched := 0, false, 0
	for _, line := range pageLines(out) {
		switch {
		case strings.HasPrefix(line, "@@ "):
			rest := strings.TrimPrefix(line, "@@ -") // 12,0 +13,2 @@
			at = leadingNumber(rest)
			rest = strings.TrimLeft(rest, "0123456789")
			old := 1
			if strings.HasPrefix(rest, ",") {
				old = leadingNumber(rest[1:])
			}
			insert = old == 0
			if !insert {
				touched += old
			}
		case strings.HasPrefix(line, "+"):
			// Only inside an insertion hunk: the +++ of git's own header is
			// not a note, and neither is the new side of an edit.
			if !insert {
				continue
			}
			text := strings.TrimLeft(line[1:], " \t\n\v\f\r")
			if text == "" { // a blank line says nothing
				continue
			}
			notes = append(notes, anchorOf(all, at)+": "+text)
		}
	}
	if touched != 0 {
		fmt.Fprintf(r.stderr, "valley: warning: %d changed or deleted diff line(s) ignored — a note is a line you insert\n", touched)
	}
	return notes
}

// notesFile is where the notes taken under key live.
func notesFile(key string) string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = os.Getenv("HOME") + "/.local/state"
	}
	return state + "/valley/notes/" + key + ".md"
}

// collectNotes reads the notes out of the paged page and keeps them:
// appended to the notes file under key, under the commit they were taken
// against, and held for the print that follows the verdict. A notes file
// that cannot be written is a warning, never a failure: the notes are
// still printed, and a review is not undone by a missing directory.
func (r *review) collectNotes(key, sha, orig, edited string) {
	found := r.notesIn(orig, edited)
	if len(found) == 0 {
		return
	}
	r.notes = append(r.notes, found...)
	file := notesFile(key)
	text := fmt.Sprintf("## %s at %s\n\n%s\n\n", key, sha, strings.Join(found, "\n"))
	if os.MkdirAll(filepath.Dir(file), 0o777) == nil {
		if f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); err == nil {
			_, err = f.WriteString(text)
			if f.Close() == nil && err == nil {
				return
			}
		}
	}
	fmt.Fprintf(r.stderr, "valley: warning: could not write %s — the notes are printed only\n", file)
}

// printNotes prints the notes taken under key, after the verdict.
func (r *review) printNotes(key string, notes []string) {
	if len(notes) == 0 {
		return
	}
	fmt.Fprintf(r.stdout, "\n%d note(s) on %s, appended to %s:\n", len(notes), key, notesFile(key))
	for _, n := range notes {
		fmt.Fprintf(r.stdout, "  %s\n", n)
	}
}

// readDoes reads the plan graph once for every change in the review, for
// the knots each does, and starts reading their checks. A missing or
// unreadable graph shows no knots; -v warns on stderr.
func (r *review) readDoes() {
	var ids []string
	if len(r.layers) > 1 {
		for _, l := range r.layers {
			if l.id != "" {
				ids = append(ids, l.id)
			}
		}
	} else if r.c.id != "" {
		ids = []string{r.c.id}
	}
	if len(ids) == 0 {
		return
	}
	r.does = readDoers(r.g, project(r.g), r.verbose, r.stderr)
	r.startChecksRead(&checksRead{ids: ids, verbose: r.verbose})
}

// doesOf is the `does ...` lines of one change.
func (r *review) doesOf(id string) string {
	if id == "" {
		return ""
	}
	var lines []string
	for _, k := range r.does[id] {
		lines = append(lines, k.line())
	}
	return strings.Join(lines, "\n")
}

// checksRead is the read of the changes' checks lines, which takes
// seconds: it reads the policy and the host's telemetry. It runs this
// program's checkline (versionchecks.go) in the background, in a process
// group of its own, so that a Ctrl-C the operator gives the pager does not
// reach it, while the page is shown, and the line is printed just before
// the prompt when the read has finished. It never waits for the read,
// unless VALLEY_REVIEW_CHECKS_WAIT gives the seconds to wait for it.
type checksRead struct {
	ids      []string
	verbose  bool // pass on what the read said on stderr
	cmd      *exec.Cmd
	out, err bytes.Buffer
	done     chan struct{}
	shown    map[string]bool // the changes whose line is not shown again
}

// startChecksRead starts the read c names, in place of any read started.
func (r *review) startChecksRead(c *checksRead) {
	r.mu.Lock()
	old := r.checks
	r.checks = nil
	r.mu.Unlock()
	old.stop()
	c = &checksRead{ids: c.ids, verbose: c.verbose, done: make(chan struct{}), shown: map[string]bool{}}
	exe, err := os.Executable()
	if err != nil {
		close(c.done)
	} else {
		c.cmd = exec.Command(exe, append([]string{"checkline"}, c.ids...)...)
		c.cmd.Stdout, c.cmd.Stderr = &c.out, &c.err
		c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		processStarted()
		if c.cmd.Start() != nil {
			c.cmd = nil
			close(c.done)
		} else {
			go func() {
				_ = c.cmd.Wait()
				close(c.done)
			}()
		}
	}
	r.mu.Lock()
	exiting := r.exiting
	if !exiting {
		r.checks = c
	}
	r.mu.Unlock()
	if exiting {
		c.stop()
	}
}

// restartChecksRead reads the checks again, as for heads that moved, when
// a read was started.
func (r *review) restartChecksRead() {
	r.mu.Lock()
	c := r.checks
	r.mu.Unlock()
	if c != nil {
		r.startChecksRead(c)
	}
}

// stop stops the read, if it still runs, with everything it started.
func (c *checksRead) stop() {
	if c == nil || c.cmd == nil {
		return
	}
	select {
	case <-c.done:
	default:
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
	}
}

// checksLine is the `checks: …` line of one change, if the read has
// finished and gave it one, or "". mark says it is not shown again.
func (r *review) checksLine(id string, mark bool) string {
	r.mu.Lock()
	c := r.checks
	r.mu.Unlock()
	if c == nil || id == "" || c.shown[id] {
		return ""
	}
	// Normally the read has had the whole time the page was read in. A host
	// that wants the line at the prompt can have review wait for it.
	wait := os.Getenv("VALLEY_REVIEW_CHECKS_WAIT")
	seconds, err := strconv.Atoi(wait)
	if err != nil || strings.Trim(wait, "0123456789") != "" {
		seconds = 0
	}
	select {
	case <-c.done:
	default:
		select {
		case <-c.done:
		case <-time.After(time.Duration(seconds) * time.Second):
			return ""
		}
	}
	if c.verbose && c.err.Len() > 0 {
		r.stderr.Write(c.err.Bytes())
		c.err.Reset()
	}
	var found []string
	for _, line := range pageLines(c.out.Bytes()) {
		if changeID, text, _ := strings.Cut(line, "\t"); changeID == id {
			found = append(found, text)
			if mark {
				c.shown[id] = true
			}
		}
	}
	return strings.Join(found, "\n")
}

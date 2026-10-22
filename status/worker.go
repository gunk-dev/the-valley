package main

// work is one pass of a check worker. It builds the nix checks that ready
// changes require before anyone approves them, and publishes the evidence
// on origin, where approve (bin/valley) reuses it instead of building it.
// A timer runs it; it keeps no queue, lease or schedule of its own.
//
// A pass reads each project named with --repo, and works through the
// project's ready changes:
//
//  1. It lists the ready changes whose active version has not landed, as
//     valley pending does (pending.go), and pins each one's active head.
//  2. It derives the checks the head requires, as valley-go checks does
//     (checks.go), over the head's merge base with main. Both policy
//     layers are read at main's tip on origin, as approve reads them,
//     never from the change.
//  3. It asks coverage (coverage.go) which of the head's nix checks origin
//     already holds evidence for. Command checks are left to approve.
//  4. It runs the rest in one batch, with attest run --rev <head> under
//     the worker's key, and pushes the one attestation ref attest stored.
//     It pushes nothing else.
//
// The evidence is about the head's tree, so it stays true if the change
// moves on while the batch runs. The pass does not read the change's
// record again before it publishes.
//
// The attestation namespace admits creations only, so the worker never
// builds what it could not publish. When origin already holds this
// worker's ref for a tree and the ref lacks a required check, the pass
// reports it and builds nothing. approve builds the rest under the
// operator's key (bd-7b7d58f). When the worker's ref is stored here but
// not on origin, an earlier pass built it and its push was lost, so the
// pass pushes that same ref again rather than building it again.
//
// A batch that fails is not run again at once. The pass records it in
// failures.json in the state directory, and runs it again only after a
// wait that starts at firstBackoff and doubles with each failure in a row,
// up to longestBackoff. A batch is the project, the tree and the checks it
// runs, so a new version is a new batch and runs at once. VALLEY_NOW, when
// set, is the time a pass reads as now (main.go's clock). Deleting the
// record lets every batch run again at once.
//
// Passes that share a state directory take turns: each holds the lock
// file there while it runs, and a pass that finds it held waits for it.
//
// A read of origin that fails stops the pass, because a read that did not
// happen must not read as no changes or no evidence. Anything else that
// goes wrong with one change is reported, and the pass goes on to the
// next. The pass exits 1 when anything went wrong.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"the-valley/change"
	"the-valley/composed"
)

const workUsage = "usage: valley-go work --repo <name>... --key <file> [--name <name>] [--state <dir>]"

// projectLayer is the project's own policy layer, in main's tree.
const projectLayer = "policy/project"

// The wait after a batch fails before it runs again: the first, and the
// longest it grows to by doubling.
const (
	firstBackoff   = 15 * time.Minute
	longestBackoff = 24 * time.Hour
)

// worker is one pass: the signer it attests as, what it says, and what it
// has found going wrong.
type worker struct {
	key, name string
	keyhash   string // the key hash an attestation ref of this signer ends in
	now       time.Time
	failures  *failureRecord
	stdout    io.Writer
	stderr    io.Writer
	troubles  int // things that went wrong that did not stop the pass
}

func runWork(g git, args []string, stdout, stderr io.Writer) error {
	var repos []string
	var key, name, state string
	for i := 0; i < len(args); i++ {
		opt, value, inline := strings.Cut(args[i], "=")
		if opt != "--repo" && opt != "--key" && opt != "--name" && opt != "--state" {
			return failure(workUsage)
		}
		if !inline {
			if i+1 == len(args) {
				return failure(workUsage)
			}
			i++
			value = args[i]
		}
		switch opt {
		case "--repo":
			if !change.ValidPrincipal(value) {
				return failure(fmt.Sprintf("--repo %s: not a project name", value))
			}
			repos = append(repos, value)
		case "--key":
			key = value
		case "--name":
			name = value
		case "--state":
			state = value
		}
	}
	if len(repos) == 0 || key == "" {
		return failure(workUsage)
	}
	// attest runs in the repository a project is read through, which may
	// be a cache clone, so a key named relative to here is made absolute.
	abs, err := filepath.Abs(key)
	if err != nil {
		return failure(fmt.Sprintf("--key %s: %v", key, err))
	}
	key = abs
	now := clock()
	if state == "" {
		if state, err = stateDirectory(); err != nil {
			return failure("no state directory: name one with --state")
		}
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		return failure(fmt.Sprintf("could not make the state directory %s", state))
	}

	unlock, err := lockState(filepath.Join(state, "lock"), stderr)
	if err != nil {
		return failure(fmt.Sprintf("could not take the lock in %s: %v", state, err))
	}
	defer unlock()

	// The signer is read before anything else is done, so a pass that
	// could only end in "no key" says so before it reads anything.
	keyArgs := []string{"key", "--key", key}
	if name != "" {
		keyArgs = append(keyArgs, "--name", name)
	}
	out, err := attestTool(g, keyArgs...)
	fields := strings.SplitN(strings.TrimSpace(out), "+", 3)
	if err != nil || len(fields) != 3 || len(fields[1]) != 8 {
		return failure(fmt.Sprintf("could not read the signing key at %s: %s", key, refusalOf(out)))
	}

	w := &worker{key: key, name: name, keyhash: fields[1], now: now, stdout: stdout, stderr: stderr}
	w.failures = readFailures(filepath.Join(state, "failures.json"), stderr)
	seen := map[string]bool{}
	for _, repo := range repos {
		if err := w.project(g, repo, seen); err != nil {
			return err
		}
	}
	// Only a whole pass knows which batches are still waiting to be built.
	// The rest are forgotten, so the record stays as small as the work.
	w.failures.keep(seen)
	if err := w.failures.save(); err != nil {
		fmt.Fprintf(stderr, "valley: could not write %s: %v\n", w.failures.path, err)
		w.troubles++
	}
	if w.troubles > 0 {
		return failure(fmt.Sprintf("the pass finished, but %s went wrong in it; each is said above", counted(w.troubles, "thing")))
	}
	return nil
}

// stateDirectory is where the worker keeps its lock and its record of
// failed batches by default: $XDG_STATE_HOME/valley/worker, or
// ~/.local/state/valley/worker, beside attest's timing log.
func stateDirectory() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "valley", "worker"), nil
}

// lockState takes the lock file at path, waiting for whoever holds it, and
// returns what releases it. The lock is flock's, as self-update's is, so
// it is released when the process ends, however it ends.
func lockState(path string, stderr io.Writer) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		fmt.Fprintf(stderr, "valley: another pass holds %s; waiting for it to end\n", path)
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil // close drops the flock
}

// project works through one project's ready changes. It returns an error
// only when a read of origin failed, which stops the pass.
func (w *worker) project(g git, name string, seen map[string]bool) error {
	list, _, err := sources(g, listing{repo: name}, w.stderr)
	if err != nil {
		return failure(fmt.Sprintf("%s: %v; the pass stops here", name, err))
	}
	repos := readSources(list, newVerifier())
	r := repos[0]
	if r.err != nil {
		return failure(fmt.Sprintf("%s: %v; the pass stops here", name, r.err))
	}
	view := r.pendingView(handles(repos))
	if len(view.Changes) == 0 {
		return nil
	}
	if r.snap.tip == "" {
		fmt.Fprintf(w.stdout, "%s: origin has no main to read the policy at, so nothing is built\n", name)
		w.troubles++
		return nil
	}
	instance, project, done, err := policyLayers(r.g, r.snap.tip)
	defer done()
	if err != nil {
		fmt.Fprintf(w.stdout, "%s: the policy at main did not read, so nothing is built: %v\n", name, err)
		w.troubles++
		return nil
	}
	for _, c := range view.Changes {
		if err := w.change(r, instance, project, c, seen); err != nil {
			return err
		}
	}
	return nil
}

// change builds and publishes what one ready change's head lacks.
func (w *worker) change(r listed, instance, project string, c jsonChange, seen map[string]bool) error {
	v := c.Versions[c.Active-1]
	head := v.Head
	say := func(format string, args ...any) {
		fmt.Fprintf(w.stdout, "%s: %s v%d at %s: %s\n", r.name, c.Handle, v.N, head[:min(7, len(head))], fmt.Sprintf(format, args...))
	}
	base, err := r.g.output(true, "merge-base", r.snap.tip, head)
	if err != nil {
		say("shares no history with main, so nothing is built")
		w.troubles++
		return nil
	}
	doc, err := derivedChecks(r.g, "", instance, project, base, head)
	if err != nil {
		say("its checks did not derive, so nothing is built: %v", err)
		w.troubles++
		return nil
	}
	var asked []nixCheck
	for _, q := range doc.Required {
		if q.Runner == "nix" {
			asked = append(asked, nixCheck{q.Name, q.Attribute})
		}
	}
	if len(asked) == 0 {
		say("requires no nix check")
		return nil
	}

	e, err := coverageOf(r.g, head, asked, w.stderr)
	if err != nil {
		return failure(fmt.Sprintf("%s: %s v%d: %v; the pass stops here", r.name, c.Handle, v.N, err))
	}
	var missing []nixCheck
	var names []string
	for _, q := range asked {
		if _, ok := e.covered[q.name]; !ok {
			missing = append(missing, q)
			names = append(names, q.name)
		}
	}
	if len(missing) == 0 {
		say("origin holds evidence for every nix check it requires")
		return nil
	}

	own := attestationRefs + e.digest + "/" + w.keyhash
	if _, ok := e.listed[own]; ok {
		say("origin already holds %s, this worker's evidence for its tree, and it does not cover %s. "+
			"That namespace admits creations only, so approve builds the rest (bd-7b7d58f)", own, strings.Join(names, ", "))
		return nil
	}
	if stored, err := r.g.output(true, "rev-parse", "--verify", "--quiet", own); err == nil && stored != "" {
		say("publishing %s, which an earlier pass stored and origin does not hold", own)
		w.publish(r.g, own, say)
		return nil
	}

	batch := r.name + " " + e.digest + " " + strings.Join(names, ",")
	seen[batch] = true
	if f, waiting := w.failures.waiting(batch, w.now); waiting {
		say("its batch %s failed %s in a row, so it is not built again before %s", strings.Join(names, ", "), counted(f.Failures, "time"), f.Retry.Format(time.RFC3339))
		return nil
	}
	say("building %s", strings.Join(names, ", "))
	if err := w.attest(r.g, head, missing); err != nil {
		f := w.failures.failed(batch, w.now)
		say("the batch did not pass (%v); it is not built again before %s", err, f.Retry.Format(time.RFC3339))
		w.troubles++
		if err := w.failures.save(); err != nil {
			fmt.Fprintf(w.stderr, "valley: could not write %s: %v\n", w.failures.path, err)
		}
		return nil
	}
	w.failures.passed(batch)
	w.publish(r.g, own, say)
	return nil
}

// attest runs one batch with attest run, which stores the notes at the
// worker's attestation ref for the head's tree and publishes nothing.
// What attest prints is passed on.
func (w *worker) attest(g git, head string, checks []nixCheck) error {
	repo := g.dir
	if repo == "" {
		repo = "."
	}
	args := []string{"run", "--repo", repo, "--rev", head, "--key", w.key}
	if w.name != "" {
		args = append(args, "--name", w.name)
	}
	for _, q := range checks {
		args = append(args, "--check", q.name+"="+q.attribute)
	}
	cmd := exec.Command("attest", args...)
	cmd.Env = g.env
	cmd.Dir = g.dir
	cmd.Stdout, cmd.Stderr = w.stdout, w.stderr
	return cmd.Run()
}

// publish pushes one attestation ref to origin, and nothing else. The hook
// verifies the notes against the tree and the signer whoever pushes them.
// A push that fails leaves the ref stored here for the next pass.
func (w *worker) publish(g git, ref string, say func(string, ...any)) {
	if _, err := g.output(false, "push", "--quiet", "origin", ref+":"+ref); err != nil {
		say("could not publish %s; the next pass publishes it again", ref)
		w.troubles++
		return
	}
	say("published %s", ref)
}

// policyLayers resolves the two policy layers a project's changes are
// derived under, as approve resolves them (bin/valley's
// resolve_instance_layer and resolve_project_layer), and returns what
// removes them. Both are read at main's tip on origin. The floor is that
// tip's own policy/instance when it carries one, and otherwise
// policy/instance at the tip of main in the owning valley that
// policy/valley names. A tip with no policy/project is derived under the
// floor alone.
func policyLayers(g git, tip string) (instance, project string, done func(), err error) {
	ctx := context.Background()
	var ends []func()
	done = func() {
		for _, end := range ends {
			end()
		}
	}
	repo := g.dir
	if repo == "" {
		repo = "."
	}
	floor := composed.Floor{Repo: repo, Ref: tip, Dir: instanceLayer}
	if !g.succeeds(true, "cat-file", "-e", tip+":"+instanceLayer) {
		pointer, err := g.output(true, "cat-file", "blob", tip+":"+valleyPointer)
		if err != nil {
			return "", "", done, fmt.Errorf("main names no owning valley at %s", valleyPointer)
		}
		url := pointerURL(pointer)
		if url == "" {
			return "", "", done, fmt.Errorf("%s on main names no repository", valleyPointer)
		}
		if err := checkURL(url); err != nil {
			return "", "", done, err
		}
		scratch, err := os.MkdirTemp("", "valley-work-valley-")
		if err != nil {
			return "", "", done, err
		}
		ends = append(ends, func() { _ = os.RemoveAll(scratch) })
		v := git{env: cacheEnvironment(g.env), stderr: g.stderr, dir: scratch}
		if _, err := v.output(true, "init", "--quiet", "--bare"); err != nil {
			return "", "", done, errors.New("no scratch repository to fetch the floor into")
		}
		if _, err := v.output(true, "fetch", "--quiet", "--depth", "1", "--no-tags", "--end-of-options", url, "refs/heads/main"); err != nil {
			return "", "", done, fmt.Errorf("could not fetch the owning valley %s", url)
		}
		floor = composed.Floor{Repo: scratch, Ref: "FETCH_HEAD", Dir: instanceLayer}
	}
	instance, end, err := floor.Open(ctx, "valley-work-floor")
	ends = append(ends, end)
	if err != nil {
		return "", "", done, err
	}
	project, _, end, err = composed.Project(ctx, repo, tip, projectLayer, "valley-work-project")
	ends = append(ends, end)
	return instance, project, done, err
}

// failureRecord is the worker's record of the batches that failed: for
// each, how many times in a row, and the time before which it is not
// built again. It is kept as JSON at path.
type failureRecord struct {
	path    string
	Batches map[string]failedBatch `json:"batches"`
}

type failedBatch struct {
	Failures int       `json:"failures"`
	Retry    time.Time `json:"retry"`
}

// readFailures reads the record at path. A record that is not there is
// empty. One that does not read is said, and started again empty: the
// worst it costs is one more run of each batch it held.
func readFailures(path string, stderr io.Writer) *failureRecord {
	r := &failureRecord{path: path, Batches: map[string]failedBatch{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r
	}
	if err == nil {
		err = json.Unmarshal(data, r)
	}
	if err != nil || r.Batches == nil {
		fmt.Fprintf(stderr, "valley: %s does not read, so every batch it held runs again\n", path)
		r.Batches = map[string]failedBatch{}
	}
	return r
}

// waiting reports whether a batch failed and its wait is not over.
func (r *failureRecord) waiting(batch string, now time.Time) (failedBatch, bool) {
	f, ok := r.Batches[batch]
	return f, ok && now.Before(f.Retry)
}

// failed records one more failure of a batch in a row, and the time it
// may run again.
func (r *failureRecord) failed(batch string, now time.Time) failedBatch {
	f := r.Batches[batch]
	f.Failures++
	wait := firstBackoff
	for i := 1; i < f.Failures && wait < longestBackoff; i++ {
		wait *= 2
	}
	f.Retry = now.Add(min(wait, longestBackoff))
	r.Batches[batch] = f
	return f
}

func (r *failureRecord) passed(batch string) { delete(r.Batches, batch) }

// keep forgets every batch not in seen.
func (r *failureRecord) keep(seen map[string]bool) {
	for batch := range r.Batches {
		if !seen[batch] {
			delete(r.Batches, batch)
		}
	}
}

// save writes the record whole, through a file renamed into place, so a
// pass that is killed leaves the last record or the new one.
func (r *failureRecord) save() error {
	if len(r.Batches) == 0 {
		if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(r, "", "  ") // in key order
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".failures-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(data, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), r.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

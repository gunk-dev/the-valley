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
// A batch that fails is not run again at once, and neither is a push that
// fails. The pass records each failure in failures.json in the state
// directory, under its project: a batch by the tree and the checks it
// runs, and a push by the object it publishes. A failed batch waits
// firstBuildWait before it runs again, and a failed push waits
// firstPushWait. Each wait doubles with every failure in a row, up to
// longestWait. The clock is read when the pass asks whether a wait is
// over, and again when an attempt ends, so a long build does not use up
// its own wait. A new version is a new batch, and runs at once. VALLEY_NOW,
// when set, is the time the pass reads as now (main.go's clock). Deleting
// the record lets everything run again at once.
//
// A pass forgets the failures it did not meet in a project, but only for
// a project it read whole: every ready change's checks derived and its
// evidence read. A pass over other projects, or one that could not read
// this one, leaves this one's record as it is.
//
// Passes that share a state directory take turns: each holds the lock
// file there while it runs, and a pass that finds it held waits for it.
// attest is handed the lock too, so it stays held while a batch runs even
// when the pass that started the batch is killed.
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

// The waits after a failure before the same thing is tried again: the
// first after a failed batch, the first after a failed push, and the
// longest either grows to by doubling.
const (
	firstBuildWait = 15 * time.Minute
	firstPushWait  = time.Minute
	longestWait    = 24 * time.Hour
)

// worker is one pass: the signer it attests as, the lock it holds, what it
// says, and what it has found going wrong.
type worker struct {
	key, name string
	keyhash   string   // the key hash an attestation ref of this signer ends in
	lock      *os.File // the state directory's lock, held for the pass
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
	if state == "" {
		if state, err = stateDirectory(); err != nil {
			return failure("no state directory: name one with --state")
		}
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		return failure(fmt.Sprintf("could not make the state directory %s", state))
	}

	lock, err := lockState(filepath.Join(state, "lock"), stderr)
	if err != nil {
		return failure(fmt.Sprintf("could not take the lock in %s: %v", state, err))
	}
	defer lock.Close() // close drops the flock; every attest handed it has ended by then

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

	w := &worker{key: key, name: name, keyhash: fields[1], lock: lock, stdout: stdout, stderr: stderr}
	w.failures = readFailures(filepath.Join(state, "failures.json"), stderr)
	for _, repo := range repos {
		if err := w.project(g, repo); err != nil {
			return err
		}
	}
	w.save()
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
// returns the open file, whose closing releases it. The lock is flock's,
// as self-update's is. It belongs to the open file, so it is released when
// every process holding that file has closed it or ended, however each
// ends.
func lockState(path string, stderr io.Writer) (*os.File, error) {
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
	return f, nil
}

// project works through one project's ready changes. It returns an error
// only when a read of origin failed, which stops the pass. When it has
// read the project whole, it forgets the project's failures it did not
// meet.
func (w *worker) project(g git, name string) error {
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
	met := map[string]bool{} // the failures this pass met, by key
	if len(view.Changes) == 0 {
		w.failures.keep(name, met)
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
	whole := true
	for _, c := range view.Changes {
		read, err := w.change(r, instance, project, c, met)
		if err != nil {
			return err
		}
		whole = whole && read
	}
	if whole {
		w.failures.keep(name, met)
	}
	return nil
}

// change builds and publishes what one ready change's head lacks. read
// says whether it read the change through: derived its checks, and read
// the evidence origin holds for them.
func (w *worker) change(r listed, instance, project string, c jsonChange, met map[string]bool) (read bool, err error) {
	v := c.Versions[c.Active-1]
	head := v.Head
	say := func(format string, args ...any) {
		fmt.Fprintf(w.stdout, "%s: %s v%d at %s: %s\n", r.name, c.Handle, v.N, head[:min(7, len(head))], fmt.Sprintf(format, args...))
	}
	base, err := r.g.output(true, "merge-base", r.snap.tip, head)
	if err != nil {
		say("shares no history with main, so nothing is built")
		w.troubles++
		return false, nil
	}
	doc, err := derivedChecks(r.g, "", instance, project, base, head)
	if err != nil {
		say("its checks did not derive, so nothing is built: %v", err)
		w.troubles++
		return false, nil
	}
	var asked []nixCheck
	for _, q := range doc.Required {
		if q.Runner == "nix" {
			asked = append(asked, nixCheck{q.Name, q.Attribute})
		}
	}
	if len(asked) == 0 {
		say("requires no nix check")
		return true, nil
	}

	e, err := coverageOf(r.g, head, asked, w.stderr)
	if err != nil {
		return false, failure(fmt.Sprintf("%s: %s v%d: %v; the pass stops here", r.name, c.Handle, v.N, err))
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
		return true, nil
	}

	own := attestationRefs + e.digest + "/" + w.keyhash
	if _, ok := e.listed[own]; ok {
		say("origin already holds %s, this worker's evidence for its tree, and it does not cover %s. "+
			"That namespace admits creations only, so approve builds the rest (bd-7b7d58f)", own, strings.Join(names, ", "))
		return true, nil
	}
	if stored, found, err := r.g.ref(own); err == nil && found {
		say("an earlier pass stored %s, and origin does not hold it", own)
		w.publish(r, own, stored, met, say)
		return true, nil
	}

	batch := "build " + e.digest + " " + strings.Join(names, ",")
	met[batch] = true
	if f, waiting := w.failures.waiting(r.name, batch, clock()); waiting {
		say("its batch %s failed %s in a row, so it is not built again before %s", strings.Join(names, ", "), counted(f.Failures, "time"), f.Retry.Format(time.RFC3339))
		return true, nil
	}
	say("building %s", strings.Join(names, ", "))
	if err := w.attest(r.g, head, missing); err != nil {
		// The wait runs from when the batch ended, not from when it began.
		f := w.failures.failed(r.name, batch, clock(), firstBuildWait)
		say("the batch did not pass (%v); it is not built again before %s", err, f.Retry.Format(time.RFC3339))
		w.troubles++
		w.save()
		return true, nil
	}
	w.failures.passed(r.name, batch)
	stored, found, err := r.g.ref(own)
	if err != nil || !found {
		say("attest stored no %s, so nothing is published", own)
		w.troubles++
		return true, nil
	}
	w.publish(r, own, stored, met, say)
	return true, nil
}

// attest runs one batch with attest run, which stores the notes at the
// worker's attestation ref for the head's tree and publishes nothing.
// What attest prints is passed on.
//
// attest is handed the lock file as well, and holds it until it ends. The
// flock belongs to the open file, so the lock stays held while attest
// runs, and while anything it started that inherited the file runs, even
// when this pass is killed. A later pass waits for all of it, and then
// finds the ref attest stored.
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
	cmd.ExtraFiles = []*os.File{w.lock}
	return cmd.Run()
}

// publish pushes one attestation ref to origin, and nothing else. The hook
// verifies the notes against the tree and the signer whoever pushes them.
// A push that fails leaves the ref stored here, and the same object is not
// pushed again until its wait is over: by any pass, and for any change
// whose tree it is about.
func (w *worker) publish(r listed, ref, object string, met map[string]bool, say func(string, ...any)) {
	key := "push " + object
	met[key] = true
	if f, waiting := w.failures.waiting(r.name, key, clock()); waiting {
		say("publishing %s failed %s in a row, so it is not pushed again before %s", ref, counted(f.Failures, "time"), f.Retry.Format(time.RFC3339))
		return
	}
	if _, err := r.g.output(false, "push", "--quiet", "origin", object+":"+ref); err != nil {
		f := w.failures.failed(r.name, key, clock(), firstPushWait)
		say("could not publish %s; it is not pushed again before %s", ref, f.Retry.Format(time.RFC3339))
		w.troubles++
		w.save()
		return
	}
	w.failures.passed(r.name, key)
	say("published %s", ref)
}

// save writes the failure record, and counts a write that fails as
// something that went wrong.
func (w *worker) save() {
	if err := w.failures.save(); err != nil {
		fmt.Fprintf(w.stderr, "valley: could not write %s: %v\n", w.failures.path, err)
		w.troubles++
	}
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

// failureRecord is the worker's record of what failed, project by
// project: each batch, keyed "build <tree digest> <check>,<check>", and
// each push, keyed "push <object ID>". For each it holds how many times in
// a row it failed, and the time before which it is not tried again. It is
// kept as JSON at path.
type failureRecord struct {
	path     string
	Projects map[string]map[string]backoff `json:"projects"`
}

type backoff struct {
	Failures int       `json:"failures"`
	Retry    time.Time `json:"retry"`
}

// readFailures reads the record at path. A record that is not there is
// empty. One that does not read is said, and started again empty: the
// worst it costs is one more try of each thing it held.
func readFailures(path string, stderr io.Writer) *failureRecord {
	r := &failureRecord{path: path}
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, r)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "valley: %s does not read, so everything it held is tried again\n", path)
		r.Projects = nil
	}
	if r.Projects == nil {
		r.Projects = map[string]map[string]backoff{}
	}
	return r
}

// waiting reports whether something failed and its wait is not over.
func (r *failureRecord) waiting(project, key string, now time.Time) (backoff, bool) {
	f, ok := r.Projects[project][key]
	return f, ok && now.Before(f.Retry)
}

// failed records one more failure in a row, and the time it may be tried
// again: first after the first failure, doubled for each one after it, and
// never more than longestWait.
func (r *failureRecord) failed(project, key string, now time.Time, first time.Duration) backoff {
	if r.Projects[project] == nil {
		r.Projects[project] = map[string]backoff{}
	}
	f := r.Projects[project][key]
	f.Failures++
	wait := first
	for i := 1; i < f.Failures && wait < longestWait; i++ {
		wait *= 2
	}
	f.Retry = now.Add(min(wait, longestWait))
	r.Projects[project][key] = f
	return f
}

func (r *failureRecord) passed(project, key string) { delete(r.Projects[project], key) }

// keep forgets every failure of the project that is not in met. Other
// projects' failures are kept.
func (r *failureRecord) keep(project string, met map[string]bool) {
	for key := range r.Projects[project] {
		if !met[key] {
			delete(r.Projects[project], key)
		}
	}
	if len(r.Projects[project]) == 0 {
		delete(r.Projects, project)
	}
}

// save writes the record whole, through a file renamed into place, so a
// pass that is killed leaves the last record or the new one. An empty
// record is no file.
func (r *failureRecord) save() error {
	for project, failures := range r.Projects {
		if len(failures) == 0 {
			delete(r.Projects, project)
		}
	}
	if len(r.Projects) == 0 {
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

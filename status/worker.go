package main

// work is one pass of a check worker. It builds the nix checks that ready
// changes require before anyone approves them, and publishes the evidence
// on origin, where approve (bin/valley) reuses it instead of building it,
// and where the integrator finds it for a request approve filed without it.
// A timer starts a pass, and so does a wake mark. A pass keeps nothing
// between passes but its record of failures.
//
// A pass reads each project named with --repo, and works out what each
// of the project's ready changes needs:
//
//  1. It lists the ready changes whose active version has not landed, as
//     valley pending does (pending.go), and pins each one's active head.
//  2. It derives the checks the head requires, as valley-go checks does
//     (checks.go), over the head's merge base with main. Both policy
//     layers are read at main's tip on origin, as approve reads them,
//     never from the change.
//  3. It asks coverage (coverage.go) which of the head's nix checks origin
//     already holds evidence for. Command checks are left to approve.
//  4. The rest is one batch: attest run --rev <head> under the worker's
//     key, which stores one attestation ref, and a push of that ref alone.
//     Given --slots, the batch builds that many checks at once; without
//     it, attest's own default decides.
//
// That read of every project is a scan. A pass is one loop, the
// scheduler, which runs scans and batches until there is nothing left to
// do. The loop alone starts batches, publishes, writes the failure record
// and the telemetry, and claims wake marks. Each batch is one attest run,
// which a goroutine waits for. When attest ends, the goroutine hands the
// loop what the batch did, and changes nothing itself.
//
// Up to --batches batches run at once; without it, one. The loop starts
// the batches a scan found in the order their versions became ready.
// Batches overlap their builds, and their evaluations are admitted by a
// memory budget (evalbudget.go). One evaluation can need most of the
// memory the worker's slice has, so evaluations that together need more
// than the slice holds could get one of them killed. A batch's evaluation
// starts when its estimate fits the budget beside the evaluations
// admitted before it, and otherwise waits. attest admits it through
// reservations in the state directory, which a pass or an attest killed
// during an evaluation does not leave counting once the evaluation is
// gone. An attest that cannot make its reservation evaluates nothing and
// fails, and its batch backs off like any other that failed. The time a batch waits to
// be admitted is recorded apart from its evaluation's.
// Change records carry no time, so that order is the order in which this
// pass first saw each version ready. Versions first seen in the same scan
// go by project name, then by change ID. A pass started by a timer or a
// mark sees all the versions ready then in its first scan.
//
// One batch at a time builds a tree's evidence. A batch's destination is
// the ref it stores and pushes: the project, the tree's digest and this
// worker's key hash. A destination is held from the moment its batch
// starts until its push has been tried, and no second batch for it starts
// meanwhile. attest stores its ref with a plain update-ref, so two
// batches for one ref must never run at once. Two changes whose heads
// hold the same tree, or two versions of a change with the same tree,
// share one batch. Right before it starts a batch, the loop reads the
// head's evidence on origin and the refs stored here again, as a scan
// does. Evidence that arrived since the scan is not built again.
//
// The loop scans again when a batch ends, when a wake mark arrives, and
// every --interval while batches run. It drops a job that has not
// started when a later scan does not find it: its version was superseded,
// landed, or has the evidence it lacked. A batch that runs goes on to its
// end, and its evidence is published: it is about the head's tree, so it
// stays true if the change moves on meanwhile. The pass ends when nothing
// runs and a scan finds nothing to start.
//
// The exception is a batch whose version a scan finds superseded, as a
// restack supersedes one, when no ready change's active version holds its
// tree. An approval lands a change as its active version stands, so that
// evidence would serve no landing. A scan that read the batch's project
// whole stops it (stopSuperseded): the loop kills its attest alone, and
// attest's nix processes die with it, which releases its evaluation's
// reservation. git, which stores the ref in one update, is left to
// finish, so no ref is left half written. A stopped batch publishes
// nothing and records no failure, and its slot and destination are free
// once attest has ended. A batch whose tree a ready version still holds,
// as after a restack that left the tree as it was, runs on.
//
// When a batch ends, its slot takes the next job from the last scan's
// queue at once, and the scan comes after. So a version superseded since
// that scan can start a batch, which the next scan stops as above. A free
// slot does not wait for a read of every project.
//
// A scan forgets each version it did not find ready that no batch builds:
// when the pass first saw it, and the notes said about it. A pass lives as
// long as work keeps arriving, and holds no more than the versions it
// still meets.
//
// The attestation namespace admits creations only, so the worker never
// builds what it could not publish. When origin already holds this
// worker's ref for a tree and the ref lacks a required check, the pass
// reports it and builds nothing. approve --build-here builds the rest
// under the operator's key (bd-7b7d58f). When the worker's ref is stored
// here but not on origin, an earlier batch built it and its push was
// lost, so the pass pushes that same ref again rather than building it
// again.
//
// A batch that fails is not run again at once, and neither is a push that
// fails. The pass records each failure in failures.json in the state
// directory, under its project, as soon as it happens: a batch by the
// tree and the checks it runs, and a push by the object it publishes. A
// failed batch waits firstBuildWait before it runs again, and a failed
// push waits firstPushWait. Each wait doubles with every failure in a
// row, up to longestWait. A wait runs from when the attempt ended, so a
// long build does not use up its own wait. A scan in a pass that is still
// busy starts a batch whose wait is over. A new version is a new batch,
// and runs at once. VALLEY_NOW, when set, is the time the pass reads as
// now (main.go's clock). Deleting the record lets everything run again at
// once.
//
// A scan forgets the failures it did not meet in a project, but only for
// a project it read whole: every ready change's checks derived and its
// evidence read. It never forgets the failure key of a batch still
// running. A scan over other projects, or one that could not read this
// one, leaves this one's record as it is.
//
// Passes that share a state directory take turns: each holds the lock
// file there while it runs, and a pass that finds it held waits for it.
// Every attest is handed the lock too, so it stays held while a batch
// runs even when the pass that started the batch is killed. A stop of
// the unit stops the pass and every attest it started.
//
// A read of origin that fails stops the pass, because a read that did not
// happen must not read as no changes or no evidence. The loop starts
// nothing more, lets the running batches end, publishes and records what
// they did, and then fails. Anything else that goes wrong with one change
// or one batch is reported, and the pass goes on with the rest. The pass
// exits 1 when anything went wrong.
//
// Given --wake, a pass claims that wake directory's due mark before it
// reads anything, and claims again each mark made while it runs, before
// the scan that mark starts (wake.go). It acknowledges the claim when it
// ends having handled what it read. A pass has handled it when every scan
// read every project whole and everything that went wrong was a failed
// batch or push whose wait it recorded: those are retried when their
// wait is over, and a new mark for the same refs would only find them
// still waiting. Anything else that went wrong (a policy that did not
// read, checks that did not derive, a read that stopped the pass, a
// record that was not written) leaves the claim unacknowledged. The
// worker's own pushes mark due, so a pass that publishes scans once more,
// finds nothing new, and ends. A pass that could not claim due reads and
// builds all the same, and then exits claimFailedStatus instead of 0 or
// 1.
//
// Given --telemetry, or VALLEY_TELEMETRY_DIR in its environment, which is
// how the host module names it, a pass says what it is doing as it does
// it, and keeps its recent attempts, its running batches and the failures
// waiting out their backoff with what each was about
// (workertelemetry.go). It hands attest the same stream for each batch's
// progress, and the capacity it builds on (capacity.go), which each check
// build's records carry.
//
// Given --keep-outputs, a pass keeps the outputs of the checks it builds
// alive against nix's garbage collection, so a later batch whose checks
// have the same derivations finds them built. It hands attest
// --gc-roots with the directory roots in its state directory, where
// attest makes one indirect root per output, named by the output's store
// path basename, and makes it afresh whenever a batch uses the output.
// Before its batches, the pass removes each root there that no batch has
// used for longer than the age --keep-outputs gives (pruneRoots). It
// removes only the symlink; nix's own garbage collection frees the output
// later. Without --keep-outputs, attest roots nothing and the pass prunes
// nothing.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"the-valley/change"
	"the-valley/composed"
	"the-valley/telemetry"
)

const workUsage = "usage: valley-go work --repo <name>... --key <file> [--name <name>] [--state <dir>] [--wake <dir>] [--telemetry <dir>] [--slots <n>] [--batches <n>] [--interval <span>] [--keep-outputs <age>] [--eval-budget <size>] [--eval-share <fraction>]"

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

// pausedPushes is what the host's ssh gate says when it refuses a push
// because valley-init is converging (valleyhook/shell.go), and
// pausedPushRetry is how soon the worker tries such a push again.
const (
	pausedPushes    = "pushes to this host are paused until valley-init converges"
	pausedPushRetry = 2 * time.Second
)

// defaultInterval is how often a pass with batches running scans again
// when --interval is not given: the host module's own default.
const defaultInterval = time.Minute

// markPoll is how often a pass with batches running looks for a new wake
// mark.
const markPoll = time.Second

// worker is one pass: the signer it attests as, the lock it holds, what it
// says, what it has found going wrong, and the scheduler's state. Only the
// loop reads or changes any of it once the first batch starts.
type worker struct {
	key, name string
	slots     int           // how many checks a batch builds at once, or 0 for attest's default
	batches   int           // how many batches run at once
	interval  time.Duration // how often a pass with batches running scans again
	roots     string        // the directory attest roots the outputs it builds in, or "" to root nothing
	evals     *evaluations  // the evaluation budget and the record of peaks, or nil to admit evaluations by nothing
	keyhash   string        // the key hash an attestation ref of this signer ends in
	lock      *os.File      // the state directory's lock, held for the pass
	wake      *wake         // the wake directory's claim, or nil without --wake
	tel       *workerTelemetry
	failures  *failureRecord
	stdout    io.Writer
	stderr    io.Writer
	out, errs io.Writer // the files attest writes to: the pass's own stdout and stderr
	troubles  int       // things that went wrong that did not stop the pass
	waits     int       // of the troubles, the failures whose wait was recorded

	capacity string // what the pass builds on (capacity.go), or "" without telemetry

	scans   int                        // the scans so far
	seen    map[string]int             // the scan that first saw each version ready, by job.version
	ready   map[string]bool            // the versions the scan under way has found ready, by job.version
	wanted  map[string]bool            // the destinations of the ready versions the scan under way assessed, by job.dest
	running map[string]*batch          // the batches running, by job.dest
	queue   []*job                     // what the last scan found to build and has not started, in order
	said    map[string]map[string]bool // the notes said about each version or project since the pass last acted on it
	fresh   map[string]map[string]bool // the failure keys recorded since the scan began, by project
	done    chan completion            // how a batch's goroutine hands the loop what the batch did

	// How the loop reads a project, reassesses a job right before its
	// batch starts, and starts a batch's attest run: project, assess and
	// attest, unless a test gives its own.
	read    func(g git, name string) ([]*job, error)
	recheck func(j *job) (bool, error)
	launch  func(b *batch)
}

// job is one ready version as a scan read it: its head, the nix checks the
// head requires and the ones origin holds no evidence for, and the batch
// that would build them.
type job struct {
	r       listed
	c       jsonChange
	v       jsonVersion
	asked   []nixCheck // every nix check the head requires
	missing []nixCheck // the ones origin holds no evidence for
	digest  string     // the head's tree digest
	ref     string     // this worker's attestation ref for the tree
	key     string     // the batch's key in the failure record
	seen    int        // the scan that first saw the version ready
}

// version names the job's change version, project by project.
func (j *job) version() string { return versionName(j.r.name, j.c.ChangeID, j.v.N) }

// versionName names a change version, project by project.
func versionName(project, change string, n int) string {
	return project + " " + change + " " + strconv.Itoa(n)
}

// dest is the batch's destination: the project and the ref it stores.
func (j *job) dest() string { return j.r.name + " " + j.ref }

func (j *job) names() []string {
	var names []string
	for _, q := range j.missing {
		names = append(names, q.name)
	}
	return names
}

// line opens every line said about the job's version.
func (j *job) line() string {
	return fmt.Sprintf("%s: %s v%d at %s", j.r.name, j.c.Handle, j.v.N, j.v.Head[:min(7, len(j.v.Head))])
}

func (j *job) identity() identity {
	return identity{project: j.r.name, change: j.c.ChangeID, cv: j.v.N, head: j.v.Head, tree: j.digest}
}

// jobBefore orders jobs: by the scan that first saw each version ready, then
// by project name, then by change ID.
func jobBefore(a, b *job) bool {
	if a.seen != b.seen {
		return a.seen < b.seen
	}
	if a.r.name != b.r.name {
		return a.r.name < b.r.name
	}
	return a.c.ChangeID < b.c.ChangeID
}

// batch is a job whose attest run has started.
type batch struct {
	job      *job
	run      string // the attest run's ID
	mark     int64  // the newest record in the telemetry stream when it started
	estimate int64  // the peak memory its evaluation is expected to need, in bytes
	budget   int64  // the budget its attest is handed beside the estimate, in bytes
	attempt  *attempt
	stop     func() // kills its attest run, or nil when none started
	stopped  string // why the loop stopped it, or "" when it did not
}

// completion is what a batch did, as its goroutine hands it to the loop:
// how attest ended and when, the object it stored at the job's ref, and
// the checks it recorded as not passing. It is not changed once sent.
type completion struct {
	b       *batch
	err     error
	ended   time.Time
	stored  string // "" when attest stored nothing there
	failed  []string
	peak    int64  // the evaluation's peak memory in bytes, or 0 when attest did not say
	outcome string // how the evaluation ended, as attest said: "ok", "failed" or "killed", or empty when it did not say
}

func runWork(g git, args []string, stdout, stderr io.Writer) (err error) {
	var repos []string
	var key, name, state, wakeDir, telemetryDir string
	slots, batches := 0, 1
	interval := defaultInterval
	var keep time.Duration
	var evalBudget int64
	evalShare := defaultEvalShare
	for i := 0; i < len(args); i++ {
		opt, value, inline := strings.Cut(args[i], "=")
		switch opt {
		case "--repo", "--key", "--name", "--state", "--wake", "--telemetry", "--slots", "--batches", "--interval", "--keep-outputs", "--eval-budget", "--eval-share":
		default:
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
		case "--wake":
			wakeDir = value
		case "--telemetry":
			telemetryDir = value
		case "--slots", "--batches":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return failure(fmt.Sprintf("%s %s: not a whole number of at least 1", opt, value))
			}
			if opt == "--slots" {
				slots = n
			} else {
				batches = n
			}
		case "--interval":
			d, err := timeSpan(value)
			if err != nil {
				return failure(fmt.Sprintf("--interval: %v; give a systemd time span, such as 1m, 15s or 5min", err))
			}
			// A pass reads its projects no more often than it looks for
			// a mark, so a span of 0, which a timer takes, does not spin.
			interval = max(d, markPoll)
		case "--keep-outputs":
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return failure(fmt.Sprintf("--keep-outputs %s: not a positive duration, such as 168h", value))
			}
			keep = d
		case "--eval-budget":
			n, err := parseSize(value)
			if err != nil {
				return failure(fmt.Sprintf("--eval-budget %s: %v", value, err))
			}
			evalBudget = n
		case "--eval-share":
			f, err := strconv.ParseFloat(value, 64)
			if err != nil || !(f > 0 && f <= 1) {
				return failure(fmt.Sprintf("--eval-share %s: not a fraction greater than 0 and at most 1, such as 0.6", value))
			}
			evalShare = f
		}
	}
	if len(repos) == 0 || key == "" {
		return failure(workUsage)
	}
	if telemetryDir == "" {
		telemetryDir = os.Getenv("VALLEY_TELEMETRY_DIR")
	}
	// The pass's own lines go out whole, one Write each; every attest
	// writes to the files beneath.
	files, errFiles := stdout, stderr
	stdout, stderr = &lineSink{to: stdout}, &lineSink{to: stderr}
	// From here on, a pass given --wake that ends without having claimed
	// due exits claimFailedStatus, however it ends, so whatever started
	// it for that due holds off before it starts another.
	var claim *wake
	if wakeDir != "" {
		defer func() {
			if claim != nil && claim.err == nil {
				return
			}
			why := "due was not claimed"
			if claim != nil {
				why = claim.err.Error()
			}
			if err != nil {
				fmt.Fprintf(stderr, "valley: %s\n", err)
			}
			err = claimFailure{fmt.Sprintf("%s, so the pass exits %d", why, claimFailedStatus)}
		}()
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

	// The claim comes before any read, so a move marked after it is
	// either read by this pass or left marked for the next.
	if wakeDir != "" {
		claim = claimWake(wakeDir, stderr)
		defer claim.close()
		if claim.err != nil {
			fmt.Fprintf(stderr, "valley: %v; the pass reads everything all the same\n", claim.err)
		}
	}

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

	w := &worker{key: key, name: name, slots: slots, batches: batches, interval: interval, keyhash: fields[1],
		lock: lock, wake: claim, stdout: stdout, stderr: stderr, out: files, errs: errFiles,
		seen: map[string]int{}, running: map[string]*batch{}, said: map[string]map[string]bool{}}
	w.read, w.launch = w.project, w.attest
	w.recheck = func(j *job) (bool, error) { return w.assess(j, nil) }
	w.tel = openWorkerTelemetry(telemetryDir)
	if w.tel != nil {
		w.capacity = measureCapacity(slots, batches)
	}
	// The loop returns only once no batch runs, so nothing changes the
	// state the telemetry finishes with.
	defer func() { w.tel.finish(w.failures, w.troubles, err) }()
	w.failures = readFailures(filepath.Join(state, "failures.json"), stderr)
	// attest runs in each project's repository, so the state directory is
	// named absolutely for the reservations, as it is for the roots.
	abState, err := filepath.Abs(state)
	if err != nil {
		return failure(fmt.Sprintf("--state %s: %v", state, err))
	}
	if w.evals, err = openEvaluations(abState, evalBudget, evalShare); err != nil {
		return failure(fmt.Sprintf("the evaluation budget: %v", err))
	}
	if w.evals.budget == 0 {
		fmt.Fprintf(stderr, "valley: the evaluation budget could not be found (%s), so this pass evaluates one batch at a time. "+
			"Give the memory evaluations may hold together with services.valley.worker.evalMemoryBudget, such as \"12G\" (--eval-budget 12G)\n", w.evals.source)
	}
	if keep > 0 {
		// attest runs in each project's repository, so the roots are
		// named absolutely, where this pass prunes them.
		if w.roots, err = filepath.Abs(filepath.Join(state, "roots")); err != nil {
			return failure(fmt.Sprintf("--state %s: %v", state, err))
		}
		if err := pruneRoots(w.roots, keep, clock()); err != nil {
			fmt.Fprintf(stderr, "valley: could not prune the roots in %s: %v\n", w.roots, err)
			w.troubles++
		}
	}
	return w.pass(g, repos)
}

// pass runs the loop, writes the failure record, and acknowledges the
// wake claim when the pass handled what it read. It returns the error of
// a read of origin that failed, and otherwise says how many things went
// wrong.
func (w *worker) pass(g git, repos []string) error {
	stop := w.loop(g, repos)
	w.save()
	if stop != nil {
		return stop
	}
	if w.wake != nil && w.troubles == w.waits {
		if err := w.wake.ack(); err != nil {
			fmt.Fprintf(w.stderr, "valley: could not acknowledge %s: %v\n", filepath.Join(w.wake.dir, "claimed"), err)
			w.troubles++
		}
	}
	if w.troubles > 0 {
		return failure(fmt.Sprintf("the pass finished, but %s went wrong in it; each is said above", counted(w.troubles, "thing")))
	}
	return nil
}

// spanUnits are the units of a systemd time span in microseconds, in the
// order systemd tries them (extract_multiplier in its time-util.c): the
// first a span's text starts with is its unit. A month is 30.44 days and a
// year 365.25 days, as systemd counts them.
var spanUnits = []struct {
	name string
	usec uint64
}{
	{"seconds", 1e6}, {"second", 1e6}, {"sec", 1e6}, {"s", 1e6},
	{"minutes", 60e6}, {"minute", 60e6}, {"min", 60e6},
	{"months", 2629800e6}, {"month", 2629800e6}, {"M", 2629800e6},
	{"msec", 1e3}, {"ms", 1e3}, {"m", 60e6},
	{"hours", 3600e6}, {"hour", 3600e6}, {"hr", 3600e6}, {"h", 3600e6},
	{"days", 86400e6}, {"day", 86400e6}, {"d", 86400e6},
	{"weeks", 604800e6}, {"week", 604800e6}, {"w", 604800e6},
	{"years", 31557600e6}, {"year", 31557600e6}, {"y", 31557600e6},
	{"usec", 1}, {"us", 1}, {"\u03bcs", 1}, {"\u00b5s", 1},
}

// timeSpan reads a time span as systemd reads a timer's (parse_sec in its
// time-util.c), such as 1m, 15s, 5min, "1h 30min", 1.5h, 2months or
// infinity. A number with no unit is seconds. Like systemd, it counts
// whole microseconds, and refuses a span of 2^64-1 microseconds or more.
// A span longer than a time.Duration holds, about 292 years, reads as the
// longest one, as infinity does.
func timeSpan(s string) (time.Duration, error) {
	const space = " \t\n\r"
	bad := fmt.Errorf("%q is not a time span", s)
	long := fmt.Errorf("%q is longer than systemd reads", s)
	rest := strings.TrimLeft(s, space)
	if after, ok := strings.CutPrefix(rest, "infinity"); ok {
		if strings.TrimLeft(after, space) != "" {
			return 0, bad
		}
		return math.MaxInt64, nil
	}
	digits := func(s string) string { return s[:len(s)-len(strings.TrimLeft(s, "0123456789"))] }
	if rest == "" {
		return 0, bad
	}
	var usec uint64
	for ; rest != ""; rest = strings.TrimLeft(rest, space) {
		whole := digits(rest)
		after := rest[len(whole):]
		fraction := ""
		if tail, dot := strings.CutPrefix(after, "."); dot {
			if fraction = digits(tail); fraction == "" {
				return 0, bad
			}
			after = tail[len(fraction):]
		} else if whole == "" {
			return 0, bad
		}
		// The unit may follow a space. A number followed at once by
		// anything else, such as 12.34.56, is not a span.
		unit, mult := strings.TrimLeft(after, space), uint64(1e6)
		matched := false
		for _, u := range spanUnits {
			if strings.HasPrefix(unit, u.name) {
				unit, mult, matched = unit[len(u.name):], u.usec, true
				break
			}
		}
		if !matched && unit == after && after != "" {
			return 0, bad
		}
		rest = unit
		n, err := strconv.ParseInt(cmp.Or(whole, "0"), 10, 64)
		if err != nil || uint64(n) >= math.MaxUint64/mult {
			return 0, long
		}
		if k := uint64(n) * mult; k < math.MaxUint64-usec {
			usec += k
		} else {
			return 0, long
		}
		for i, m := 0, mult/10; i < len(fraction); i, m = i+1, m/10 {
			k := uint64(fraction[i]-'0') * m
			if k >= math.MaxUint64-usec {
				return 0, long
			}
			usec += k
		}
	}
	if usec > math.MaxInt64/uint64(time.Microsecond) {
		return math.MaxInt64, nil
	}
	return time.Duration(usec) * time.Microsecond, nil
}

// lineSink passes on the pass's own lines, so that no two of its Writes
// overlap.
type lineSink struct {
	mu sync.Mutex
	to io.Writer
}

func (s *lineSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.to.Write(p)
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

// pruneRoots removes each symlink in dir whose modification time is more
// than keep before now. attest makes a root afresh each time a batch uses
// its output, so that time is the output's last use. Only symlinks are
// removed, never what they point to, and a dir that does not exist yet
// holds nothing to prune.
func pruneRoots(dir string, keep time.Duration, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		info, err := e.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if now.Sub(info.ModTime()) > keep {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
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

// loop is the pass's scheduler. It scans, starts what the scan found, and
// handles each batch as it ends, until nothing runs and a scan finds
// nothing to start. It returns the error of a read of origin that failed,
// once every batch running then has ended and been handled. It never
// returns while a batch runs.
func (w *worker) loop(g git, repos []string) error {
	w.done = make(chan completion)
	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	poll := time.NewTicker(markPoll)
	defer poll.Stop()
	var stop error
	scan := true
	for {
		if scan && stop == nil {
			scan = false
			// A mark made since the last claim is claimed before the
			// scan reads anything, as the first is.
			w.wake.again(w.stderr)
			if stop = w.scan(g, repos); stop == nil {
				stop = w.admit()
			}
			w.tel.settle()
		}
		if len(w.running) == 0 {
			if stop != nil || !w.wake.marked() {
				return stop
			}
			scan = true
			continue
		}
		select {
		case c := <-w.done:
			w.complete(c)
			// A freed slot takes the next job from the last scan's
			// queue at once, before the scan that follows finds what
			// changed meanwhile. A version superseded since that scan
			// can start a batch this way, which that scan stops.
			if err := w.reap(); stop == nil {
				if stop = err; stop == nil {
					stop = w.admit()
				}
			}
			scan = true
		case <-tick.C:
			scan = true
		case <-poll.C:
			scan = scan || w.wake.marked()
		}
	}
}

// scan reads every project, and makes the queue what it found to build,
// in order. It returns an error only when a read of origin failed.
func (w *worker) scan(g git, repos []string) error {
	w.scans++
	w.fresh = map[string]map[string]bool{}
	w.ready = map[string]bool{}
	w.wanted = map[string]bool{}
	var found []*job
	for _, name := range repos {
		jobs, err := w.read(g, name)
		if err != nil {
			w.queue = nil
			return err
		}
		found = append(found, jobs...)
	}
	w.queue = w.order(found)
	w.forget(repos)
	return nil
}

// forget drops what the pass keeps about each version that the scan just
// ended did not find ready and no batch builds: when it was first seen,
// and the notes said about it. A pass lives as long as work keeps
// arriving, and this keeps what it holds to the versions it still meets.
// A version found ready again later is seen anew.
func (w *worker) forget(repos []string) {
	keep := map[string]bool{}
	for v := range w.ready {
		keep[v] = true
	}
	for _, b := range w.running {
		keep[b.job.version()] = true
	}
	for v := range w.seen {
		if !keep[v] {
			delete(w.seen, v)
		}
	}
	for _, name := range repos {
		keep[name] = true // the notes said about a project as a whole
	}
	for about := range w.said {
		if !keep[about] {
			delete(w.said, about)
		}
	}
}

// order sorts what a scan found, and keeps the first job for each
// destination. A later job for the same destination is the same tree
// under the same signer, which the first job's batch builds.
func (w *worker) order(found []*job) []*job {
	sort.SliceStable(found, func(a, b int) bool { return jobBefore(found[a], found[b]) })
	first := map[string]*job{}
	var queue []*job
	for _, j := range found {
		if f, ok := first[j.dest()]; ok {
			w.note(j, "the batch for %s v%d builds the same tree", f.c.Handle, f.v.N)
			continue
		}
		first[j.dest()] = j
		queue = append(queue, j)
	}
	return queue
}

// project reads one project's ready changes, and returns the jobs that
// need a batch. It returns an error only when a read of origin failed.
// When it has read the project whole, it forgets the project's failures
// it did not meet, and stops the project's batches whose evidence would
// serve no landing.
func (w *worker) project(g git, name string) ([]*job, error) {
	list, _, err := sources(g, listing{repo: name}, w.stderr)
	if err != nil {
		return nil, failure(fmt.Sprintf("%s: %v; the pass stops here", name, err))
	}
	repos := readSources(list, newVerifier())
	r := repos[0]
	if r.err != nil {
		return nil, failure(fmt.Sprintf("%s: %v; the pass stops here", name, r.err))
	}
	view := r.pendingView(handles(repos))
	for _, c := range view.Changes {
		w.ready[versionName(name, c.ChangeID, c.Versions[c.Active-1].N)] = true
	}
	met := map[string]bool{} // the failures this scan met, by key
	if len(view.Changes) == 0 {
		w.prune(name, met)
		return nil, nil
	}
	if r.snap.tip == "" {
		if w.noteAs(name, name+": origin has no main to read the policy at, so nothing is built") {
			w.troubles++
		}
		return nil, nil
	}
	instance, project, done, err := policyLayers(r.g, r.snap.tip)
	defer done()
	if err != nil {
		if w.noteAs(name, fmt.Sprintf("%s: the policy at main did not read, so nothing is built: %v", name, err)) {
			w.troubles++
		}
		return nil, nil
	}
	delete(w.said, name)
	whole := true
	var jobs []*job
	for _, c := range view.Changes {
		// A scan reads one change after another, which can take a while,
		// so a batch that ended meanwhile is handled between them.
		if err := w.reap(); err != nil {
			return nil, err
		}
		j, read, err := w.change(r, instance, project, c, met)
		if err != nil {
			return nil, err
		}
		whole = whole && read
		if j != nil {
			jobs = append(jobs, j)
		}
	}
	if whole {
		w.prune(name, met)
		w.stopSuperseded(name, view.Changes)
	}
	return jobs, nil
}

// stopSuperseded stops each of the project's running batches whose
// version is no longer its change's active version, when no ready version
// the scan assessed holds its tree. changes are the project's ready
// changes, as the scan read them whole. Each batch is stopped once; its
// end is handled when attest has ended (complete).
func (w *worker) stopSuperseded(project string, changes []jsonChange) {
	active := map[string]int{}
	for _, c := range changes {
		active[c.ChangeID] = c.Versions[c.Active-1].N
	}
	for dest, b := range w.running {
		j := b.job
		n, ready := active[j.c.ChangeID]
		if j.r.name != project || !ready || n == j.v.N || w.wanted[dest] || b.stop == nil || b.stopped != "" {
			continue
		}
		b.stopped = fmt.Sprintf("v%d supersedes it, and no ready change's active version holds its tree", n)
		w.say(j, "stopping the batch: %s", b.stopped)
		b.stop()
	}
}

// prune forgets the project's failures that a scan which read the project
// whole did not meet. It never forgets the key of a batch still running,
// nor a failure recorded since the scan began, which the scan's reads may
// have come too early to meet.
func (w *worker) prune(project string, met map[string]bool) {
	for _, b := range w.running {
		if b.job.r.name == project {
			met[b.job.key] = true
		}
	}
	for key := range w.fresh[project] {
		met[key] = true
	}
	w.failures.keep(project, met)
}

// failed records a failure, and that it was recorded during this scan.
func (w *worker) failed(project, key string, at time.Time, first time.Duration) backoff {
	if w.fresh != nil {
		if w.fresh[project] == nil {
			w.fresh[project] = map[string]bool{}
		}
		w.fresh[project][key] = true
	}
	return w.failures.failed(project, key, at, first)
}

// reap handles every batch that has ended and not been handled, and then
// starts what the queue holds in the slots they freed. It returns an
// error only when a read of origin failed.
func (w *worker) reap() error {
	ended := false
	for {
		select {
		case c := <-w.done:
			w.complete(c)
			ended = true
			continue
		default:
		}
		break
	}
	if !ended {
		return nil
	}
	return w.admit()
}

// change reads one ready change, and returns its job when it needs a
// batch. read says whether it read the change through: derived its
// checks, and read the evidence origin holds for them.
func (w *worker) change(r listed, instance, project string, c jsonChange, met map[string]bool) (j *job, read bool, err error) {
	v := c.Versions[c.Active-1]
	j = &job{r: r, c: c, v: v}
	first, ok := w.seen[j.version()]
	if !ok {
		first = w.scans
		w.seen[j.version()] = first
	}
	j.seen = first
	w.tel.reading(r.name, c, v, !ok)
	base, err := r.g.output(true, "merge-base", r.snap.tip, v.Head)
	if err != nil {
		if w.note(j, "shares no history with main, so nothing is built") {
			w.troubles++
		}
		return nil, false, nil
	}
	doc, err := derivedChecks(r.g, "", instance, project, base, v.Head)
	if err != nil {
		if w.note(j, "its checks did not derive, so nothing is built: %v", err) {
			w.troubles++
		}
		return nil, false, nil
	}
	for _, q := range doc.Required {
		if q.Runner == "nix" {
			j.asked = append(j.asked, nixCheck{q.Name, q.Attribute})
		}
	}
	if len(j.asked) == 0 {
		w.note(j, "requires no nix check")
		return nil, true, nil
	}
	needed, err := w.assess(j, met)
	if err != nil || !needed {
		return nil, err == nil, err
	}
	return j, true, nil
}

// assess reads the evidence origin holds for a job's head, and reports
// whether the job needs a batch now. It does not when origin covers every
// check, when a batch for its destination runs, when origin already holds
// this worker's ref for the tree, when its batch is waiting out a
// failure, or when a batch stored the ref here and origin lacks it, which
// it publishes. met, when given, gets the failure keys it meets, and the
// scan reading the job counts its destination as wanted, so a batch
// building that tree is not stopped. A reassessment of a queued job gives
// no met: its version may since have been superseded. It returns an error
// only when a read of origin failed.
func (w *worker) assess(j *job, met map[string]bool) (bool, error) {
	e, err := coverageOf(j.r.g, j.v.Head, j.asked, w.stderr)
	if err != nil {
		return false, failure(fmt.Sprintf("%s: %s v%d: %v; the pass stops here", j.r.name, j.c.Handle, j.v.N, err))
	}
	j.digest, j.missing = e.digest, nil
	j.ref = attestationRefs + e.digest + "/" + w.keyhash
	if met != nil {
		w.wanted[j.dest()] = true
	}
	for _, q := range j.asked {
		if _, ok := e.covered[q.name]; !ok {
			j.missing = append(j.missing, q)
		}
	}
	if len(j.missing) == 0 {
		w.note(j, "origin holds evidence for every nix check it requires")
		return false, nil
	}
	if b, ok := w.running[j.dest()]; ok {
		if b.job.version() != j.version() {
			w.note(j, "the batch for %s v%d builds the same tree", b.job.c.Handle, b.job.v.N)
		}
		return false, nil
	}
	names := strings.Join(j.names(), ", ")
	if _, ok := e.listed[j.ref]; ok {
		w.note(j, "origin already holds %s, this worker's evidence for its tree, and it does not cover %s. "+
			"That namespace admits creations only, so approve --build-here builds the rest (bd-7b7d58f)", j.ref, names)
		return false, nil
	}
	if stored, found, err := j.r.g.ref(j.ref); err == nil && found {
		w.note(j, "an earlier pass stored %s, and origin does not hold it", j.ref)
		w.publish(j, stored, met)
		return false, nil
	}
	j.key = "build " + e.digest + " " + strings.Join(j.names(), ",")
	if met != nil {
		met[j.key] = true
	}
	if f, waiting := w.failures.waiting(j.r.name, j.key, clock()); waiting {
		w.note(j, "its batch %s failed %s in a row, so it is not built again before %s", names, counted(f.Failures, "time"), f.Retry.Format(time.RFC3339))
		return false, nil
	}
	return true, nil
}

// admit starts the queue's jobs in order while fewer than w.batches run.
// It assesses each job again right before it starts it, so evidence that
// arrived since the scan is not built again. A job whose destination a
// batch holds, or that finds no slot, stays queued for the next scan to
// find again. It returns an error only when a read of origin failed.
func (w *worker) admit() error {
	var left []*job
	for i, j := range w.queue {
		if len(w.running) >= w.batches {
			left = append(left, w.queue[i:]...)
			break
		}
		if _, held := w.running[j.dest()]; held {
			left = append(left, j)
			continue
		}
		needed, err := w.recheck(j)
		if err != nil {
			w.queue = nil
			return err
		}
		if needed {
			w.start(j)
		}
	}
	w.queue = left
	return nil
}

// start runs a job's batch: it holds the job's destination, starts attest
// run, and leaves a goroutine to wait for it. The goroutine reads what
// attest stored and recorded, and hands it to the loop; it changes
// nothing the loop owns.
//
// attest is handed the lock file as well, and holds it until it ends, and
// the wake directory's lock with it. The flock belongs to the open file,
// so the lock stays held while attest runs, and while anything it started
// that inherited the file runs, even when this pass is killed. A later
// pass waits for all of it, and then finds the ref attest stored.
//
// The batch's evaluation is estimated from its project's record of peaks.
// An estimate larger than the whole budget cannot fit beside anything, so
// the evaluation runs alone, and the pass says so, once for the batch, in
// its output and as a capacity record. A pass that found no budget hands
// attest a budget equal to the estimate, so each evaluation runs alone,
// and said so once when it began.
func (w *worker) start(j *job) {
	b := &batch{job: j, run: telemetry.NewRunID(), mark: w.tel.streamEnd()}
	w.say(j, "building %s", strings.Join(j.names(), ", "))
	data := map[string]any{"attest_run": b.run}
	if w.evals != nil {
		b.estimate = w.evals.estimate(j.r.name)
		b.budget = w.evals.handed(b.estimate)
		data["eval_estimate_bytes"], data["eval_budget_bytes"] = b.estimate, b.budget
		if w.evals.budget > 0 && b.estimate > w.evals.budget {
			w.say(j, "its evaluation is estimated at %s, more than the evaluation budget of %s (%s), so it evaluates alone; give the worker more memory, or a larger --eval-budget or --eval-share",
				bytesText(b.estimate), bytesText(w.evals.budget), w.evals.source)
			w.tel.capacity(j.identity(), b.estimate, w.evals.budget, w.evals.source)
		}
	}
	b.attempt = w.tel.begin("build", j.identity(), j.names(), b.run, data)
	w.running[j.dest()] = b
	w.launch(b)
}

// attest starts a batch's attest run, and a goroutine that waits for it
// and hands the loop its completion.
func (w *worker) attest(b *batch) {
	j := b.job
	repo := j.r.g.dir
	if repo == "" {
		repo = "."
	}
	args := []string{"run", "--repo", repo, "--rev", j.v.Head, "--key", w.key}
	if w.name != "" {
		args = append(args, "--name", w.name)
	}
	if w.slots > 0 {
		args = append(args, "--slots", strconv.Itoa(w.slots))
	}
	if w.roots != "" {
		args = append(args, "--gc-roots", w.roots)
	}
	peak := ""
	if w.evals != nil {
		peak = filepath.Join(w.evals.dir, b.run+".peak")
		args = append(args, "--eval-reservations", w.evals.dir, "--eval-estimate", strconv.FormatInt(b.estimate, 10),
			"--eval-budget", strconv.FormatInt(b.budget, 10), "--eval-peak", peak)
	}
	for _, q := range j.missing {
		args = append(args, "--check", q.name+"="+q.attribute)
	}
	stream := ""
	if w.tel != nil {
		stream = w.tel.stream()
		args = append(args, "--telemetry", stream, "--telemetry-run", b.run, "--project", j.r.name,
			"--change", j.c.ChangeID, "--change-version", strconv.Itoa(j.v.N), "--capacity", w.capacity)
	}
	// attest writes to the pass's own output files, not through a pipe the
	// pass reads: an attest that outlives a killed pass must still be able
	// to write, and store its ref.
	cmd := exec.Command("attest", args...)
	cmd.Env = j.r.g.env
	cmd.Dir = j.r.g.dir
	cmd.Stdout, cmd.Stderr = w.out, w.errs
	cmd.ExtraFiles = []*os.File{w.lock}
	if w.wake != nil && w.wake.lock != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, w.wake.lock)
	}
	if err := cmd.Start(); err != nil {
		go func() { w.done <- completion{b: b, err: err, ended: clock()} }()
		return
	}
	// A stop kills attest alone, never its process group: its nix
	// processes die with it (attest's dieWithParent), and a git storing
	// the ref finishes its one update.
	b.stop = func() { _ = cmd.Process.Kill() }
	g, ref, run, mark := j.r.g, j.ref, b.run, b.mark
	go func() {
		err := cmd.Wait()
		c := completion{b: b, err: err, ended: clock()}
		if peak != "" {
			if text, rerr := os.ReadFile(peak); rerr == nil {
				c.peak, c.outcome = readPeak(string(text))
			}
			os.Remove(peak)
		}
		if err != nil {
			c.failed = failedChecks(stream, run, mark)
		} else if stored, found, rerr := g.ref(ref); rerr == nil && found {
			c.stored = stored
		}
		w.done <- c
	}()
}

// complete handles what one batch did: it records a failure at once, with
// its wait measured from when the batch ended, or publishes what the batch
// stored. The batch's destination is held until its push has been tried.
// A batch the loop stopped that did not pass is recorded as cancelled,
// with why it was stopped, and nothing else: it backs nothing off, and
// publishes nothing. One that passed before its stop took effect is
// handled as any other.
func (w *worker) complete(c completion) {
	b, j := c.b, c.b.job
	defer func() {
		delete(w.running, j.dest())
		w.tel.settle()
	}()
	cancelled := b.stopped != "" && c.err != nil
	outcome := c.outcome
	if outcome == "" && endedBySignal(c.err) && !cancelled {
		// attest was killed before it said how its evaluation ended,
		// which may have been during the evaluation. The loop's own stop
		// says nothing of the evaluation's memory.
		outcome = "killed"
	}
	if outcome != "" && w.evals != nil {
		if err := w.evals.record(j.r.name, c.peak, outcome); err != nil {
			fmt.Fprintf(w.stderr, "valley: could not record the evaluation's peak memory in %s: %v\n", w.evals.path, err)
		}
	}
	if cancelled {
		w.tel.end(b.attempt, "cancelled", b.stopped, j.key, nil)
		w.say(j, "the batch stopped; nothing is published")
		return
	}
	if c.err != nil {
		f := w.failed(j.r.name, j.key, c.ended, firstBuildWait)
		reason := fmt.Sprintf("attest run %s: %v", b.run, c.err)
		if len(c.failed) > 0 {
			reason += "; " + strings.Join(c.failed, ", ")
		}
		w.tel.end(b.attempt, "failed", reason, j.key, &f)
		w.say(j, "the batch did not pass (%v); it is not built again before %s", c.err, f.Retry.Format(time.RFC3339))
		w.troubles++
		w.waits++
		w.save()
		return
	}
	w.failures.passed(j.r.name, j.key)
	w.tel.end(b.attempt, "passed", "", j.key, nil)
	w.save()
	if c.stored == "" {
		w.say(j, "attest stored no %s, so nothing is published", j.ref)
		w.troubles++
		return
	}
	w.publish(j, c.stored, nil)
}

// publish pushes a job's attestation ref to origin, and nothing else. The
// hook verifies the notes against the tree and the signer whoever pushes
// them. A push that fails leaves the ref stored here, and the same object
// is not pushed again until its wait is over: by any pass, and for any
// change whose tree it is about. met, when given, gets the push's key.
func (w *worker) publish(j *job, object string, met map[string]bool) {
	key := "push " + object
	if met != nil {
		met[key] = true
	}
	if f, waiting := w.failures.waiting(j.r.name, key, clock()); waiting {
		w.note(j, "publishing %s failed %s in a row, so it is not pushed again before %s", j.ref, counted(f.Failures, "time"), f.Retry.Format(time.RFC3339))
		return
	}
	a := w.tel.begin("push", j.identity(), nil, "", map[string]any{"ref": j.ref, "object": object})
	if err := w.push(j, object); err != nil {
		f := w.failed(j.r.name, key, clock(), firstPushWait)
		w.tel.end(a, "failed", fmt.Sprintf("pushing %s to origin: %v", j.ref, err), key, &f)
		w.say(j, "could not publish %s; it is not pushed again before %s", j.ref, f.Retry.Format(time.RFC3339))
		w.troubles++
		w.waits++
		w.save()
		return
	}
	w.failures.passed(j.r.name, key)
	w.tel.end(a, "passed", "", key, nil)
	w.say(j, "published %s", j.ref)
}

// push pushes object to the job's ref on origin. A host refuses every
// push while valley-init converges on its configuration, which takes
// seconds. A push refused for that is tried again every pausedPushRetry,
// for up to firstPushWait, before it fails and waits as any failed push
// does. The loop waits meanwhile; another batch's push would be refused
// too.
func (w *worker) push(j *job, object string) error {
	g := j.r.g
	var said bytes.Buffer
	g.stderr = &said
	if j.r.g.stderr != nil {
		g.stderr = io.MultiWriter(j.r.g.stderr, &said)
	}
	for began := time.Now(); ; time.Sleep(pausedPushRetry) {
		said.Reset()
		_, err := g.output(false, "push", "--quiet", "origin", object+":"+j.ref)
		if err == nil || !strings.Contains(said.String(), pausedPushes) || time.Since(began) >= firstPushWait {
			return err
		}
		w.note(j, "origin paused pushes while it converges, so publishing %s is tried again", j.ref)
	}
}

// say prints a line about what the pass did with a version. The version's
// notes may be said again after it.
func (w *worker) say(j *job, format string, args ...any) {
	delete(w.said, j.version())
	fmt.Fprintf(w.stdout, "%s: %s\n", j.line(), fmt.Sprintf(format, args...))
}

// note prints a line about how a version stands, unless the pass has said
// that line of it since it last did something with it, so a scan that
// finds things as they were says nothing new. It reports whether it
// printed.
func (w *worker) note(j *job, format string, args ...any) bool {
	return w.noteAs(j.version(), j.line()+": "+fmt.Sprintf(format, args...))
}

func (w *worker) noteAs(about, line string) bool {
	if w.said[about][line] {
		return false
	}
	if w.said[about] == nil {
		w.said[about] = map[string]bool{}
	}
	w.said[about][line] = true
	fmt.Fprintln(w.stdout, line)
	return true
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
		// The valley's main is read by its exact name, and fetched by the
		// object it names: given a ref name the valley lacks, fetch takes
		// another ref whose name ends in it, such as
		// refs/heads/refs/heads/main.
		at, err := remoteRef(v, url, "refs/heads/main")
		if err != nil {
			return "", "", done, fmt.Errorf("could not fetch the owning valley %s", url)
		} else if at == "" {
			return "", "", done, fmt.Errorf("the owning valley %s has no refs/heads/main", url)
		}
		if _, err := v.output(true, "fetch", "--quiet", "--depth", "1", "--no-tags", "--end-of-options", url, at); err != nil {
			return "", "", done, fmt.Errorf("could not fetch the owning valley %s", url)
		}
		floor = composed.Floor{Repo: scratch, Ref: at, Dir: instanceLayer}
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

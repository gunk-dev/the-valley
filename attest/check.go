package main

// Running a check, and the digests a run yields.
//
// The nix runner claims purity strongly: it builds a flake check
// derivation, so the check's inputs, the derivation itself and its output
// are all named, and any verifier holding the same tree can re-derive
// them. The command runner claims nothing beyond having run — its
// statement is a notarization naming the environment and the time
// (archive/design/verification.md).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type checkSpec struct {
	name      string
	attribute string // nix runner
	command   string // command runner
}

func (c checkSpec) runner() string {
	if c.command != "" {
		return "command"
	}
	return "nix"
}

// checkOutcome is what running one check produced: whether it passed, the
// output a reader needs when it did not, how it was satisfied, and the
// digests of a pure run.
type checkOutcome struct {
	spec      checkSpec
	passed    bool
	log       string
	satisfied string

	inputs     string
	derivation string
	output     string
	observedAt string
}

// ranCheck is one check as a run ran it: its outcome, or the error that
// kept it from having one, and when it started and how long it took. eval
// is how long a pure check took to evaluate, and is zero for a command.
type ranCheck struct {
	out   checkOutcome
	err   error
	began time.Time
	took  time.Duration
	eval  time.Duration
}

// nixSystem is the flake system attribute of the machine running the
// check. A check is built where the work was written, so this is the
// runner's own system and never something the caller states about another
// machine.
func nixSystem() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "386":
		arch = "i686"
	}
	return arch + "-" + runtime.GOOS
}

func nix(dir string, args ...string) (string, string, error) {
	out, errb, _, err := nixPeak(dir, nil, args...)
	return out, errb, err
}

// peak is what an evaluation's nix processes measured: the most memory
// any of them held, in bytes, and how they ended. The outcome is "ok"
// when every one exited successfully, "killed" when any was ended by a
// signal, as the kernel's out-of-memory killer ends one, "failed" when
// any other failed, and empty when none ran. Only an ok peak is the
// evaluation's whole high-water mark: one that failed or was killed
// stopped short of it.
type peak struct {
	bytes   int64
	outcome string
}

// add takes in another process's measure: the larger peak, and the worse
// outcome.
func (p peak) add(q peak) peak {
	rank := map[string]int{"": 0, "ok": 1, "failed": 2, "killed": 3}
	if rank[q.outcome] > rank[p.outcome] {
		p.outcome = q.outcome
	}
	p.bytes = max(p.bytes, q.bytes)
	return p
}

// nixPeak runs nix as nix does, and also measures it: the most memory the
// nix process held, in bytes, which is its maximum resident set size from
// its resource usage once it exited, and how it ended. An evaluation runs
// in the nix process itself, not in the daemon, so this is the
// evaluation's peak. The process inherits held, the run's locked
// reservation file when it has one, so the reservation stays held while
// the process runs (admit), and it is killed if attest dies, so it never
// outlives the run that counted it (dieWithParent).
func nixPeak(dir string, held *os.File, args ...string) (string, string, peak, error) {
	cmd := exec.Command("nix", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.SysProcAttr = dieWithParent()
	if held != nil {
		cmd.ExtraFiles = []*os.File{held}
	}
	err := cmd.Run()
	m := peak{bytes: maxRSS(cmd.ProcessState), outcome: "ok"}
	if err != nil {
		m.outcome = "failed"
		if cmd.ProcessState != nil {
			if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				m.outcome = "killed"
			}
		}
	}
	return out.String(), errb.String(), m, err
}

// maxRSS is the maximum resident set size of a process that exited, in
// bytes, or 0 when it is not known. Linux counts it in kibibytes.
func maxRSS(ps *os.ProcessState) int64 {
	if ps == nil {
		return 0
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return ru.Maxrss
	}
	return ru.Maxrss * 1024
}

// nixStore runs nix-store, and stops it when ctx is done.
func nixStore(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "nix-store", args...)
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// flakeChecks lists every check attribute a flake defines for a system —
// what "the repo's canonical checks" means when the caller names none.
func flakeChecks(tree, system string) ([]string, error) {
	out, errb, err := nix(tree, "eval", "--json", "--apply", "builtins.attrNames", tree+"#checks."+system)
	if err != nil {
		return nil, fmt.Errorf("listing checks: %w: %s", err, strings.TrimSpace(errb))
	}
	var names []string
	if err := json.Unmarshal([]byte(out), &names); err != nil {
		return nil, fmt.Errorf("listing checks: %w", err)
	}
	return names, nil
}

// closure is what evaluating a pure check names before anything is built:
// the derivation, and the digest of its input closure. Both are functions
// of the tree alone, so an integrator can recompute the closure digest on a
// rebased tree without running the check — which is the whole of the pure
// transfer rule (dcr-439b771).
type closure struct {
	drv        string
	derivation string
	inputs     string
}

// evalClosure evaluates one flake check over an exported tree and digests
// what the evaluation named. Nothing is built. It also returns the
// evaluation's measure, as nixPeak takes it, whose nix process inherits
// held.
func evalClosure(tree, system string, spec checkSpec, held *os.File) (closure, peak, error) {
	attr := tree + "#checks." + system + "." + spec.attribute
	drv, errb, peak, err := nixPeak(tree, held, "eval", "--raw", attr+".drvPath")
	if err != nil {
		return closure{}, peak, fmt.Errorf("evaluating %s: %w: %s", attr, err, strings.TrimSpace(errb))
	}
	c, err := closureOf(strings.TrimSpace(drv))
	return c, peak, err
}

// closureOf digests a derivation an evaluation named: the derivation file
// itself, and the store paths it reads.
func closureOf(drv string) (closure, error) {
	drvBytes, err := os.ReadFile(drv)
	if err != nil {
		return closure{}, err
	}
	inputs, err := drvInputs(drv)
	if err != nil {
		return closure{}, err
	}
	return closure{
		drv:        drv,
		derivation: fmt.Sprintf("%x", sha256sum(drvBytes)),
		inputs:     inputsDigest(inputs),
	}, nil
}

// evaluation is one pure check's evaluation: the closure it named, or why
// it named none, and how long it took.
type evaluation struct {
	closure
	err  error
	took time.Duration
}

// plainAttribute is a check attribute the shared evaluation can name in a
// nix string as it is: one attribute, with no dot, quote, backslash or
// dollar sign in it.
var plainAttribute = regexp.MustCompile(`^[A-Za-z0-9_'+-]+$`)

// evalClosures evaluates every pure check over the exported tree. One nix
// evaluation names the derivations of all of them together, so nixpkgs and
// whatever else the checks share are evaluated once and not once per
// check. A check that evaluation could not name is evaluated alone, as
// evalClosure does, so an error is reported against the check that has it
// and stops no other. Each check is timed as the shared evaluation plus
// whatever was done for it alone. The second answer measures every nix
// evaluation it ran, the shared one and each alone, together (peak.add),
// so a peak counts as the evaluation's whole high-water mark only when
// none of them failed. Each nix process inherits held.
func evalClosures(tree, system string, specs []checkSpec, held *os.File) ([]evaluation, peak) {
	var attrs []string
	shared := map[string]bool{}
	for _, spec := range specs {
		if plainAttribute.MatchString(spec.attribute) && !shared[spec.attribute] {
			shared[spec.attribute] = true
			attrs = append(attrs, spec.attribute)
		}
	}
	began := time.Now()
	drvs, measured := evalDrvPaths(tree, system, attrs, held)
	sharedTook := time.Since(began)

	evals := make([]evaluation, len(specs))
	for i, spec := range specs {
		began := time.Now()
		var c closure
		var err error
		if drv, ok := drvs[spec.attribute]; ok {
			c, err = closureOf(drv)
		} else {
			var alone peak
			c, alone, err = evalClosure(tree, system, spec, held)
			measured = measured.add(alone)
		}
		evals[i] = evaluation{closure: c, err: err, took: time.Since(began)}
		if shared[spec.attribute] {
			evals[i].took += sharedTook
		}
	}
	return evals, measured
}

// evalDrvPaths names the derivation of every check attribute given, in one
// nix evaluation. A check whose evaluation throws, or that is missing or
// is not a derivation, is left out of the answer. Some evaluation errors
// cannot be caught inside nix, such as a type error, and they end the
// whole evaluation; then the answer is empty, and every check is evaluated
// alone. One attribute has nothing to share, so it is left to be evaluated
// alone from the start. The second answer is the evaluation's measure,
// or an empty one when it did not run. Its nix process inherits held.
func evalDrvPaths(tree, system string, attrs []string, held *os.File) (map[string]string, peak) {
	drvs := map[string]string{}
	if len(attrs) < 2 {
		return drvs, peak{}
	}
	quoted := make([]string, len(attrs))
	for i, a := range attrs {
		quoted[i] = `"` + a + `"`
	}
	apply := `checks: builtins.listToAttrs (map (name: { inherit name; value = ` +
		`let r = builtins.tryEval (if checks ? ${name} && checks.${name} ? drvPath then checks.${name}.drvPath else null); ` +
		`in if r.success then r.value else null; }) [ ` + strings.Join(quoted, " ") + ` ])`
	out, _, peak, err := nixPeak(tree, held, "eval", "--json", "--apply", apply, tree+"#checks."+system)
	if err != nil {
		return drvs, peak
	}
	var named map[string]any
	if json.Unmarshal([]byte(out), &named) != nil {
		return drvs, peak
	}
	for _, a := range attrs {
		if drv, ok := named[a].(string); ok && drv != "" {
			drvs[a] = drv
		}
	}
	return drvs, peak
}

// defaultSlots is how many nix build processes attest run starts at once
// when --slots does not say: one per CPU attest may run on.
var defaultSlots = runtime.NumCPU()

// runPure runs every pure check and returns their runs in the order given.
// It evaluates them all together (evalClosures), then builds each
// derivation that evaluated in a nix build of its own, slots of them at
// once, so the nix daemon builds them side by side (buildChecks).
// Checks that evaluate to the same derivation share its one build. With
// report, it hands each run to report in the order given, as soon as that
// check and every one before it have ended.
//
// Each check's outcome is its own derivation's build: the exit status and
// standard error of the nix build that built it, exactly as when checks
// were built one at a time. With probe, it first asks the nix store
// whether each output is already there, for the check's timing; without,
// it asks nothing about outputs.
//
// A check is timed from the start of the shared evaluation. Its duration
// is its evaluation, its probe, and its derivation's build. One that does
// not evaluate ends at its evaluation error.
//
// With an admission, the evaluation waits until its estimate fits the
// memory budget beside the evaluations admitted before it, and its
// reservation is removed when the evaluation ends, before the builds
// (admit). The wait is not part of any check's time: eval.end records it
// apart, as eval_wait_ms. An admission that fails is an error, and then
// nothing is evaluated or built: no evaluation runs without being counted.
// eval.end also records the evaluation's peak memory, as
// eval_max_rss_bytes, and its outcome, as eval_outcome (peak). With
// peakFile, the peak in bytes and the outcome are written there too, on
// one line.
//
// With roots, every output path a build prints is rooted under that
// directory (rootOutputs), whether nix built it or found it valid.
func runPure(tree, system string, specs []checkSpec, probe bool, slots int, roots string, adm *admission, peakFile string, report func(ranCheck), obs *observer) ([]ranCheck, error) {
	waited, held, release, err := admit(adm)
	if err != nil {
		return nil, err
	}
	began := time.Now()
	start := map[string]any{"checks": specNames(specs)}
	if adm != nil {
		start["eval_estimate_bytes"], start["eval_budget_bytes"] = adm.estimate, adm.budget
	}
	obs.emit("eval.start", "", start)
	evals, measured := evalClosures(tree, system, specs, held)
	end := map[string]any{"eval_ms": time.Since(began).Milliseconds(), "eval_wait_ms": waited.Milliseconds(), "eval_max_rss_bytes": measured.bytes}
	if measured.outcome != "" {
		end["eval_outcome"] = measured.outcome
	}
	obs.emit("eval.end", "", end)
	release()
	if peakFile != "" && measured.outcome != "" {
		if err := os.WriteFile(peakFile, []byte(strconv.FormatInt(measured.bytes, 10)+" "+measured.outcome+"\n"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "attest: warning: could not record the evaluation's peak memory: %v\n", err)
		}
	}
	runs := make([]ranCheck, len(specs))
	for i, spec := range specs {
		e := evals[i]
		runs[i] = ranCheck{out: checkOutcome{spec: spec}, began: began, took: e.took, eval: e.took, err: e.err}
		if e.err != nil {
			obs.emit("check.result", spec.name, map[string]any{"result": "error", "error": clipError(e.err.Error()), "eval_ms": e.took.Milliseconds()})
			continue
		}
		runs[i].out.derivation, runs[i].out.inputs = e.derivation, e.inputs
		if probe {
			probed := time.Now()
			runs[i].out.satisfied = outputsCached(e.drv)
			runs[i].took += time.Since(probed)
		}
		obs.name(e.drv, spec.name)
		obs.emit("check.queued", spec.name, map[string]any{"eval_ms": e.took.Milliseconds(), "probe_ms": (runs[i].took - e.took).Milliseconds()})
	}

	drvs := make([]string, len(evals))
	for i, e := range evals {
		drvs[i] = e.drv
	}
	build := func(drv string) (string, string, error) {
		return nix(tree, "build", "--no-link", "--print-out-paths", drv+"^*")
	}
	if obs != nil {
		build = func(drv string) (string, string, error) { return obs.nixBuild(tree, drv) }
	}
	if roots != "" {
		built := build
		build = func(drv string) (string, string, error) {
			out, errText, err := built(drv)
			rootOutputs(roots, out)
			return out, errText, err
		}
	}
	buildChecks(runs, drvs, slots, build, report, obs)
	return runs, nil
}

// rootOutputs registers an indirect garbage-collector root in dir for each
// store path in built, which is what nix build --print-out-paths printed.
// A root is a symlink named by its path's basename, so an output is rooted
// once however many trees use it. nix-store makes the symlink afresh each
// time, so its modification time is when the output was last used. The
// daemon records the root, so a user who is not root can add one. A root
// that cannot be added is warned about and changes no check's outcome.
func rootOutputs(dir, built string) {
	for _, path := range strings.Fields(built) {
		root := filepath.Join(dir, filepath.Base(path))
		if _, errText, err := nixStore(context.Background(), "--add-root", root, "--realise", path); err != nil {
			fmt.Fprintf(os.Stderr, "attest: warning: %s is not rooted: %v: %s\n", path, err, strings.TrimSpace(errText))
		}
	}
}

func specNames(specs []checkSpec) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.name
	}
	return names
}

// buildChecks builds the derivation of every run that has no error yet
// and decides each from its derivation's build (pureOutcome). drvs[i] is
// runs[i]'s derivation, and build builds one derivation, returning what it
// printed on standard output and standard error and how it ended. Builds
// start in the order of runs, at most slots of them at once, and runs that
// share a derivation share its one build. Each run is handed to report in
// order, as soon as it and every run before it have ended. With obs, each
// build's start and end and each run's result are recorded as they
// happen, whatever the order.
func buildChecks(runs []ranCheck, drvs []string, slots int, build func(drv string) (string, string, error), report func(ranCheck), obs *observer) {
	builds := map[string]*drvBuild{}
	users := map[string][]int{} // the runs each derivation decides
	pending := make([]bool, len(runs))
	var order []*drvBuild
	for i := range runs {
		if runs[i].err != nil {
			continue
		}
		pending[i] = true
		users[drvs[i]] = append(users[drvs[i]], i)
		if builds[drvs[i]] != nil {
			continue
		}
		b := &drvBuild{drv: drvs[i], done: make(chan struct{})}
		builds[drvs[i]] = b
		order = append(order, b)
	}
	free := make(chan struct{}, max(slots, 1))
	go func() {
		for _, b := range order {
			queued := time.Now()
			free <- struct{}{}
			wait := time.Since(queued)
			go func() {
				defer close(b.done)
				defer func() { <-free }()
				for _, i := range users[b.drv] {
					obs.emit("check.build-start", runs[i].out.spec.name, map[string]any{"slot_wait_ms": wait.Milliseconds()})
				}
				began := time.Now()
				b.paths, b.log, b.err = build(b.drv)
				b.took = time.Since(began)
				ended := time.Now()
				// Each run the build decides is decided as soon as it
				// ends, and its result recorded then; it is reported in
				// order below.
				for _, i := range users[b.drv] {
					runs[i].took += b.took
					runs[i].err = pureOutcome(b, &runs[i].out)
					if obs != nil {
						d := buildData(b, wait, began, ended, obs.logOf(b.drv))
						if obs.capacity != "" {
							d["capacity"] = obs.capacity
						}
						obs.emit("check.build-end", runs[i].out.spec.name, d)
						d["result"] = resultOf(runs[i])
						d["eval_ms"] = runs[i].eval.Milliseconds()
						d["check_ms"] = runs[i].took.Milliseconds()
						obs.emit("check.result", runs[i].out.spec.name, d)
					}
				}
			}()
		}
	}()
	for i := range runs {
		if pending[i] {
			<-builds[drvs[i]].done
		}
		if report != nil {
			report(runs[i])
		}
	}
}

// resultOf is a run's result as a timing and a record name it.
func resultOf(r ranCheck) string {
	switch {
	case r.err != nil:
		return "error"
	case !r.out.passed:
		return "failed"
	}
	return "passed"
}

// drvBuild is the nix build of one derivation: what it printed on standard
// output and on standard error, how it ended, and how long it took. done
// is closed once it has ended.
type drvBuild struct {
	drv   string
	done  chan struct{}
	paths string
	log   string
	err   error
	took  time.Duration
}

// pureOutcome decides one pure check from its derivation's build, and
// digests the output when the build succeeded. Building from the
// derivation path rather than the attribute a second time guarantees the
// thing built is the thing recorded. The build's standard error is the
// check's log. A build that failed with nothing on standard error, as when
// nix could not be started or was killed by a signal, gets the error that
// ended it as its log instead.
func pureOutcome(b *drvBuild, out *checkOutcome) error {
	out.log = strings.TrimSpace(b.log)
	if b.err != nil {
		if out.log == "" {
			out.log = b.err.Error()
		}
		return nil // a failed check is an outcome, not an error
	}
	paths := strings.Fields(b.paths)
	if len(paths) != 1 {
		return fmt.Errorf("%s produced %d outputs; a check attribute must produce exactly one", out.spec.name, len(paths))
	}
	entries, err := fsEntries(paths[0])
	if err != nil {
		return err
	}
	out.output = treeDigest(entries)
	out.passed = true
	return nil
}

// drvInputs collects the store paths a derivation reads: the derivations
// it depends on and the sources it takes directly. A .drv's references are
// exactly that set, and `nix-store --query --references` prints them as
// lines — a shape that has not changed in the lifetime of nix, unlike the
// versioned JSON of `nix derivation show`.
func drvInputs(drv string) ([]string, error) {
	out, errb, err := nixStore(context.Background(), "--query", "--references", drv)
	if err != nil {
		return nil, fmt.Errorf("querying the inputs of %s: %w: %s", drv, err, strings.TrimSpace(errb))
	}
	inputs := strings.Fields(out)
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%s has no inputs at all", drv)
	}
	return inputs, nil
}

// runEffectful runs a command over the exported tree and notarizes what it
// observed. No digests: there is nothing to re-derive, which is exactly
// what the effectful predicate type says.
func runEffectful(tree string, spec checkSpec, logTo io.Writer) checkOutcome {
	out := checkOutcome{spec: spec, satisfied: satisfiedRan}
	cmd := exec.Command("sh", "-c", spec.command)
	cmd.Dir = tree
	var log bytes.Buffer
	if logTo == nil {
		logTo = io.Discard
	}
	cmd.Stdout = io.MultiWriter(&log, logTo)
	cmd.Stderr = cmd.Stdout
	err := cmd.Run()
	out.log = strings.TrimSpace(log.String())
	out.observedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	out.passed = err == nil
	return out
}

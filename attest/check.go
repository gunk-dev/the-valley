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
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
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
	cmd := exec.Command("nix", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
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
// what the evaluation named. Nothing is built.
func evalClosure(tree, system string, spec checkSpec) (closure, error) {
	attr := tree + "#checks." + system + "." + spec.attribute
	drv, errb, err := nix(tree, "eval", "--raw", attr+".drvPath")
	if err != nil {
		return closure{}, fmt.Errorf("evaluating %s: %w: %s", attr, err, strings.TrimSpace(errb))
	}
	return closureOf(strings.TrimSpace(drv))
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
// whatever was done for it alone.
func evalClosures(tree, system string, specs []checkSpec) []evaluation {
	var attrs []string
	shared := map[string]bool{}
	for _, spec := range specs {
		if plainAttribute.MatchString(spec.attribute) && !shared[spec.attribute] {
			shared[spec.attribute] = true
			attrs = append(attrs, spec.attribute)
		}
	}
	began := time.Now()
	drvs := evalDrvPaths(tree, system, attrs)
	sharedTook := time.Since(began)

	evals := make([]evaluation, len(specs))
	for i, spec := range specs {
		began := time.Now()
		var c closure
		var err error
		if drv, ok := drvs[spec.attribute]; ok {
			c, err = closureOf(drv)
		} else {
			c, err = evalClosure(tree, system, spec)
		}
		evals[i] = evaluation{closure: c, err: err, took: time.Since(began)}
		if shared[spec.attribute] {
			evals[i].took += sharedTook
		}
	}
	return evals
}

// evalDrvPaths names the derivation of every check attribute given, in one
// nix evaluation. A check whose evaluation throws, or that is missing or
// is not a derivation, is left out of the answer. Some evaluation errors
// cannot be caught inside nix, such as a type error, and they end the
// whole evaluation; then the answer is empty, and every check is evaluated
// alone.
func evalDrvPaths(tree, system string, attrs []string) map[string]string {
	drvs := map[string]string{}
	if len(attrs) == 0 {
		return drvs
	}
	quoted := make([]string, len(attrs))
	for i, a := range attrs {
		quoted[i] = `"` + a + `"`
	}
	apply := `checks: builtins.listToAttrs (map (name: { inherit name; value = ` +
		`let r = builtins.tryEval (if checks ? ${name} && checks.${name} ? drvPath then checks.${name}.drvPath else null); ` +
		`in if r.success then r.value else null; }) [ ` + strings.Join(quoted, " ") + ` ])`
	out, _, err := nix(tree, "eval", "--json", "--apply", apply, tree+"#checks."+system)
	if err != nil {
		return drvs
	}
	var named map[string]any
	if json.Unmarshal([]byte(out), &named) != nil {
		return drvs
	}
	for _, a := range attrs {
		if drv, ok := named[a].(string); ok && drv != "" {
			drvs[a] = drv
		}
	}
	return drvs
}

// buildSlots is how many nix build processes runPure runs at once.
var buildSlots = runtime.NumCPU()

// runPure runs every pure check and returns their runs in the order given.
// It evaluates them all together (evalClosures), then builds each
// derivation that evaluated in a nix build of its own, buildSlots of them
// at once, so the nix daemon builds them side by side (buildChecks).
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
func runPure(tree, system string, specs []checkSpec, probe bool, report func(ranCheck)) []ranCheck {
	began := time.Now()
	evals := evalClosures(tree, system, specs)
	runs := make([]ranCheck, len(specs))
	for i, spec := range specs {
		e := evals[i]
		runs[i] = ranCheck{out: checkOutcome{spec: spec}, began: began, took: e.took, eval: e.took, err: e.err}
		if e.err != nil {
			continue
		}
		runs[i].out.derivation, runs[i].out.inputs = e.derivation, e.inputs
		if probe {
			probed := time.Now()
			runs[i].out.satisfied = outputsCached(e.drv)
			runs[i].took += time.Since(probed)
		}
	}

	drvs := make([]string, len(evals))
	for i, e := range evals {
		drvs[i] = e.drv
	}
	buildChecks(runs, drvs, buildSlots, func(drv string) (string, string, error) {
		return nix(tree, "build", "--no-link", "--print-out-paths", drv+"^*")
	}, report)
	return runs
}

// buildChecks builds the derivation of every run that has no error yet
// and decides each from its derivation's build (pureOutcome). drvs[i] is
// runs[i]'s derivation, and build builds one derivation, returning what it
// printed on standard output and standard error and how it ended. Builds
// start in the order of runs, at most slots of them at once, and runs that
// share a derivation share its one build. Each run is handed to report in
// order, as soon as it and every run before it have ended.
func buildChecks(runs []ranCheck, drvs []string, slots int, build func(drv string) (string, string, error), report func(ranCheck)) {
	builds := map[string]*drvBuild{}
	var order []*drvBuild
	for i := range runs {
		if runs[i].err != nil || builds[drvs[i]] != nil {
			continue
		}
		b := &drvBuild{drv: drvs[i], done: make(chan struct{})}
		builds[drvs[i]] = b
		order = append(order, b)
	}
	free := make(chan struct{}, max(slots, 1))
	go func() {
		for _, b := range order {
			free <- struct{}{}
			go func() {
				defer close(b.done)
				defer func() { <-free }()
				began := time.Now()
				b.paths, b.log, b.err = build(b.drv)
				b.took = time.Since(began)
			}()
		}
	}()
	for i := range runs {
		if runs[i].err == nil {
			b := builds[drvs[i]]
			<-b.done
			runs[i].took += b.took
			runs[i].err = pureOutcome(b, &runs[i].out)
		}
		if report != nil {
			report(runs[i])
		}
	}
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
func runEffectful(tree string, spec checkSpec) checkOutcome {
	out := checkOutcome{spec: spec, satisfied: satisfiedRan}
	cmd := exec.Command("sh", "-c", spec.command)
	cmd.Dir = tree
	log, err := cmd.CombinedOutput()
	out.log = strings.TrimSpace(string(log))
	out.observedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	out.passed = err == nil
	return out
}

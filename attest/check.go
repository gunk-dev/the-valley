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

// runPure runs every pure check together and returns their runs in the
// order given. It evaluates them all (evalClosures), then builds every
// derivation that evaluated in one nix build, so nix builds them side by
// side under the machine's own max-jobs and cores settings. --keep-going
// lets every build finish whatever another does.
//
// Each check's outcome is still its own, decided from the store after the
// build: it passed when every output of its derivation is there. Building
// from the derivation path rather than the attribute a second time
// guarantees the thing built is the thing recorded. With probe, it first
// asks the nix store whether each output is already there, for the check's
// timing; without, it asks nothing before the build.
//
// A check is timed from the start of the evaluation. One that does not
// evaluate ends there. Every other one ends when the whole build does,
// since nix gives no time for one derivation in a build of many.
func runPure(tree, system string, specs []checkSpec, probe bool) []ranCheck {
	began := time.Now()
	evals := evalClosures(tree, system, specs)
	runs := make([]ranCheck, len(specs))
	var drvs []string
	for i, spec := range specs {
		e := evals[i]
		runs[i] = ranCheck{out: checkOutcome{spec: spec}, began: began, took: e.took, eval: e.took, err: e.err}
		if e.err != nil {
			continue
		}
		runs[i].out.derivation, runs[i].out.inputs = e.derivation, e.inputs
		if probe {
			runs[i].out.satisfied = outputsCached(e.drv)
		}
		drvs = append(drvs, e.drv)
	}
	if len(drvs) == 0 {
		return runs
	}

	buildLog := buildAll(tree, drvs)
	for i := range runs {
		if runs[i].err == nil {
			runs[i].err = pureOutcome(tree, evals[i].drv, buildLog, &runs[i].out)
		}
	}
	took := time.Since(began)
	for i := range runs {
		if evals[i].err == nil {
			runs[i].took = took
		}
	}
	return runs
}

// buildAll builds every output of every derivation given in one nix build,
// and returns what nix wrote on standard error. Its exit status says
// nothing about any one check, and when any build fails nix prints no
// output path at all, so neither is read: each check's outcome is read
// from the store afterwards.
func buildAll(tree string, drvs []string) string {
	args := []string{"build", "--no-link", "--keep-going"}
	seen := map[string]bool{}
	for _, drv := range drvs {
		if !seen[drv] {
			seen[drv] = true
			args = append(args, drv+"^*")
		}
	}
	_, errb, err := nix(tree, args...)
	if log := strings.TrimSpace(errb); log != "" || err == nil {
		return log
	}
	return err.Error()
}

// pureOutcome decides one pure check after the shared build, and digests
// its output when it passed. A check whose outputs are not all in the
// store failed, and its log is the one nix kept for its derivation. A
// derivation nix holds no log for, such as one whose dependency failed so
// that it never started, gets the whole build's log instead.
func pureOutcome(tree, drv, buildLog string, out *checkOutcome) error {
	paths, built, err := storeOutputs(context.Background(), drv)
	if err != nil {
		return fmt.Errorf("%s: %w", out.spec.name, err)
	}
	if !built {
		log, _, err := nix(tree, "log", drv)
		out.log = strings.TrimSpace(log)
		if err != nil || out.log == "" {
			out.log = buildLog
		}
		return nil // a failed check is an outcome, not an error
	}
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

// storeOutputs names every output of a derivation and says whether all of
// them are in the local nix store.
func storeOutputs(ctx context.Context, drv string) ([]string, bool, error) {
	outputs, errb, err := nixStore(ctx, "--query", "--outputs", drv)
	if err != nil {
		return nil, false, fmt.Errorf("querying the outputs of %s: %w: %s", drv, err, strings.TrimSpace(errb))
	}
	paths := strings.Fields(outputs)
	if len(paths) == 0 {
		return nil, false, fmt.Errorf("%s names no outputs", drv)
	}
	invalid, errb, err := nixStore(ctx, append([]string{"--check-validity", "--print-invalid"}, paths...)...)
	if err != nil {
		return nil, false, fmt.Errorf("checking the outputs of %s: %w: %s", drv, err, strings.TrimSpace(errb))
	}
	return paths, strings.TrimSpace(invalid) == "", nil
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

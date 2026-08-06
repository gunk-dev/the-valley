package main

// Running a check, and the digests a run yields.
//
// The nix runner claims purity strongly: it builds a flake check
// derivation, so the check's inputs, the derivation itself and its output
// are all named, and any verifier holding the same tree can re-derive
// them. The command runner claims nothing beyond having run — its
// statement is a notarization naming the environment and the time
// (design/verification.md).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
// output a reader needs when it did not, and the digests of a pure run.
type checkOutcome struct {
	spec   checkSpec
	passed bool
	log    string

	inputs     string
	derivation string
	output     string
	observedAt string
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
	drv = strings.TrimSpace(drv)

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

// runPure builds one flake check over the exported tree and digests what
// the build named: the derivation's inputs, the derivation, and its
// output. Building from the derivation path rather than the attribute a
// second time guarantees the thing built is the thing recorded.
func runPure(tree, system string, spec checkSpec) (checkOutcome, error) {
	out := checkOutcome{spec: spec}
	c, err := evalClosure(tree, system, spec)
	if err != nil {
		return out, err
	}
	out.derivation, out.inputs = c.derivation, c.inputs

	built, buildLog, err := nix(tree, "build", "--no-link", "--print-out-paths", c.drv+"^*")
	out.log = strings.TrimSpace(buildLog)
	if err != nil {
		return out, nil // a failed check is an outcome, not an error
	}
	paths := strings.Fields(built)
	if len(paths) != 1 {
		return out, fmt.Errorf("%s produced %d outputs; a check attribute must produce exactly one", spec.name, len(paths))
	}
	entries, err := fsEntries(paths[0])
	if err != nil {
		return out, err
	}
	out.output = treeDigest(entries)
	out.passed = true
	return out, nil
}

// drvInputs collects the store paths a derivation reads: the derivations
// it depends on and the sources it takes directly. A .drv's references are
// exactly that set, and `nix-store --query --references` prints them as
// lines — a shape that has not changed in the lifetime of nix, unlike the
// versioned JSON of `nix derivation show`.
func drvInputs(drv string) ([]string, error) {
	cmd := exec.Command("nix-store", "--query", "--references", drv)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("querying the inputs of %s: %w: %s", drv, err, strings.TrimSpace(errb.String()))
	}
	inputs := strings.Fields(out.String())
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%s has no inputs at all", drv)
	}
	return inputs, nil
}

// runEffectful runs a command over the exported tree and notarizes what it
// observed. No digests: there is nothing to re-derive, which is exactly
// what the effectful predicate type says.
func runEffectful(tree string, spec checkSpec) checkOutcome {
	out := checkOutcome{spec: spec}
	cmd := exec.Command("sh", "-c", spec.command)
	cmd.Dir = tree
	log, err := cmd.CombinedOutput()
	out.log = strings.TrimSpace(string(log))
	out.observedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	out.passed = err == nil
	return out
}

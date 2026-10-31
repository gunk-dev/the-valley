package main

import (
	"fmt"
	"os"
)

// cmdInputs prints the input-closure digest of one or more pure checks over
// a revision's tree, without building anything. Every check asked for is
// evaluated in one nix evaluation where it can be (evalClosures).
//
// This is what an integrator needs and a contributor does not. The pure
// transfer rule of dcr-439b771 is closure-digest equality: the integrator
// recomputes each required check's closure over the rebased tree and
// compares it with the digest the attestation recorded. Equality means the
// rebased tree presents the same computation over the same inputs, so the
// recorded result is the result on the new base too. Inequality means that
// check, and only that check, is stale.
//
// Evaluating rather than building is the point. The serialized critical
// section at the commit point is digest comparison and a ref write; check
// latency never enters it.
func cmdInputs(args []string) error {
	fs := newFlagSet("inputs")
	repo := fs.String("repo", "", "repository")
	rev := fs.String("rev", "HEAD", "revision")
	system := fs.String("system", nixSystem(), "flake system")
	var checks repeatable
	fs.Var(&checks, "check", "flake check to digest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := repoRoot(*repo)
	if err != nil {
		return err
	}

	tree, err := os.MkdirTemp("", "valley-attest-inputs")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tree)
	if err := exportTree(root, *rev, tree); err != nil {
		return err
	}

	specs, err := checkSpecs(tree, *system, checks, nil)
	if err != nil {
		return err
	}
	// The checks are evaluated together, and printed in the order asked
	// for, up to the first that did not evaluate. Its error ends the
	// command, as when each check was evaluated in turn.
	for i, e := range evalClosures(tree, *system, specs) {
		if e.err != nil {
			return e.err
		}
		fmt.Printf("%s %s:%s sha256:%s\n", specs[i].name, inputsScheme, e.inputs, e.derivation)
	}
	return nil
}

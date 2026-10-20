package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// cmdRun is the whole protocol in order: digest the subject tree, run the
// checks, compose a statement per check, vet each against the schema,
// write each out and sign it as a note, store the notes at one ref keyed
// by the subject digest, and publish with a single atomic push. Any step
// failing stops the run before the next — a failed check publishes
// nothing, and neither does a statement the schema rejects. How long each
// check took is printed, and logged with --timings (timing.go), whatever
// the check's result.
func cmdRun(args []string) error {
	fs := newFlagSet("run")
	repo := fs.String("repo", "", "repository")
	rev := fs.String("rev", "HEAD", "revision")
	key := fs.String("key", "", "ed25519 private key")
	name := fs.String("name", defaultSignerName(), "the name that key signs under")
	environment := fs.String("environment", "local", "environment an effectful statement names")
	provenanceFile := fs.String("provenance", "", "json run provenance")
	schemaFlag := fs.String("schema", "", "attestation schema")
	system := fs.String("system", nixSystem(), "flake system")
	push := fs.String("push", "", "remote to publish to")
	branch := fs.String("branch", "", "branch pushed alongside")
	timings := fs.Bool("timings", false, "append each check's timing to the timing log")
	changeID := fs.String("change", "", "the change ID a timing names")
	changeVersion := fs.Int("change-version", 0, "the change version a timing names")
	var checks, commands, costs repeatable
	fs.Var(&checks, "check", "flake check to run")
	fs.Var(&commands, "command", "effectful check to run")
	fs.Var(&costs, "cost", "a check's declared cost class, for its timing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	classes, err := costClasses(costs)
	if err != nil {
		return err
	}

	root, err := repoRoot(*repo)
	if err != nil {
		return err
	}
	schema, err := schemaPath(*schemaFlag, root)
	if err != nil {
		return err
	}
	prov, err := loadProvenance(*provenanceFile)
	if err != nil {
		return err
	}

	// The signer is checked before any work is done: a run that can only
	// end in "no key" should say so before it builds anything.
	host, err := loadSigner(*key, *name)
	if err != nil {
		return err
	}

	subj, err := composeSubject(root, *rev)
	if err != nil {
		return err
	}
	fmt.Printf("subject %s:%s\n", subj.Primary, subj.Digest[subj.Primary])
	for _, alg := range sortedKeys(subj.Digest) {
		if alg != subj.Primary {
			fmt.Printf("        %s:%s (advisory)\n", alg, subj.Digest[alg])
		}
	}
	fmt.Printf("signer  %s\n", host.verifierKey())

	tree, err := os.MkdirTemp("", "valley-attest-tree")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tree)
	if err := exportTree(root, *rev, tree); err != nil {
		return err
	}

	specs, err := checkSpecs(tree, *system, checks, commands)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("no checks to run")
	}

	log := &timingLog{warn: timingStderr()}
	repository := ""
	if *timings {
		path, pathErr := timingsPath()
		if pathErr != nil {
			fmt.Fprintf(timingStderr(), "attest: warning: timings not logged: %v\n", pathErr)
		}
		log.path = path
		repository = repositoryName(root)
	}
	commit := subj.Digest["git-sha1"] + subj.Digest["git-sha256"]

	outcomes := make([]checkOutcome, 0, len(specs))
	failed := 0
	for _, spec := range specs {
		began := time.Now()
		var out checkOutcome
		var runErr error
		if spec.runner() == "nix" {
			out, runErr = runPure(tree, *system, spec, *timings)
		} else {
			out = runEffectful(tree, spec)
		}
		took := time.Since(began)
		result := "passed"
		switch {
		case runErr != nil:
			result = "error"
		case !out.passed:
			result = "failed"
			failed++
		}
		t := checkTiming{
			Time:       began.UTC().Format("2006-01-02T15:04:05.000Z"),
			Repository: repository,
			Change:     *changeID,
			Version:    *changeVersion,
			Head:       commit,
			Check:      spec.name,
			Runner:     spec.runner(),
			Cost:       classes[spec.name],
			Result:     result,
			Satisfied:  out.satisfied,
			DurationMS: took.Milliseconds(),
		}
		if t.Satisfied == "" {
			t.Satisfied = satisfiedUnknown
		}
		log.append(t)
		if runErr != nil {
			fmt.Fprintln(timingStdout(), timingLine(t, took))
			return runErr
		}
		fmt.Printf("check   %-24s %-8s %s\n", spec.name, spec.runner(), result)
		fmt.Fprintln(timingStdout(), timingLine(t, took))
		outcomes = append(outcomes, out)
	}
	if failed > 0 {
		for _, out := range outcomes {
			if !out.passed && out.log != "" {
				fmt.Fprintf(os.Stderr, "\n--- %s ---\n%s\n", out.spec.name, out.log)
			}
		}
		return fmt.Errorf("%d of %d checks failed; nothing published", failed, len(specs))
	}

	// Compose, validate, render. Every statement is vetted before any of
	// them is written out, so a malformed one cannot leave a half-signed
	// set behind. The schema is the gate and it reads JSON; the text is
	// what a signature covers, and it carries exactly what the JSON does.
	texts := make([][]byte, len(outcomes))
	for i, out := range outcomes {
		st := composeStatement(subj, out, *environment, prov)
		doc, err := json.Marshal(st)
		if err != nil {
			return err
		}
		if err := vetStatement(schema, doc); err != nil {
			return fmt.Errorf("%s: %w\nnothing published", out.spec.name, err)
		}
		if texts[i], err = renderJSON(doc); err != nil {
			return fmt.Errorf("%s: %w\nnothing published", out.spec.name, err)
		}
	}

	subtrees := map[string]string{}
	for i, out := range outcomes {
		note, err := signNote(texts[i], host)
		if err != nil {
			return err
		}
		noteBlob, err := writeBlob(root, note)
		if err != nil {
			return err
		}
		sub, err := mkTree(root, map[string]string{"statement.note": noteBlob}, nil)
		if err != nil {
			return err
		}
		subtrees[out.spec.name] = sub
	}
	top, err := mkTree(root, nil, subtrees)
	if err != nil {
		return err
	}
	ref := attestationRef(subj.Digest[subj.Primary], fmt.Sprintf("%08x", host.hash))
	if _, err := git(root, "update-ref", ref, top); err != nil {
		return err
	}
	fmt.Printf("stored  %s -> %s\n", ref, top)

	if *push == "" {
		return nil
	}
	head := *branch
	if head == "" {
		head, err = gitLine(root, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return fmt.Errorf("no branch to push: %w", err)
		}
	}
	head = strings.TrimPrefix(head, "refs/heads/")
	branchSpec := fmt.Sprintf("refs/heads/%s:refs/heads/%s", head, head)
	refSpec := ref + ":" + ref
	out, err := gitPush(root, "--atomic", *push, branchSpec, refSpec)
	if err != nil {
		return err
	}
	fmt.Printf("pushed  %s --atomic %s %s\n%s", *push, branchSpec, refSpec, out)
	return nil
}

// composeSubject builds the digest set: the content-addressed tree digest
// as the member that IS the identity, and the commit as an advisory member
// for finding the change.
func composeSubject(root, rev string) (subject, error) {
	entries, err := gitTreeEntries(root, rev)
	if err != nil {
		return subject{}, err
	}
	commit, err := gitLine(root, "rev-parse", rev+"^{commit}")
	if err != nil {
		return subject{}, err
	}
	subj := subject{Primary: treeScheme, Digest: map[string]string{treeScheme: treeDigest(entries)}}
	switch len(commit) {
	case 40:
		subj.Digest["git-sha1"] = commit
	case 64:
		subj.Digest["git-sha256"] = commit
	default:
		return subject{}, fmt.Errorf("unexpected object id %q", commit)
	}
	return subj, nil
}

func composeStatement(subj subject, out checkOutcome, environment string, prov json.RawMessage) statement {
	result := "failed"
	if out.passed {
		result = "passed"
	}
	st := statement{
		StatementType: statementType,
		Subject:       subj,
		Provenance:    prov,
		Predicate: predicate{
			Check: checkRun{
				Name:      out.spec.name,
				Runner:    out.spec.runner(),
				Attribute: out.spec.attribute,
				Command:   out.spec.command,
			},
			Result: result,
		},
	}
	if out.spec.runner() == "nix" {
		st.PredicateType = predicatePure
		st.Predicate.Inputs = map[string]string{inputsScheme: out.inputs}
		st.Predicate.Derivation = map[string]string{"sha256": out.derivation}
		st.Predicate.Output = map[string]string{treeScheme: out.output}
	} else {
		st.PredicateType = predicateEffect
		st.Predicate.Environment = environment
		st.Predicate.ObservedAt = out.observedAt
	}
	return st
}

// checkSpecs resolves what to run. Naming no check means the repo's whole
// canonical set: every check the flake defines for this system.
func checkSpecs(tree, system string, checks, commands repeatable) ([]checkSpec, error) {
	var specs []checkSpec
	for _, c := range checks {
		name, attr, found := strings.Cut(c, "=")
		if !found {
			attr = name
		}
		specs = append(specs, checkSpec{name: name, attribute: attr})
	}
	for _, c := range commands {
		name, command, found := strings.Cut(c, "=")
		if !found {
			return nil, fmt.Errorf("--command %q: expected NAME=COMMAND", c)
		}
		specs = append(specs, checkSpec{name: name, command: command})
	}
	if len(specs) > 0 {
		return specs, nil
	}
	names, err := flakeChecks(tree, system)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		specs = append(specs, checkSpec{name: n, attribute: n})
	}
	return specs, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

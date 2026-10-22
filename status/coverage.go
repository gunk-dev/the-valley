package main

// coverage says which of a head's required nix checks already have evidence
// on origin that approval can reuse, so that approve (bin/valley) runs only
// the rest. A check worker publishes such evidence, and so does an earlier
// approval.
//
// Only what origin holds counts. coverage lists origin's attestation refs
// for the head's tree, fetches the objects they name by ID, and reads the
// notes from those objects. It never reads a local ref, so a note that
// was stored here and never published is not mistaken for evidence the
// integrator will see. A listing or a fetch that fails is an error, never
// an empty answer.
//
// A check is covered when one of those refs holds a note for it that
// meets every one of these:
//
//   - attest verify accepts it: the note is in the one written form, it
//     satisfies the schema, a key in the known-signers file signed it, and
//     its subject is the head's tree;
//   - it is pure evidence, from the nix runner, for this check's name and
//     this check's flake attribute;
//   - it records the check as passed.
//
// The known-signers file is the one a transfer statement is verified with
// (signers.go): VALLEY_KNOWN_SIGNERS or the serving host's publication.
// With none, nothing is covered and every check runs.
//
// The attribute test is stricter than the integrator's rule for an
// unmoved tree, which compares no attribute. A command check is never
// covered: approve runs it again. The integrator judges every note again
// when it decides the request, so coverage decides only what approve
// builds, never what lands.
//
// The answer is one line per fact, fields separated by single spaces:
//
//	digest <valley-tree-v1 digest of the head's tree>
//	stored <ref> <object ID>   one per attestation ref origin holds for it
//	covered <check> <ref> <signer>+<key hash>
//	missing <check>
//
// with a covered or a missing line for every check asked about, in the
// order asked.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

const coverageUsage = "usage: valley-go coverage --head <sha> [--check <name>=<attribute>]..."

const predicatePure = "the-valley/check/pure/v1"

// checkName is a check's name or flake attribute as the policy schema
// admits one (#Name in schema/verification.cue).
var checkName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// nixCheck is one required check the nix runner runs: its name, and the
// flake attribute the policy builds it from.
type nixCheck struct{ name, attribute string }

func runCoverage(g git, args []string, w, stderr io.Writer) error {
	var head string
	var checks []nixCheck
	for i := 0; i < len(args); i++ {
		opt, value, inline := strings.Cut(args[i], "=")
		if opt != "--head" && opt != "--check" {
			return failure(coverageUsage)
		}
		if !inline {
			if i+1 == len(args) {
				return failure(coverageUsage)
			}
			i++
			value = args[i]
		}
		if opt == "--head" {
			head = value
			continue
		}
		name, attribute, ok := strings.Cut(value, "=")
		if !ok || !checkName.MatchString(name) || !checkName.MatchString(attribute) {
			return failure(fmt.Sprintf("--check %q: expected <name>=<attribute>", value))
		}
		checks = append(checks, nixCheck{name, attribute})
	}
	if head == "" {
		return failure(coverageUsage)
	}
	oid, err := g.output(true, "rev-parse", "--verify", "--quiet", "--end-of-options", head+"^{commit}")
	if err != nil || !isObjectID(oid) {
		return failure("no such commit: " + head)
	}
	head = oid
	repo := g.dir
	if repo == "" {
		repo = "."
	}

	out, err := attestTool(g, "digest", "--repo", repo, "--rev", head)
	digest, ok := strings.CutPrefix(strings.TrimSpace(out), "valley-tree-v1:")
	if err != nil || !ok || digest == "" {
		return failure("could not digest the tree of " + head)
	}

	// Every attestation ref origin holds for the tree, signer by signer.
	prefix := attestationRefs + digest + "/"
	o, err := listOrigin(g, prefix+"*")
	if err != nil {
		return failure("could not list the evidence origin holds for this tree")
	}
	listed := o.refs
	var stored []string
	for ref := range listed {
		if signer, ok := strings.CutPrefix(ref, prefix); ok && signer != "" && !strings.Contains(signer, "/") {
			stored = append(stored, ref)
		}
	}
	sort.Strings(stored)

	var lines []string
	lines = append(lines, "digest "+digest)
	for _, ref := range stored {
		lines = append(lines, "stored "+ref+" "+listed[ref])
	}
	covered, err := cover(g, repo, head, stored, listed, checks, stderr)
	if err != nil {
		return err
	}
	for _, c := range checks {
		if by, ok := covered[c.name]; ok {
			lines = append(lines, "covered "+c.name+" "+by)
		} else {
			lines = append(lines, "missing "+c.name)
		}
	}
	_, err = io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}

// cover is the checks the stored refs hold acceptable evidence for, each
// mapped to "<ref> <signer>". It fetches the refs' objects only when there
// are checks to look for.
func cover(g git, repo, head string, stored []string, listed map[string]string, checks []nixCheck, stderr io.Writer) (map[string]string, error) {
	covered := map[string]string{}
	if len(stored) == 0 || len(checks) == 0 {
		return covered, nil
	}
	var oids []string
	for _, ref := range stored {
		oids = append(oids, listed[ref])
	}
	if err := fetch(g, oids...); err != nil {
		return nil, failure("could not fetch the evidence origin holds for this tree")
	}
	keys, _ := verification.resolve()
	if keys == "" {
		fmt.Fprintf(stderr, "valley: no known-signers file to verify the evidence on origin with, so none of it is reused\n")
		return covered, nil
	}
	for _, c := range checks {
		for _, ref := range stored {
			blob, err := g.output(true, "rev-parse", "--verify", "--quiet", listed[ref]+":"+c.name+"/statement.note")
			if err != nil {
				continue // this signer attested no such check
			}
			note, err := g.output(true, "cat-file", "blob", blob)
			if err != nil {
				return nil, fmt.Errorf("could not read %s from %s", c.name, ref)
			}
			signer, why, err := accept(g, repo, head, keys, c, note+"\n")
			if err != nil {
				return nil, err
			}
			if why != "" {
				fmt.Fprintf(stderr, "valley: not reusing %s from %s: %s\n", c.name, ref, why)
				continue
			}
			covered[c.name] = ref + " " + signer
			break
		}
	}
	return covered, nil
}

// accept judges one note for one check. It returns the signer when the
// note is acceptable evidence for the check, and why not when it is not.
// The note is verified before anything is read out of its text, since
// statementValue is a lookup that is sound only over text attest verify
// has established is the written form of what it says.
func accept(g git, repo, head, keys string, c nixCheck, note string) (signer, why string, err error) {
	file, err := os.CreateTemp("", "valley-coverage-*.note")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(note)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", err
	}
	out, err := attestTool(g, "verify", "--note", file.Name(), "--known-keys", keys, "--repo", repo, "--rev", head)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", "", fmt.Errorf("could not run attest verify: %v", err)
		}
		return "", "attest verify refused it: " + refusal(out), nil
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "signature" && f[1] == "ok" {
			signer = f[2]
			break
		}
	}
	text := note[:strings.LastIndex(note, "\n\n")+1] // verified, so it holds the blank line
	for _, want := range [][2]string{
		{"predicateType", predicatePure},
		{"predicate.check.name", c.name},
		{"predicate.check.runner", "nix"},
		{"predicate.check.attribute", c.attribute},
		{"predicate.result", "passed"},
	} {
		if got := statementValue(text, want[0]); got != want[1] {
			return "", fmt.Sprintf("its %s is %q, not %q", want[0], got, want[1]), nil
		}
	}
	if signer == "" {
		return "", "attest verify named no signer", nil
	}
	return signer, "", nil
}

// attestTool runs the attest helper in g's repository and environment,
// and returns what it printed on stdout and stderr together.
func attestTool(g git, args ...string) (string, error) {
	cmd := exec.Command("attest", args...)
	cmd.Env = g.env
	cmd.Dir = g.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// statementValue reads one key's value from statement text, where each
// line is a key, one space and a value.
func statementValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			return v
		}
	}
	return ""
}

// refusal is the sentence attest refused with: the line it prefixes with
// its own name, or else the last line it printed.
func refusal(out string) string {
	last := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if why, ok := strings.CutPrefix(line, "attest: "); ok {
			return strings.TrimSpace(why)
		}
		if strings.TrimSpace(line) != "" {
			last = strings.TrimSpace(line)
		}
	}
	return last
}

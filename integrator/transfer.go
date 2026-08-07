package main

// What the integrator writes down when a change lands.
//
// Two things, side by side in one attestation ref per subject
// (dcr-439b771). The contributor's original notes gain the integrator's
// signature as sibling lines under the text they already carry — several
// parties attesting to one statement, which is the composition
// dcr-0de694f already provides. And the integrator's own transfer
// statement records what it decided: the change, the stream, the base the
// evidence was produced over, the policy the verdict was derived under, and
// the per-check verdicts.
//
// The two are separate statements because they are about different trees.
// A contributor's statement is about the tree it was produced over; the
// transfer statement is about the tree that landed. When nothing
// intervened those are the same tree and the two notes share one ref.

import (
	"the-valley/integrator/verdict"

	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const transferNote = "transfer.note"

// landedEvidence is the note set the integrator stores for a landing.
type landedEvidence struct {
	// refs maps a ref name to the blobs beneath it, keyed by their path.
	refs map[string]map[string]string
}

// composeTransfer builds the transfer statement, vets it against the
// attestation schema, renders it as the bytes a signature covers, and signs
// it. Nothing is stored before the schema passes: a statement no verifier
// could read is not a thing to publish.
func (in *integrator) composeTransfer(ch verdict.Change, v verdict.Verdict, landedTree, landedCommit, tip, policyDigest string) ([]byte, error) {
	type digestSet map[string]string
	type checkTransfer struct {
		Name     string    `json:"name"`
		Rule     string    `json:"rule"`
		Evidence digestSet `json:"evidence"`
		Inputs   digestSet `json:"inputs,omitempty"`
	}
	checks := make([]checkTransfer, 0, len(v.Checks))
	for _, cv := range v.Checks {
		entry := checkTransfer{Name: cv.Name, Rule: cv.Rule, Evidence: digestSet{"sha256": cv.Evidence}}
		if cv.Inputs != "" {
			entry.Inputs = digestSet{"valley-inputs-v1": cv.Inputs}
		}
		checks = append(checks, entry)
	}
	doc := map[string]any{
		"statementType": "the-valley/attestation/v1",
		"subject": map[string]any{
			"primary": "valley-tree-v1",
			"digest":  digestSet{"valley-tree-v1": landedTree, "git-sha1": landedCommit},
		},
		"predicateType": predicateTransfer,
		"predicate": map[string]any{
			"change": ch.ID,
			"target": ch.Target,
			"base":   digestSet{"valley-tree-v1": ch.Attested},
			"policy": digestSet{"sha256": policyDigest, "git-sha1": tip},
			"checks": checks,
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if err := in.vet(raw); err != nil {
		return nil, err
	}
	text, err := in.pipe(raw, in.attest, "render")
	if err != nil {
		return nil, fmt.Errorf("rendering the transfer statement: %w", err)
	}
	return in.sign(text)
}

// vet holds a composed statement to schema/attestation.cue before it is
// rendered or signed, exactly as attest holds its own.
func (in *integrator) vet(doc []byte) error {
	dir, err := scratch("valley-integrator-vet")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "statement.json"), doc, 0o644); err != nil {
		return err
	}
	cmd := exec.Command(in.cue, "vet", "-c", in.attestSchema, "statement.json")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("the transfer statement does not satisfy %s:\n%s", in.attestSchema, strings.TrimSpace(string(out)))
	}
	return nil
}

// sign turns statement text into a note, or adds the integrator's
// signature to a note that already carries one. `attest sign` decides which
// from the input, so both cases are the same call.
func (in *integrator) sign(document []byte) ([]byte, error) {
	return in.pipe(document, in.attest, "sign", "--key", in.key, "--name", in.signerName)
}

func (in *integrator) pipe(stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(string(stdin))
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}

// countersign builds the note set a landing stores: every transferring
// check's note with the integrator's sibling signature added, keyed by the
// subject it is about, plus the transfer statement keyed by the tree that
// landed.
func (in *integrator) countersign(ch verdict.Change, v verdict.Verdict, l landing, transfer []byte) (landedEvidence, error) {
	out := landedEvidence{refs: map[string]map[string]string{}}
	add := func(ref, path string, content []byte) error {
		blob, err := in.writeBlob(content)
		if err != nil {
			return err
		}
		if out.refs[ref] == nil {
			out.refs[ref] = map[string]string{}
		}
		out.refs[ref][path] = blob
		return nil
	}
	original := fmt.Sprintf("%s/%s/%s", attestationPrefix, ch.Attested, in.keyHash)
	for _, cv := range v.Checks {
		note, ok := l.notes[cv.Name]
		if !ok || !cv.Transferred {
			continue
		}
		signed, err := in.sign(note)
		if err != nil {
			return out, fmt.Errorf("countersigning %s: %w", cv.Name, err)
		}
		if err := add(original, cv.Name+"/"+statementNote, signed); err != nil {
			return out, err
		}
	}
	landed := fmt.Sprintf("%s/%s/%s", attestationPrefix, l.digest, in.keyHash)
	if err := add(landed, transferNote, transfer); err != nil {
		return out, err
	}
	return out, nil
}

// store writes the note set into the repository as one ref per subject.
// Deterministic by construction: a ref's value is a function of the notes
// beneath it and nothing else.
func (in *integrator) store(e landedEvidence) ([]string, error) {
	var written []string
	for ref := range e.refs {
		written = append(written, ref)
	}
	sort.Strings(written)
	for _, ref := range written {
		top, err := in.writeTree(e.refs[ref])
		if err != nil {
			return nil, err
		}
		if _, err := git(in.repo, "update-ref", ref, top); err != nil {
			return nil, err
		}
	}
	return written, nil
}

func (in *integrator) writeBlob(content []byte) (string, error) {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = in.repo
	cmd.Stdin = strings.NewReader(string(content))
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("storing a note: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// writeTree builds a tree from paths to blob ids, creating a subtree per
// path segment. Paths here are one or two segments deep — "transfer.note",
// "<check>/statement.note" — so the recursion is shallow by construction.
func (in *integrator) writeTree(blobs map[string]string) (string, error) {
	here := map[string]string{}
	under := map[string]map[string]string{}
	for path, blob := range blobs {
		dir, rest, nested := strings.Cut(path, "/")
		if !nested {
			here[path] = blob
			continue
		}
		if under[dir] == nil {
			under[dir] = map[string]string{}
		}
		under[dir][rest] = blob
	}
	var lines []string
	for name, blob := range here {
		lines = append(lines, fmt.Sprintf("100644 blob %s\t%s", blob, name))
	}
	for name, sub := range under {
		oid, err := in.writeTree(sub)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("040000 tree %s\t%s", oid, name))
	}
	sort.Strings(lines)
	cmd := exec.Command("git", "mktree")
	cmd.Dir = in.repo
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("building an attestation tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

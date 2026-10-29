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
//
// One tree can land more than once, as when a change lands and a later
// change reverts its revert. An attestation ref can therefore hold several
// transfer statements. Each statement is stored under the commit it
// records landing, at change.TransferPath. Storing a landing adds its notes
// to the ref and keeps every note already there that it does not
// overwrite: other landings' statements, and every other path.

import (
	"the-valley/change"
	"the-valley/integrator/verdict"

	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

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
	predicate := map[string]any{
		"change": ch.ID,
		"target": ch.Target,
		"base":   digestSet{"valley-tree-v1": ch.Attested},
		"policy": digestSet{"sha256": policyDigest, "git-sha1": tip},
	}
	// A policy that required nothing of this diff says so, rather than
	// carrying a list with nothing in it. An empty array has no lines and
	// so no written form (dcr-de9d996), so a statement composed that way
	// could be vetted and never signed — which is how a landing with no
	// required checks used to strand. The zero case is an ordinary
	// outcome, so it gets an ordinary line.
	if len(checks) == 0 {
		predicate["required"] = "none"
	} else {
		predicate["checks"] = checks
	}
	doc := map[string]any{
		"statementType": "the-valley/attestation/v1",
		"subject": map[string]any{
			"primary": "valley-tree-v1",
			"digest":  digestSet{"valley-tree-v1": landedTree, "git-sha1": landedCommit},
		},
		"predicateType": predicateTransfer,
		"predicate":     predicate,
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
// subject it is about, plus the transfer statement keyed by the landed tree
// and the commit it landed as.
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
	// A check's name never holds a dot, so no check's directory is named
	// change.TransferDir.
	if err := add(landed, change.TransferPath(l.commit), transfer); err != nil {
		return out, err
	}
	return out, nil
}

// evidenceUpdates builds the tree each ref of the note set moves to, and
// returns the ref updates in the order of the refs' names. Nothing is
// written to a ref here: the updates go into the landing's one ref
// transaction (refs.go). A ref that exists keeps every note this landing
// does not overwrite, so an earlier landing's transfer statement stays in
// place. Each update names the value the ref was read at, so a note
// written to the ref since is never dropped: the transaction fails, and is
// built again from what the ref holds then.
//
// A tree's value is a function of the notes beneath it and nothing else.
// A tree the push hook would refuse to read is not written, and nothing
// lands.
func (in *integrator) evidenceUpdates(e landedEvidence, zero string) ([]refUpdate, error) {
	var refs []string
	for ref := range e.refs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	var updates []refUpdate
	for _, ref := range refs {
		old, found, err := resolveRef(in.repo, ref)
		if err != nil {
			return nil, err
		}
		blobs := map[string]string{}
		if found {
			if blobs, err = in.readNotes(old); err != nil {
				return nil, err
			}
		} else {
			old = zero
		}
		for path, blob := range e.refs[ref] {
			blobs[path] = blob
		}
		sizes, err := in.blobSizes(blobs)
		if err != nil {
			return nil, err
		}
		if err := hookReadable(blobs, sizes, len(zero)/2); err != nil {
			return nil, fmt.Errorf("the evidence for %s cannot be stored: %w", ref, err)
		}
		top, err := in.writeTree(blobs)
		if err != nil {
			return nil, err
		}
		updates = append(updates, refUpdate{ref: ref, new: top, old: old})
	}
	return updates, nil
}

// hookReadable refuses a note set whose tree the push hook would refuse
// to read: a tree past change.AttestationDepth or any bound beside it.
// It counts what the hook's walk reads. Every entry of every tree counts,
// a directory's own entry in its parent included. A tree object's bytes
// are its entries as git stores them: the mode, a space, the name, a NUL
// and the raw object ID, hashSize bytes long. Every note counts once for
// each path it sits at. sizes holds each blob's size in bytes.
func hookReadable(blobs map[string]string, sizes map[string]int, hashSize int) error {
	paths := make([]string, 0, len(blobs))
	for path := range blobs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	read := 0                               // bytes the walk reads
	entries := map[string]map[string]bool{} // a directory's path → its names, each true for a tree
	for _, path := range paths {
		segments := strings.Split(path, "/")
		if len(segments) > change.AttestationDepth {
			return fmt.Errorf("%s would nest trees deeper than the %d the push hook reads", path, change.AttestationDepth)
		}
		size := sizes[blobs[path]]
		if size > change.AttestationNoteBytes {
			return fmt.Errorf("the note at %s would be %d bytes, and the push hook reads at most %d in one note", path, size, change.AttestationNoteBytes)
		}
		read += size
		dir := ""
		for i, name := range segments {
			if entries[dir] == nil {
				entries[dir] = map[string]bool{}
			}
			entries[dir][name] = i < len(segments)-1
			dir = strings.TrimPrefix(dir+"/"+name, "/")
		}
	}
	dirs := make([]string, 0, len(entries))
	for dir := range entries {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	total := 0
	for _, dir := range dirs {
		names := entries[dir]
		where := dir
		if where == "" {
			where = "the top of the tree"
		}
		if len(names) > change.AttestationTreeEntries {
			return fmt.Errorf("%s would hold %d entries, and the push hook reads at most %d in one tree", where, len(names), change.AttestationTreeEntries)
		}
		total += len(names)
		size := 0
		for name, tree := range names {
			mode := "100644"
			if tree {
				mode = "40000"
			}
			size += len(mode) + 1 + len(name) + 1 + hashSize
		}
		if size > change.AttestationTreeBytes {
			return fmt.Errorf("%s would be a tree of %d bytes, and the push hook reads at most %d in one tree", where, size, change.AttestationTreeBytes)
		}
		read += size
	}
	if total > change.AttestationEntries {
		return fmt.Errorf("its trees would hold %d entries, and the push hook reads at most %d in all", total, change.AttestationEntries)
	}
	if read > change.AttestationBytes {
		return fmt.Errorf("its trees and notes would come to %d bytes, and the push hook reads at most %d in all", read, change.AttestationBytes)
	}
	return nil
}

// blobSizes asks git, in one call, for the size in bytes of each blob
// blobs names.
func (in *integrator) blobSizes(blobs map[string]string) (map[string]int, error) {
	sizes := map[string]int{}
	var ids []string
	for _, id := range blobs {
		if _, seen := sizes[id]; !seen {
			sizes[id] = -1
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return sizes, nil
	}
	out, err := gitInput(in.repo, []byte(strings.Join(ids, "\n")+"\n"),
		"cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	if err != nil {
		return nil, fmt.Errorf("sizing the notes: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[1] != "blob" {
			return nil, fmt.Errorf("sizing the notes: git answered %q", line)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("sizing the notes: git answered %q", line)
		}
		sizes[fields[0]] = size
	}
	for _, id := range ids {
		if sizes[id] < 0 {
			return nil, fmt.Errorf("sizing the notes: git gave no size for %s", id)
		}
	}
	return sizes, nil
}

// readNotes lists the note blobs beneath an attestation ref's tree, keyed
// by their path.
func (in *integrator) readNotes(tree string) (map[string]string, error) {
	out, err := git(in.repo, "ls-tree", "-r", "-z", tree)
	if err != nil {
		return nil, err
	}
	blobs := map[string]string{}
	for _, entry := range strings.Split(out, "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		if fields := strings.Fields(meta); ok && len(fields) == 3 && fields[1] == "blob" {
			blobs[path] = fields[2]
		}
	}
	return blobs, nil
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
// path segment. Paths here are at most change.AttestationDepth segments
// deep — "<check>/statement.note", "transfer.d/<2 hex>/<2 hex>/<rest>.note",
// and in a ref written before statements were kept per landing,
// "transfer.note" — so the recursion is shallow by construction.
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

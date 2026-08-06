package main

// Reading a change's evidence. Every judgement about whether an attestation
// is what it claims to be is `attest verify`'s: the note's signatures, the
// text being the one written form of what it says, the statement satisfying
// the schema, and its subject digest being the tree in front of the
// verifier. This file runs that verifier and classifies what it said.
//
// Nothing here re-implements the envelope or the written form. What it does
// do is read values out of text that `attest verify` has already established
// is the written form of what it says — where a line is a key, one space,
// and a value, with no escapes. That is a lookup, not a parser, and it is
// sound only in that order: verify first, read second.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	attestationPrefix = "refs/the-valley/attestations"
	statementNote     = "statement.note"

	predicatePure     = "the-valley/check/pure/v1"
	predicateEffect   = "the-valley/check/effectful/v1"
	predicateTransfer = "the-valley/integration/transfer/v1"
)

// unknownSigner is the one sentence `attest verify` refuses a well-formed
// note with when nobody who signed it is a key the verifier holds. Every
// other refusal is a forgery as far as the integrator is concerned, so this
// classification fails closed: an unrecognised message is a rejection.
const unknownSigner = "no signature on this note is by a key the verifier holds"

// collectEvidence finds every attestation stored for a subject digest and
// classifies it. Attestations are stored one ref per signer
// (dcr-0de694f), so a subject may carry several notes for one check; the
// first admissible one is the evidence, and a note that does not verify at
// all is reported so the verdict can reject rather than quietly look past
// it.
func (in *integrator) collectEvidence(wt *worktree, rev, subject string) (map[string]Evidence, error) {
	refs, err := git(in.repo, "for-each-ref", "--format=%(refname)", attestationPrefix+"/"+subject)
	if err != nil {
		return nil, err
	}
	found := map[string]Evidence{}
	for _, ref := range strings.Fields(refs) {
		listing, err := git(in.repo, "ls-tree", "-r", "--name-only", ref)
		if err != nil {
			return nil, err
		}
		for _, entry := range strings.Fields(listing) {
			if path.Base(entry) != statementNote {
				continue
			}
			note, err := git(in.repo, "cat-file", "blob", ref+":"+entry)
			if err != nil {
				return nil, err
			}
			ev, err := in.classify(wt, rev, []byte(note))
			if err != nil {
				return nil, err
			}
			ev.ref, ev.entry = ref, entry
			// A check with an admissible note keeps it; otherwise the
			// first thing found stands, so a forgery is never hidden by a
			// good note arriving after it.
			if old, seen := found[ev.Check]; seen && old.Admissible == Admissible {
				continue
			}
			found[ev.Check] = ev
		}
	}
	return found, nil
}

// classify runs `attest verify` over one note and reads the statement back.
func (in *integrator) classify(wt *worktree, rev string, note []byte) (Evidence, error) {
	text, err := noteText(note)
	if err != nil {
		return Evidence{}, err
	}
	ev := Evidence{
		note:       note,
		text:       text,
		TextDigest: sha256hex(text),
		Check:      statementValue(text, "predicate.check.name"),
		Result:     statementValue(text, "predicate.result"),
	}
	switch statementValue(text, "predicateType") {
	case predicatePure:
		ev.Kind = Pure
		ev.Inputs = statementValue(text, "predicate.inputs.valley-inputs-v1")
	case predicateEffect:
		ev.Kind = Effectful
		if at := statementValue(text, "predicate.observedAt"); at != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z", at); err == nil {
				ev.ObservedAt = t
			}
		}
	}
	if ev.Check == "" {
		ev.Check = "(unnamed)"
	}

	file, err := os.CreateTemp("", "valley-integrator-note")
	if err != nil {
		return ev, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(note); err != nil {
		return ev, err
	}
	file.Close()

	cmd := exec.Command(in.attest, "verify",
		"--note", file.Name(),
		"--known-keys", in.knownSigners,
		"--schema", in.attestSchema,
		"--repo", wt.dir, "--rev", rev)
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		ev.Admissible = Admissible
		ev.Signer = verifiedSigner(string(out))
	case strings.Contains(string(out), unknownSigner):
		ev.Admissible = UnknownSigner
	default:
		ev.Admissible = Tampered
		ev.refusal = strings.TrimSpace(string(out))
	}
	return ev, nil
}

// verifiedSigner reads the name whose signature checked out, from the
// verifier's own report.
func verifiedSigner(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) == 3 && fields[0] == "signature" && fields[1] == "ok" {
			return fields[2]
		}
	}
	return ""
}

// noteText returns the bytes a signature covers: everything before the
// blank line that separates the text from the signature block. The last
// blank line wins, so text holding one of its own does not move the split
// — the rule dcr-de9d996 fixes.
func noteText(note []byte) ([]byte, error) {
	split := bytes.LastIndex(note, []byte("\n\n"))
	if split < 0 {
		return nil, fmt.Errorf("this is not a note: no blank line separates the text from its signatures")
	}
	return note[:split+1], nil
}

// statementValue reads one line's value out of statement text. Sound only
// because the form has no escapes and `attest verify` has established the
// text is the written form of what it says: one line, one key, one value.
func statementValue(text []byte, key string) string {
	for _, line := range strings.Split(string(text), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			return v
		}
	}
	return ""
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// treeDigest asks attest for a revision's valley tree digest. One
// implementation of the content-addressing scheme, in the program that
// defines it.
func (in *integrator) treeDigest(wt *worktree, rev string) (string, error) {
	out, err := exec.Command(in.attest, "digest", "--repo", wt.dir, "--rev", rev).Output()
	if err != nil {
		return "", fmt.Errorf("digesting %s: %w", short(rev), err)
	}
	digest := strings.TrimSpace(string(out))
	scheme, value, ok := strings.Cut(digest, ":")
	if !ok || scheme != "valley-tree-v1" {
		return "", fmt.Errorf("attest digest printed %q", digest)
	}
	return value, nil
}

// closures recomputes the input-closure digest of each pure check over the
// tree that would land. `attest inputs` evaluates and digests; it builds
// nothing, so this stays off the latency path the commit point serializes.
func (in *integrator) closures(wt *worktree, rev string, required []Required) (map[string]string, map[string]string) {
	got, bad := map[string]string{}, map[string]string{}
	names := make([]string, 0, len(required))
	byAttr := map[string]string{}
	for _, r := range required {
		if r.Runner != "nix" {
			continue
		}
		names = append(names, r.Name)
		byAttr[r.Name] = r.Attribute
	}
	sort.Strings(names)
	for _, name := range names {
		args := []string{"inputs", "--repo", wt.dir, "--rev", rev, "--check", name + "=" + byAttr[name]}
		if in.system != "" {
			args = append(args, "--system", in.system)
		}
		out, err := exec.Command(in.attest, args...).CombinedOutput()
		if err != nil {
			bad[name] = firstLine(string(out))
			continue
		}
		fields := strings.Fields(string(out))
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "valley-inputs-v1:") {
			bad[name] = "attest inputs printed " + firstLine(string(out))
			continue
		}
		got[name] = strings.TrimPrefix(fields[1], "valley-inputs-v1:")
	}
	return got, bad
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

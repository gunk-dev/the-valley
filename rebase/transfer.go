// How a snapshot reads and verifies the integrator's transfer statements,
// in this process.
//
// Only the attestation refs the integrator writes are read: those named
// for a key the known-signers file holds under the integrator's name
// (refs/the-valley/attestations/<tree>/<key hash>). A statement counts
// when all of these hold, which are the checks `attest verify --repo
// --rev` makes, each made here with the code attest makes it with:
//
//   - the note opens under every key the known-signers file holds, and a
//     signature by a key under the integrator's name verifies (note.Open);
//   - its text is the one written form of what it says (statement.Parse);
//   - every field the landing decision reads has its schema's form: the
//     predicate type, the change, the base digest set and the subject's
//     digest set (transferFields);
//   - its subject's valley-tree-v1 digest is that of the landing commit's
//     tree (statement.GitTree).
//
// attest verify also vets a statement against schema/attestation.cue. A
// read here does not: the integrator vets every transfer statement against
// that schema before it signs one (integrator/transfer.go), so a statement
// whose signature verifies as the integrator's passed it when it was made,
// and the fields a landing is decided on are checked here.
//
// What passes every content check is remembered (cache.go), so each
// landing's tree digests and merge are computed once per machine. The
// signature and the key that made it are checked on every read, so a key
// the known-signers file no longer holds stops its statements counting at
// once.

package rebase

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"the-valley/change"
	"the-valley/note"
	"the-valley/note/statement"
)

// transferStatement is one transfer statement found under the integrator's
// attestation refs, whose text is the written form of a transfer, with the
// values a landing is decided on.
type transferStatement struct {
	note   []byte
	change string // predicate.change: the request's change name
	base   string // predicate.base.valley-tree-v1: the tree the evidence was over
	landed string // subject.digest.git-sha1: the commit that landed
	tree   string // subject.digest.valley-tree-v1: that commit's tree
}

// keyring is the verifier a snapshot resolved: every key the known-signers
// file holds, the integrator's name, and the key hashes of the keys held
// under that name, as the integrator's attestation refs are named. ok is
// false when no statement can be verified: no keys, no name, no key under
// the name, or a name the signer projects file restricts.
type keyring struct {
	ok     bool
	known  []note.VerifierKey
	signer string
	hashes map[string]bool
}

// keyring resolves the verifier once, when statements are first needed.
func (s *Snapshot) keyring() keyring {
	if s.keys != nil {
		return *s.keys
	}
	v := s.verify
	if v.Resolve != nil {
		v.Keys, v.Signer, v.SignerProjects = v.Resolve()
	}
	k := keyring{signer: v.Signer, hashes: map[string]bool{}}
	if _, restricted := v.SignerProjects[v.Signer]; !restricted && v.Keys != "" && v.Signer != "" {
		if data, err := os.ReadFile(v.Keys); err == nil {
			if known, err := note.ParseVerifierKeys(data); err == nil {
				k.known = known
				for _, key := range known {
					if key.Name == v.Signer {
						k.hashes[fmt.Sprintf("%08x", key.Hash)] = true
					}
				}
			}
		}
	}
	k.ok = len(k.hashes) > 0
	s.keys = &k
	return k
}

// signedBy is the integrator's key that signed a note: the note opens
// under every known key, as attest verify opens it, and a signature by a
// key under the integrator's name verifies.
func (k keyring) signedBy(n []byte) (note.VerifierKey, bool) {
	_, checked, err := note.Open(n, k.known)
	if err != nil {
		return note.VerifierKey{}, false
	}
	for _, c := range checked {
		if !c.Verified || c.Name != k.signer {
			continue
		}
		for _, key := range k.known {
			if key.Name == c.Name && key.Hash == c.Hash {
				return key, true
			}
		}
	}
	return note.VerifierKey{}, false
}

// transferStatements reads every transfer statement under the integrator's
// attestation refs, once. A transfer statement is a note at
// change.TransferPath of a landed commit, under the transfer.d directory
// at the top of an attestation ref's tree, or a single transfer.note at
// the top of the tree, as a ref written before statements were kept per
// landing holds it. Anything unreadable, or not the written form of a
// transfer, is skipped: what is not read is not believed. When origin
// holds attestation refs and the verifier cannot verify a statement, none
// is read, and the snapshot is unverified.
func (s *Snapshot) transferStatements() []transferStatement {
	if s.read {
		return s.transfers
	}
	s.read = true
	defer s.g.phase("transfers")()
	any := false
	for ref := range s.Refs {
		any = any || strings.HasPrefix(ref, AttestationRefs)
	}
	if !any {
		return nil
	}
	k := s.keyring()
	if !k.ok {
		s.unverified = true
		return nil
	}
	var trees []string
	for ref, oid := range s.Refs {
		if rest, ok := strings.CutPrefix(ref, AttestationRefs); ok {
			if _, hash, ok := strings.Cut(rest, "/"); ok && k.hashes[hash] {
				trees = append(trees, oid)
			}
		}
	}
	if len(trees) == 0 {
		return nil
	}
	sort.Strings(trees)
	if err := Fetch(s.ctx, s.g, trees...); err != nil {
		s.unverified = true
		return nil
	}
	var query bytes.Buffer
	for _, tree := range trees {
		fmt.Fprintf(&query, "%s:%s\n%s:%s\n", tree, change.TransferNote, tree, change.TransferDir)
	}
	out, err := s.g.input(s.ctx, true, &query, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		s.unverified = true
		return nil
	}
	type found struct{ oid, kind string }
	var at []found
	var dirs []string
	for _, line := range strings.Split(out, "\n") {
		oid, kind, _ := strings.Cut(line, " ")
		at = append(at, found{oid, kind})
		if kind == "tree" {
			dirs = append(dirs, oid)
		}
	}
	listed := s.transferBlobs(dirs)
	var blobs []string
	for _, f := range at {
		switch f.kind {
		case "blob":
			blobs = append(blobs, f.oid)
		case "tree":
			notes, ok := listed[f.oid]
			if !ok {
				s.unverified = true
				continue
			}
			blobs = append(blobs, notes...)
		}
	}
	read := s.g.blobs(s.ctx, blobs)
	for _, oid := range blobs {
		data, ok := read[oid]
		if !ok {
			var err error
			if data, err = s.g.output(s.ctx, true, "cat-file", "blob", oid); err != nil {
				continue
			}
		}
		if t, ok := readTransfer([]byte(data + "\n")); ok {
			s.transfers = append(s.transfers, t)
		}
	}
	return s.transfers
}

// transferBlobs lists, for each transfer.d tree, the blobs at the paths
// change.TransferPath gives, in the order `ls-tree -r` lists them. The
// three levels of the directory are read with one `cat-file --batch` each,
// for every tree at once. A tree some object of which does not read is
// left out.
func (s *Snapshot) transferBlobs(dirs []string) map[string][]string {
	levels := [3]map[string][]treeEntry{}
	want := dirs
	for i := range levels {
		levels[i] = s.g.trees(s.ctx, want)
		want = nil
		for _, entries := range levels[i] {
			for _, e := range entries {
				if e.mode == "40000" && i < 2 {
					want = append(want, e.oid)
				}
			}
		}
	}
	listed := map[string][]string{}
	for _, dir := range dirs {
		var notes []string
		ok := true
		top, read := levels[0][dir]
		ok = ok && read
		for _, a := range top {
			if a.mode != "40000" {
				continue
			}
			mid, read := levels[1][a.oid]
			ok = ok && read
			for _, b := range mid {
				if b.mode != "40000" {
					continue
				}
				leaves, read := levels[2][b.oid]
				ok = ok && read
				for _, c := range leaves {
					if c.mode != "40000" && c.mode != "160000" && isTransferPath(a.name+"/"+b.name+"/"+c.name) {
						notes = append(notes, c.oid)
					}
				}
			}
		}
		if ok {
			listed[dir] = notes
		}
	}
	return listed
}

// isTransferPath reports whether a path within the transfer.d directory is
// where change.TransferPath puts the statement for some commit:
// <2 hex>/<2 hex>/<rest>.note, the three parts a full object ID.
func isTransferPath(path string) bool {
	parts := strings.Split(strings.TrimSuffix(path, ".note"), "/")
	if len(parts) != 3 || len(parts[0]) != 2 || len(parts[1]) != 2 || !strings.HasSuffix(path, ".note") {
		return false
	}
	return IsObjectID(parts[0] + parts[1] + parts[2])
}

// readTransfer reads a note as a transfer statement: a note whose text is
// the written form of a transfer, every field of which a landing is
// decided on has its schema's form. Its signature is not checked here.
func readTransfer(n []byte) (transferStatement, bool) {
	text, _, err := note.Split(n)
	if err != nil {
		return transferStatement{}, false
	}
	doc, err := statement.Parse(text)
	if err != nil {
		return transferStatement{}, false
	}
	t, ok := transferFields(doc)
	t.note = n
	return t, ok
}

// transferFields reads the fields a landing is decided on from a parsed
// statement, held to schema/attestation.cue's forms for them: the
// predicate type is a transfer's; the change is a line, as the parse
// already holds every value; the base is a digest set holding a
// valley-tree-v1 digest; and the subject's primary is valley-tree-v1, in a
// digest set holding that digest and the commit's git-sha1 ID.
func transferFields(doc map[string]any) (transferStatement, bool) {
	if doc["predicateType"] != statement.PredicateTransfer {
		return transferStatement{}, false
	}
	predicate, _ := doc["predicate"].(map[string]any)
	subject, _ := doc["subject"].(map[string]any)
	name, _ := predicate["change"].(string)
	base, okBase := digestSet(predicate["base"])
	digests, okSubject := digestSet(subject["digest"])
	t := transferStatement{change: name, base: base[statement.TreeScheme], landed: digests["git-sha1"], tree: digests[statement.TreeScheme]}
	ok := name != "" && okBase && okSubject && subject["primary"] == statement.TreeScheme &&
		t.base != "" && t.landed != "" && t.tree != ""
	return t, ok
}

// digestForms are the schemes a digest set may hold, as #DigestSet closes
// it, and the length of each one's lowercase hex value.
var digestForms = map[string]int{
	statement.TreeScheme: 64, "valley-inputs-v1": 64, "sha256": 64, "git-sha1": 40, "git-sha256": 64,
}

// digestSet reads a digest set: an object whose every member is a scheme
// #DigestSet names, with a value of its form.
func digestSet(v any) (map[string]string, bool) {
	members, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	set := map[string]string{}
	for scheme, value := range members {
		s, ok := value.(string)
		if n, known := digestForms[scheme]; !ok || !known || !isHex(s, n) {
			return nil, false
		}
		set[scheme] = s
	}
	return set, true
}

// verifierVersion is the version of the checks verified makes. A
// remembered landing names it, so one remembered under other checks is
// verified again. Bump it whenever what verified checks changes.
const verifierVersion = "1"

// verified reports whether t, a statement whose records bind head to it,
// is the integrator's statement that head landed as t.landed: signed by a
// key the known-signers file holds under the integrator's name, with the
// head's tree as its base and the landing commit's as its subject, and
// with the integrator's merge of head onto the landing commit's parent
// giving that tree. A landing that passed every check is remembered, and
// only its signature is checked again.
func (s *Snapshot) verified(t transferStatement, head string) bool {
	defer s.g.phase("transfers")()
	key, ok := s.keyring().signedBy(t.note)
	if !ok {
		return false
	}
	sum := sha256.Sum256(t.note)
	remembered := fmt.Sprintf("the-valley transfer landing %s\nnote %s\nlanded %s\nhead %s\nrequest %s\nkey %s\n",
		verifierVersion, hex.EncodeToString(sum[:]), t.landed, head, t.change, key)
	if s.cache.landing(remembered) {
		return true
	}
	digest, ok := s.digest(head)
	if !ok {
		s.unverified = true
		return false
	}
	if t.base != digest {
		return false
	}
	if tree, ok := s.digest(t.landed); !ok || tree != t.tree || !s.mergesTo(head, t.landed) {
		return false
	}
	s.cache.putLanding(remembered)
	return true
}

// digest is the valley-tree-v1 digest of a commit's tree, read with
// replacement refs off, so the tree is the one the commit names.
func (s *Snapshot) digest(commit string) (string, bool) {
	if d, ok := s.digestOf[commit]; ok {
		return d, d != ""
	}
	git := func(args ...string) *exec.Cmd {
		return s.g.command(s.ctx, append([]string{"--no-replace-objects"}, args...)...)
	}
	d := ""
	if tree, err := s.g.output(s.ctx, true, "--no-replace-objects", "rev-parse", "--verify", "--quiet", commit+"^{tree}"); err == nil && IsObjectID(tree) {
		if cached, ok := s.cache.tree(tree); ok {
			d = cached
		} else if entries, err := statement.GitTree(git, tree); err == nil {
			d = statement.TreeDigest(entries)
			s.cache.putTree(tree, d)
		}
	}
	s.digestOf[commit] = d
	return d, d != ""
}

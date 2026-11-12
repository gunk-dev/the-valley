package rebase

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"the-valley/change"
	"the-valley/note"
	"the-valley/note/statement"
)

// signer is a key that signs notes under a name, as the integrator and the
// check worker do.
type signer struct {
	name string
	priv ed25519.PrivateKey
	key  note.VerifierKey
}

func newSigner(t *testing.T, name string) signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return signer{name: name, priv: priv, key: note.NewVerifierKey(name, pub)}
}

func (s signer) hash() string { return fmt.Sprintf("%08x", s.key.Hash) }

// host writes to the test's origin what a valley host's integrator writes
// when it lands a change: the landing commit on main, the transfer
// statement under an attestation ref, the landing record and the outcome
// record. Nothing here starts attest.
type host struct {
	w          *world
	integrator signer
	worker     signer
	refs       map[string]map[string]string // attestation ref → path → blob
}

func newHost(w *world) *host {
	return &host{w: w, integrator: newSigner(w.t, "valley-host.example.org/attestations"),
		worker: newSigner(w.t, "worker/attestations"), refs: map[string]map[string]string{}}
}

// origin runs git in the origin repository, with stdin and more
// environment, and returns its output.
func (h *host) origin(stdin []byte, env []string, args ...string) string {
	h.w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = h.w.origin, append(append([]string{}, h.w.env...), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.w.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// digest is the valley-tree-v1 digest of a commit's tree on origin.
func (h *host) digest(commit string) string {
	h.w.t.Helper()
	entries, err := statement.GitTree(func(args ...string) *exec.Cmd {
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = h.w.origin, h.w.env
		return cmd
	}, commit)
	if err != nil {
		h.w.t.Fatal(err)
	}
	return statement.TreeDigest(entries)
}

// land lands head onto origin's main as the integrator does: one new
// commit whose tree is the merge of head onto main's tip, and whose one
// parent is that tip. It returns the landing commit.
func (h *host) land(head string) string {
	h.w.t.Helper()
	tip := h.w.at("refs/heads/main")
	base := h.origin(nil, nil, "merge-base", tip, head)
	tree, _, _ := strings.Cut(h.origin(nil, nil, "merge-tree", "--write-tree", "--merge-base="+base, tip, head), "\n")
	landed := h.origin(nil, nil, "commit-tree", tree, "-p", tip, "-m", "land "+short(head))
	h.origin(nil, nil, "update-ref", "refs/heads/main", landed, tip)
	return landed
}

// transfer is the written form of a transfer statement saying head's tree
// landed as landed under the request name.
func (h *host) transfer(name, head, landed, policy string) []byte {
	h.w.t.Helper()
	text, err := statement.Render(map[string]any{
		"statementType": statement.Type,
		"subject": map[string]any{
			"primary": statement.TreeScheme,
			"digest":  map[string]any{statement.TreeScheme: h.digest(landed), "git-sha1": landed},
		},
		"predicateType": statement.PredicateTransfer,
		"predicate": map[string]any{
			"change":   name,
			"target":   "refs/heads/main",
			"base":     map[string]any{statement.TreeScheme: h.digest(head)},
			"policy":   map[string]any{"sha256": policy},
			"required": "none",
		},
	})
	if err != nil {
		h.w.t.Fatal(err)
	}
	return text
}

func (s signer) sign(t *testing.T, text []byte) []byte {
	t.Helper()
	n, err := note.Sign(text, s.name, s.priv)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// store keeps a note at change.TransferPath of landed, under the
// attestation ref for landed's tree and the key hash given, keeping every
// note the ref already holds.
func (h *host) store(hash, landed string, n []byte) {
	h.w.t.Helper()
	ref := AttestationRefs + h.digest(landed) + "/" + hash
	if h.refs[ref] == nil {
		h.refs[ref] = map[string]string{}
	}
	h.refs[ref][change.TransferPath(landed)] = h.origin(n, nil, "hash-object", "-w", "--stdin")
	index := filepath.Join(h.w.t.TempDir(), "index")
	var paths []string
	for p := range h.refs[ref] {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		h.origin(nil, []string{"GIT_INDEX_FILE=" + index}, "update-index", "--add", "--cacheinfo", "100644,"+h.refs[ref][p]+","+p)
	}
	h.origin(nil, nil, "update-ref", ref, h.origin(nil, []string{"GIT_INDEX_FILE=" + index}, "write-tree"))
}

// record writes the landing record for head.
func (h *host) record(head, landed, name string) {
	h.w.t.Helper()
	l := change.Landing{Landed: landed, Request: name, Head: head}
	blob := h.origin(l.Format(), nil, "hash-object", "-w", "--stdin")
	tree := h.origin([]byte("100644 blob "+blob+"\t"+change.LandingFile+"\n"), nil, "mktree")
	commit := h.origin(nil, nil, "commit-tree", tree, "-m", "landing")
	h.origin(nil, nil, "update-ref", LandingPrefix+head, commit)
}

// outcome writes the outcome record land for head under the request name.
func (h *host) outcome(name, head, tip string) {
	h.w.t.Helper()
	blob := h.origin([]byte(head+" "+tip+" 0123456789abcdef land\n"), nil, "hash-object", "-w", "--stdin")
	h.origin(nil, nil, "update-ref", OutcomePrefix+name, blob)
}

// keys writes a known-signers file holding the keys given.
func keysFile(t *testing.T, keys ...note.VerifierKey) string {
	t.Helper()
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k.String() + "\n")
	}
	path := filepath.Join(t.TempDir(), "known-signers")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The changes a landings world holds, by ID.
var (
	idRecorded = strings.Repeat("m", 32) // landed, with a landing record
	idOutcome  = strings.Repeat("n", 32) // landed before landing records: its outcome record binds it
	idTwoHeads = strings.Repeat("p", 32) // landed as v1; v2 has v1's tree on another parent; v3's landing record names v1's landing
	idForged   = strings.Repeat("q", 32) // its records bind it, and only the worker signed a statement
	idDirect   = strings.Repeat("r", 32) // its head is on main
	idOpen     = strings.Repeat("s", 32) // not landed
)

// landings is a world whose origin holds several landings by the
// integrator, each a different case of the landing rules.
type landings struct {
	*world
	host *host
	// statements is each recorded landing's statement as the integrator
	// signed and stored it: the landing commit and the request name.
	statements map[string][2]string
}

func newLandings(t *testing.T) *landings {
	t.Helper()
	w := newWorld(t)
	h := newHost(w)
	l := &landings{world: w, host: h, statements: map[string][2]string{}}
	base := w.at("refs/heads/main")

	recorded := w.layer("recorded", base)
	outcome := w.layer("outcome", base)
	v1 := w.layer("two-heads", base)
	forged := w.layer("forged", base)
	w.layer("open", base)
	// v2 has v1's tree on another parent: an honest mix-up the records
	// and the merge tell apart from v1's landing.
	w.git("checkout", "--quiet", "-b", "two-heads-v2", base)
	w.commit("another parent", "other.txt", "other\n")
	w.git("checkout", "--quiet", v1, "--", ".")
	w.git("rm", "--quiet", "other.txt")
	w.git("commit", "--quiet", "-m", "v1's tree")
	v2 := w.git("rev-parse", "HEAD")
	w.git("push", "--quiet", "origin", "two-heads-v2")
	if w.git("rev-parse", v1+"^{tree}") != w.git("rev-parse", v2+"^{tree}") {
		t.Fatal("the two heads have different trees")
	}
	// v3 has another tree, and a landing record binds it to v1's landing
	// commit and request name: the statement's checks for v1 do not hold
	// for it, though the integrator remembered them for v1.
	w.git("checkout", "--quiet", "-b", "two-heads-v3", base)
	v3 := w.commit("another tree", "two-heads.txt", "v3\n")
	w.git("push", "--quiet", "origin", "two-heads-v3")
	for id, branch := range map[string]string{idRecorded: "recorded", idOutcome: "outcome", idTwoHeads: "two-heads", idForged: "forged", idOpen: "open"} {
		w.ready(id, branch)
	}
	r := w.record(idTwoHeads)
	old := w.at(change.Ref(Target, idTwoHeads))
	r.Versions = append(r.Versions,
		change.Version{N: 2, Head: change.HeadPrefix + v2, Title: "v2", Branch: "two-heads", Uploader: "author"},
		change.Version{N: 3, Head: change.HeadPrefix + v3, Title: "v3", Branch: "two-heads", Uploader: "author"})
	r.Active, r.Seq = 3, r.Seq+1
	w.write(idTwoHeads, r, old, old, v2, v3)

	policy := strings.Repeat("ab", 32)
	for _, c := range []struct {
		head, name string
		record     bool
	}{{recorded, "recorded", true}, {outcome, "outcome", false}, {v1, "two-heads", true}} {
		tip := w.at("refs/heads/main")
		landed := h.land(c.head)
		h.store(h.integrator.hash(), landed, h.integrator.sign(t, h.transfer(c.name, c.head, landed, policy)))
		if c.record {
			h.record(c.head, landed, c.name)
		}
		h.outcome(c.name, c.head, tip)
		l.statements[c.head] = [2]string{landed, c.name}
	}
	h.record(v3, l.statements[v1][0], "two-heads")
	// The worker signs a statement for the forged change's landing, and
	// stores it under its own key hash and under the integrator's.
	landed := h.land(forged)
	text := h.transfer("forged", forged, landed, policy)
	h.store(h.worker.hash(), landed, h.worker.sign(t, text))
	h.store(h.integrator.hash(), landed, h.worker.sign(t, text))
	h.record(forged, landed, "forged")

	// A head written to main directly.
	w.git("fetch", "--quiet", "origin")
	w.git("checkout", "--quiet", "-b", "direct", "origin/main")
	w.commit("direct", "direct.txt", "direct\n")
	w.git("push", "--quiet", "origin", "direct", "direct:main")
	w.ready(idDirect, "direct")
	return l
}

// kinds reads a snapshot with v and returns every version's landing
// evidence and commit, and whether the snapshot is unverified.
func (l *landings) kinds(v Verifier) (map[string]string, bool) {
	l.t.Helper()
	s, err := ReadSnapshot(context.Background(), l.g, v)
	if err != nil {
		l.t.Fatal(err)
	}
	got := map[string]string{}
	for _, n := range s.Changes {
		for _, ver := range n.Record.Versions {
			kind, commit := s.Landing(n.ID, ver)
			got[fmt.Sprintf("%s v%d", n.ID[:1], ver.N)] = kind + " " + short(commit)
		}
	}
	return got, s.Unverified()
}

// verifier is the verifier the integrator's statements are believed
// under: both keys known, the integrator's name, and the cache given.
func (l *landings) verifier(cache string) Verifier {
	return Verifier{Keys: keysFile(l.t, l.host.integrator.key, l.host.worker.key), Signer: l.host.integrator.name, Cache: cache}
}

// shim puts git, attest and cue on PATH as programs that log each call
// to the file it returns; git then runs the real git, and attest and cue
// fail.
func shim(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	for name, then := range map[string]string{"git": "exec '" + real + "' \"$@\"", "attest": "exit 1", "cue": "exit 1"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> '%s'\n%s\n", name, calls, then)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// calls reads the shim's log and empties it.
func calls(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	os.Remove(path)
	return string(data)
}

var wantKinds = map[string]string{
	"m v1": LandedRecorded,
	"n v1": LandedRecorded,
	"p v1": LandedRecorded,
	"p v2": NotLanded,
	"p v3": LandingUncertain,
	"q v1": LandingUncertain,
	"r v1": Landed,
	"s v1": NotLanded,
}

// A landing reads the same with an empty cache, a warm one and none: a
// landing with a landing record, one made before landing records, a head
// with another head's tree on another parent, a head with another tree
// that a landing record binds to a remembered landing, and a landing only
// a non-integrator key signed for. No read starts attest or cue, and a warm
// read recomputes no merge.
func TestALandingReadsTheSameColdWarmAndWithoutACache(t *testing.T) {
	l := newLandings(t)
	log := shim(t)
	cache := filepath.Join(t.TempDir(), "cache")

	cold, coldUnverified := l.kinds(l.verifier(cache))
	coldCalls := calls(t, log)
	warm, warmUnverified := l.kinds(l.verifier(cache))
	warmCalls := calls(t, log)
	none, noneUnverified := l.kinds(l.verifier(""))
	noneCalls := calls(t, log)

	for key, want := range wantKinds {
		if kind, _, _ := strings.Cut(cold[key], " "); kind != want {
			t.Errorf("%s reads %s cold, want %s", key, cold[key], want)
		}
	}
	if fmt.Sprint(cold) != fmt.Sprint(warm) || fmt.Sprint(cold) != fmt.Sprint(none) {
		t.Errorf("the reads disagree:\n cold %v\n warm %v\n none %v", cold, warm, none)
	}
	if coldUnverified || warmUnverified || noneUnverified {
		t.Errorf("a read with the integrator's key is unverified: %v %v %v", coldUnverified, warmUnverified, noneUnverified)
	}
	for name, log := range map[string]string{"cold": coldCalls, "warm": warmCalls, "uncached": noneCalls} {
		if strings.Contains(log, "\nattest ") || strings.HasPrefix(log, "attest ") || strings.Contains(log, "\ncue ") || strings.HasPrefix(log, "cue ") {
			t.Errorf("a %s read started attest or cue:\n%s", name, log)
		}
	}
	if n := strings.Count(coldCalls, "merge-tree"); n != 3 {
		t.Errorf("a cold read recomputed %d merges, want one per recorded landing:\n%s", n, coldCalls)
	}
	if strings.Contains(warmCalls, "merge-tree") || strings.Contains(warmCalls, "ls-tree") {
		t.Errorf("a warm read verified a landing again:\n%s", warmCalls)
	}
	if strings.Count(warmCalls, "\n") >= strings.Count(coldCalls, "\n") {
		t.Errorf("a warm read started %d processes, and a cold one %d", strings.Count(warmCalls, "\n"), strings.Count(coldCalls, "\n"))
	}
}

// What the cache remembers stops counting the moment what it was checked
// with changes: a statement whose bytes differ by one byte, an entry under
// another version of the checks, and a damaged entry are verified again; a
// landing commit that has left main, and a key the known-signers file no
// longer holds, stop the landing counting whatever the cache holds.
func TestTheCacheNeverOutlivesWhatItChecked(t *testing.T) {
	l := newLandings(t)
	log := shim(t)
	cache := filepath.Join(t.TempDir(), "cache")
	want, _ := l.kinds(l.verifier(cache))
	calls(t, log)
	again := func(what string, merges int) {
		t.Helper()
		got, _ := l.kinds(l.verifier(cache))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("after %s the reads disagree:\n got  %v\n want %v", what, got, want)
		}
		if n := strings.Count(calls(t, log), "merge-tree"); n != merges {
			t.Errorf("after %s a read recomputed %d merges, want %d", what, n, merges)
		}
	}
	entries := func() []string {
		t.Helper()
		found, err := filepath.Glob(filepath.Join(cache, "landings", "*"))
		if err != nil || len(found) < 3 {
			t.Fatalf("the cache holds %d landing entries, want one per recorded landing at least: %v", len(found), err)
		}
		return found
	}

	// The recorded change's statement, signed again over text one byte
	// different, is verified again; the other two are remembered.
	head := HeadOf(l.record(idRecorded).Versions[0])
	s := l.statements[head]
	text := l.host.transfer(s[1], head, s[0], strings.Repeat("ab", 31)+"ac")
	l.host.store(l.host.integrator.hash(), s[0], l.host.integrator.sign(t, text))
	again("a statement one byte different", 1)

	// Entries written under another version of the checks.
	for _, path := range entries() {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		other := strings.Replace(string(data), "landing "+verifierVersion+"\n", "landing 0\n", 1)
		os.Remove(path)
		remembered{cache}.putLanding(other)
	}
	again("entries under another version", 3)

	// Damaged entries, and damaged tree digests.
	for _, path := range entries() {
		if err := os.WriteFile(path, []byte("the-valley transfer landing"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A tree entry keeps its checksum line, and its digest has one hex
	// digit changed: only the checksum tells it from a sound entry.
	trees, _ := filepath.Glob(filepath.Join(cache, "trees", "*"))
	if len(trees) == 0 {
		t.Fatal("the cache holds no tree entries")
	}
	for _, path := range trees {
		data, _ := os.ReadFile(path)
		line, sum, _ := strings.Cut(string(data), "\n")
		digit := "0"
		if strings.HasSuffix(line, digit) {
			digit = "1"
		}
		if err := os.WriteFile(path, []byte(line[:len(line)-1]+digit+"\n"+sum), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	again("damaged entries", 3)
	again("entries written again", 0)

	// A key the known-signers file no longer holds: the integrator's key
	// is gone, or another key holds its name.
	for what, keys := range map[string]string{
		"the integrator's key is removed":  keysFile(t, l.host.worker.key),
		"the integrator's key is replaced": keysFile(t, newSigner(t, l.host.integrator.name).key, l.host.worker.key),
	} {
		got, unverified := l.kinds(Verifier{Keys: keys, Signer: l.host.integrator.name, Cache: cache})
		for _, key := range []string{"m v1", "n v1", "p v1"} {
			if got[key] != LandingUncertain+" " {
				t.Errorf("once %s, %s reads %s, want uncertain", what, key, got[key])
			}
		}
		if what == "the integrator's key is removed" && !unverified {
			t.Errorf("once %s, the read is not unverified", what)
		}
	}
	calls(t, log)

	// Main rewritten without the landings: the cache still holds them, and
	// none reads landed.
	l.host.origin(nil, nil, "update-ref", "refs/heads/main", l.host.origin(nil, nil, "commit-tree",
		l.at("refs/heads/main")+"^{tree}", "-m", "main rewritten"))
	got, _ := l.kinds(l.verifier(cache))
	for _, key := range []string{"m v1", "n v1", "p v1"} {
		if kind, _, _ := strings.Cut(got[key], " "); kind == Landed || kind == LandedRecorded {
			t.Errorf("once main no longer holds its landing commit, %s reads %s", key, got[key])
		}
	}
}

// A replacement ref in the reading repository changes no landing: a cold
// read with a recorded head replaced by a commit that merges to another
// tree agrees with a warm read of what was remembered without it.
func TestAReplacementRefChangesNoLanding(t *testing.T) {
	l := newLandings(t)
	cache := filepath.Join(t.TempDir(), "cache")
	want, _ := l.kinds(l.verifier(cache))
	head := HeadOf(l.record(idRecorded).Versions[0])
	tree := l.git("rev-parse", head+"^{tree}")
	l.git("replace", head, l.git("commit-tree", "-p", head+"^", "-m", "replacement", head+"^^{tree}"))
	if l.git("rev-parse", head+"^{tree}") == tree {
		t.Fatal("the replacement ref is not read")
	}
	cold, _ := l.kinds(l.verifier(filepath.Join(t.TempDir(), "cold")))
	warm, _ := l.kinds(l.verifier(cache))
	if fmt.Sprint(cold) != fmt.Sprint(warm) || fmt.Sprint(cold) != fmt.Sprint(want) {
		t.Errorf("with the head replaced the reads disagree:\n cold %v\n warm %v\n want %v", cold, warm, want)
	}
}

// A tree's modes read in one form however the object writes them: git
// walks a tree written 040000 as one written 40000.
func TestATreeModeReadsWithoutLeadingZeros(t *testing.T) {
	id := bytes.Repeat([]byte{0xab}, 20)
	var raw []byte
	for _, entry := range []string{"040000 a", "40000 b", "0100644 c", "100755 d", "0160000 e"} {
		raw = append(append(append(raw, entry...), 0), id...)
	}
	entries, ok := parseTree(raw, len(id))
	var modes []string
	for _, e := range entries {
		modes = append(modes, e.mode)
	}
	if got, want := strings.Join(modes, " "), "40000 40000 100644 100755 160000"; !ok || got != want {
		t.Errorf("the modes read %q (%v), want %q", got, ok, want)
	}
}

// remembered is a cache directory, for a test to plant an entry in.
type remembered struct{ dir string }

func (r remembered) putLanding(text string) { cache{dir: r.dir}.putLanding(text) }

// A statement signed by the worker, a key the known-signers file holds,
// is not believed as the integrator's, under its own key hash or the
// integrator's, cold or warm, though the landing record binds the head.
func TestAStatementByAnotherKnownKeyIsNotTheIntegrators(t *testing.T) {
	l := newLandings(t)
	cache := filepath.Join(t.TempDir(), "cache")
	for _, read := range []string{"cold", "warm"} {
		got, _ := l.kinds(l.verifier(cache))
		if got["q v1"] != LandingUncertain+" " {
			t.Errorf("a %s read believes the worker's statement: %s", read, got["q v1"])
		}
	}
	// The worker named as the integrator believes it: the statement itself
	// is sound, and only the signer kept it out.
	got, _ := l.kinds(Verifier{Keys: keysFile(t, l.host.integrator.key, l.host.worker.key), Signer: l.host.worker.name, Cache: cache})
	if kind, _, _ := strings.Cut(got["q v1"], " "); kind != LandedRecorded {
		t.Errorf("under the worker's name, the worker's statement reads %s", got["q v1"])
	}
}

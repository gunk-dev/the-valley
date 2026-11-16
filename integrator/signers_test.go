package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/approval"
	"the-valley/note"
)

// verifierLine is a verifier key line for who, from a key made of seed.
func verifierLine(who string, seed byte) string {
	priv := ed25519.NewKeyFromSeed([]byte(strings.Repeat(string(rune(seed)), ed25519.SeedSize)))
	return note.NewVerifierKey(who, priv.Public().(ed25519.PublicKey)).String() + "\n"
}

// The publication carries both approval signers files byte for byte, and
// the instance identity derived from the root commits of the instance
// repository's main. A run with nothing new moves nothing. A file with no
// key is published, and so is an empty file where none is named, so a
// revoked or unconfigured authority replaces the keys published before.
// A named file that cannot be read, or does not read as verifier keys,
// publishes nothing, and no file stands in for another.
func TestThePublicationSaysWhoMayApproveInWhichInstance(t *testing.T) {
	s := newScratchRepo(t)
	dir := t.TempDir()
	const name = "valley-host/attestations"
	key := verifierLine
	write := func(file, content string) string {
		path := filepath.Join(dir, file)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	known := write("known-signers", key(name, 1))
	approvers := write("approval-signers", "# compiled\n"+key("operator", 2)+key("alice", 3))
	hardware := write("approval-signers-hardware", "# compiled\n"+key("operator", 2))
	publish := func(extra ...string) error {
		t.Helper()
		stdout, stderr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = devNull(t), devNull(t)
		defer func() { os.Stdout, os.Stderr = stdout, stderr }()
		return publishSigners(append([]string{"--instance-repo", s.repo, "--known-signers", known, "--name", name}, extra...))
	}
	published := func() string {
		oid, _, err := resolveRef(s.repo, signersRef)
		if err != nil {
			t.Fatal(err)
		}
		return oid
	}
	holds := func(file, want string) {
		t.Helper()
		got, err := git(s.repo, "cat-file", "blob", published()+":"+file)
		if err != nil || got != want {
			t.Errorf("the publication's %s holds %q (%v), want %q", file, got, err, want)
		}
	}
	identity := func(roots ...string) string {
		t.Helper()
		id, err := approval.InstanceID("sha1", roots)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	both := []string{"--approval-signers", approvers, "--approval-signers-hardware", hardware}

	base := s.git("rev-parse", "refs/heads/main")
	if err := publish(both...); err != nil {
		t.Fatal(err)
	}
	if got := s.git("ls-tree", "--name-only", published()); got != "approval-signers\napproval-signers-hardware\ninstance-id\nintegrator-name\nknown-signers" {
		t.Fatalf("the publication holds %q", got)
	}
	holds(signersApprovers, "# compiled\n"+key("operator", 2)+key("alice", 3))
	holds(signersHardware, "# compiled\n"+key("operator", 2))
	holds(signersInstance, identity(base)+"\n")

	first := published()
	if err := publish(both...); err != nil || published() != first {
		t.Fatalf("publishing what is published moved the ref (%v)", err)
	}

	// The registry revokes every approval key: the files hold no key, and
	// are published as they are.
	write("approval-signers", "# compiled\n")
	write("approval-signers-hardware", "# compiled\n")
	if err := publish(both...); err != nil {
		t.Fatal(err)
	}
	holds(signersApprovers, "# compiled\n")
	holds(signersHardware, "# compiled\n")

	// A host that configures no approval keys publishes empty files.
	write("approval-signers", key("operator", 2))
	if err := publish("--approval-signers", approvers); err != nil {
		t.Fatal(err)
	}
	holds(signersApprovers, key("operator", 2))
	holds(signersHardware, "")
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	holds(signersApprovers, "")
	holds(signersHardware, "")

	// A named file that is missing or does not read publishes nothing.
	last := published()
	for what, args := range map[string][]string{
		"a missing approval signers file":   {"--approval-signers", filepath.Join(dir, "missing")},
		"a missing hardware file":           {"--approval-signers", approvers, "--approval-signers-hardware", filepath.Join(dir, "missing")},
		"an approval signers file of prose": {"--approval-signers", write("prose", "not a verifier key\n")},
	} {
		if err := publish(args...); err == nil {
			t.Errorf("%s was published", what)
		}
		if published() != last {
			t.Errorf("%s moved the publication", what)
		}
	}

	// The identity is the lineage's: a clone shares it, an unrelated root
	// integrated into main changes it, and a shallow clone has none.
	clone := filepath.Join(t.TempDir(), "clone.git")
	s.git("clone", "--quiet", "--bare", s.repo, clone)
	if id, err := instanceID(clone); err != nil || id != identity(base) {
		t.Errorf("the clone's identity is %q (%v), want the original's", id, err)
	}
	other := s.commit("an unrelated root")
	merge := s.commit("the merge", base, other)
	s.git("update-ref", "refs/heads/main", merge)
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	holds(signersInstance, identity(base, other)+"\n")
	shallow := filepath.Join(t.TempDir(), "shallow.git")
	s.git("clone", "--quiet", "--bare", "--depth", "1", "file://"+s.repo, shallow)
	if _, err := instanceID(shallow); err == nil || !strings.Contains(err.Error(), "is shallow") {
		t.Errorf("a shallow repository's identity: %v", err)
	}

	// An instance repository with no main yet publishes the rest.
	empty := filepath.Join(t.TempDir(), "empty.git")
	s.git("init", "--quiet", "--bare", empty)
	s.repo = empty
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if got := s.git("ls-tree", "--name-only", published()); got != "approval-signers\napproval-signers-hardware\nintegrator-name\nknown-signers" {
		t.Errorf("the publication of a repository with no main holds %q", got)
	}
}

// The publication carries the approval signers files without the
// authority stamp the compiler opens them with. A compilation that moves
// only the stamp, to another registry commit or another day, leaves the
// publication's tree as it was, and the ref does not move. A compilation
// that changes a key moves it.
func TestTheStampAloneMovesNoPublication(t *testing.T) {
	s := newScratchRepo(t)
	// Every write of the ref is logged, so a run that wrote the same tree
	// again would show.
	s.git("config", "core.logAllRefUpdates", "always")
	dir := t.TempDir()
	write := func(file, content string) string {
		path := filepath.Join(dir, file)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stamp := func(commit, day string) string {
		return "# valley-approval-authority-v1 ref=refs/heads/main commit=" + strings.Repeat(commit, 40) + " day=" + day + "\n"
	}
	const name = "valley-host/attestations"
	const header = "# Compiled from the identity registry — do not edit.\n"
	keys := header + verifierLine("operator", 2)
	args := []string{"--instance-repo", s.repo, "--name", name,
		"--known-signers", write("known-signers", verifierLine(name, 1)),
		"--approval-signers", write("approval-signers", stamp("1", "2026-11-16")+keys),
		"--approval-signers-hardware", write("approval-signers-hardware", stamp("1", "2026-11-16")+header),
	}
	publish := func() string {
		t.Helper()
		stdout, stderr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = devNull(t), devNull(t)
		err := publishSigners(args)
		os.Stdout, os.Stderr = stdout, stderr
		if err != nil {
			t.Fatal(err)
		}
		oid, _, err := resolveRef(s.repo, signersRef)
		if err != nil {
			t.Fatal(err)
		}
		return oid
	}
	first := publish()
	for file, want := range map[string]string{signersApprovers: keys, signersHardware: header} {
		if got, err := git(s.repo, "cat-file", "blob", first+":"+file); err != nil || got != want {
			t.Errorf("the publication's %s holds %q (%v), want %q", file, got, err, want)
		}
	}
	writes := func() int {
		t.Helper()
		log, err := os.ReadFile(filepath.Join(s.repo, ".git", "logs", signersRef))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(log), "\n")
	}
	if n := writes(); n != 1 {
		t.Fatalf("the publication's log holds %d writes, not one", n)
	}

	write("approval-signers", stamp("2", "2026-11-17")+keys)
	write("approval-signers-hardware", stamp("2", "2026-11-17")+header)
	if again := publish(); again != first {
		t.Errorf("a new stamp moved the publication from %s to %s", first, again)
	}
	if n := writes(); n != 1 {
		t.Errorf("a new stamp wrote the publication again: %d writes", n)
	}

	write("approval-signers", stamp("2", "2026-11-17")+keys+verifierLine("alice", 3))
	if moved := publish(); moved == first || writes() != 2 {
		t.Error("a new key left the publication where it was")
	}
}

// A change made while a run publishes is published by that run, with
// nothing to wake it again: after each publication the run reads every
// input again and publishes again until a reading matches. The step runs
// once the first round has read its inputs and before its ref moves,
// which is where a wake from the path unit is lost. There the registry
// revokes an approval key and main takes in an unrelated root.
func TestAChangeWhileARunPublishesIsPublishedByIt(t *testing.T) {
	s := newScratchRepo(t)
	dir := t.TempDir()
	write := func(file, content string) string {
		path := filepath.Join(dir, file)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const name = "valley-host/attestations"
	args := []string{"--instance-repo", s.repo, "--name", name,
		"--known-signers", write("known-signers", verifierLine(name, 1)),
		"--approval-signers", write("approval-signers", verifierLine("operator", 2)+verifierLine("alice", 3)),
		"--approval-signers-hardware", write("approval-signers-hardware", verifierLine("operator", 2)),
	}
	base := s.git("rev-parse", "refs/heads/main")
	other := s.commit("an unrelated root")
	merge := s.commit("the merge", base, other)
	want, err := approval.InstanceID("sha1", []string{base, other})
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devNull(t), devNull(t)
	var rounds []int
	err = publishSignersWith(args, func(round int) {
		rounds = append(rounds, round)
		if round == 1 {
			write("approval-signers", verifierLine("operator", 2))
			s.git("update-ref", "refs/heads/main", merge)
		}
	})
	os.Stdout, os.Stderr = stdout, stderr
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 2 {
		t.Errorf("the run published %d times, want 2", len(rounds))
	}
	if got := s.git("cat-file", "blob", signersRef+":"+signersApprovers); got+"\n" != verifierLine("operator", 2) {
		t.Errorf("the revoked key is still published: %q", got)
	}
	if got := s.git("cat-file", "blob", signersRef+":"+signersInstance); got != want {
		t.Errorf("the publication's instance-id is %s, want %s, from both roots", got, want)
	}
}

// A run whose inputs change again after every publication stops after
// one round per input that can change and one more, and fails saying so.
func TestARunWhoseInputsKeepChangingFails(t *testing.T) {
	s := newScratchRepo(t)
	dir := t.TempDir()
	approvers := filepath.Join(dir, "approval-signers")
	known := filepath.Join(dir, "known-signers")
	for path, content := range map[string]string{approvers: "", known: verifierLine("valley-host/attestations", 1)} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devNull(t), devNull(t)
	rounds := 0
	err := publishSignersWith([]string{"--instance-repo", s.repo, "--known-signers", known, "--approval-signers", approvers}, func(round int) {
		rounds = round
		if err := os.WriteFile(approvers, []byte(verifierLine("operator", byte(10+round))), 0o644); err != nil {
			t.Error(err)
		}
	})
	os.Stdout, os.Stderr = stdout, stderr
	// The known-signers file, main and the approval signers file can each
	// change once, and one round more publishes the last of them.
	if err == nil || !strings.Contains(err.Error(), "the inputs kept changing") || rounds != 4 {
		t.Fatalf("after %d rounds: %v", rounds, err)
	}
	if got := s.git("cat-file", "blob", signersRef+":"+signersApprovers); got+"\n" != verifierLine("operator", 13) {
		t.Errorf("the last round did not publish what it read: %q", got)
	}
}

package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"the-valley/approval"
)

// A run compiles until a reading taken after a compilation matches the one
// it compiled. Each case changes the inputs from inside a compilation, as a
// push landing or midnight passing while the compiler runs would.
func TestARunCompilesUntilTheRefAndTheDayHoldStill(t *testing.T) {
	monday, tuesday := day(t, "2026-11-16"), day(t, "2026-11-17")
	for _, c := range []struct {
		name string
		// change is called inside the nth compilation, and may change the
		// inputs or fail it.
		change  func(n int, in *reading) error
		want    []reading
		wantErr bool
	}{
		{
			name:   "nothing changes",
			change: func(int, *reading) error { return nil },
			want:   []reading{{"a", monday}},
		},
		{
			name: "the ref moves during the first compilation",
			change: func(n int, in *reading) error {
				if n == 1 {
					in.oid = "b"
				}
				return nil
			},
			want: []reading{{"a", monday}, {"b", monday}},
		},
		{
			name: "the day turns during the first compilation",
			change: func(n int, in *reading) error {
				if n == 1 {
					in.day = tuesday
				}
				return nil
			},
			want: []reading{{"a", monday}, {"a", tuesday}},
		},
		{
			name: "the ref moves during every compilation",
			change: func(n int, in *reading) error {
				in.oid = string(rune('a' + n))
				return nil
			},
			want:    []reading{{"a", monday}, {"b", monday}, {"c", monday}},
			wantErr: true,
		},
		{
			name: "a compilation fails on a commit the ref has left",
			change: func(n int, in *reading) error {
				if n == 1 {
					in.oid = "b"
					return errors.New("does not compile")
				}
				return nil
			},
			want: []reading{{"a", monday}, {"b", monday}},
		},
		{
			name: "a compilation fails while the day turns",
			change: func(n int, in *reading) error {
				if n == 1 {
					in.day = tuesday
					return errors.New("does not compile")
				}
				return nil
			},
			want: []reading{{"a", monday}, {"a", tuesday}},
		},
		{
			name: "the commit the ref moved to does not compile",
			change: func(n int, in *reading) error {
				if n == 2 {
					return errors.New("does not compile")
				}
				in.oid = "b"
				return nil
			},
			want:    []reading{{"a", monday}, {"b", monday}},
			wantErr: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := reading{"a", monday}
			var compiled []reading
			err := converge("refs/heads/main", 3,
				func() (reading, error) { return in, nil },
				func(at reading) error {
					compiled = append(compiled, at)
					return c.change(len(compiled), &in)
				})
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, want an error: %t", err, c.wantErr)
			}
			if !slices.EqualFunc(compiled, c.want, reading.equal) {
				t.Errorf("compiled %v, want %v", compiled, c.want)
			}
		})
	}
}

// A ref that cannot be read after a compilation fails the run. Its
// artifacts stay as that compilation wrote them.
func TestARunWhoseRefCannotBeReadAgainFails(t *testing.T) {
	reads := 0
	err := converge("refs/heads/main", 3,
		func() (reading, error) {
			reads++
			if reads > 1 {
				return reading{}, errors.New("has no refs/heads/main")
			}
			return reading{"a", day(t, "2026-11-16")}, nil
		},
		func(reading) error { return nil })
	if err == nil {
		t.Error("a run whose ref vanished after its compilation succeeded")
	}
}

// A push landing while a run reads the registry cannot mix two registries,
// and the run compiles the commit the push moved the ref to before it
// exits. The ref moves right after the run's first reading, before its
// first compilation reads a document. That compilation must render the
// commit the reading resolved, under that commit's name, and stamp the
// approval signers file with it; a compilation reading through the ref
// would render the forged registry, whose intruder pushes. It runs over a real repository with git and cue, which
// the compiler shells out to.
func TestAPushMidReadIsCompiledUnmixedByTheSameRun(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "instance.git")
	work := filepath.Join(dir, "work")
	runGit(t, "", "init", "--quiet", "--bare", repo)
	runGit(t, "", "init", "--quiet", work)
	operator, intruder := sshPublicKey(t), sshPublicKey(t)
	landed := commitRegistry(t, work, repo, "refs/heads/main", testRegistry(operator, ""))
	forged := commitRegistry(t, work, repo, "refs/heads/forged", testRegistry(operator, intruder))

	keys := filepath.Join(dir, "authorized_keys")
	approvers := filepath.Join(dir, "approval-signers")
	c := compilation{
		repo: repo, ref: "refs/heads/main", dir: "identity", schema: "../schema/identity.cue",
		knownSigners: filepath.Join(dir, "known-signers"), authorizedKeys: keys,
		grants: filepath.Join(dir, "grants"), approvalSigners: approvers,
		today: func() time.Time { return day(t, "2026-11-16") },
	}
	stamp := func() string {
		body, err := os.ReadFile(approvers)
		if err != nil {
			t.Fatal(err)
		}
		a, err := approval.ReadAuthority(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return a.Commit
	}
	var readings []reading
	var first []byte
	var firstStamp string
	err := converge(c.ref, 2,
		func() (reading, error) {
			r, err := c.read()
			readings = append(readings, r)
			if len(readings) == 1 {
				runGit(t, "", "-C", repo, "update-ref", "refs/heads/main", forged)
			}
			return r, err
		},
		func(at reading) error {
			err := c.compileAt(at)
			if first == nil {
				first, _ = os.ReadFile(keys)
				firstStamp = stamp()
			}
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	if readings[0].oid != landed {
		t.Fatalf("the first reading resolved %s, want the landed %s", readings[0].oid, landed)
	}
	blob := func(key string) []byte { return []byte(strings.Fields(key)[1]) }
	if bytes.Contains(first, blob(intruder)) || !bytes.Contains(first, blob(operator)) {
		t.Errorf("the first compilation mixed in the registry the ref moved to:\n%s", first)
	}
	if firstStamp != landed {
		t.Errorf("the first compilation stamped its approval signers with %s, want the commit it resolved, %s", firstStamp, landed)
	}
	if last := stamp(); last != forged {
		t.Errorf("the run's last compilation stamped its approval signers with %s, want the commit the ref moved to, %s", last, forged)
	}
	final, err := os.ReadFile(keys)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(final, blob(intruder)) {
		t.Errorf("the run ended without compiling the commit the ref moved to:\n%s", final)
	}
}

// testRegistry is the smallest registry the schema takes: the operator
// governs it and pushes, and an intruder, when given a key, pushes too.
func testRegistry(operator, intruder string) string {
	r := fmt.Sprintf(`package identity

boundaries: {
	"push": kind:     "git-push"
	"registry": kind: "registry"
}
genesis: "operator"
principals: "operator": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: %q}]
	grants: {
		push: boundary:   "push"
		govern: boundary: "registry"
	}
}
`, operator)
	if intruder != "" {
		r += fmt.Sprintf(`principals: "intruder": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: %q}]
	grants: push: boundary: "push"
}
`, intruder)
	}
	return r
}

// commitRegistry commits registry as identity/registry.cue in work, pushes
// it to ref in repo, and returns the commit.
func commitRegistry(t *testing.T, work, repo, ref, registry string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(work, "identity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "identity", "registry.cue"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "--quiet", "-m", ref)
	runGit(t, work, "push", "--quiet", repo, "HEAD:"+ref)
	return runGit(t, work, "rev-parse", "HEAD")
}

// runGit runs git with no user or system configuration, as a fixed author.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=valley-check", "GIT_AUTHOR_EMAIL=valley-check@localhost",
		"GIT_COMMITTER_NAME=valley-check", "GIT_COMMITTER_EMAIL=valley-check@localhost",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// sshPublicKey is a fresh ed25519 key in authorized_keys form.
func sshPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, field := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(field)))
		blob = append(blob, field...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob)
}

// Each round of a run stamps the approval signers files it installs with
// the commit it compiled and the day it judged expiry on: the first round,
// one rerun because the ref moved, and one rerun because only the day
// turned.
func TestEachRoundStampsTheApprovalFilesWithItsCommitAndDay(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "instance.git")
	work := filepath.Join(dir, "work")
	runGit(t, "", "init", "--quiet", "--bare", repo)
	runGit(t, "", "init", "--quiet", work)
	operator, intruder := sshPublicKey(t), sshPublicKey(t)
	first := commitRegistry(t, work, repo, "refs/heads/main", testRegistry(operator, ""))
	second := commitRegistry(t, work, repo, "refs/heads/next", testRegistry(operator, intruder))
	monday, tuesday := day(t, "2026-11-16"), day(t, "2026-11-17")

	today := monday
	files := []string{filepath.Join(dir, "approval-signers"), filepath.Join(dir, "approval-signers-hardware")}
	c := compilation{
		repo: repo, ref: "refs/heads/main", dir: "identity", schema: "../schema/identity.cue",
		knownSigners: filepath.Join(dir, "known-signers"), authorizedKeys: filepath.Join(dir, "authorized_keys"),
		grants: filepath.Join(dir, "grants"), approvalSigners: files[0], approvalHardware: files[1],
		today: func() time.Time { return today },
	}
	var stamped [][]approval.Authority
	err := converge(c.ref, 3, c.read, func(at reading) error {
		if err := c.compileAt(at); err != nil {
			return err
		}
		var round []approval.Authority
		for _, f := range files {
			body, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			stamp, err := approval.ReadAuthority(bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
			round = append(round, stamp)
		}
		stamped = append(stamped, round)
		switch len(stamped) {
		case 1:
			runGit(t, "", "-C", repo, "update-ref", "refs/heads/main", second)
		case 2:
			today = tuesday
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		commit string
		day    time.Time
	}{{first, monday}, {second, monday}, {second, tuesday}}
	if len(stamped) != len(want) {
		t.Fatalf("the run compiled %d rounds, want %d", len(stamped), len(want))
	}
	for i, w := range want {
		for j, stamp := range stamped[i] {
			if stamp.Ref != c.ref || stamp.Commit != w.commit || !stamp.Day.Equal(w.day) {
				t.Errorf("round %d stamped %s with %s at %s on %s, want %s on %s",
					i+1, files[j], stamp.Ref, stamp.Commit, stamp.Day.Format(dateLayout), w.commit, w.day.Format(dateLayout))
			}
		}
	}
}

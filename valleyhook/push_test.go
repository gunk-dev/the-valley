package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushTo runs preReceive over the lines as git would hand them to the
// hook, as who, against repo, under a policy that protects nothing.
func pushTo(t *testing.T, repo repository, who string, lines []string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	policyFile := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"refs": [], "writers": [], "grants": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := preReceive([]string{"--project", "project", "--policy", policyFile, "--principal", who},
		strings.NewReader(strings.Join(lines, "\n")+"\n"), &stderr, repo)
	return code, stderr.String()
}

// A push names as many refs as its sender likes. The hook keeps at most
// maxUpdates of them, so it asks git about at most that many.
func TestAPushIsBoundInTheRefsItUpdates(t *testing.T) {
	r := newRecords(t)
	h := r.source("one")
	topics := func(n int) []string {
		var lines []string
		for i := 0; i < n; i++ {
			lines = append(lines, fmt.Sprintf("%s %s refs/heads/topic/%04d", zeroID, h, i))
		}
		return lines
	}

	// One past the bound is refused before any ref is looked up: this
	// repository fails the test if one is.
	code, says := pushTo(t, unreadable{t}, "alice", topics(maxUpdates+1))
	if code == 0 || !strings.Contains(says, "it updates more than 1000 refs, the most one push may") {
		t.Errorf("1001 updates: exit %d, %q", code, says)
	}
	if code, says := pushTo(t, gitRepository{}, "alice", topics(maxUpdates)); code != 0 {
		t.Errorf("1000 updates: exit %d, %q", code, says)
	}

	// A symbolic ref among many is still found, in every form git reads
	// as one: written by git symbolic-ref, naming a ref that does not exist,
	// "ref:" followed by a tab or by nothing, and a filesystem symlink.
	// Each would have a push write the ref it names.
	r.git("", "update-ref", "refs/heads/real", h)
	r.git("", "symbolic-ref", "refs/heads/alias", "refs/heads/real")
	r.git("", "symbolic-ref", "refs/heads/dangling", "refs/heads/main")
	heads := filepath.Join(r.dir, "refs", "heads")
	for name, content := range map[string]string{
		"tabbed": "ref:\trefs/heads/real\n",
		"tight":  "ref:refs/heads/real\n",
	} {
		if err := os.WriteFile(filepath.Join(heads, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("refs/heads/real", filepath.Join(heads, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"alias", "dangling", "tabbed", "tight", "linked"} {
		ref := "refs/heads/" + alias
		lines := append(topics(maxUpdates-1), fmt.Sprintf("%s %s %s", zeroID, h, ref))
		code, says := pushTo(t, gitRepository{}, "alice", lines)
		want := "valley: alice may not write " + ref + " — it is a symbolic ref in this repository, and a push to it would write the ref it points at instead"
		if code == 0 || !strings.Contains(says, want) {
			t.Errorf("%s among %d updates: exit %d, %q", ref, len(lines), code, says)
		}
		if strings.Count(says, "\n") != 1 {
			t.Errorf("%s among %d updates: refused more than the symbolic ref: %q", ref, len(lines), says)
		}
	}
}

// The push policy is read with every key spelled exactly. Each policy
// below makes alice a writer of tags only under a key that does not spell
// its field exactly, or under a key written twice, and encoding/json alone
// would read her in: it matches a key to a field without regard to case,
// under Unicode's case folding, and merges two objects read into one map.
// Each refuses the whole push, naming the key. The same grant spelled
// exactly lets her push the tag.
func TestAPolicyKeyNotSpelledExactlyRefusesThePush(t *testing.T) {
	r := newRecords(t)
	h := r.source("one")
	push := func(policy string) (int, string) {
		t.Helper()
		file := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(file, []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		code := preReceive([]string{"--project", "project", "--policy", file, "--principal", "alice"},
			strings.NewReader(zeroID+" "+h+" refs/tags/v1\n"), &stderr, gitRepository{})
		return code, stderr.String()
	}
	const release = `{"refs": ["refs/tags/*"], "writers": ["alice"]}`
	if code, says := push(`{"grants": {"release": ` + release + `}, "refs": [], "writers": []}`); code != 0 {
		t.Fatalf("alice under a grant spelled exactly: exit %d, %q", code, says)
	}
	// The keys of each policy are in the order Nix writes them, sorted
	// bytewise, as the host module renders the policy file.
	for _, c := range []struct{ name, policy, says string }{
		{"a grant's writers spelled with a long s, after its own writers",
			`{"grants": {"release": {"refs": ["refs/tags/*"], "writers": ["operator"], "writerſ": ["alice"]}}, "refs": [], "writers": []}`,
			`grants["release"] holds the key "writerſ", which is its field "writers" spelled another way`},
		{"Grants beside an empty grants",
			`{"Grants": {"release": ` + release + `}, "grants": {}, "refs": [], "writers": []}`,
			`the document holds the key "Grants", which is its field "grants" spelled another way`},
		{"a grant written twice",
			`{"grants": {"release": {"refs": ["refs/tags/*"], "writers": ["operator"]}, "release": ` + release + `}, "refs": [], "writers": []}`,
			`grants holds the key "release" twice`},
	} {
		code, says := push(c.policy)
		if code == 0 || !strings.Contains(says, "its policy could not be applied") || !strings.Contains(says, c.says) {
			t.Errorf("%s: exit %d, %q", c.name, code, says)
		}
	}
}

// git looks a missing ref up under other names: refs/, refs/tags/,
// refs/heads/ or refs/remotes/ followed by the name it was given. A ref
// named that way could stand in for a ref the valley reads, so no push
// may create or update one, even under a policy that makes the pusher a
// writer of every ref and opens every ref to her through a grant. Each
// may still be deleted. Names that hold refs anywhere else are ordinary.
func TestARefNamedLikeAnotherIsNeitherCreatedNorUpdated(t *testing.T) {
	r := newRecords(t)
	h, h2 := r.source("one"), r.source("two")
	file := filepath.Join(t.TempDir(), "policy.json")
	policy := `{"grants": {"every": {"refs": ["refs/*"], "writers": ["alice"]}}, "refs": ["refs/*"], "writers": ["alice"]}`
	if err := os.WriteFile(file, []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	push := func(old, new, ref string) (int, string) {
		t.Helper()
		var stderr bytes.Buffer
		code := preReceive([]string{"--project", "project", "--policy", file, "--principal", "alice"},
			strings.NewReader(old+" "+new+" "+ref+"\n"), &stderr, gitRepository{})
		return code, stderr.String()
	}

	for _, c := range []struct{ ref, prefix string }{
		{"refs/heads/refs/the-valley/knots", "refs/heads/"},
		{"refs/heads/refs/heads/main", "refs/heads/"},
		{"refs/tags/refs/heads/main", "refs/tags/"},
		{"refs/tags/refs/the-valley/integration-requests/main/c-1", "refs/tags/"},
		{"refs/refs/the-valley/signers", "refs/"},
		{"refs/remotes/refs/heads/registry", "refs/remotes/"},
		{"refs/remotes/refs/heads/main/HEAD", "refs/remotes/"},
	} {
		for _, w := range []struct{ verb, old, new string }{
			{"create", zeroID, h},
			{"update", h, h2},
		} {
			code, says := push(w.old, w.new, c.ref)
			want := fmt.Sprintf("valley: alice may not %s %s — no push may create or update a ref named %s followed by a full ref name: when %s is missing",
				w.verb, c.ref, c.prefix, strings.TrimPrefix(c.ref, c.prefix))
			if code == 0 || !strings.Contains(says, want) {
				t.Errorf("%s %s: exit %d, %q", w.verb, c.ref, code, says)
			}
		}
		if code, says := push(h, zeroID, c.ref); code != 0 {
			t.Errorf("delete %s: exit %d, %q", c.ref, code, says)
		}
	}

	for _, ref := range []string{
		"refs/heads/fix-refs",
		"refs/heads/a/refs/b",
		"refs/heads/refs",
		"refs/heads/refsx/y",
		"refs/tags/a/refs/heads/main",
		"refs/tags/refs",
		"refs/remotes/origin/main",
		"refs/meta/refs/heads/main",
	} {
		if code, says := push(zeroID, h, ref); code != 0 {
			t.Errorf("create %s: exit %d, %q", ref, code, says)
		}
		if code, says := push(h, h2, ref); code != 0 {
			t.Errorf("update %s: exit %d, %q", ref, code, says)
		}
	}
}

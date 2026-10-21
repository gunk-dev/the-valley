package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// checksRepo is a repository with a base and a head commit, and a policy
// beside it, for the checks verb's tests.
type checksRepo struct {
	schema, instance, project, base, head string
}

func newChecksRepo(t *testing.T) *checksRepo {
	t.Helper()
	if _, err := exec.LookPath("cue"); err != nil {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatal("no cue on PATH, and VALLEY_TEST_TOOLS=required")
		}
		t.Skip("no cue to compose a policy with; the nix check provides one")
	}
	schema, err := filepath.Abs(filepath.Join("..", "schema", "verification.cue"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &checksRepo{schema: schema, instance: filepath.Join(dir, "instance"), project: filepath.Join(dir, "project")}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(r.instance, "floor.cue"), `package verification
floor: {
	checks: "c-docs": {runner: "nix", attribute: "c-docs"}
	checks: "c-live": {runner: "command", command: "probe --live", validity: "1h", cost: "slow"}
	classes: docs: {paths: "docs/**": true, requires: "c-docs": true}
	classes: run: {paths: "run/**": true, requires: "c-live": true, carry: exact: true}
	unclassified: "c-docs": true
}
`)
	write(filepath.Join(r.project, "policy.cue"), "package verification\n")
	work := filepath.Join(dir, "work")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "--quiet", "--initial-branch=main")
	git("config", "user.name", "checks-test")
	git("config", "user.email", "checks-test@localhost")
	write(filepath.Join(work, "docs", "a.md"), "base\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "base")
	r.base = git("rev-parse", "HEAD")
	write(filepath.Join(work, "run", "caf\xe9.sh"), "new\n")
	write(filepath.Join(work, "docs", "new\nline.md"), "new\n")
	write(filepath.Join(work, "Makefile"), "new\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "head")
	r.head = git("rev-parse", "HEAD")
	t.Chdir(work)
	return r
}

func (r *checksRepo) checks(args ...string) (stdout, stderr string, code int) {
	var out, errb bytes.Buffer
	code = run(append([]string{"checks"}, args...), &out, &errb)
	return out.String(), errb.String(), code
}

// TestChecksPrintsTheDerivation holds the valley-checks/v1 document to the
// derivation: every path as git gave it, a path that is not UTF-8 in
// base64, and each required check with its classes, pin, cost and how it
// is run.
func TestChecksPrintsTheDerivation(t *testing.T) {
	r := newChecksRepo(t)
	out, stderr, code := r.checks("--schema", r.schema, "--instance", r.instance, "--project="+r.project, r.base, "HEAD")
	if code != 0 {
		t.Fatalf("checks exited %d: %s", code, stderr)
	}
	type path struct {
		Path   *string `json:"path"`
		Base64 *string `json:"base64"`
	}
	var doc struct {
		Schema            string `json:"schema"`
		Base              string `json:"base"`
		Head              string `json:"head"`
		PolicyDigest      string `json:"policy_digest"`
		Paths             []path `json:"paths"`
		Classes           []string
		UnclassifiedPaths []path `json:"unclassified_paths"`
		Carry             string
		Required          []map[string]any
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("the document does not read: %v\n%s", err, out)
	}
	bytesOf := func(ps []path) []string {
		var got []string
		for _, p := range ps {
			switch {
			case p.Path != nil && p.Base64 == nil:
				got = append(got, *p.Path)
			case p.Base64 != nil && p.Path == nil:
				b, err := base64.StdEncoding.Strict().DecodeString(*p.Base64)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, string(b))
			default:
				t.Errorf("a path is %+v, not one of path and base64", p)
			}
		}
		return got
	}
	if doc.Schema != "valley-checks/v1" || doc.Base != r.base || doc.Head != r.head || len(doc.PolicyDigest) != 64 {
		t.Errorf("schema %q, base %q, head %q, digest %q", doc.Schema, doc.Base, doc.Head, doc.PolicyDigest)
	}
	if got, want := bytesOf(doc.Paths), []string{"Makefile", "docs/new\nline.md", "run/caf\xe9.sh"}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %q", got)
	}
	if !strings.Contains(out, `"base64": "`+base64.StdEncoding.EncodeToString([]byte("run/caf\xe9.sh"))+`"`) {
		t.Error("the path that is not UTF-8 is not in base64")
	}
	if got := bytesOf(doc.UnclassifiedPaths); !reflect.DeepEqual(got, []string{"Makefile"}) {
		t.Errorf("unclassified paths = %q", got)
	}
	if !reflect.DeepEqual(doc.Classes, []string{"docs", "run"}) || doc.Carry != "exact" {
		t.Errorf("classes %q, carry %q", doc.Classes, doc.Carry)
	}
	want := []map[string]any{
		{"name": "c-docs", "classes": []any{"docs"}, "unclassified": true, "pin": "mandatory", "cost": "quick", "runner": "nix", "attribute": "c-docs"},
		{"name": "c-live", "classes": []any{"run"}, "unclassified": false, "pin": "mandatory", "cost": "slow", "runner": "command", "command": "probe --live", "validity": "1h"},
	}
	if !reflect.DeepEqual(doc.Required, want) {
		t.Errorf("required = %v", doc.Required)
	}

	// The same two commits with nothing between them require nothing, and
	// every list is still a list.
	out, _, code = r.checks("--schema", r.schema, "--instance", r.instance, "--project", r.project, r.head, r.head)
	for _, field := range []string{`"paths": []`, `"classes": []`, `"unclassified_paths": []`, `"required": []`, `"carry": "same-paths"`} {
		if code != 0 || !strings.Contains(out, field) {
			t.Errorf("an empty change's document has no %s:\n%s", field, out)
		}
	}
}

// TestChecksPrintsNothingWhenItFails holds the verb to printing nothing on
// stdout when it derives nothing, and to saying why.
func TestChecksPrintsNothingWhenItFails(t *testing.T) {
	r := newChecksRepo(t)
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "policy.cue"), []byte("package verification\nproject: classes: x: requires: nothing: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		says string
	}{
		{"no project layer named", []string{"--instance", r.instance, r.base, r.head}, "usage: valley-go checks"},
		{"one commit", []string{"--instance", r.instance, "--project", r.project, r.head}, "usage: valley-go checks"},
		{"an unknown option", []string{"--names", "--instance", r.instance, "--project", r.project, r.base, r.head}, "usage: valley-go checks"},
		{"no such base", []string{"--instance", r.instance, "--project", r.project, "nowhere", r.head}, "no such commit: nowhere"},
		{"a layer that does not compose", []string{"--instance", r.instance, "--project", broken, r.base, r.head}, "do not compose"},
		{"no schema there", []string{"--schema", filepath.Join(broken, "none.cue"), "--instance", r.instance, "--project", r.project, r.base, r.head}, "no policy schema at"},
	} {
		args := c.args
		if !strings.HasPrefix(c.name, "no schema") {
			args = append([]string{"--schema", r.schema}, args...)
		}
		out, stderr, code := r.checks(args...)
		if code == 0 || out != "" || !strings.Contains(stderr, c.says) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out, stderr)
		}
	}
}

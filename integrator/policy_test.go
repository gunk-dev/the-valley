package main

// The policy bridge, where this program is coupled to two others: the
// deriver's output, and the window a policy writes down. Both are pinned
// here rather than assumed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeriverOutputIsRead(t *testing.T) {
	out := `abc1234..def5678: 3 changed path(s)
policy: instance + policy
classes matched: code, prose

required checks:
  code-build      mandatory  code
  prose-format    default    prose, knowledge

unclassified: 1 path(s) matched no class
  odd.txt
`
	d, err := parseDerived(out)
	if err != nil {
		t.Fatal(err)
	}
	if !d.classes["code"] || !d.classes["prose"] || len(d.classes) != 2 {
		t.Fatalf("classes = %v", d.classes)
	}
	if got := strings.Join(d.requires["prose-format"], "+"); got != "prose+knowledge" {
		t.Fatalf("prose-format required by %q", got)
	}
	if got := strings.Join(d.requires["code-build"], "+"); got != "code" {
		t.Fatalf("code-build required by %q", got)
	}
	if len(d.requires) != 2 {
		t.Fatalf("required = %v", d.requires)
	}
}

func TestValidityWindowsAreReadAsThePolicyWritesThem(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"30s", 30 * time.Second},
		{"15m", 15 * time.Minute},
		{"6h", 6 * time.Hour},
		{"2d", 48 * time.Hour},
	} {
		got, err := parseWindow(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseWindow(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

// Each layer's absence defaults in the safe direction of that layer's job,
// and the two directions are opposite (dcr-f41f718). Both are pinned here,
// because the value of the rule is that neither is the other.

func TestATargetWithNoProjectLayerIsJudgedUnderTheFloorAlone(t *testing.T) {
	tip := &worktree{dir: t.TempDir()}
	p := policySource{projectDir: "policy/project"}
	done, err := p.openProject(tip)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if p.note == "" {
		t.Fatal("the pass said nothing about composing the floor alone")
	}
	// What the deriver and the composer are handed is a layer that adds
	// nothing, not a directory that is not there.
	docs, err := filepath.Glob(filepath.Join(p.project, "*.cue"))
	if err != nil || len(docs) != 1 {
		t.Fatalf("the resolved project layer holds %v (%v)", docs, err)
	}
	if body, err := os.ReadFile(docs[0]); err != nil || strings.TrimSpace(string(body)) != "package verification" {
		t.Fatalf("the layer that adds nothing reads %q (%v)", body, err)
	}
	done()
	if _, err := os.Stat(p.project); !os.IsNotExist(err) {
		t.Fatalf("the pass left its scratch layer behind: %v", err)
	}
}

func TestATargetsOwnProjectLayerIsUsedWhereItIs(t *testing.T) {
	tip := &worktree{dir: t.TempDir()}
	own := filepath.Join(tip.dir, "policy/project")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "policy.cue"), []byte("package verification\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := policySource{projectDir: "policy/project"}
	done, err := p.openProject(tip)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if p.project != own {
		t.Fatalf("the project layer resolved to %s, not to %s", p.project, own)
	}
	if p.note != "" {
		t.Fatalf("an ordinary composition said %q", p.note)
	}
}

func TestAFloorThatIsNotThereIsRefused(t *testing.T) {
	// The materialized source, which is the one a test can drive without a
	// repository. The repository source refuses the same way, and the e2e
	// observes it there.
	_, done, err := instanceLayer{path: t.TempDir()}.open()
	defer done()
	if err == nil {
		t.Fatal("a floor directory with no documents in it was accepted")
	}
	if !strings.Contains(err.Error(), floorless) {
		t.Fatalf("the refusal reads %q", err)
	}
}

func TestARefusalIsReadAsTheSentenceItIs(t *testing.T) {
	// attest reports what it checked, then refuses, then elaborates. The
	// sentence is the refusal; the elaboration is not.
	out := `note      /tmp/x.note
text      ok (411 bytes signed)
attest: tree digest mismatch: the statement is about a different tree
  recorded aaaa
  found    bbbb
`
	if got := refusalLine(out); got != "tree digest mismatch: the statement is about a different tree" {
		t.Fatalf("refusalLine = %q", got)
	}
	if got := refusalLine("something with no prefix at all\n"); got != "something with no prefix at all" {
		t.Fatalf("refusalLine fallback = %q", got)
	}
}

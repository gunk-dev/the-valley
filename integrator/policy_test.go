package main

// The policy bridge, where this program is coupled to two others: the
// deriver's output, and the window a policy writes down. Both are pinned
// here rather than assumed.

import (
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

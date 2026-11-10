package main

import "testing"

// An answer carries over landings only as far as the change's carry mode
// allows: same-paths over any, same-class over those that touched none
// of its classes, exact over none.
func TestCarryAllows(t *testing.T) {
	for _, c := range []struct {
		mode   string
		shared bool
		want   bool
	}{
		{"same-paths", false, true},
		{"same-paths", true, true},
		{"same-class", false, true},
		{"same-class", true, false},
		{"exact", false, false},
		{"exact", true, false},
		{"unknown", false, false},
	} {
		if got := carryAllows(c.mode, c.shared); got != c.want {
			t.Errorf("carryAllows(%q, %v) = %v, want %v", c.mode, c.shared, got, c.want)
		}
	}
}

// A comparison that needs no carry mode is answered from the comparison
// alone: why the answer does not carry, and whether the later version is
// offered for approval at once, after the comparison is paged, or not.
func TestCarryVerdictReasons(t *testing.T) {
	for _, c := range []struct {
		c                      comparison
		verdict, reason, offer string
	}{
		{comparison{kind: compareNone}, "review", "it could not be set against v1", "none"},
		{comparison{kind: compareChanged, verb: "rebased", has: 1, had: 1, mainEdited: []string{"status/list.go"}},
			"review", "rebased onto main; its commit is unchanged, but main also edited 1 of its files (status/list.go)", "approve"},
		{comparison{kind: compareChanged, verb: "rebased", has: 2, had: 2, mainEdited: []string{"a", "b", "c", "d"}},
			"review", "rebased onto main; its 2 commits are unchanged, but main also edited 4 of its files (a, b, c, …)", "approve"},
		{comparison{kind: compareChanged, verb: "rebased", has: 1, had: 1, mainEdited: []string{"a\tb"}},
			"review", `rebased onto main; its commit is unchanged, but main also edited 1 of its files ("a\tb")`, "approve"},
		{comparison{kind: compareChanged, verb: "edited", edited: 1, has: 2, had: 2}, "review", "its commits changed (1 of 2)", "view"},
		{comparison{kind: compareChanged, verb: "edited", edited: 1, has: 1, had: 1}, "review", "its commit changed", "view"},
		{comparison{kind: compareChanged, verb: "edited", edited: 1, has: 2, had: 1},
			"review", "its commits changed (2 commits, where v1 had 1)", "view"},
		{comparison{kind: compareClean, meta: true, landings: []string{"b"}}, "review", "its commit messages or authors differ", "view"},
		{comparison{kind: compareClean, landings: []string{"b"}}, "carries", "v2 makes the same changes as v1 on the same main", ""},
	} {
		verdict, reason, offer, err := carryVerdict(git{}, c.c, 1, 2, nil)
		if err != nil || verdict != c.verdict || reason != c.reason || offer != c.offer {
			t.Errorf("carryVerdict(%+v) = %q, %q, %q, %v; want %q, %q, %q", c.c, verdict, reason, offer, err, c.verdict, c.reason, c.offer)
		}
	}
}

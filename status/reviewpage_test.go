package main

import "testing"

// A note's anchor is the file a page line belongs to, by the name a rename
// gives it, and the new-side line it sits on, which a removal and the
// no-newline marker do not move. Inside a hunk, a line that reads like a
// file header is a line of the diff, and anchors as one.
func TestAnchors(t *testing.T) {
	page := []byte(`branch topic, log against origin/main:

abc1234 rename and edit
diff --git a/old.md b/new.md
similarity index 90%
@@ -3,3 +3,3 @@ heading
 kept 3
-removed
\ No newline at end of file
+added 4
diff --git a/quoted.diff b/quoted.diff
@@ -1,0 +1,2 @@
+diff --git a/inner b/inner
+@@ -9 +9 @@
`)
	all := anchors(page)
	for n, want := range map[int]string{
		0:  "(unanchored)",
		3:  "(unanchored)",
		4:  "new.md",
		7:  "new.md:3",
		9:  "new.md:3",
		10: "new.md:4",
		11: "quoted.diff",
		13: "quoted.diff:1",
		14: "quoted.diff:2",
		15: "(unanchored)",
	} {
		if got := anchorOf(all, n); got != want {
			t.Errorf("a note after line %d is anchored to %s, want %s", n, got, want)
		}
	}
}

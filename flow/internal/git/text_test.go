package git

import "testing"

func TestTextDiffSizes(t *testing.T) {
	sizes := map[string]int64{"1111111": 7, "2222222": 9}
	size := func(blob string) int64 {
		if n, ok := sizes[blob]; ok {
			return n
		}
		return -1
	}
	hunk := "@@ -1 +1 @@\n-\xff\n+\x00\n"
	for _, tc := range []struct {
		name, header string
		size         int64
	}{
		{"a changed file has its size after the change", "diff --git a/k b/k\nindex 1111111..2222222 100644\n--- a/k\n+++ b/k\n", 9},
		{"a deleted file has its size before the change", "diff --git a/k b/k\ndeleted file mode 100644\nindex 1111111..0000000\n--- a/k\n+++ /dev/null\n", 7},
		{"a blob the repository lacks has no size", "diff --git a/k b/k\nindex 3333333..4444444 100644\n--- a/k\n+++ b/k\n", -1},
		{"a header without an index line has no size", "diff --git a/k b/k\n--- a/k\n+++ b/k\n", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.header + NotText(tc.size) + "\n"
			if got := textDiff(tc.header+hunk, size); got != want {
				t.Errorf("textDiff:\n got %q\nwant %q", got, want)
			}
		})
	}
}

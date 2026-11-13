package rebase

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A pair entry with more conflicted paths than fit is cut to the first in
// sorted order, keeps the full count, and reads back as it was returned,
// with its heads in either order. Its file fits what an entry is read to,
// and a damaged one is a miss.
func TestAPairEntryIsCutToWhatFits(t *testing.T) {
	dir := t.TempDir()
	tip, a, b := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	var paths []string
	for i := 300; i > 0; i-- {
		paths = append(paths, fmt.Sprintf("docs/a-long-directory-name/file-%03d.md", i))
	}
	kept := RememberPair(dir, tip, b, a, Pair{Total: len(paths), Paths: paths})
	if kept.Total != 300 || len(kept.Paths) == 0 || len(kept.Paths) >= 300 || kept.Paths[0] != "docs/a-long-directory-name/file-001.md" {
		t.Fatalf("RememberPair kept %d of %d paths, the first %q", len(kept.Paths), kept.Total, kept.Paths)
	}
	got, ok := RememberedPair(dir, tip, a, b)
	if !ok || got.Total != kept.Total || strings.Join(got.Paths, "\n") != strings.Join(kept.Paths, "\n") {
		t.Fatalf("RememberedPair = %d paths of %d, %v; want what RememberPair kept", len(got.Paths), got.Total, ok)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "pairs", "*"))
	if len(entries) != 1 {
		t.Fatalf("the pair is %d entries", len(entries))
	}
	data, err := os.ReadFile(entries[0])
	if err != nil || len(data) > entryLimit {
		t.Fatalf("the entry is %d bytes, more than %d, or does not read: %v", len(data), entryLimit, err)
	}
	os.WriteFile(entries[0], []byte(strings.Replace(string(data), "file-001", "file-000", 1)), 0o600)
	if _, ok := RememberedPair(dir, tip, a, b); ok {
		t.Error("a damaged pair entry is found")
	}
	if clean := RememberPair(dir, tip, a, b, Pair{}); clean.Total != 0 {
		t.Errorf("a clean pair kept %d paths", clean.Total)
	}
	if got, ok := RememberedPair(dir, tip, b, a); !ok || got.Total != 0 || len(got.Paths) != 0 {
		t.Errorf("a clean pair reads back as %+v, %v", got, ok)
	}
}

package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// NotText is the line that replaces the hunks of a file that is not text in
// TextDiff, giving the file's size in bytes; a negative size is unknown.
func NotText(size int64) string {
	if size < 0 {
		return "[the content of this file is not text and was left out]"
	}
	return fmt.Sprintf("[the content of this file is not text and was left out: %d bytes]", size)
}

// TextDiff returns diff, a unified diff of the repository at dir, as text
// for a model's prompt: valid UTF-8 without NUL bytes, as the CLIs refuse
// other input. A file whose hunks aren't valid UTF-8 or hold a NUL byte is
// not text, whatever git took it for: it keeps its header, so the reader
// still sees that it changed, and has its hunks replaced with the line
// NotText giving its size after the change, or before it when the change
// deletes it. With dir "", the sizes are unknown.
func TextDiff(ctx context.Context, dir, diff string) string {
	return textDiff(diff, func(blob string) int64 {
		if dir == "" {
			return -1
		}
		out, err := runGit(ctx, dir, "cat-file", "-s", blob)
		if err != nil {
			return -1
		}
		n, err := strconv.ParseInt(out, 10, 64)
		if err != nil {
			return -1
		}
		return n
	})
}

// textDiff is TextDiff with size giving a blob's size in bytes, or -1.
func textDiff(diff string, size func(blob string) int64) string {
	if isText(diff) {
		return diff
	}
	var b strings.Builder
	for _, file := range splitFiles(diff) {
		if isText(file) {
			b.WriteString(file)
			continue
		}
		header, hunks := file, ""
		if strings.HasPrefix(file, "@@ ") {
			header, hunks = "", file
		} else if i := strings.Index(file, "\n@@ "); i >= 0 {
			header, hunks = file[:i+1], file[i+1:]
		}
		// Only a path can make a header invalid; keep it, marked.
		b.WriteString(strings.ToValidUTF8(header, "�"))
		if isText(hunks) {
			b.WriteString(hunks)
			continue
		}
		n := int64(-1)
		if blob := changedBlob(header); blob != "" {
			n = size(blob)
		}
		b.WriteString(NotText(n) + "\n")
	}
	return b.String()
}

func isText(s string) bool {
	return utf8.ValidString(s) && strings.IndexByte(s, 0) < 0
}

// changedBlob returns the object name a file's diff header gives for its
// content after the change, or before it when the change deletes the file;
// "" when the header has no index line.
func changedBlob(header string) string {
	for _, l := range strings.Split(header, "\n") {
		rest, ok := strings.CutPrefix(l, "index ")
		if !ok {
			continue
		}
		ids, _, _ := strings.Cut(rest, " ")
		before, after, ok := strings.Cut(ids, "..")
		if !ok {
			return ""
		}
		if strings.Trim(after, "0") == "" {
			return before
		}
		return after
	}
	return ""
}

// splitFiles splits a diff before each "diff --git" line.
func splitFiles(diff string) []string {
	var files []string
	for diff != "" {
		end := strings.Index(diff, "\ndiff --git ")
		if end < 0 {
			return append(files, diff)
		}
		files = append(files, diff[:end+1])
		diff = diff[end+1:]
	}
	return files
}

// TruncateText cuts s to at most n bytes at a rune boundary, so valid UTF-8
// stays valid.
func TruncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

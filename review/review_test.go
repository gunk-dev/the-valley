package review

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	id1    = "0123456789abcdef0123456789abcdef"
	id2    = "fedcba9876543210fedcba9876543210"
	sha1a  = "git-sha1:1111111111111111111111111111111111111111"
	sha1b  = "git-sha1:2222222222222222222222222222222222222222"
	sha1c  = "git-sha1:3333333333333333333333333333333333333333"
	digest = "abcdef4444444444444444444444444444444444444444444444444444444444"
)

// text is a well-formed review that judges a version, as text, so each
// case below can change one thing about it.
func text() string {
	return `{
  "schema": "the-valley/review/v1",
  "review_id": "` + id1 + `",
  "change_id": "` + changeID + `",
  "target": "main",
  "review": "docs-style",
  "reviewer": "klaus-env",
  "verdict": "request-changes",
  "withdraws": null,
  "binding": {
    "record": "` + sha1a + `",
    "version": 2,
    "head": "` + sha1b + `",
    "target_tip": "` + sha1c + `",
    "base": "` + sha1a + `",
    "delta": "` + digest + `"
  },
  "summary": "Two sentences need splitting.\nSee the notes.",
  "notes": [
    {"side": "after", "path": "docs/guide.md", "blob": "` + sha1b + `", "lines": {"start": 3, "end": 4}, "body": "Split this sentence."},
    {"side": "before", "path": "docs/old file.md", "blob": "` + sha1c + `", "lines": null, "body": "Why was this removed?"}
  ],
  "provenance": {
    "harness": "klaus 0.9.1",
    "model": "claude-opus-5-5",
    "settings": "` + digest + `",
    "inputs": "` + digest + `",
    "input_tokens": 1200,
    "output_tokens": null
  }
}
`
}

// withdrawalText is a well-formed withdrawal.
func withdrawalText() string {
	return `{"schema": "the-valley/review/v1", "review_id": "` + id2 + `", "change_id": "` + changeID + `",
"target": "main", "review": "docs-style", "reviewer": "klaus-env", "verdict": "withdraw",
"withdraws": "` + id1 + `", "binding": null, "summary": "Judged in error.", "notes": [], "provenance": null}`
}

func TestAWellFormedReviewReadsWhole(t *testing.T) {
	r, err := Parse([]byte(text()))
	if err != nil {
		t.Fatal(err)
	}
	tokens := int64(1200)
	want := &Record{
		Schema: Schema, ReviewID: id1, ChangeID: changeID, Target: "main", Review: "docs-style",
		Reviewer: "klaus-env", Verdict: RequestChanges,
		Binding: &Binding{Record: sha1a, Version: 2, Head: sha1b, TargetTip: sha1c, Base: sha1a, Delta: digest},
		Summary: "Two sentences need splitting.\nSee the notes.",
		Notes: []Note{
			{Side: After, Path: "docs/guide.md", Blob: sha1b, Lines: &Lines{Start: 3, End: 4}, Body: "Split this sentence."},
			{Side: Before, Path: "docs/old file.md", Blob: sha1c, Body: "Why was this removed?"},
		},
		Provenance: &Provenance{Harness: "klaus 0.9.1", Model: "claude-opus-5-5", Settings: digest, Inputs: digest, InputTokens: &tokens},
	}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("Parse = %+v, want %+v", r, want)
	}
	for _, data := range []string{text(), withdrawalText()} {
		r, err := Parse([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(encoded)
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Fatalf("an encoded review read back as %+v, %v", again, err)
		}
	}
	w, err := Parse([]byte(withdrawalText()))
	if err != nil || w.Judges() || *w.Withdraws != id1 {
		t.Fatalf("the withdrawal reads as %+v, %v", w, err)
	}
}

// Each case changes one thing about a well-formed review, and the parser
// refuses it, saying what is wrong.
func TestMalformedReviewsAreRefused(t *testing.T) {
	cases := []struct {
		name, old, new, says string
		withdrawal           bool
	}{
		{"a key given twice", `"verdict": "request-changes",`, `"verdict": "request-changes", "verdict": "approve",`, `names the key "verdict" twice`, false},
		{"a key given twice in the binding", `"version": 2,`, `"version": 2, "version": 3,`, `names the key "version" twice`, false},
		{"a field the schema does not have", `"target": "main",`, `"target": "main", "instance": "qinling",`, `the field "instance"`, false},
		{"a field in another case", `"verdict": "request-changes",`, `"Verdict": "request-changes",`, `the field "Verdict"`, false},
		{"a field missing", `"review": "docs-style",`, ``, `no field "review"`, false},
		{"a field in a note the schema does not have", `"body": "Split this sentence."`, `"body": "Split this sentence.", "line": 3`, `notes[0] holds the field "line"`, false},
		{"a field in the provenance the schema does not have", `"output_tokens": null`, `"output_tokens": null, "cost": 3`, `the field "cost"`, false},
		{"a version as a string", `"version": 2,`, `"version": "2",`, "binding.version is a string", false},
		{"a version as a fraction", `"version": 2,`, `"version": 2.0,`, "binding.version is 2.0", false},
		{"a version with an exponent", `"version": 2,`, `"version": 2e0,`, "binding.version is 2e0", false},
		{"a version of 0", `"version": 2,`, `"version": 0,`, "outside 1 to 1000", false},
		{"a verdict of null", `"verdict": "request-changes",`, `"verdict": null,`, "verdict is a literal", false},
		{"a verdict the schema does not have", `"verdict": "request-changes",`, `"verdict": "reject",`, `verdict is "reject"`, false},
		{"an approval that withdraws", `"withdraws": null,`, `"withdraws": "` + id2 + `",`, "only a withdrawal names a review", false},
		{"a judgment with no binding", `"binding": {`, `"binding": null, "x": {`, `the field "x"`, false},
		{"a review ID in capitals", `"review_id": "` + id1 + `"`, `"review_id": "` + strings.ToUpper(id1) + `"`, "is not a review ID", false},
		{"a review ID too short", `"review_id": "` + id1 + `"`, `"review_id": "` + id1[:31] + `"`, "is not a review ID", false},
		{"a change ID that is not one", `"change_id": "` + changeID + `"`, `"change_id": "abc"`, "is not a change ID", false},
		{"a target that is no branch", `"target": "main"`, `"target": "ma..in"`, "is not a branch name", false},
		{"a review name in capitals", `"review": "docs-style"`, `"review": "Docs-style"`, "is not a review's name", false},
		{"a review name too long", `"review": "docs-style"`, `"review": "` + strings.Repeat("d", 65) + `"`, "is not a review's name", false},
		{"a reviewer that is no principal", `"reviewer": "klaus-env"`, `"reviewer": "-klaus"`, "is not a principal name", false},
		{"a record named by another hash", `"record": "` + sha1a + `"`, `"record": "git-sha256:` + digest + `"`, "binding.record", false},
		{"a delta in capitals", `"delta": "` + digest + `"`, `"delta": "` + strings.ToUpper(digest) + `"`, "binding.delta", false},
		{"a side the schema does not have", `"side": "after"`, `"side": "middle"`, `side is "middle"`, false},
		{"a path that climbs", `"path": "docs/guide.md"`, `"path": "../guide.md"`, "has the component ..", false},
		{"a path into .git", `"path": "docs/guide.md"`, `"path": ".Git/config"`, "git keeps for itself", false},
		{"a path with an empty component", `"path": "docs/guide.md"`, `"path": "docs//guide.md"`, "an empty component", false},
		{"a path holding a newline", `"path": "docs/guide.md"`, `"path": "docs/guide\n.md"`, "holds a control character", false},
		{"a blob that is no object", `"blob": "` + sha1b + `"`, `"blob": "docs/guide.md"`, "notes[0].blob", false},
		{"lines that run backwards", `"start": 3, "end": 4`, `"start": 4, "end": 3`, "run from 4 to 3", false},
		{"a line of 0", `"start": 3, "end": 4`, `"start": 0, "end": 4`, "outside 1 to", false},
		{"a line past the last a note may name", `"start": 3, "end": 4`, fmt.Sprintf(`"start": 3, "end": %d`, MaxLine+1), "outside 1 to", false},
		{"an empty summary", `"summary": "Two sentences need splitting.\nSee the notes."`, `"summary": ""`, "summary is not 1 to", false},
		{"a summary with a bell", `"summary": "Two sentences`, `"summary": "\u0007Two sentences`, "summary is not 1 to", false},
		{"an empty note", `"body": "Split this sentence."`, `"body": ""`, "notes[0].body", false},
		{"settings that are no digest", `"settings": "` + digest + `"`, `"settings": "sha256"`, "provenance.settings", false},
		{"negative tokens", `"input_tokens": 1200`, `"input_tokens": -1`, "input_tokens is -1", false},
		{"more tokens than any run", `"input_tokens": 1200`, `"input_tokens": 1000000000001`, "outside 0 to", false},
		{"a model holding a newline", `"model": "claude-opus-5-5"`, `"model": "claude\nopus"`, "provenance.model", false},
		{"a value after the review", "\n}\n", "\n}\n{}", "more after the value", false},
		{"a lone surrogate", `"Split this sentence."`, `"Split \ud800 this sentence."`, "surrogate", false},
		{"a withdrawal of nothing", `"withdraws": "` + id1 + `"`, `"withdraws": null`, "withdraws is not a review ID", true},
		{"a withdrawal of itself", `"withdraws": "` + id1 + `"`, `"withdraws": "` + id2 + `"`, "withdrawal of itself", true},
		{"a withdrawal with a binding", `"binding": null`, `"binding": {"record": "` + sha1a + `", "version": 1, "head": "` + sha1a + `", "target_tip": "` + sha1a + `", "base": "` + sha1a + `", "delta": "` + digest + `"}`, "has no binding", true},
		{"a withdrawal with notes", `"notes": []`, `"notes": [{"side": "after", "path": "a", "blob": "` + sha1a + `", "lines": null, "body": "b"}]`, "has no notes", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := text()
			if c.withdrawal {
				base = withdrawalText()
			}
			if !strings.Contains(base, c.old) {
				t.Fatalf("the review does not hold %q", c.old)
			}
			_, err := Parse([]byte(strings.Replace(base, c.old, c.new, 1)))
			if err == nil {
				t.Fatal("it parsed")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Fatalf("the refusal does not say %q: %v", c.says, err)
			}
		})
	}
	if _, err := Parse(append([]byte(text()[:20]), 0xff)); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("bytes that are not UTF-8: %v", err)
	}
}

// A review is at most MaxRecord bytes, read or written, and its lists and
// strings are held to their own bounds.
func TestAReviewIsBounded(t *testing.T) {
	r, err := Parse([]byte(text()))
	if err != nil {
		t.Fatal(err)
	}
	padded := text() + strings.Repeat(" ", MaxRecord)
	if _, err := Parse([]byte(padded)); err == nil || !strings.Contains(err.Error(), "more than the 65536") {
		t.Fatalf("a review of more than 64 KiB: %v", err)
	}

	note := r.Notes[0]
	note.Body = strings.Repeat("n", MaxNoteBody)
	r.Notes = []Note{note}
	if _, err := Encode(r); err != nil {
		t.Fatalf("a note of %d bytes: %v", MaxNoteBody, err)
	}
	r.Notes[0].Body += "n"
	if _, err := Encode(r); err == nil || !strings.Contains(err.Error(), "notes[0].body") {
		t.Fatalf("a note of %d bytes: %v", MaxNoteBody+1, err)
	}

	note.Body = "short"
	r.Notes = nil
	for range MaxNotes {
		r.Notes = append(r.Notes, note)
	}
	data, err := Encode(r)
	if err != nil {
		t.Fatalf("%d notes: %v", MaxNotes, err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("%d notes read back: %v", MaxNotes, err)
	}
	r.Notes = append(r.Notes, note)
	if _, err := Encode(r); err == nil || !strings.Contains(err.Error(), "more than the 256") {
		t.Fatalf("%d notes: %v", MaxNotes+1, err)
	}
	refused := strings.Replace(string(data), `"notes": [`, `"notes": [`+strings.Repeat(`{"side": "after", "path": "a", "blob": "`+sha1a+`", "lines": null, "body": "b"},`, 1), 1)
	if _, err := Parse([]byte(refused)); err == nil || !strings.Contains(err.Error(), "257 notes") {
		t.Fatalf("%d notes read: %v", MaxNotes+1, err)
	}

	// Each note within its bounds, and the whole past 64 KiB.
	note.Body = strings.Repeat("n", 4096)
	r.Notes = nil
	for range 20 {
		r.Notes = append(r.Notes, note)
	}
	if _, err := Encode(r); err == nil || !strings.Contains(err.Error(), "more than the 65536") {
		t.Fatalf("a review of 20 notes of 4 KiB: %v", err)
	}

	r.Notes = nil
	r.Summary = strings.Repeat("s", MaxSummary+1)
	if _, err := Encode(r); err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("a summary of %d bytes: %v", MaxSummary+1, err)
	}
	r.Summary = "s"
	r.Notes = []Note{{Side: After, Path: strings.Repeat("p/", MaxPath/2+1) + "x", Blob: sha1a, Body: "b"}}
	if _, err := Encode(r); err == nil || !strings.Contains(err.Error(), "is not 1 to 4095 bytes") {
		t.Fatalf("a path of %d bytes: %v", len(r.Notes[0].Path), err)
	}
}

func TestReviewIDsAndRefs(t *testing.T) {
	a, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewID()
	if !ValidID(a) || !ValidID(b) || a == b {
		t.Fatalf("NewID gave %q and %q", a, b)
	}
	ref := Ref("main", changeID)
	if ref != "refs/the-valley/reviews/main/"+changeID {
		t.Fatalf("Ref = %s", ref)
	}
	if target, id, ok := ParseRef(ref); !ok || target != "main" || id != changeID {
		t.Fatalf("ParseRef(%s) = %s, %s, %v", ref, target, id, ok)
	}
	if target, _, ok := ParseRef(Ref("release/1.0", changeID)); !ok || target != "release/1.0" {
		t.Fatalf("a target with a slash: %s, %v", target, ok)
	}
	for _, bad := range []string{
		"refs/the-valley/changes/main/" + changeID,
		RefPrefix + changeID,
		RefPrefix + "main/" + id1,
		RefPrefix + "ma..in/" + changeID,
	} {
		if _, _, ok := ParseRef(bad); ok {
			t.Errorf("ParseRef took %s", bad)
		}
	}
}

package change

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	id2     = "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"
	record1 = HeadPrefix + "3333333333333333333333333333333333333333"
	record2 = HeadPrefix + "4444444444444444444444444444444444444444"
	tip     = HeadPrefix + "5555555555555555555555555555555555555555"
)

// report is a well-formed report of a conflict in a stack of two layers,
// as text, so each case below can change one thing about it.
func report() string {
	return `{
  "schema": "valley-restack-report/v1",
  "change_id": "` + id1 + `",
  "target": "main",
  "target_tip": "` + tip + `",
  "operation": "20261017T203500Z-1",
  "result": "conflict",
  "layers": [
    {"change_id": "` + id2 + `", "record": "` + record2 + `", "version": 1, "branch": "lower", "head": "` + head2 + `", "published": null},
    {"change_id": "` + id1 + `", "record": "` + record1 + `", "version": 2, "branch": "topic", "head": "` + head1 + `", "published": null}
  ],
  "failing_layer": "` + id1 + `",
  "conflict_paths": ["src/main.go", "docs/a b.md"],
  "blocking_requests": [],
  "refusal": null
}
`
}

// published is the same stack, both layers moved and versioned.
func published() string {
	r := strings.Replace(report(), `"result": "conflict"`, `"result": "complete"`, 1)
	r = strings.Replace(r, `"failing_layer": "`+id1+`"`, `"failing_layer": null`, 1)
	r = strings.Replace(r, `"conflict_paths": ["src/main.go", "docs/a b.md"]`, `"conflict_paths": []`, 1)
	r = strings.Replace(r, `"head": "`+head2+`", "published": null`,
		`"head": "`+head2+`", "published": {"head": "`+head1+`", "version": 2, "record": "`+record1+`"}`, 1)
	return strings.Replace(r, `"head": "`+head1+`", "published": null`,
		`"head": "`+head1+`", "published": {"head": "`+head2+`", "version": 3, "record": "`+record2+`"}`, 1)
}

func TestAWellFormedReportReadsWhole(t *testing.T) {
	r, err := ParseRestackReport([]byte(report()))
	if err != nil {
		t.Fatal(err)
	}
	failing := id1
	want := &RestackReport{
		Schema: RestackSchema, ChangeID: id1, Target: "main", TargetTip: tip,
		Operation: "20261017T203500Z-1", Result: RestackConflict,
		Layers: []RestackLayer{
			{ChangeID: id2, Record: record2, Version: 1, Branch: "lower", Head: head2},
			{ChangeID: id1, Record: record1, Version: 2, Branch: "topic", Head: head1},
		},
		Failing: &failing, Conflicts: []string{"src/main.go", "docs/a b.md"}, Requests: []string{},
	}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("ParseRestackReport = %+v, want %+v", r, want)
	}
	for _, text := range []string{report(), published()} {
		r, err := ParseRestackReport([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := EncodeRestackReport(r)
		if err != nil {
			t.Fatal(err)
		}
		again, err := ParseRestackReport(encoded)
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Errorf("an encoded report read back as %+v, %v", again, err)
		}
	}
	// A report written with no lists at all is written with empty ones.
	r.Conflicts, r.Requests = nil, nil
	encoded, err := EncodeRestackReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRestackReport(encoded); err != nil {
		t.Errorf("a report with nil lists encoded as one that does not read: %v", err)
	}
}

// Each case changes one thing about a well-formed report, and each change
// is one the reading has to refuse.
func TestTheReportReadingIsStrict(t *testing.T) {
	r := report()
	swap := func(from, to string) string {
		if !strings.Contains(r, from) {
			t.Fatalf("the report holds no %q to change", from)
		}
		return strings.Replace(r, from, to, 1)
	}
	for _, c := range []struct {
		what, text, says string
	}{
		// Structure and types, read by the same reader as a record.
		{"bytes that are not UTF-8", swap("src/main.go", "src/\xff.go"), "not valid UTF-8"},
		{"a duplicate key", swap(`"result": "conflict",`, `"result": "conflict", "result": "complete",`), `"result" twice`},
		{"a duplicate key in a layer", swap(`"version": 1,`, `"version": 1, "version": 1,`), `"version" twice`},
		{"an unknown field", swap(`"refusal": null`, `"refusal": null, "note": "x"`), `"note"`},
		{"an unknown layer field", swap(`"version": 1,`, `"version": 1, "owner": "x",`), `"owner"`},
		{"an unknown published field", published2(t, `"version": 2,`, `"version": 2, "at": 1,`), `"at"`},
		{"a missing field", swap(`"operation": "20261017T203500Z-1",`, ``), `no field "operation"`},
		{"a missing layer field", swap(`"branch": "lower", `, ``), `no field "branch"`},
		{"trailing data", r + "{}", "more after the value"},
		{"a version as a string", swap(`"version": 1`, `"version": "1"`), "where an integer is expected"},
		{"a refusal as a number", swap(`"refusal": null`, `"refusal": 1`), "a string or null"},
		{"a failing layer as true", swap(`"failing_layer": "`+id1+`"`, `"failing_layer": true`), "a string or null"},
		{"paths as an object", swap(`"conflict_paths": ["src/main.go", "docs/a b.md"]`, `"conflict_paths": {}`), "where an array is expected"},
		{"a path as a number", swap(`"src/main.go"`, `7`), "where a string is expected"},
		{"requests as null", swap(`"blocking_requests": []`, `"blocking_requests": null`), "where an array is expected"},
		{"layers as null", withLayers(t, r, `null`), "where an array is expected"},
		{"published as a string", swap(`"published": null`, `"published": "x"`), "where an object is expected"},

		// Formats.
		{"another schema", swap(`restack-report/v1`, `restack-report/v2`), "its schema"},
		{"a change ID outside k to z", swap(`"change_id": "`+id1+`",
  "target"`, `"change_id": "a`+id1[1:]+`",
  "target"`), "not a change ID"},
		{"a target that is no branch", swap(`"target": "main"`, `"target": "ma in"`), "its target"},
		{"a tip that is no head", swap(tip, strings.ToUpper(tip)), "its target_tip"},
		{"an empty operation", swap(`"20261017T203500Z-1"`, `""`), "its operation"},
		{"an operation with a space", swap(`"20261017T203500Z-1"`, `"one two"`), "its operation"},
		{"an operation past its bound", swap(`"20261017T203500Z-1"`, `"`+strings.Repeat("o", MaxOperation+1)+`"`), "its operation"},
		{"another result", swap(`"result": "conflict"`, `"result": "merged"`), "its result"},
		{"a layer's record that is no head", swap(record2, "git-sha1:xyz"), "layers[0].record"},
		{"a layer's version of zero", swap(`"version": 1`, `"version": 0`), "outside 1 to 1000"},
		{"a layer with no branch", swap(`"branch": "lower"`, `"branch": ""`), "layers[0].branch"},
		{"a layer's branch git refuses", swap(`"branch": "lower"`, `"branch": "lo..wer"`), "layers[0].branch"},
		{"a layer's head that is no head", swap(`"head": "`+head2+`"`, `"head": "`+head2[:20]+`"`), "layers[0].head"},
		{"an empty path", swap(`"src/main.go"`, `""`), "conflict_paths[0]"},
		{"a path with a newline", swap(`"src/main.go"`, `"src/\nmain.go"`), "conflict_paths[0]"},
		{"a path past its bound", swap(`"src/main.go"`, `"`+strings.Repeat("p", MaxConflictPath+1)+`"`), "conflict_paths[0]"},
		{"too many paths", swap(`"src/main.go", `, strings.Repeat(`"p", `, MaxConflictPaths)), "more than the 100"},
		{"a request that is no request", swap(`"blocking_requests": []`, `"blocking_requests": ["refs/heads/main"]`), "blocking_requests[0]"},
		{"a request on another target", swap(`"blocking_requests": []`, `"blocking_requests": ["refs/the-valley/integration-requests/next/x"]`), "blocking_requests[0]"},
		{"a request past its bound", swap(`"blocking_requests": []`, `"blocking_requests": ["refs/the-valley/integration-requests/main/`+strings.Repeat("r", MaxRequestRef)+`"]`), "blocking_requests[0]"},
		{"an empty refusal", swap(`"refusal": null`, `"refusal": ""`), "its refusal"},
		{"a refusal past its bound", swap(`"refusal": null`, `"refusal": "`+strings.Repeat("r", MaxRefusal+1)+`"`), "its refusal"},
		{"a refusal with a control character", swap(`"refusal": null`, `"refusal": "a\u0007b"`), "its refusal"},
		{"too many layers", swap(`"layers": [`, `"layers": [`+strings.Repeat(`{"change_id": "`+id2+`", "record": "`+record2+`", "version": 1, "branch": "b", "head": "`+head2+`", "published": null}, `, MaxRestackLayers)), "more than the 64"},

		// What the fields say about one another.
		{"no layer", withLayers(t, r, `[]`), "holds no layer"},
		{"no layer for its own change", swap(`"change_id": "`+id1+`", "record"`, `"change_id": "`+strings.Repeat("l", 32)+`", "record"`), "no layer names the change"},
		{"a layer twice", swap(`"change_id": "`+id2+`", "record"`, `"change_id": "`+id1+`", "record"`), "which an earlier layer names"},
		{"a failing layer that is no layer", swap(`"failing_layer": "`+id1+`"`, `"failing_layer": "`+strings.Repeat("l", 32)+`"`), "none of its layers"},
		{"a conflict with no failing layer", swap(`"failing_layer": "`+id1+`"`, `"failing_layer": null`), "names its failing_layer"},
		{"a conflict that published", swap(`"published": null`, `"published": {"head": "`+head1+`", "version": null, "record": null}`), "publishes nothing"},
		{"a block that published", strings.Replace(swap(`"published": null`, `"published": {"head": "`+head1+`", "version": null, "record": null}`), `"result": "conflict"`, `"result": "blocked"`, 1), "publishes nothing"},
		{"a complete attempt with a layer unpublished", strings.Replace(published(), `"published": {"head": "`+head1+`", "version": 2, "record": "`+record1+`"}`, `"published": null`, 1), "a version for every layer"},
		{"a complete attempt with a layer unversioned", strings.Replace(published(), `"version": 2, "record": "`+record1+`"`, `"version": null, "record": null`, 1), "a version for every layer"},
		{"a version without its record", published2(t, `"record": "`+record1+`"`, `"record": null`), "come together"},
		{"a record without its version", published2(t, `"version": 2,`, `"version": null,`), "come together"},
		{"a published head that is no head", published2(t, `"published": {"head": "`+head1+`"`, `"published": {"head": "x"`), "published.head"},
		{"a published record that is no head", published2(t, `"record": "`+record1+`"`, `"record": "x"`), "published.record"},
	} {
		_, err := ParseRestackReport([]byte(c.text))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: error = %v, want one saying %q", c.what, err, c.says)
		}
	}

	// A partial attempt may leave a layer's version unadded, and a block
	// may name the requests that held it and the refusal it met.
	partial := strings.Replace(published(), `"result": "complete"`, `"result": "partial"`, 1)
	partial = strings.Replace(partial, `"version": 2, "record": "`+record1+`"`, `"version": null, "record": null`, 1)
	blocked := strings.Replace(swap(`"result": "conflict"`, `"result": "blocked"`), `"blocking_requests": []`,
		`"blocking_requests": ["refs/the-valley/integration-requests/main/c-`+id1+`", "refs/the-valley/integration-requests/main/topic/one"]`, 1)
	blocked = strings.Replace(blocked, `"refusal": null`, `"refusal": "the branch lower is not at the head its record names"`, 1)
	for what, text := range map[string]string{"a partial attempt": partial, "a block": blocked} {
		if _, err := ParseRestackReport([]byte(text)); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
}

// withLayers is the report r with its layers replaced by the JSON given.
func withLayers(t *testing.T, r, layers string) string {
	t.Helper()
	start := strings.Index(r, `"layers": `)
	end := strings.Index(r, `,
  "failing_layer"`)
	if start < 0 || end < start {
		t.Fatal("the report holds no layers to replace")
	}
	return r[:start] + `"layers": ` + layers + r[end:]
}

// published2 is the published report with one change made to it.
func published2(t *testing.T, from, to string) string {
	t.Helper()
	p := published()
	if !strings.Contains(p, from) {
		t.Fatalf("the published report holds no %q to change", from)
	}
	return strings.Replace(p, from, to, 1)
}

func TestARestackRefNamesATargetAndAChange(t *testing.T) {
	if target, id, ok := ParseRestackRef(RestackRef("main", id1)); !ok || target != "main" || id != id1 {
		t.Errorf("ParseRestackRef(RestackRef(main, %s)) = %s, %s, %v", id1, target, id, ok)
	}
	for _, ref := range []string{
		RestackPrefix + id1,
		RestackPrefix + "main/" + id1[:31],
		RestackPrefix + "ma..in/" + id1,
		Ref("main", id1),
		"refs/the-valley/restacked/main/" + id1,
	} {
		if _, _, ok := ParseRestackRef(ref); ok {
			t.Errorf("ParseRestackRef(%s) read it as a restack report's ref", ref)
		}
	}
}

// The report parser reads bytes a restacker pushes. Whatever they are, it
// returns a report or an error and never panics. What it accepts is a
// report EncodeRestackReport writes back unchanged, and is never something
// encoding/json reads differently.
func FuzzParseRestackReport(f *testing.F) {
	f.Add([]byte(report()))
	f.Add([]byte(published()))
	f.Add([]byte(`{}`))
	f.Add([]byte(strings.Replace(report(), `"refusal": null`, `"refusal": null, "refusal": "x"`, 1)))
	f.Add([]byte(strings.Replace(report(), `"published": null`, `"published": {"head": "x"}`, 1)))
	f.Add([]byte(report() + "[]"))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := ParseRestackReport(data)
		if err != nil {
			return
		}
		out, err := EncodeRestackReport(r)
		if err != nil {
			t.Fatalf("a report ParseRestackReport accepted cannot be encoded: %v", err)
		}
		again, err := ParseRestackReport(out)
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Fatalf("an encoded report read back as %+v, %v; want %+v", again, err, r)
		}
		var lenient RestackReport
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&lenient); err != nil || !reflect.DeepEqual(&lenient, r) {
			t.Fatalf("encoding/json read the report as %+v, %v; want %+v", lenient, err, r)
		}
	})
}

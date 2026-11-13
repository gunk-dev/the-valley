package main

import (
	"reflect"
	"strings"
	"testing"
)

// Main reads clear, clearing or muddy from the walk and the worker's runs:
// a failure, in a build or an evaluation, counts only when it is newer
// than the check's last pass. A landing is named by its change's handle,
// and a main commit without a change ID by its commit ID.
func TestMainLines(t *testing.T) {
	c3, c2, c1 := "c3000000000", "c2000000000", "c1000000000"
	walk := func(checks ...followCheck) mainWalk {
		return mainWalk{checks: checks, walked: []string{c3, c2, c1}, tip: "t3",
			trees: map[string]string{"t3": c3, "t2": c2, "t1": c1}, ids: map[string]string{c3: "kkkk", c2: ""}}
	}
	h := handleMap{{"app", recordTarget, "kkkk"}: "kk"}
	e2e := nixCheck{"worker-e2e", "worker-e2e"}
	due := followCheck{nixCheck: e2e, due: true, covers: []string{c3, c2}, passed: c1}
	failedAt := func(tree string) []checkBuild {
		return []checkBuild{{project: "app", check: "worker-e2e", tree: tree, result: "failed"}}
	}
	// A check that did not evaluate records no build time (attest/check.go).
	page, _, err := parsePage(strings.NewReader(`{"kind":"record","type":"check.result","project":"app","tree":"t2",` +
		`"check":"worker-e2e","data":{"result":"error","eval_ms":900}}` + "\n" + `{"kind":"end","cursor":""}` + "\n"))
	if err != nil || len(page.builds) != 1 {
		t.Fatalf("the page of a check that did not evaluate read as %v, %v", page.builds, err)
	}
	unevaluated := []checkBuild{page.builds[0].checkBuild}
	running := []mainRun{{project: "app", tree: "t3", run: "r", checks: []string{"worker-e2e"}}}
	term := terminal{width: 80}
	for _, tc := range []struct {
		name   string
		walk   mainWalk
		builds []checkBuild
		runs   []mainRun
		want   []string
	}{
		{"passed", walk(followCheck{nixCheck: e2e, passed: c2}), nil, nil,
			[]string{"main  ✓ clear · checks passed at c200000"}},
		{"due", walk(due), nil, nil,
			[]string{"main  ◐ clearing · worker-e2e due · covers kk, c200000"}},
		{"running", walk(due), nil, running,
			[]string{"main  ◐ clearing · worker-e2e running · covers kk, c200000"}},
		{"failed since its pass", walk(due), failedAt("t2"), nil,
			[]string{"main  ✗ muddy · worker-e2e failed at c200000 · covers kk, c200000",
				"  see: valley telemetry read --source worker"}},
		{"did not evaluate", walk(due), unevaluated, nil,
			[]string{"main  ✗ muddy · worker-e2e failed at c200000 · covers kk, c200000",
				"  see: valley telemetry read --source worker"}},
		{"failed before its pass", walk(due), failedAt("t1"), nil,
			[]string{"main  ◐ clearing · worker-e2e due · covers kk, c200000"}},
	} {
		if got := term.mainLines(tc.walk, "main  ", "app", h, tc.builds, tc.runs); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A check request's batch carries no change, as a follow run's does, and
// its start is not a run on main. A check's result counts by the tree it
// is about: at a tree the walk read on main it is a run on main whoever
// asked for it, and at any other it is not, even when the records naming
// its batch's class were not read and it is the check's newest result.
// Each tree is judged by its own newest result, so a pass at an older
// commit's tree hides no failure at a newer one's. A
// worker that names no class ran no request, so its batches with no
// change read as follow runs, as they always did.
func TestARequestBatchIsNotARunOnMain(t *testing.T) {
	c2, c1 := "c2000000000", "c1000000000"
	walk := mainWalk{walked: []string{c2, c1}, tip: "t2", trees: map[string]string{"t2": c2, "t1": c1},
		checks: []followCheck{{nixCheck: nixCheck{"worker-e2e", "worker-e2e"}, due: true, passed: c1}}}
	term := terminal{width: 80}
	read := func(lines ...string) workerPage {
		t.Helper()
		page, _, err := parsePage(strings.NewReader(strings.Join(append(lines, `{"kind":"end","cursor":""}`), "\n") + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	batch := func(typ, run, class, tree string) string {
		data := `"attest_run":"` + run + `","checks":["worker-e2e"]`
		if class != "" {
			data += `,"class":"` + class + `"`
		}
		return `{"kind":"record","type":"` + typ + `","project":"app","tree":"` + tree + `","data":{` + data + `}}`
	}
	result := func(run, tree, result string) string {
		return `{"kind":"record","type":"check.result","run":"` + run + `","project":"app","tree":"` + tree +
			`","check":"worker-e2e","data":{"result":"` + result + `","build_ms":5000}}`
	}
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"a request running at main's tip", []string{batch("batch.start", "q", "request", "t2")},
			"main  ◐ clearing · worker-e2e due"},
		{"a request that failed at a main tree", []string{
			batch("batch.end", "q", "request", "t2"), result("q", "t2", "failed"), batch("batch.start", "q", "request", "t2")},
			"main  ✗ muddy · worker-e2e failed at c200000"},
		{"a request that failed at another tree, its batch unread", []string{result("q", "t9", "failed")},
			"main  ◐ clearing · worker-e2e due"},
		{"a request's newest result at another tree, its batch unread, since a follow run failed", []string{
			result("q", "t9", "passed"),
			batch("batch.end", "f", "follow", "t2"), result("f", "t2", "failed"), batch("batch.start", "f", "follow", "t2")},
			"main  ✗ muddy · worker-e2e failed at c200000"},
		{"a request that passed since a follow run failed", []string{
			batch("batch.end", "q", "request", "t9"), result("q", "t9", "passed"), batch("batch.start", "q", "request", "t9"),
			batch("batch.end", "f", "follow", "t2"), result("f", "t2", "failed"), batch("batch.start", "f", "follow", "t2")},
			"main  ✗ muddy · worker-e2e failed at c200000"},
		{"a request's pass at an older commit's tree since the tip's run failed", []string{
			result("q", "t1", "passed"),
			batch("batch.end", "f", "follow", "t2"), result("f", "t2", "failed"), batch("batch.start", "f", "follow", "t2")},
			"main  ✗ muddy · worker-e2e failed at c200000"},
		{"a follow run running", []string{batch("batch.start", "f", "follow", "t2")},
			"main  ◐ clearing · worker-e2e running"},
		{"a run that names no class", []string{batch("batch.start", "o", "", "t2")},
			"main  ◐ clearing · worker-e2e running"},
		{"a run that names no class and failed", []string{
			batch("batch.end", "o", "", "t2"), result("o", "t2", "failed"), batch("batch.start", "o", "", "t2")},
			"main  ✗ muddy · worker-e2e failed at c200000"},
	} {
		page := read(tc.lines...)
		var builds []checkBuild
		for _, b := range page.builds {
			builds = append(builds, b.checkBuild)
		}
		if got := term.mainLines(walk, "main  ", "app", nil, builds, page.mains); got[0] != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	ready := read(`{"kind":"record","type":"batch.start","project":"app","change":"kkkk","tree":"t2","data":{"attest_run":"g","class":"ready"}}`)
	if len(ready.mains) != 0 {
		t.Errorf("a ready change's batch read as %v", ready.mains)
	}
}

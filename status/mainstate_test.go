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
		if got := term.mainLines(tc.walk, "app", h, tc.builds, tc.runs); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

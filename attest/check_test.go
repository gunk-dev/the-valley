package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// buildChecks builds checks side by side and decides each one from its own
// derivation's build. Here it is driven by a stand-in for nix build that
// holds every build until the test ends it. So how many builds run at
// once, and the order they end in, are the test's to choose, whatever the
// machine. Each derivation's result is injected: a pass, a failure with a
// log, a failure with nothing on standard error, and a build with two
// outputs. Two checks share one derivation, and one check never
// evaluated.
func TestEachBuildDecidesOnlyItsOwnChecks(t *testing.T) {
	built := t.TempDir()
	if err := os.WriteFile(filepath.Join(built, "result"), []byte("built\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	type result struct {
		paths, log string
		err        error
	}
	results := map[string]result{
		"a.drv": {paths: built + "\n"},
		"b.drv": {log: "b: the builder failed\n", err: errors.New("exit status 1")},
		"c.drv": {err: errors.New("signal: killed")},
		"d.drv": {paths: built + "\n" + built + "-dev\n"},
	}
	names := []string{"first", "broken", "silent", "split", "twin", "unevaluated"}
	drvs := []string{"a.drv", "b.drv", "c.drv", "d.drv", "a.drv", ""}
	runs := make([]ranCheck, len(names))
	for i, name := range names {
		runs[i] = ranCheck{out: checkOutcome{spec: checkSpec{name: name, attribute: name}}}
	}
	unevaluated := errors.New("evaluating unevaluated: no such check")
	runs[5].err = unevaluated

	var mu sync.Mutex
	var events []string
	running, most := 0, 0
	note := func(event string, change int) {
		mu.Lock()
		defer mu.Unlock()
		running += change
		most = max(most, running)
		events = append(events, event)
	}
	started, ended := make(chan string, len(results)), make(chan string, len(results))
	release := map[string]chan struct{}{}
	for drv := range results {
		release[drv] = make(chan struct{})
	}
	build := func(drv string) (string, string, error) {
		note("start "+drv, 1)
		started <- drv
		<-release[drv]
		note("end "+drv, -1)
		ended <- drv
		r := results[drv]
		return r.paths, r.log, r.err
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		buildChecks(runs, drvs, 2, build, func(r ranCheck) { note("report "+r.out.spec.name, 0) })
	}()
	await := func(c chan string, what string) string {
		t.Helper()
		select {
		case drv := <-c:
			return drv
		case <-time.After(10 * time.Second):
			t.Fatalf("no build %s", what)
			return ""
		}
	}

	// Two builds at once, started in the order of the checks. A third
	// waits for one of them to end.
	if first, second := await(started, "started"), await(started, "started"); first+second != "a.drvb.drv" && first+second != "b.drva.drv" {
		t.Fatalf("%s and %s started first, want a.drv and b.drv", first, second)
	}
	if len(started) != 0 {
		t.Fatalf("a third build started beside two")
	}
	// The builds end in another order than the checks': b, c and d before
	// a. Each one that ends lets the next start.
	for _, next := range [][2]string{{"b.drv", "c.drv"}, {"c.drv", "d.drv"}, {"d.drv", ""}} {
		close(release[next[0]])
		if drv := await(ended, "ended"); drv != next[0] {
			t.Fatalf("%s ended, want %s", drv, next[0])
		}
		if next[1] != "" {
			if drv := await(started, "started"); drv != next[1] {
				t.Fatalf("%s started, want %s", drv, next[1])
			}
		}
	}
	close(release["a.drv"])
	await(ended, "ended")
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the builds ended and the checks were never all reported")
	}

	// Nothing is reported before the first check's build ends, and then
	// every check is reported in order. Each derivation was built once.
	want := []string{
		"start a.drv", "start b.drv", "end b.drv", "start c.drv", "end c.drv", "start d.drv", "end d.drv", "end a.drv",
		"report first", "report broken", "report silent", "report split", "report twin", "report unevaluated",
	}
	if events[0] == "start b.drv" {
		events[0], events[1] = events[1], events[0]
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("the builds and reports went\n  %q\nwant\n  %q", events, want)
	}
	if most != 2 {
		t.Errorf("%d builds ran at once, with room for 2", most)
	}

	// Each check has its own derivation's outcome and log, and no other's.
	entries, err := fsEntries(built)
	if err != nil {
		t.Fatal(err)
	}
	output := treeDigest(entries)
	for i, check := range []struct {
		passed bool
		log    string
		err    string
	}{
		{passed: true},
		{log: "b: the builder failed"},
		{log: "signal: killed"},
		{err: "split produced 2 outputs; a check attribute must produce exactly one"},
		{passed: true},
		{err: unevaluated.Error()},
	} {
		r := runs[i]
		gotErr := ""
		if r.err != nil {
			gotErr = r.err.Error()
		}
		if r.out.passed != check.passed || r.out.log != check.log || gotErr != check.err {
			t.Errorf("%s: passed %v, log %q, error %q; want passed %v, log %q, error %q",
				names[i], r.out.passed, r.out.log, gotErr, check.passed, check.log, check.err)
		}
		if check.passed && r.out.output != output {
			t.Errorf("%s: output %s, want %s", names[i], r.out.output, output)
		}
		if !check.passed && r.out.output != "" {
			t.Errorf("%s did not pass and still has an output digest", names[i])
		}
	}
}

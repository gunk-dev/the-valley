package main

import (
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"the-valley/telemetry"
)

// A pass prunes exactly the roots no batch has used for longer than the
// age it keeps outputs for, and nothing in the directory that is not a
// root symlink.
func TestPruneRootsRemovesExactlyTheRootsOlderThanTheAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	keep := 7 * 24 * time.Hour
	used := map[string]time.Time{
		"aaaa-stale":     now.Add(-keep - time.Hour),
		"bbbb-dangling":  now.Add(-30 * 24 * time.Hour),
		"cccc-recent":    now.Add(-keep + time.Hour),
		"dddd-today":     now,
		"eeee-not-alink": now.Add(-30 * 24 * time.Hour),
	}
	for name, at := range used {
		path := filepath.Join(dir, name)
		if name == "eeee-not-alink" {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink("/nix/store/"+name, path); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("touch", "-h", "-t", at.Local().Format("200601021504.05"), path).CombinedOutput(); err != nil {
			t.Fatalf("touch %s: %v: %s", path, err, out)
		}
	}

	if err := pruneRoots(dir, keep, now); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := []string{"cccc-recent", "dddd-today", "eeee-not-alink"}
	if !slices.Equal(left, want) {
		t.Errorf("pruning left %v, want %v", left, want)
	}

	if err := pruneRoots(filepath.Join(dir, "missing"), keep, now); err != nil {
		t.Errorf("pruning a roots directory that does not exist yet: %v", err)
	}
}

// testJob is a job in a project, for a change's version first seen in a
// scan, whose batch would store ref.
func testJob(project, id string, seen int, ref, key string) *job {
	return &job{r: listed{source: source{name: project}}, c: jsonChange{ChangeID: id, Handle: id},
		v: jsonVersion{N: 1, Head: "0123456789"}, seen: seen, ref: ref, key: key}
}

// A scan's jobs start in the order their versions were first seen ready,
// then by project, then by change ID, and one job holds each destination:
// a later job for the same project and ref is the same tree, and is
// dropped. The same ref in another project is another destination.
func TestOrderIsFirstSeenThenProjectThenChangeAndOneJobPerDestination(t *testing.T) {
	w := &worker{said: map[string]map[string]bool{}, stdout: io.Discard}
	a := testJob("app", "m", 1, "refs/one", "")
	b := testJob("app", "l", 1, "refs/two", "")
	c := testJob("site", "a", 1, "refs/one", "")
	d := testJob("app", "b", 2, "refs/two", "")
	e := testJob("app", "c", 3, "refs/one", "")
	f := testJob("app", "d", 2, "refs/three", "")
	got := w.order([]*job{e, d, c, a, f, b})
	want := []*job{b, a, c, f}
	if !slices.Equal(got, want) {
		var names []string
		for _, j := range got {
			names = append(names, j.r.name+"/"+j.c.ChangeID)
		}
		t.Errorf("the queue is %v, want app/l app/m site/a app/d", names)
	}
}

// A scan that read a project whole forgets the failures it did not meet,
// except the key of a batch still running there, whose failure is
// recorded once it ends. Other projects' failures are kept.
func TestPruneKeepsTheKeysOfRunningBatches(t *testing.T) {
	record := &failureRecord{Projects: map[string]map[string]backoff{
		"app":  {"build one a": {Failures: 1}, "build two b": {Failures: 1}, "push 0a1b": {Failures: 2}},
		"site": {"build one a": {Failures: 1}},
	}}
	running := testJob("app", "x", 1, "refs/one", "build one a")
	w := &worker{failures: record, running: map[string]*batch{running.dest(): {job: running}}}
	w.prune("app", map[string]bool{"push 0a1b": true})
	var got []string
	for key := range record.Projects["app"] {
		got = append(got, key)
	}
	sort.Strings(got)
	if !slices.Equal(got, []string{"build one a", "push 0a1b"}) {
		t.Errorf("app keeps %v, want the running batch's key and the push the scan met", got)
	}
	if len(record.Projects["site"]) != 1 {
		t.Errorf("pruning app changed site's failures: %v", record.Projects["site"])
	}
}

// The time spans the host module passes as --interval read as systemd
// reads a timer's, and the ones systemd refuses are refused.
func TestTimeSpanReadsSystemdSpans(t *testing.T) {
	month, year := 2629800*time.Second, 31557600*time.Second
	for in, want := range map[string]time.Duration{
		"1m": time.Minute, "15s": 15 * time.Second, "5min": 5 * time.Minute, "1h 30min": 90 * time.Minute,
		"1m30s": 90 * time.Second, "90": 90 * time.Second, "2 days": 48 * time.Hour, "1.5h": 90 * time.Minute,
		"0": 0, " 2months": 2 * month, "1y 1M": year + month, "1 year": year, "3weeks": 21 * 24 * time.Hour,
		"5ms": 5 * time.Millisecond, "7\u00b5s": 7 * time.Microsecond, "7\u03bcs": 7 * time.Microsecond,
		".5s": 500 * time.Millisecond, "1.0000005s": time.Second, "5 3": 8 * time.Second,
		"infinity": math.MaxInt64, " infinity ": math.MaxInt64,
		// Longer than a time.Duration holds, but not than systemd reads.
		"300y": math.MaxInt64, "9223372036854775807us": math.MaxInt64,
	} {
		if got, err := timeSpan(in); err != nil || got != want {
			t.Errorf("timeSpan(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", " ", "m", "5 fortnights", "-1m", "1m x", "5mins", "12.34.56", "3.s", "1.", "+5s", "infinity 1s", "1 infinity",
		// As many microseconds as systemd's infinity, or more.
		"18446744073709551615us", "9223372036854775808", "584543y", "500000y 500000y",
	} {
		if got, err := timeSpan(in); err == nil {
			t.Errorf("timeSpan(%q) = %v, want an error", in, got)
		}
	}
}

// A read of origin that fails while two batches run stops the pass from
// starting anything more: the job queued behind them never starts, and no
// read follows. The loop still takes both batches' ends, which come after
// the failed read began, and records each one's failure and wait, with its
// attest run. The pass then fails with the read's error, and leaves its
// claim, including the mark it claimed for that read, unacknowledged.
func TestAFailedReadStopsAdmissionAndDrainsTheRunningBatches(t *testing.T) {
	state, wakeDir, telDir := t.TempDir(), t.TempDir(), t.TempDir()
	mark := func() {
		if err := os.WriteFile(filepath.Join(wakeDir, "due"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mark()
	claim := claimWake(wakeDir, io.Discard)
	defer claim.close()
	// The emitter drops a record it cannot store within its deadline, and
	// every record emitted while that store is still under way, so on a
	// loaded machine the records below would be missing. Each record is
	// waited for instead.
	tel := openWorkerTelemetry(telDir)
	tel.tel.SetDeadline(time.Minute)
	w := &worker{batches: 2, interval: time.Millisecond, wake: claim, stdout: io.Discard, stderr: io.Discard,
		seen: map[string]int{}, running: map[string]*batch{}, said: map[string]map[string]bool{},
		tel: tel, failures: readFailures(filepath.Join(state, "failures.json"), io.Discard)}
	jobs := []*job{testJob("app", "a", 1, "refs/a", "build a"), testJob("app", "b", 1, "refs/b", "build b"),
		testJob("app", "c", 1, "refs/c", "build c")}
	var launched []*batch
	reads := 0
	w.read = func(git, string) ([]*job, error) {
		reads++
		if reads == 1 {
			mark() // a ready push while the pass reads
			return jobs, nil
		}
		// Both batches end while this read is under way, and fail.
		for _, b := range launched {
			go func() { w.done <- completion{b: b, err: errors.New("exit status 1"), ended: time.Now()} }()
		}
		return nil, failure("app: origin did not answer; the pass stops here")
	}
	w.recheck = func(*job) (bool, error) { return true, nil }
	w.launch = func(b *batch) { launched = append(launched, b) }

	err := w.pass(git{}, []string{"app"})
	if err == nil || err.Error() != "app: origin did not answer; the pass stops here" {
		t.Fatalf("the pass returned %v, want the read's error", err)
	}
	if reads != 2 {
		t.Errorf("the pass read %d times, want 2: none after the read that failed", reads)
	}
	if len(launched) != 2 || launched[0].job != jobs[0] || launched[1].job != jobs[1] {
		t.Errorf("the pass started %d batches, want a's and b's alone", len(launched))
	}
	if len(w.running) != 0 {
		t.Errorf("the pass ended with %d batches running", len(w.running))
	}
	record := readFailures(filepath.Join(state, "failures.json"), io.Discard).Projects["app"]
	for _, key := range []string{"build a", "build b"} {
		if record[key].Failures != 1 {
			t.Errorf("failures.json holds %q as %+v, want one failure", key, record[key])
		}
	}
	if _, err := os.Stat(filepath.Join(wakeDir, "claimed")); err != nil {
		t.Errorf("the claim was acknowledged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wakeDir, "due")); err == nil {
		t.Error("the mark made during the first read was not claimed before the second")
	}
	recs, _, err := (&telemetry.Stream{Dir: w.tel.stream()}).Read(0, 1000, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range launched {
		for _, typ := range []string{"batch.start", "batch.end", "backoff"} {
			n := 0
			for _, r := range recs {
				if r.Type == typ && r.Data["attest_run"] == b.run {
					n++
				}
			}
			if n != 1 {
				t.Errorf("the stream holds %d %s records naming attest run %s, want 1", n, typ, b.run)
			}
		}
	}
}

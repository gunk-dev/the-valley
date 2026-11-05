package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An evaluation starts when the reservations made before it, plus its
// own estimate, fit the budget, or when it is first in line. Reservations
// made after it never hold it back, and one made before it that still
// waits counts as if it ran, so runs start in the order they asked.
func TestFits(t *testing.T) {
	r := func(order, estimate int64) reservation { return reservation{Order: order, Estimate: estimate} }
	for _, c := range []struct {
		why    string
		others []reservation
		mine   reservation
		want   bool
	}{
		{"first in line", nil, r(1, 6), true},
		{"fits exactly beside one", []reservation{r(1, 4)}, r(2, 6), true},
		{"does not fit beside one", []reservation{r(1, 4)}, r(2, 7), false},
		{"larger than the budget, first in line, runs alone", []reservation{r(5, 1)}, r(3, 20), true},
		{"larger than the budget, behind another, waits", []reservation{r(1, 1)}, r(2, 20), false},
		{"behind one larger than the budget, waits", []reservation{r(1, 20)}, r(2, 1), false},
		{"reservations made later do not count", []reservation{r(4, 9), r(5, 9)}, r(3, 5), true},
		{"a run that waits ahead is not passed over", []reservation{r(1, 3), r(2, 8)}, r(3, 2), false},
		{"several fit together", []reservation{r(1, 3), r(2, 3)}, r(3, 4), true},
		{"huge estimates do not wrap around to fit", []reservation{r(1, 1<<62), r(2, 1<<62)}, r(3, 1<<62), false},
		{"a huge estimate behind a small one waits", []reservation{r(1, 1)}, r(2, 1<<63-1), false},
	} {
		if got := fits(c.others, c.mine, 10); got != c.want {
			t.Errorf("%s: fits = %v, want %v", c.why, got, c.want)
		}
	}
}

// hold makes a reservation file as a running attest does and keeps its
// flock, as another process would, until the test releases it.
func hold(t *testing.T, dir string, r reservation) func() {
	t.Helper()
	data, _ := json.Marshal(r)
	path := filepath.Join(dir, strconv.Itoa(r.PID))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return func() { os.Remove(path); f.Close() }
}

// A reservation no running attest holds, left by one that was killed,
// does not count and is removed; a held one counts; files not named by a
// pid are not reservations. A held file that is not a valid reservation
// for its pid counts as stale too: its estimate or place in line is
// negative, too large to follow or to parse, or it names another pid.
func TestScanSkipsStaleReservations(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "4000001")
	data, _ := json.Marshal(reservation{Order: 1, PID: 4000001, Estimate: 9})
	if err := os.WriteFile(stale, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.peak"), []byte("7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer hold(t, dir, reservation{Order: 2, PID: 4000002, Estimate: 3})()
	invalid := []string{
		`{"order":1,"pid":4000003,"estimate_bytes":-5}`,
		`{"order":-1,"pid":4000004,"estimate_bytes":5}`,
		`{"order":9223372036854775807,"pid":4000005,"estimate_bytes":5}`,
		`{"order":1,"pid":4000006,"estimate_bytes":92233720368547758070}`,
		`{"order":1,"pid":1,"estimate_bytes":5}`,
		`not json`,
	}
	for i, text := range invalid {
		path := filepath.Join(dir, strconv.Itoa(4000003+i))
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
	}
	a := &admission{dir: dir, estimate: 4, budget: 10}
	live, err := a.look(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].PID != 4000002 || live[0].Estimate != 3 {
		t.Fatalf("live reservations %+v, want only the held one", live)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("the stale reservation is still there: %v", err)
	}
	for i := range invalid {
		if _, err := os.Stat(filepath.Join(dir, strconv.Itoa(4000003+i))); !os.IsNotExist(err) {
			t.Errorf("the invalid reservation %s is still there: %v", invalid[i], err)
		}
	}
}

// A run that cannot make its reservation does not evaluate: runPure
// fails before it runs any nix, and before it reports any check.
func TestAdmissionFailureEvaluatesNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	ran := filepath.Join(bin, "ran")
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte("#!/bin/sh\ntouch '"+ran+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	reported := false
	runs, err := runPure(t.TempDir(), "x86_64-linux", []checkSpec{{name: "a", attribute: "a"}}, false, 1, "",
		&admission{dir: filepath.Join(file, "reservations"), estimate: 1, budget: 10}, "", func(ranCheck) { reported = true }, nil)
	if err == nil || runs != nil || reported {
		t.Fatalf("runPure = %v, %v, reported %v; want an error and nothing run", runs, err, reported)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatalf("nix ran without a reservation: %v", err)
	}
}

// The evaluation helper is attest in TestKilledRunHoldsItsReservation: it
// admits an evaluation through the reservations directory it is given and
// evaluates one check with the nix first on PATH, which never ends.
func TestEvaluationHelper(t *testing.T) {
	dir := os.Getenv("ATTEST_HELPER_RESERVATIONS")
	if dir == "" {
		t.Skip("run by TestKilledRunHoldsItsReservation")
	}
	runPure(t.TempDir(), "x86_64-linux", []checkSpec{{name: "slow", attribute: "slow"}}, false, 1, "",
		&admission{dir: dir, estimate: 5, budget: 10}, "", nil, nil)
}

// gone says whether a process has exited: it is no longer listed, or it
// is a zombie, which holds no files.
func gone(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	return len(fields) == 0 || fields[0] == "Z"
}

// A run killed during its evaluation takes its nix eval with it, and its
// reservation is held while any process of that evaluation still runs:
// here nix leaves behind a child that inherited the locked file, and the
// reservation counts until that child is gone too.
func TestKilledRunHoldsItsReservation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a child is killed with its parent only on Linux")
	}
	dir, bin, pids := t.TempDir(), t.TempDir(), t.TempDir()
	script := "#!/bin/sh\necho $$ > '" + pids + "/nix.new' && mv '" + pids + "/nix.new' '" + pids + "/nix'\n" +
		"sleep 300 &\necho $! > '" + pids + "/child.new' && mv '" + pids + "/child.new' '" + pids + "/child'\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	attest := exec.Command(os.Args[0], "-test.run=^TestEvaluationHelper$")
	attest.Env = append(os.Environ(), "ATTEST_HELPER_RESERVATIONS="+dir, "PATH="+bin+":"+os.Getenv("PATH"))
	if err := attest.Start(); err != nil {
		t.Fatal(err)
	}
	pidOf := func(name string) int {
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if text, err := os.ReadFile(filepath.Join(pids, name)); err == nil {
				pid, _ := strconv.Atoi(strings.TrimSpace(string(text)))
				t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
				return pid
			}
		}
		attest.Process.Kill()
		t.Fatalf("the evaluation's %s never started", name)
		return 0
	}
	nix, child := pidOf("nix"), pidOf("child")
	looker := &admission{dir: dir, budget: 10}
	held := func() bool {
		live, err := looker.look(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		return len(live) == 1 && live[0].PID == attest.Process.Pid
	}
	if !held() {
		t.Fatal("no reservation counts while the run evaluates")
	}

	attest.Process.Kill()
	attest.Wait()
	for deadline := time.Now().Add(10 * time.Second); !gone(nix); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("nix eval outlived the attest that started it")
		}
	}
	if !held() {
		t.Fatal("the reservation stopped counting while a process of the evaluation still runs")
	}

	syscall.Kill(child, syscall.SIGKILL)
	for deadline := time.Now().Add(10 * time.Second); held(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the reservation still counts after every process of the evaluation is gone")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, strconv.Itoa(attest.Process.Pid))); !os.IsNotExist(err) {
		t.Fatalf("the killed run's reservation was not removed: %v", err)
	}
}

// A run whose estimate does not fit beside a running evaluation waits
// until that evaluation's reservation goes, then starts, and its own
// reservation is gone once it releases it. A stale reservation ahead of
// it never holds it back.
func TestAdmitWaitsForRoom(t *testing.T) {
	dir := t.TempDir()
	stale, _ := json.Marshal(reservation{Order: 1, PID: 4000001, Estimate: 10})
	if err := os.WriteFile(filepath.Join(dir, "4000001"), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	release := hold(t, dir, reservation{Order: 2, PID: 4000002, Estimate: 6})
	go func() {
		time.Sleep(600 * time.Millisecond)
		release()
	}()
	waited, held, done, err := admit(&admission{dir: dir, estimate: 6, budget: 10})
	if err != nil || held == nil {
		t.Fatalf("admit: %v, held %v", err, held)
	}
	if waited < 500*time.Millisecond {
		t.Errorf("admitted after %v, before the running evaluation ended", waited)
	}
	mine := filepath.Join(dir, strconv.Itoa(os.Getpid()))
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("no reservation while evaluating: %v", err)
	}
	done()
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatalf("the reservation outlived the evaluation: %v", err)
	}

	// With room for both, a run starts at once beside a running one.
	defer hold(t, dir, reservation{Order: 1, PID: 4000003, Estimate: 4})()
	waited, _, done, err = admit(&admission{dir: dir, estimate: 6, budget: 10})
	if err != nil {
		t.Fatal(err)
	}
	done()
	if waited > 200*time.Millisecond {
		t.Errorf("a run that fits waited %v", waited)
	}
}

// A reservation still held under this run's pid, by an evaluation of an
// earlier run with the same pid, is neither emptied nor replaced: the run
// fails, and the held reservation still counts.
func TestReserveLeavesHeldReservationAlone(t *testing.T) {
	dir := t.TempDir()
	held := reservation{Order: 3, PID: os.Getpid(), Estimate: 7}
	release := hold(t, dir, held)
	defer release()
	path := filepath.Join(dir, strconv.Itoa(os.Getpid()))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, f, err := reserve(&admission{dir: dir, estimate: 1, budget: 10}); err == nil {
		f.Close()
		t.Fatal("reserve succeeded over a held reservation")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("held reservation is now %q, %v; want %q", after, err, before)
	}
	others, err := (&admission{dir: dir}).look(os.Getpid() + 1)
	if err != nil || len(others) != 1 || others[0] != held {
		t.Fatalf("look = %v, %v; want the held reservation to count", others, err)
	}
}

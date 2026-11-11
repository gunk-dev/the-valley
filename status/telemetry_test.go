package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"the-valley/telemetry"
)

// TestMain runs the test binary as the fake ssh the telemetry tests put
// on PATH, when it is invoked under that name, and as the checks read a
// review starts, which runs this program's checkline (walk_test.go).
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "ssh" {
		os.Exit(fakeSSH(os.Args[1:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "checkline" {
		os.Exit(fakeCheckline())
	}
	os.Exit(m.Run())
}

// fakeSSH is a telemetry receiver per destination: it keeps every report
// line it is sent in $FAKE_SSH_DIR/<destination>.jsonl and acknowledges
// the newest record of each sender epoch, as the collector does. A
// destination with a .down file beside it cannot be reached.
func fakeSSH(args []string) int {
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o", "-p":
			i++
		case "--":
			rest = args[i+1:]
			i = len(args)
		}
	}
	if len(rest) != 2 {
		fmt.Fprintln(os.Stderr, "fake ssh: no destination and command")
		return 255
	}
	dest, command := rest[0], rest[1]
	dir := os.Getenv("FAKE_SSH_DIR")
	if _, err := os.Stat(filepath.Join(dir, dest+".down")); err == nil {
		fmt.Fprintf(os.Stderr, "ssh: connect to host %s: Connection refused\n", dest)
		return 255
	}
	if command != "valley-telemetry report" {
		return 1
	}
	body, _ := io.ReadAll(os.Stdin)
	f, err := os.OpenFile(filepath.Join(dir, dest+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 1
	}
	f.Write(body)
	f.Close()
	acked := map[string]int64{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var r telemetry.Record
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		epoch, seq, _ := strings.Cut(r.ClientID, ":")
		n, _ := strconv.ParseInt(seq, 10, 64)
		acked[epoch] = max(acked[epoch], n)
	}
	ack, _ := json.Marshal(map[string]any{"kind": "ack", "acked": acked})
	fmt.Printf("%s\n{\"kind\":\"end\"}\n", ack)
	return 0
}

// received is what a fake receiver holds: each record by its client ID.
func received(t *testing.T, dir, dest string) []telemetry.Record {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, dest+".jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var recs []telemetry.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		r, err := telemetry.DecodeRecord(sc.Bytes())
		if err != nil {
			t.Fatalf("%s received %q: %v", dest, sc.Text(), err)
		}
		recs = append(recs, r)
	}
	return recs
}

// Each approval run's records go to the valley its checkout named when
// the run began, and to no other: whichever checkout or remote a flush
// runs from, whatever the checkout's remote says later, whichever
// receiver is down, and however many flushes run at once.
func TestSpoolsAreBoundToTheirDestinations(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatal("no git on PATH, and VALLEY_TEST_TOOLS=required")
		}
		t.Skip("no git on PATH")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	recv := t.TempDir()
	t.Setenv("FAKE_SSH_DIR", recv)
	bin := t.TempDir()
	self, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "ssh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	checkout := func(origin string) git {
		dir := t.TempDir()
		for _, args := range [][]string{{"init", "--quiet"}, {"remote", "add", "origin", origin}} {
			if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		return git{env: gitEnvironment(os.Environ()), stderr: io.Discard, dir: dir}
	}
	tele := func(g git, args ...string) (string, error) {
		var out, errs bytes.Buffer
		err := runTelemetry(g, args, &out, &errs)
		return out.String() + errs.String(), err
	}
	start := func(g git) (run, spool, project string) {
		t.Helper()
		out, err := tele(g, "start")
		if err != nil {
			t.Fatalf("start: %v %s", err, out)
		}
		f := strings.Split(strings.TrimSpace(out), "\t")
		if len(f) != 3 {
			t.Fatalf("start printed %q", out)
		}
		return f[0], f[1], f[2]
	}
	emit := func(g git, spool, run, typ string) {
		t.Helper()
		if out, err := tele(g, "emit", "--spool", spool, "--run", run, "--type", typ); err != nil {
			t.Fatalf("emit: %v %s", err, out)
		}
	}
	output := func(spool, run, text string) {
		l := (&telemetry.Stream{Dir: spool}).Log(run+"-checks", "slow", 1<<20)
		l.Write([]byte(text + "\n"))
		l.Close()
	}

	a := checkout("ssh://git@valley-a/app.git")
	b := checkout("git@valley-b:site.git")
	runA, spoolA, projectA := start(a)
	runB, spoolB, projectB := start(b)
	if spoolA == spoolB || projectA != "app" || projectB != "site" {
		t.Fatalf("spools %s %s, projects %s %s", spoolA, spoolB, projectA, projectB)
	}
	// Interleaved, as two approvals running at once write them.
	emit(a, spoolA, runA, "approve.start")
	emit(b, spoolB, runB, "approve.start")
	output(spoolA, runA, "output of app")
	emit(a, spoolA, runA, "approve.checks")
	output(spoolB, runB, "output of site")
	emit(b, spoolB, runB, "approve.checks")

	// valley-b is down. A flush from b's checkout still sends a's run to
	// valley-a alone, and says what it could not send.
	os.WriteFile(filepath.Join(recv, "git@valley-b.down"), nil, 0o600)
	if out, err := tele(b, "flush"); err == nil || !strings.Contains(out, "records sent to ssh://git@valley-a") {
		t.Fatalf("flush with valley-b down: %v %s", err, out)
	}
	if got := received(t, recv, "git@valley-b"); got != nil {
		t.Fatalf("valley-b, down, received %v", got)
	}

	// a's checkout moves to valley-b. Its open run still belongs to
	// valley-a; only a run begun after the move goes to valley-b.
	if out, err := exec.Command("git", "-C", a.dir, "remote", "set-url", "origin", "git@valley-b:app.git").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	emit(a, spoolA, runA, "approve.end")
	runA2, spoolA2, projectA2 := start(a)
	if spoolA2 != spoolB || projectA2 != "app" {
		t.Fatalf("a run begun after the move is in %s for %s, not valley-b's %s", spoolA2, projectA2, spoolB)
	}
	emit(a, spoolA2, runA2, "approve.start")
	// --remote limits a flush to that destination's spool: it neither
	// reroutes another's nor sends it.
	if out, err := tele(b, "--remote", "ssh://git@valley-a", "flush"); err != nil || strings.Contains(out, "valley-b") {
		t.Fatalf("flush --remote valley-a: %v %s", err, out)
	}

	// valley-b is back, and two flushes race for its spool.
	os.Remove(filepath.Join(recv, "git@valley-b.down"))
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := flushSpool(spoolB, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if out, err := tele(a, "flush"); err != nil || !strings.Contains(out, "0 records sent to ssh://git@valley-b") {
		t.Fatalf("a last flush: %v %s", err, out)
	}

	want := map[string][]string{
		"git@valley-a": {runA, runA + "-checks"},
		"git@valley-b": {runB, runB + "-checks", runA2},
	}
	texts := map[string]string{"git@valley-a": "output of app", "git@valley-b": "output of site"}
	for dest, runs := range want {
		got := received(t, recv, dest)
		seen := map[string]bool{}
		for _, r := range got {
			if seen[r.ClientID] {
				t.Errorf("%s received %s twice", dest, r.ClientID)
			}
			seen[r.ClientID] = true
			ok := false
			for _, run := range runs {
				ok = ok || r.Run == run
			}
			if !ok {
				t.Errorf("%s received run %s's %s", dest, r.Run, r.Type)
			}
			if text, _ := r.Data["text"].(string); r.Type == "check.output" && !strings.Contains(text, texts[dest]) {
				t.Errorf("%s received output %q", dest, text)
			}
		}
		for _, run := range runs {
			found := false
			for _, r := range got {
				found = found || r.Run == run
			}
			if !found {
				t.Errorf("%s never received run %s", dest, run)
			}
		}
	}
	if n := len(received(t, recv, "git@valley-a")); n != 4 {
		t.Errorf("valley-a received %d records, want approve.start, approve.checks, check.output and approve.end", n)
	}

	// The spool of an older CLI named no destination: it is kept, said,
	// and never sent.
	legacy, _ := legacySpool()
	if _, err := (&telemetry.Stream{Dir: legacy}).Append(&telemetry.Record{Type: "approve.start", Time: telemetry.Stamp(time.Now()), Run: "rlegacy"}); err != nil {
		t.Fatal(err)
	}
	before := len(received(t, recv, "git@valley-a")) + len(received(t, recv, "git@valley-b"))
	if out, err := tele(a, "flush"); err != nil || !strings.Contains(out, "never sent") {
		t.Fatalf("flush with a legacy spool: %v %s", err, out)
	}
	if after := len(received(t, recv, "git@valley-a")) + len(received(t, recv, "git@valley-b")); after != before {
		t.Fatal("the legacy spool was sent")
	}
	// Emitting into a directory that is no bound spool stores nothing.
	emit(a, legacy, "rlegacy", "approve.end")
	if rng, _ := (&telemetry.Stream{Dir: legacy}).Range(); rng.Last != 1 {
		t.Fatalf("the legacy spool took a record: %+v", rng)
	}
}

// The spools stay bounded: a fifth destination finds no room while four
// hold records to send or were used within the hour, and takes the place
// of one that has sent everything and lain idle.
func TestTheSpoolsAreBounded(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var dirs []string
	for i := 0; i < maxSpools; i++ {
		dir, err := bindSpool(sshDest{host: fmt.Sprintf("git@valley-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	if _, err := bindSpool(sshDest{host: "git@valley-x"}); err == nil || !regexp.MustCompile(`not recorded`).MatchString(err.Error()) {
		t.Fatalf("a fifth spool: %v", err)
	}
	old := time.Now().Add(-2 * idleAfter)
	entries, _ := os.ReadDir(dirs[0])
	for _, e := range entries {
		os.Chtimes(filepath.Join(dirs[0], e.Name()), old, old)
	}
	if _, err := bindSpool(sshDest{host: "git@valley-x"}); err != nil {
		t.Fatalf("a fifth spool beside an idle one: %v", err)
	}
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Fatalf("the idle spool is still there: %v", err)
	}
	if _, err := bindSpool(sshDest{host: "git@valley-1"}); err != nil {
		t.Fatalf("an existing spool: %v", err)
	}
}

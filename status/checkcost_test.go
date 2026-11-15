package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/telemetry"
)

// A host or a proxy that says far more on ssh's stderr than a read keeps
// costs it no more than sshSaidLimit bytes: the read's error carries the
// last of what was said, which says why it failed.
func TestAReadKeepsTheLastOfWhatSSHSaid(t *testing.T) {
	dir := t.TempDir()
	toolsOnPath(t, dir)
	ssh := "#!/bin/sh\nhead -c 1048576 /dev/zero | tr '\\0' x >&2\necho ' the host went away' >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := readPage(context.Background(), sshDest{host: "valley-host"}, "")
	if err == nil || !strings.HasSuffix(err.Error(), "x the host went away") || len(err.Error()) > sshSaidLimit+200 {
		t.Errorf("the read said %d bytes, ending %q", len(fmt.Sprint(err)), fmt.Sprint(err)[max(len(fmt.Sprint(err))-60, 0):])
	}
}

// One valley pending's history reads its older pages of the worker's
// stream at once, and a host whose collector gives the principal
// telemetry.BoundedSlots bounded slots admits every one: each page's
// build is read, none refused. Each older page's read joins a barrier
// and tells it whether it took a slot; an admitted one keeps its slot and
// answers only once every older page has told, so the slots held when
// the last one tells are held at once, and every one must be.
func TestAHistoryReadFitsTheBoundedSlots(t *testing.T) {
	dir, bin := t.TempDir(), t.TempDir()
	if err := os.Symlink(os.Args[0], filepath.Join(bin, "ssh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SSH_POOL", dir)
	barrier, err := net.Listen("unix", filepath.Join(dir, "barrier"))
	if err != nil {
		t.Fatal(err)
	}
	older := costRecords/telemetry.MaxReadLimit - 1
	held := make(chan int, 1)
	go func() {
		var joined []net.Conn
		defer func() {
			for _, c := range joined {
				c.Close()
			}
		}()
		n := 0
		for len(joined) < older {
			c, err := barrier.Accept()
			if err != nil {
				held <- -1
				return
			}
			joined = append(joined, c)
			c.SetReadDeadline(time.Now().Add(time.Minute))
			told, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				held <- -1
				return
			}
			if told == "held\n" {
				n++
			}
		}
		for _, c := range joined {
			c.Write([]byte("go\n"))
		}
		held <- n
	}()
	builds, _, _, why := readBuilds(context.Background(), sshDest{host: "valley-host"})
	barrier.Close()
	if n := <-held; n != older {
		t.Errorf("%d of the %d older pages' reads held a slot at once", n, older)
	}
	if pages := costRecords / telemetry.MaxReadLimit; why != "" || len(builds) != pages {
		t.Errorf("the history read %d of %d pages' builds: %q", len(builds), pages, why)
	}
}

// fakePooledSSH is a host whose collector admits telemetry.BoundedSlots
// reads at once, as it does for one principal, and refuses any more. The
// newest page answers at once. An older page's read joins the test's
// barrier, tries for a slot and tells the barrier whether it holds one;
// admitted, it answers once the barrier says go, and fails when the
// barrier closes without saying it.
func fakePooledSSH(dir string, args []string) int {
	_, command, ok := sshDestCommand(args)
	if !ok {
		return 255
	}
	req, err := telemetry.ParseCommand(command)
	if err != nil || req.Op != telemetry.OpRead {
		return 1
	}
	answer := func(seq, end int64) {
		fmt.Printf(`{"kind":"record","stream":"worker","seq":%d,"type":"check.result","project":"app","tree":"t1","check":"c-%d","data":{"result":"passed","build_ms":1000}}`+"\n", seq, seq)
		fmt.Printf(`{"kind":"end","cursor":"worker=0123456789abcdef:%d"}`+"\n", end)
	}
	if req.Cursor == "" {
		answer(costRecords, costRecords)
		return 0
	}
	cursor, _ := telemetry.ParseCursor(req.Cursor)
	after := cursor["worker"].Seq
	barrier, err := net.Dial("unix", filepath.Join(dir, "barrier"))
	if err != nil {
		return 1
	}
	defer barrier.Close()
	var slot *os.File
	for i := range telemetry.BoundedSlots {
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("slot.%d", i)), os.O_CREATE|os.O_RDWR, 0o600)
		if err == nil && syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			slot = f
			break
		}
		f.Close()
	}
	if slot == nil {
		barrier.Write([]byte("refused\n"))
		fmt.Printf(`{"kind":"error","error":%q}`+"\n", telemetry.SlotRefusal("operator", telemetry.BoundedSlots, "bounded"))
		return 0
	}
	defer slot.Close()
	if _, err := barrier.Write([]byte("held\n")); err != nil {
		return 1
	}
	if said, _ := bufio.NewReader(barrier).ReadString('\n'); said != "go\n" {
		return 1
	}
	answer(after+1, after+telemetry.MaxReadLimit)
	return 0
}

// lastBytes keeps the last bytes written through it, however they come.
func TestLastBytes(t *testing.T) {
	l := &lastBytes{max: 4}
	for _, w := range []string{"ab", "cde", "f", "ghijklm", "n"} {
		l.Write([]byte(w))
	}
	if string(l.b) != "klmn" {
		t.Errorf("kept %q, not the last four bytes", l.b)
	}
}

// Closing the reads cancels one still running at once, even when a
// process its ssh started, as a ProxyCommand is, holds ssh's output open:
// close never waits for that output, which a wait for ssh would take up
// to sshWaitDelay to give up on.
func TestClosingAReadDoesNotWaitForOutputHeldOpen(t *testing.T) {
	dir := t.TempDir()
	toolsOnPath(t, dir, "git")
	said := filepath.Join(dir, "ssh.pids")
	if err := syscall.Mkfifo(said, 0o600); err != nil {
		t.Fatal(err)
	}
	// ssh starts a process that holds its output, says both process IDs,
	// and stalls.
	ssh := fmt.Sprintf("#!/bin/sh\nsleep 86397 &\necho \"$! $$\" > '%s'\nexec sleep 86396\n", said)
	if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	for _, args := range [][]string{{"init", "--quiet", repo}, {"-C", repo, "config", "valley.telemetry.remote", "ssh://git@valley-host/app.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	c := newCosts()
	read := c.start(git{env: os.Environ(), dir: repo})
	pids := make(chan []string, 1)
	go func() {
		data, _ := os.ReadFile(said)
		pids <- strings.Fields(string(data))
	}()
	var held []string
	select {
	case held = <-pids:
	case <-read.done:
		unheard(said)
		t.Fatalf("the read ended before ssh started: %s", read.why)
	case <-time.After(time.Minute):
		unheard(said)
		t.Fatal("the read's ssh did not start in a minute")
	}
	for _, p := range held {
		if n, err := strconv.Atoi(p); err == nil {
			defer syscall.Kill(n, syscall.SIGKILL)
		}
	}
	began := time.Now()
	c.close()
	if took := time.Since(began); took >= sshWaitDelay {
		t.Errorf("closing the read took %s, waiting for the output a process its ssh started held open", took)
	}
}

// A build has grown only when it is slower than its baseline by more than
// both 20% and 30 s.
func TestGrew(t *testing.T) {
	for _, c := range []struct {
		build, baseline int64
		grown           bool
	}{
		{100_000, 60_000, true},           // +40 s, +67%
		{500_000, 400_000, true},          // +100 s, +25%
		{431_000, 363_000, false},         // +68 s, but +18.7%
		{80_000, 60_000, false},           // +20 s, +33%
		{1_230_000, 1_200_000 - 1, false}, // +30 s, +2.5%
		{400_000, 400_000, false},
		{10_000, 400_000, false}, // faster
	} {
		if got := grew(c.build, c.baseline); got != c.grown {
			t.Errorf("grew(%d, %d) = %v, want %v", c.build, c.baseline, got, c.grown)
		}
	}
}

func TestMedian(t *testing.T) {
	for _, c := range []struct {
		ms   []int64
		want int64
	}{
		{[]int64{5}, 5},
		{[]int64{9, 1, 5}, 5},
		{[]int64{4, 1, 3, 2}, 2},
		{[]int64{10, 20}, 15},
	} {
		if got := median(c.ms); got != c.want {
			t.Errorf("median(%v) = %d, want %d", c.ms, got, c.want)
		}
	}
}

// The baseline is the median of a check's newest 20 uncached, passing
// builds in the project, with every version of the change left out.
func TestAddCosts(t *testing.T) {
	me := strings.Repeat("k", 32)
	other := func(i int) string { return fmt.Sprintf("%s%02d", strings.Repeat("m", 30), i) }
	var builds []checkBuild // newest first
	add := func(b checkBuild) {
		if b.project == "" {
			b.project = "app"
		}
		if b.result == "" {
			b.result = checkPassed
		}
		builds = append(builds, b)
	}
	// This version's own builds, in one run.
	add(checkBuild{run: "r2", change: me, version: 2, tree: "t2", check: "slow", buildMS: 200_000, evalMS: 24_000})
	add(checkBuild{run: "r2", change: me, version: 2, tree: "t2", check: "fast", buildMS: 120, evalMS: 24_000, cached: true})
	add(checkBuild{run: "r2", change: me, version: 2, tree: "t2", check: "fresh", buildMS: 9_000, evalMS: 23_000})
	// The change's earlier version, never counted.
	add(checkBuild{run: "r1", change: me, version: 1, tree: "t1", check: "slow", buildMS: 1})
	// Other changes: a cached build, a failed one and another project's
	// are left out, then 25 builds of which the newest 20 count.
	add(checkBuild{change: other(98), check: "slow", buildMS: 2, cached: true})
	add(checkBuild{change: other(99), check: "slow", buildMS: 3, result: "failed"})
	add(checkBuild{project: "site", change: other(97), check: "slow", buildMS: 4})
	for i := 0; i < 25; i++ {
		ms := int64(100_000 + i*1_000) // the newest 20: 100 s to 119 s
		if i >= 20 {
			ms = 900_000
		}
		add(checkBuild{change: other(i), check: "slow", buildMS: ms})
		add(checkBuild{change: other(i), check: "fast", buildMS: 5_000})
	}
	out := &jsonVersionChecks{Tree: "t2", Required: []jsonCheckState{{Name: "slow"}, {Name: "fast"}, {Name: "fresh"}, {Name: "manual"}}}
	required := []jsonRequired{{Name: "slow", Runner: "nix"}, {Name: "fast", Runner: "nix"}, {Name: "fresh", Runner: "nix"}, {Name: "manual", Runner: "command"}}
	addCosts(out, required, "app", me, 2, builds)

	slow, fast, fresh, manual := out.Required[0], out.Required[1], out.Required[2], out.Required[3]
	if slow.BuildMS == nil || *slow.BuildMS != 200_000 || *slow.Cached || slow.BaselineMS == nil || *slow.BaselineMS != 109_500 || !slow.Grown || slow.New {
		t.Errorf("slow: %+v", slow)
	}
	if fast.BuildMS == nil || *fast.BuildMS != 120 || !*fast.Cached || *fast.BaselineMS != 5_000 || fast.Grown {
		t.Errorf("fast: %+v", fast)
	}
	if fresh.BuildMS == nil || *fresh.BuildMS != 9_000 || fresh.BaselineMS != nil || !fresh.New || fresh.Grown {
		t.Errorf("fresh: %+v", fresh)
	}
	if manual.BuildMS != nil || manual.New || manual.BaselineMS != nil {
		t.Errorf("manual: %+v", manual)
	}
	if out.BuildMS == nil || *out.BuildMS != 209_120 || *out.EvalMS != 24_000 {
		t.Errorf("totals: build %v, eval %v", out.BuildMS, out.EvalMS)
	}
	out.State = checkPassed
	for i := range out.Required {
		out.Required[i].State = checkPassed
	}
	want := "checks: 4 passed in 3m29s (eval 24s); slow 200s, +91s against its recent 110s"
	if got := checksLine(out); got != want {
		t.Errorf("checksLine:\n got %s\nwant %s", got, want)
	}
}

// A build is compared only with builds on its own capacity. A check whose
// builds so far ran on another is new, and is not grown.
func TestAddCostsCapacity(t *testing.T) {
	builds := []checkBuild{ // newest first
		{run: "r", change: "me", version: 1, check: "same", buildMS: 200_000, capacity: "big"},
		{run: "r", change: "me", version: 1, check: "resized", buildMS: 200_000, capacity: "big"},
		{change: "o1", check: "same", buildMS: 100_000, capacity: "big"},
		{change: "o1", check: "resized", buildMS: 10_000, capacity: "small"},
	}
	var states []jsonCheckState
	var required []jsonRequired
	for _, name := range []string{"same", "resized"} {
		builds[len(states)].project, builds[len(states)+2].project = "app", "app"
		builds[len(states)].result, builds[len(states)+2].result = checkPassed, checkPassed
		states = append(states, jsonCheckState{Name: name, State: checkPassed})
		required = append(required, jsonRequired{Name: name, Runner: "nix"})
	}
	out := &jsonVersionChecks{State: checkPassed, Required: states}
	addCosts(out, required, "app", "me", 1, builds)
	if same := out.Required[0]; !same.Grown || same.New || *same.BaselineMS != 100_000 {
		t.Errorf("same: %+v", same)
	}
	if resized := out.Required[1]; !resized.New || !resized.OtherCapacity || resized.Grown || resized.BaselineMS != nil {
		t.Errorf("resized: %+v", resized)
	}
}

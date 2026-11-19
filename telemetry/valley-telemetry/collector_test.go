package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"the-valley/telemetry"
)

func testCollector(t *testing.T) (*collector, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	c := &collector{
		manifest: manifest{V: 1, Host: "valley-host", Reporters: []string{"alice"}, Sources: []source{
			{Name: "worker", Enabled: true, Location: "local", Units: []string{"valley-worker.service"}, Stream: worker},
			{Name: "bus", Enabled: false, Location: "local", Units: []string{"valley-bus.service"}},
		}},
		grants: map[string]map[string]bool{"operator": {"observe": true}, "alice": {"report": true}},
		state:  filepath.Join(dir, "collector"),
		now:    time.Now,
		systemctl: func(units []string) ([]unitState, error) {
			var s []unitState
			for _, u := range units {
				s = append(s, unitState{"Id": u, "LoadState": "loaded", "ActiveState": "inactive", "Result": "success"})
			}
			return s, nil
		},
		journal: func([]string, int, bool) ([]journalEntry, error) { return nil, nil },
		horizon: func([]string) string { return "" },
	}
	out := &bytes.Buffer{}
	c.out = &output{w: out}
	return c, out, worker
}

func ask(c *collector, out *bytes.Buffer, req telemetry.Request, body string) (int, []map[string]any) {
	out.Reset()
	c.out = &output{w: out}
	line, _ := json.Marshal(req)
	status := c.handle(bufio.NewReader(strings.NewReader(string(line)+"\n"+body)), nil)
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		json.Unmarshal([]byte(l), &m)
		lines = append(lines, m)
	}
	return status, lines
}

func kind(lines []map[string]any, k string) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if l["kind"] == k {
			out = append(out, l)
		}
	}
	return out
}

func clientRecord(epoch string, seq int, typ string) string {
	r := telemetry.Record{Type: typ, Time: telemetry.Stamp(time.Now()), Run: "r1", ClientID: fmt.Sprintf("%s:%d", epoch, seq)}
	b, _ := json.Marshal(r)
	return string(b) + "\n"
}

// A report is stored under the principal that sent it, stamped when it
// arrived, and a retried record is not stored twice.
func TestAReportIsStampedAndARetryIsHarmless(t *testing.T) {
	c, out, _ := testCollector(t)
	epoch := "0123456789abcdef"
	body := clientRecord(epoch, 1, "approve.start") + clientRecord(epoch, 2, "check.result")
	report := telemetry.Request{V: 1, Principal: "alice", Op: telemetry.OpReport}
	status, lines := ask(c, out, report, body)
	ack := kind(lines, "ack")
	if status != 0 || len(ack) != 1 || ack[0]["accepted"] != 2.0 {
		t.Fatalf("first report: %d %v", status, lines)
	}
	_, lines = ask(c, out, report, body+clientRecord(epoch, 4, "approve.end"))
	ack = kind(lines, "ack")
	if ack[0]["accepted"] != 1.0 || ack[0]["duplicates"] != 2.0 {
		t.Fatalf("retried report: %v", ack)
	}
	recs, _, _ := c.reportStream("alice").Read(0, 100, 0)
	if len(recs) != 4 || recs[2].Type != "report.gap" || recs[0].Principal != "alice" || recs[0].Received == "" {
		t.Fatalf("stored %+v", recs)
	}
}

// What a reporter may not say is refused record by record: a field the
// format lacks, a principal or receipt of its own, a host producer's
// type, a record without its own ID.
func TestAReportCannotSpeakForAnother(t *testing.T) {
	c, out, _ := testCollector(t)
	now := telemetry.Stamp(time.Now())
	body := strings.Join([]string{
		`{"type":"approve.start","time":"` + now + `","client_id":"0123456789abcdef:1","principal":"operator"}`,
		`{"type":"approve.start","time":"` + now + `","client_id":"0123456789abcdef:2","received":"` + now + `"}`,
		`{"type":"pass.start","time":"` + now + `","client_id":"0123456789abcdef:3"}`,
		`{"type":"approve.start","time":"` + now + `","client_id":"0123456789abcdef:4","command":"rm -rf /"}`,
		`{"type":"approve.start","time":"` + now + `"}`,
		`not json`,
	}, "\n") + "\n"
	_, lines := ask(c, out, telemetry.Request{V: 1, Principal: "alice", Op: telemetry.OpReport}, body)
	ack := kind(lines, "ack")
	if len(ack) != 1 || ack[0]["accepted"] != 0.0 || ack[0]["rejected"] != 6.0 {
		t.Fatalf("ack %v", lines)
	}
}

// A report larger than its bound is cut off there, and says so.
func TestAReportIsBounded(t *testing.T) {
	c, out, _ := testCollector(t)
	var body strings.Builder
	for i := 1; i <= telemetry.MaxReportRecords+10; i++ {
		body.WriteString(clientRecord("0123456789abcdef", i, "check.queued"))
	}
	_, lines := ask(c, out, telemetry.Request{V: 1, Principal: "alice", Op: telemetry.OpReport}, body.String())
	ack := kind(lines, "ack")
	if ack[0]["accepted"] != float64(telemetry.MaxReportRecords) || ack[0]["rejected"] != 1.0 {
		t.Fatalf("ack %v", ack)
	}
}

// Reading needs observe, reporting needs report, and neither implies the
// other.
func TestEachOperationNeedsItsGrant(t *testing.T) {
	c, out, _ := testCollector(t)
	for _, req := range []telemetry.Request{
		{V: 1, Principal: "alice", Op: telemetry.OpSources},
		{V: 1, Principal: "operator", Op: telemetry.OpReport},
		{V: 1, Principal: "mallory", Op: telemetry.OpRead},
	} {
		if status, lines := ask(c, out, req, ""); status == 0 || len(kind(lines, "error")) != 1 {
			t.Errorf("%s %s: %d %v", req.Principal, req.Op, status, lines)
		}
	}
}

// A cursor older than what a stream keeps reads as a gap, a cursor of a
// stream made again reads as a reset, and both resume at what is there.
func TestACursorGapIsSaid(t *testing.T) {
	c, out, worker := testCollector(t)
	st := &telemetry.Stream{Dir: worker, SegmentBytes: 1024, Segments: 2}
	for i := 0; i < 40; i++ {
		st.Append(&telemetry.Record{Type: "check.queued", Time: telemetry.Stamp(time.Now()), Run: "r1", Check: fmt.Sprintf("c%d", i)})
	}
	rng, _ := st.Range()
	read := telemetry.Request{V: 1, Principal: "operator", Op: telemetry.OpRead, Source: "worker", Cursor: "worker=" + rng.Epoch + ":3"}
	_, lines := ask(c, out, read, "")
	gaps := kind(lines, "gap")
	if len(gaps) != 1 || gaps[0]["reason"] != "retention" || gaps[0]["to"] != float64(rng.First-1) {
		t.Fatalf("gap %v", lines)
	}
	end := kind(lines, "end")
	if end[0]["cursor"] != fmt.Sprintf("worker=%s:%d", rng.Epoch, rng.Last) {
		t.Fatalf("end %v", end)
	}
	read.Cursor = "worker=fedcba9876543210:3"
	_, lines = ask(c, out, read, "")
	if gaps := kind(lines, "gap"); len(gaps) != 1 || gaps[0]["reason"] != "reset" {
		t.Fatalf("reset %v", lines)
	}
	read.Source = "bus"
	if status, _ := ask(c, out, read, ""); status == 0 {
		t.Fatal("a disabled source read")
	}
}

// A run its stream holds open while no unit of the source runs was
// interrupted; the source says so rather than idle.
func TestAnOpenRunWithNoUnitRunningIsStale(t *testing.T) {
	c, out, worker := testCollector(t)
	st := &telemetry.Stream{Dir: worker}
	st.Append(&telemetry.Record{Type: "pass.start", Time: telemetry.Stamp(time.Now()), Run: "r1"})
	_, lines := ask(c, out, telemetry.Request{V: 1, Principal: "operator", Op: telemetry.OpSnapshot}, "")
	for _, s := range kind(lines, "source") {
		switch s["name"] {
		case "worker":
			if s["state"] != "stale" {
				t.Errorf("worker %v", s)
			}
		case "bus":
			if s["state"] != "disabled" {
				t.Errorf("bus %v", s)
			}
		case "report.alice":
			if s["state"] != "idle" {
				t.Errorf("reports %v", s)
			}
		}
	}
}

// A follower sees build output as it is written, and ends with a cursor.
func TestFollowStreamsBuildOutputAsItIsWritten(t *testing.T) {
	c, out, worker := testCollector(t)
	pollEvery = 50 * time.Millisecond
	st := &telemetry.Stream{Dir: worker}
	st.Append(&telemetry.Record{Type: "run.start", Time: telemetry.Stamp(time.Now()), Run: "r1"})
	log := st.Log("r1", "slow", telemetry.MaxLogBytes)
	log.Write([]byte("before\n"))
	go func() {
		time.Sleep(300 * time.Millisecond)
		log.Write([]byte("during \x1b[31mred\n"))
		st.Append(&telemetry.Record{Type: "check.result", Time: telemetry.Stamp(time.Now()), Run: "r1", Check: "fast"})
	}()
	_, lines := ask(c, out, telemetry.Request{V: 1, Principal: "operator", Op: telemetry.OpFollow, Seconds: 1, Logs: true}, "")
	var text string
	for _, l := range kind(lines, "log") {
		text += l["text"].(string)
		if l["untrusted"] != true {
			t.Errorf("log not marked untrusted: %v", l)
		}
	}
	if !strings.Contains(text, "before") || !strings.Contains(text, `during \x1b[31mred`) {
		t.Fatalf("followed %q", text)
	}
	recs := kind(lines, "record")
	if len(recs) != 1 || recs[0]["check"] != "fast" {
		t.Fatalf("records %v", recs)
	}
	if end := kind(lines, "end"); len(end) != 1 || !strings.HasPrefix(end[0]["cursor"].(string), "worker=") {
		t.Fatalf("end %v", end)
	}
}

// A principal's follows and its bounded requests draw from pools of
// their own: five bounded requests at once are admitted, as one valley
// pending reads four older history pages alongside sources
// (status/checkcost.go, status/hostsources.go); with every follow slot
// held, a sources, snapshot and read are answered and a fifth follow is
// refused, naming follows; with every bounded slot held, a read is
// refused, naming them, and a follow is admitted.
func TestFollowsAndBoundedRequestsTakeSeparateSlots(t *testing.T) {
	c, out, _ := testCollector(t)
	as := func(op string) telemetry.Request { return telemetry.Request{V: 1, Principal: "operator", Op: op} }
	hold := func(op string, n int) []func() {
		t.Helper()
		var held []func()
		for range n {
			release, err := c.slot(as(op))
			if err != nil {
				t.Fatalf("%s slot %d: %v", op, len(held), err)
			}
			held = append(held, release)
		}
		return held
	}
	refused := func(op, what string) {
		t.Helper()
		status, lines := ask(c, out, as(op), "")
		errs := kind(lines, "error")
		if status == 0 || len(errs) != 1 {
			t.Fatalf("%s was not refused: %d %v", op, status, lines)
		}
		if e := errs[0]["error"].(string); !telemetry.IsSlotRefusal(e) || !strings.Contains(e, what) {
			t.Errorf("%s was refused with %q", op, e)
		}
	}

	for _, release := range hold(telemetry.OpRead, telemetry.BoundedSlots) {
		release()
	}

	follows := hold(telemetry.OpFollow, telemetry.FollowSlots)
	for _, op := range []string{telemetry.OpSources, telemetry.OpSnapshot, telemetry.OpRead} {
		if status, lines := ask(c, out, as(op), ""); status != 0 || len(kind(lines, "end")) != 1 {
			t.Errorf("%s with every follow slot held: %d %v", op, status, lines)
		}
	}
	refused(telemetry.OpFollow, "4 follow requests")
	for _, release := range follows {
		release()
	}

	bounded := hold(telemetry.OpSnapshot, telemetry.BoundedSlots)
	refused(telemetry.OpRead, "5 bounded requests")
	hold(telemetry.OpFollow, 1)[0]()
	for _, release := range bounded {
		release()
	}
}

// A slot's holder that dies releases it: a process holds every follow
// slot, a follow is refused, and once the process is killed a follow is
// admitted.
func TestADeadHolderReleasesItsSlots(t *testing.T) {
	follow := telemetry.Request{V: 1, Principal: "operator", Op: telemetry.OpFollow}
	if state := os.Getenv("VALLEY_TEST_SLOT_STATE"); state != "" {
		c := &collector{state: state}
		var held []func()
		for range telemetry.FollowSlots {
			release, err := c.slot(follow)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			held = append(held, release)
		}
		fmt.Println("held")
		time.Sleep(time.Hour)
		// The lock files stay referenced, so no finalizer closes them
		// before the kill.
		runtime.KeepAlive(held)
		os.Exit(1)
	}
	c, _, _ := testCollector(t)
	holder := exec.Command(os.Args[0], "-test.run=^TestADeadHolderReleasesItsSlots$")
	holder.Env = append(os.Environ(), "VALLEY_TEST_SLOT_STATE="+c.state)
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			holder.Process.Kill()
			holder.Wait()
		}
	}
	defer stop()
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); line != "held\n" {
		t.Fatalf("the holder said %q", line)
	}
	if _, err := c.slot(follow); err == nil {
		t.Fatal("a follow was admitted while another process held every follow slot")
	}
	stop()
	release, err := c.slot(follow)
	if err != nil {
		t.Fatalf("a dead holder's slot was not given: %v", err)
	}
	release()
}

// One principal cannot take every slot.
func TestSlotsArePerPrincipal(t *testing.T) {
	c, _, _ := testCollector(t)
	req := telemetry.Request{V: 1, Principal: "alice", Op: telemetry.OpReport}
	release, err := c.slot(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.slot(req); err == nil {
		t.Fatal("a second report slot was given")
	}
	other, err := c.slot(telemetry.Request{V: 1, Principal: "operator", Op: telemetry.OpRead})
	if err != nil {
		t.Fatalf("another principal was refused: %v", err)
	}
	other()
	release()
	if _, err := c.slot(req); err != nil {
		t.Fatalf("a released slot was not given again: %v", err)
	}
	os.RemoveAll(c.state)
}

package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The command is a closed grammar: what parses is exactly what Command
// writes back, and anything else is refused whole.
func TestTheCommandGrammarIsClosed(t *testing.T) {
	for _, ok := range []string{
		"valley-telemetry sources",
		"valley-telemetry snapshot",
		"valley-telemetry read",
		"valley-telemetry read --source=worker --since=worker=0123456789abcdef:12,report.alice=fedcba9876543210:3 --limit=50 --logs",
		"valley-telemetry follow --source=integrator --seconds=30 --logs",
		"valley-telemetry report",
	} {
		r, err := ParseCommand(ok)
		if err != nil {
			t.Errorf("ParseCommand(%q): %v", ok, err)
			continue
		}
		if again, err := ParseCommand(r.Command()); err != nil || again != r {
			t.Errorf("%q does not survive Command: %q", ok, r.Command())
		}
	}
	for _, bad := range []string{
		"", "valley-telemetry", "valley-telemetry  sources", "valley-telemetry sources ",
		"valley-telemetry;sh", "valley-telemetry sources; sh", "valley-telemetry sources && id",
		"valley-telemetry shell", "valley-telemetry exec /bin/sh", "valley-telemetry systemctl restart valley-worker",
		"valley-telemetry read --source=../../etc/passwd", "valley-telemetry read --source=/var/lib/valley-worker/key",
		"valley-telemetry read --source=valley-*", "valley-telemetry read --source=Worker",
		"valley-telemetry read --unit=sshd.service", "valley-telemetry read --source worker",
		"valley-telemetry read --limit=-1", "valley-telemetry read --limit=01", "valley-telemetry read --limit=1e3",
		"valley-telemetry read --logs=yes", "valley-telemetry read --logs --logs",
		"valley-telemetry read --since=worker", "valley-telemetry read --since=worker=xyz:1",
		"valley-telemetry sources --source=worker", "valley-telemetry report --source=worker",
		"valley-telemetry follow --limit=5", "valley-telemetry read\t--logs", "valley-telemetry read --source=w\x00",
		"valley-telemetry read --source=wörker", "valley-telemetry read " + strings.Repeat("x", MaxCommandBytes),
	} {
		if r, err := ParseCommand(bad); err == nil {
			t.Errorf("ParseCommand(%q) = %+v, want a refusal", bad, r)
		}
	}
}

// The collector checks the request line again, and clamps its limits
// whatever was asked.
func TestARequestIsCheckedAgainAndClamped(t *testing.T) {
	r := Request{V: 1, Principal: "alice", Op: OpFollow, Seconds: 99999}
	if err := r.Check(); err != nil || r.Seconds != MaxFollowSeconds {
		t.Fatalf("Check = %v, seconds %d", err, r.Seconds)
	}
	for _, bad := range []Request{
		{V: 1, Op: OpRead},
		{V: 2, Principal: "alice", Op: OpRead},
		{V: 1, Principal: "alice; id", Op: OpRead},
		{V: 1, Principal: "alice", Op: "shell"},
		{V: 1, Principal: "alice", Op: OpSources, Source: "worker"},
		{V: 1, Principal: "alice", Op: OpRead, Source: "/etc/shadow"},
	} {
		if err := bad.Check(); err == nil {
			t.Errorf("Check(%+v) passed", bad)
		}
	}
	if _, err := DecodeRequest([]byte(`{"v":1,"principal":"alice","op":"read","unit":"sshd.service"}`)); err == nil {
		t.Error("a request with a field the format lacks decoded")
	}
}

func record(typ string) *Record {
	return &Record{Type: typ, Time: Stamp(time.Now()), Run: "r1", Data: map[string]any{"pad": strings.Repeat("x", 200)}}
}

// A stream keeps its newest segments and loses its oldest, and what it
// lost shows as the oldest number it still holds.
func TestAStreamIsBoundedAndSaysWhatItLost(t *testing.T) {
	st := &Stream{Dir: t.TempDir(), SegmentBytes: 2048, Segments: 3}
	for i := 1; i <= 100; i++ {
		seq, err := st.Append(record("check.result"))
		if err != nil || seq != int64(i) {
			t.Fatalf("append %d = %d, %v", i, seq, err)
		}
	}
	segs, _ := st.list()
	if len(segs) != 3 {
		t.Fatalf("%d segments, want 3", len(segs))
	}
	var size int64
	for _, s := range segs {
		info, _ := os.Stat(s.path)
		size += info.Size()
	}
	if size > 3*(2048+MaxRecordBytes) {
		t.Fatalf("the stream holds %d bytes", size)
	}
	recs, rng, err := st.Read(0, 1000, 0)
	if err != nil || rng.Last != 100 || rng.First <= 1 || recs[0].Seq != rng.First || recs[len(recs)-1].Seq != 100 {
		t.Fatalf("read: %v, range %+v, %d records from %d", err, rng, len(recs), recs[0].Seq)
	}
	after, _, _ := st.Read(95, 2, 0)
	if len(after) != 2 || after[0].Seq != 96 {
		t.Fatalf("read after 95: %+v", after)
	}
}

// Writers in many processes share a stream: every record gets its own
// number, and none is lost.
func TestConcurrentWritersNumberEveryRecordOnce(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := &Stream{Dir: dir, LockWait: 10 * time.Second}
			for i := 0; i < 25; i++ {
				if _, err := st.Append(record("check.queued")); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	recs, _, _ := (&Stream{Dir: dir}).Read(0, 1000, 0)
	if len(recs) != 200 {
		t.Fatalf("%d records, want 200", len(recs))
	}
	for i, r := range recs {
		if r.Seq != int64(i+1) {
			t.Fatalf("record %d is numbered %d", i, r.Seq)
		}
	}
}

// A line a writer left half written is skipped, and the next record is
// stored whole after it.
func TestAHalfWrittenLineIsSkipped(t *testing.T) {
	st := &Stream{Dir: t.TempDir()}
	st.Append(record("run.start"))
	segs, _ := st.list()
	f, _ := os.OpenFile(segs[0].path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"seq":2,"type":"ru`)
	f.Close()
	if seq, err := st.Append(record("run.end")); err != nil || seq != 2 {
		t.Fatalf("append after a torn line = %d, %v", seq, err)
	}
	recs, _, _ := st.Read(0, 10, 0)
	if len(recs) != 2 || recs[1].Type != "run.end" {
		t.Fatalf("records %+v", recs)
	}
}

// A record that is not one is refused, so a stream holds only records.
func TestOnlyRecordsAreStored(t *testing.T) {
	st := &Stream{Dir: t.TempDir()}
	for _, r := range []*Record{
		{Type: "shell", Time: Stamp(time.Now())},
		{Type: "run.start", Time: "yesterday"},
		{Type: "run.start", Time: Stamp(time.Now()), Head: "not hex"},
		{Type: "run.start", Time: Stamp(time.Now()), Data: map[string]any{"Bad Key": 1}},
		{Type: "run.start", Time: Stamp(time.Now()), Data: map[string]any{"big": strings.Repeat("x", MaxRecordBytes)}},
		{Type: "run.start", Time: Stamp(time.Now()), Data: map[string]any{"nested": map[string]any{"a": 1}}},
	} {
		if _, err := st.Append(r); err == nil {
			t.Errorf("stored %+v", r)
		}
	}
}

// A log keeps what fits under its bound and says it dropped the rest; a
// stream keeps the newest runs' logs.
func TestLogsAreBounded(t *testing.T) {
	st := &Stream{Dir: t.TempDir()}
	l := st.Log("r1", "first", 10)
	l.Write([]byte("0123456789abcdef"))
	if n, cut := l.Close(); n != 16 || !cut {
		t.Fatalf("Close = %d, %v", n, cut)
	}
	if data, _ := os.ReadFile(filepath.Join(st.Dir, "logs", "r1", "first.log")); string(data) != "0123456789" {
		t.Fatalf("log holds %q", data)
	}
	for i := 0; i < keptRuns+4; i++ {
		l := st.Log(fmt.Sprintf("r%d", i+2), "c", 10)
		l.Write([]byte("x"))
		l.Close()
		time.Sleep(2 * time.Millisecond)
	}
	runs, _ := os.ReadDir(filepath.Join(st.Dir, "logs"))
	if len(runs) > keptRuns+1 {
		t.Fatalf("%d run logs kept", len(runs))
	}
	if st.Log("../escape", "c", 10).path != "" || st.Log("r1", "../c", 10).path != "" {
		t.Fatal("a log name left the stream")
	}
}

// Untrusted text cannot drive a terminal.
func TestSanitizeEscapesControls(t *testing.T) {
	got := Sanitize("ok\x1b[2J\x07\r\nnext\xff ")
	if strings.ContainsAny(got, "\x1b\x07\r ") || !strings.Contains(got, `\x1b[2J`) || !strings.Contains(got, "\nnext") {
		t.Fatalf("Sanitize = %q", got)
	}
}

// A store that hangs costs the producer one deadline, and later records
// are dropped at once rather than queued; the loss is said once the
// stream is writable again.
func TestEmittingNeverHoldsUpTheProducer(t *testing.T) {
	dir := t.TempDir()
	e := NewEmitter(dir, Self("test", ""), "r1")
	stored := func(typ string) bool {
		for i := 0; i < 100; i++ {
			recs, _, err := e.stream.Read(0, 100, 0)
			if err == nil && len(recs) > 0 && recs[len(recs)-1].Type == typ {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}
	e.Emit("run.start", Record{}, nil)
	if !stored("run.start") {
		t.Fatal("run.start was not stored")
	}
	lock, err := e.stream.lock()
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	for i := 0; i < 5; i++ {
		e.Emit("check.queued", Record{Check: "c"}, nil)
	}
	if took := time.Since(began); took > 3*EmitDeadline {
		t.Fatalf("five emits behind a held lock took %v", took)
	}
	lock.Close()
	time.Sleep(2 * EmitDeadline)
	e.Emit("run.end", Record{}, nil)
	if !stored("run.end") {
		t.Fatal("run.end was not stored")
	}
	recs, _, _ := e.stream.Read(0, 10, 0)
	last := recs[len(recs)-1]
	if last.Type != "run.end" || last.Data["dropped_before"] == nil {
		t.Fatalf("last record %+v", last)
	}
}

// A log whose writer is stuck drops output rather than holding up the
// build that writes it.
func TestALogNeverWaitsForItsDisk(t *testing.T) {
	l := &LogFile{max: 1 << 20, queue: make(chan []byte, logQueue), done: make(chan struct{}), failed: make(chan struct{})}
	// No drain runs: the queue fills as a stalled disk would leave it.
	began := time.Now()
	for i := 0; i < 4*logQueue; i++ {
		l.Write([]byte("line\n"))
	}
	n, cut := l.Close()
	if took := time.Since(began); took > logCloseWait+time.Second {
		t.Fatalf("writes behind a stuck disk took %v", took)
	}
	if n != int64(4*logQueue*5) || !cut {
		t.Fatalf("Close = %d, %v", n, cut)
	}
}

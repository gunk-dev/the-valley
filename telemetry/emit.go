package telemetry

// Emitting records from a producer, which must never be held up by it.
//
// Telemetry is best effort for the work it describes. Each record is
// stored apart from the caller, which waits at most EmitDeadline for it;
// a record that fails or takes longer is dropped. While one store is
// still stuck, every later record is dropped at once rather than queued
// behind it. The next record stored says how many were dropped before it,
// so a reader sees the loss rather than a quiet stream.

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EmitDeadline is how long a producer waits for one record to be stored.
const EmitDeadline = 500 * time.Millisecond

// Emitter stores one run's records in a stream. A nil Emitter stores
// nothing, so a producer without telemetry passes none.
type Emitter struct {
	stream   *Stream
	producer *Producer
	run      string
	began    time.Time
	busy     chan struct{}

	mu      sync.Mutex
	dropped int
	base    Record
}

// NewEmitter returns an emitter for one run of a producer into the stream
// at dir, or nil when dir is empty. A stream whose directory, or whose
// parent before it is made, its group may read is exported for the
// collector, and keeps what it makes group-readable (Stream.Shared).
func NewEmitter(dir string, producer *Producer, run string) *Emitter {
	if dir == "" {
		return nil
	}
	shared := false
	for _, d := range []string{dir, filepath.Dir(dir)} {
		if info, err := os.Stat(d); err == nil {
			shared = info.Mode().Perm()&0o040 != 0
			break
		}
	}
	if run == "" {
		run = NewRunID()
	}
	return &Emitter{
		stream:   &Stream{Dir: dir, Shared: shared, LockWait: EmitDeadline},
		producer: producer,
		run:      run,
		began:    time.Now(),
		busy:     make(chan struct{}, 1),
	}
}

// Run is the run the emitter's records name.
func (e *Emitter) Run() string {
	if e == nil {
		return ""
	}
	return e.run
}

// Stream is the stream the emitter stores into.
func (e *Emitter) Stream() *Stream {
	if e == nil {
		return nil
	}
	return e.stream
}

// Correlate sets what every later record is about, where a record does
// not say itself.
func (e *Emitter) Correlate(base Record) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.base = base
	e.mu.Unlock()
}

// Emit stores one record of type typ: r says what it is about beyond the
// emitter's correlation, and data says the rest. elapsed_ms, the time
// since the run began on this machine's monotonic clock, is added to
// every record that does not say its own.
func (e *Emitter) Emit(typ string, r Record, data map[string]any) {
	if e == nil {
		return
	}
	now := time.Now()
	e.mu.Lock()
	base := e.base
	e.mu.Unlock()
	r.Type, r.Time, r.Run, r.Producer = typ, Stamp(now), e.run, e.producer
	for _, f := range []struct{ to, from *string }{
		{&r.Project, &base.Project}, {&r.Change, &base.Change}, {&r.Head, &base.Head},
		{&r.Tree, &base.Tree}, {&r.Check, &base.Check},
	} {
		if *f.to == "" {
			*f.to = *f.from
		}
	}
	if r.Version == 0 {
		r.Version = base.Version
	}
	// The record gets a copy of data: the store may still be writing it
	// after Emit has returned, and the caller may go on using its map.
	own := make(map[string]any, len(data)+2)
	for k, v := range data {
		own[k] = v
	}
	data = own
	if _, said := data["elapsed_ms"]; !said {
		data["elapsed_ms"] = now.Sub(e.began).Milliseconds()
	}

	select {
	case e.busy <- struct{}{}:
	default:
		e.drop()
		return
	}
	e.mu.Lock()
	if e.dropped > 0 {
		data["dropped_before"] = e.dropped
	}
	e.mu.Unlock()
	r.Data = data
	done := make(chan error, 1)
	go func() {
		defer func() { <-e.busy }()
		_, err := e.stream.Append(&r)
		done <- err
	}()
	timer := time.NewTimer(EmitDeadline)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			e.drop()
			return
		}
		e.mu.Lock()
		if n, ok := data["dropped_before"].(int); ok {
			e.dropped -= n
		}
		e.mu.Unlock()
	case <-timer.C:
		e.drop()
	}
}

func (e *Emitter) drop() {
	e.mu.Lock()
	e.dropped++
	e.mu.Unlock()
}

// Log opens the build output log of one check in this run.
func (e *Emitter) Log(check string) *LogFile {
	if e == nil {
		return nil
	}
	return e.stream.Log(e.run, check, MaxLogBytes)
}

// MaxLogBytes is how much of one check's output a run keeps.
const MaxLogBytes = 256 << 10

// NewRunID names a run: when it began, to the second, and a random part.
func NewRunID() string {
	var b [4]byte
	rand.Read(b[:])
	return "r" + time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

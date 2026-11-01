package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// mark is a writer's mark, as the integrator and the host hook make it.
func mark(t *testing.T, dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "due"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o440)
	if errors.Is(err, fs.ErrExist) {
		return
	}
	if err != nil {
		t.Error(err)
		return
	}
	f.Close()
}

// Writers move a counter and then mark due, as a push commits its refs and
// then runs the hook. Passes claim, read the counter and acknowledge,
// while the writers run. Once they stop, the passes run until nothing is
// due. The last pass must have read the last move: a mark made during a
// pass survives its acknowledgement. And the directory never holds more
// than its three files.
func TestWakeLosesNoMark(t *testing.T) {
	dir := t.TempDir()
	wakeDir := filepath.Join(dir, "wake")
	if err := os.Mkdir(wakeDir, 0o770); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(dir, "counter")
	var moved atomic.Int64
	var write sync.Mutex
	move := func() {
		write.Lock()
		defer write.Unlock()
		n := moved.Add(1)
		os.WriteFile(counter+".new", []byte(strconv.FormatInt(n, 10)), 0o600)
		os.Rename(counter+".new", counter)
	}
	read := func() int64 {
		b, _ := os.ReadFile(counter)
		n, _ := strconv.ParseInt(string(b), 10, 64)
		return n
	}

	var seen atomic.Int64
	pass := func() bool {
		w := claimWake(wakeDir, io.Discard)
		defer w.close()
		if w.err != nil {
			t.Error(w.err)
			return false
		}
		_, err := os.Stat(filepath.Join(wakeDir, "claimed"))
		claimed := err == nil
		seen.Store(read())
		if err := w.ack(); err != nil {
			t.Error(err)
		}
		entries, _ := os.ReadDir(wakeDir)
		for _, e := range entries {
			if e.Name() != "due" && e.Name() != "claimed" && e.Name() != "lock" {
				t.Errorf("the wake directory holds %s", e.Name())
			}
		}
		return claimed
	}

	var writers, passes sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 300 {
				move()
				mark(t, wakeDir)
			}
		}()
	}
	for range 3 {
		passes.Add(1)
		go func() {
			defer passes.Done()
			for {
				select {
				case <-stop:
					return
				default:
					pass()
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	passes.Wait()

	// What runs a pass while due is there runs passes until it is not.
	for {
		if _, err := os.Stat(filepath.Join(wakeDir, "due")); err != nil {
			break
		}
		pass()
	}
	if got, want := seen.Load(), moved.Load(); got != want {
		t.Fatalf("the last pass read move %d of %d: a mark was lost", got, want)
	}
	if _, err := os.Stat(filepath.Join(wakeDir, "claimed")); err == nil {
		t.Fatal("claimed was left after a pass that handled it")
	}
}

// A pass that is killed after its claim leaves claimed and no due. A new
// mark and the claim stand side by side, and the next pass that handles
// what it reads clears both.
func TestWakeKeepsAnUnacknowledgedClaim(t *testing.T) {
	dir := t.TempDir()
	mark(t, dir)
	w := claimWake(dir, io.Discard)
	if w.err != nil {
		t.Fatal(w.err)
	}
	w.close() // killed: no ack
	if _, err := os.Stat(filepath.Join(dir, "claimed")); err != nil {
		t.Fatal("the killed pass left no claimed")
	}
	mark(t, dir)
	mark(t, dir) // a second mark is the same mark
	w = claimWake(dir, io.Discard)
	if w.err != nil {
		t.Fatal(w.err)
	}
	if err := w.ack(); err != nil {
		t.Fatal(err)
	}
	w.close()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "lock" {
		t.Fatalf("the wake directory holds %v after a handled pass", entries)
	}
}

// A wake directory that cannot be renamed in is a claim failure, which
// acknowledges nothing and leaves due for the next pass.
func TestWakeClaimFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root renames in a read-only directory")
	}
	dir := t.TempDir()
	mark(t, dir)
	if f, err := os.Create(filepath.Join(dir, "lock")); err == nil {
		f.Close()
	}
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	w := claimWake(dir, io.Discard)
	defer w.close()
	if w.err == nil {
		t.Fatal("a rename in a read-only directory was claimed")
	}
	if err := w.ack(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "due")); err != nil {
		t.Fatal("a failed claim lost due")
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Evaluations that share a reservations directory are admitted by a
// memory budget. Each attest run handed the directory makes a reservation
// there before it evaluates: a file named by its pid, holding its place in
// line and its estimate of the evaluation's peak memory. It keeps an
// exclusive flock on that file until its evaluation ends, and then removes
// the file. The evaluation's nix processes inherit the locked file
// (nixPeak), so the reservation is held while attest or any of them runs,
// and each of them is killed if attest dies. A reservation no one holds,
// left by a run that was killed, does not count, and neither does one
// whose contents are not a valid reservation: the next run to look removes
// it. The directory's lock file guards every look and every new
// reservation, so no two runs read the line while it changes.
//
// A run starts its evaluation when its estimate plus the estimates of
// every reservation made before it fit the budget, or when no reservation
// was made before it (fits). Runs that waited go first, so a large
// evaluation is not passed over by small ones that keep arriving. An
// estimate larger than the whole budget starts only at the head of the
// line, and no reservation made after it starts until it ends: it runs
// alone. A run that cannot make its reservation or read the line does not
// evaluate at all, so no evaluation runs without being counted.

// admissionPoll is how often a run waiting to be admitted looks again.
const admissionPoll = 250 * time.Millisecond

// reservation is one run's place in line and the memory it expects its
// evaluation to need.
type reservation struct {
	Order    int64 `json:"order"`
	PID      int   `json:"pid"`
	Estimate int64 `json:"estimate_bytes"`
}

// admission is where a run's evaluation is admitted: the reservations
// directory, the run's estimate, and the budget every evaluation admitted
// there shares.
type admission struct {
	dir      string
	estimate int64
	budget   int64
}

// fits says whether the reservation mine may start its evaluation, given
// the other live reservations: when the estimates of those made before it,
// plus its own, fit the budget, or when none was made before it. The sum
// is never taken past the budget, so no estimate, however large, wraps it
// around to one that fits.
func fits(others []reservation, mine reservation, budget int64) bool {
	ahead, before, over := int64(0), 0, false
	for _, r := range others {
		if r.Order < mine.Order {
			before++
			if r.Estimate > budget-ahead {
				over = true
			} else {
				ahead += r.Estimate
			}
		}
	}
	return before == 0 || !over && mine.Estimate <= budget-ahead
}

// admit makes the run's reservation and waits until its evaluation fits.
// It returns how long it waited, the reservation's locked file, which the
// evaluation's nix processes inherit, and the function that removes the
// reservation once the evaluation ends. Without an admission it waits for
// nothing and holds no file. A reservation that cannot be made, or a line
// that cannot be read, is an error, and the run must not evaluate.
func admit(a *admission) (time.Duration, *os.File, func(), error) {
	if a == nil {
		return 0, nil, func() {}, nil
	}
	began := time.Now()
	mine, held, err := reserve(a)
	if err != nil {
		return time.Since(began), nil, nil, fmt.Errorf("making a reservation for the evaluation in %s: %w", a.dir, err)
	}
	release := func() {
		os.Remove(held.Name())
		held.Close()
	}
	for {
		others, err := a.look(mine.PID)
		if err != nil {
			release()
			return time.Since(began), nil, nil, fmt.Errorf("reading the evaluation reservations in %s: %w", a.dir, err)
		}
		if fits(others, mine, a.budget) {
			break
		}
		time.Sleep(admissionPoll)
	}
	return time.Since(began), held, release, nil
}

// reserve makes this run's reservation, last in line, and returns it with
// the file whose flock keeps it live. If a live evaluation of an earlier
// run with the same pid still holds that name, the file is left alone and
// reserve fails.
func reserve(a *admission) (reservation, *os.File, error) {
	unlock, err := a.lock()
	if err != nil {
		return reservation{}, nil, err
	}
	defer unlock()
	others, err := a.scan(os.Getpid())
	if err != nil {
		return reservation{}, nil, err
	}
	mine := reservation{Order: 1, PID: os.Getpid(), Estimate: a.estimate}
	for _, r := range others {
		mine.Order = max(mine.Order, r.Order+1)
	}
	data, err := json.Marshal(mine)
	if err != nil {
		return reservation{}, nil, err
	}
	// The file is not truncated before the lock is held: a run killed with
	// a nix eval still holding its reservation can leave a live one under
	// this pid, and emptying it would make the scan drop it as stale.
	f, err := os.OpenFile(filepath.Join(a.dir, strconv.Itoa(mine.PID)), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return reservation{}, nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return reservation{}, nil, fmt.Errorf("the reservation for pid %d is held by an evaluation that is still running; it is left as it is", mine.PID)
		}
		return reservation{}, nil, err
	}
	if err := f.Truncate(0); err != nil {
		os.Remove(f.Name())
		f.Close()
		return reservation{}, nil, err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		os.Remove(f.Name())
		f.Close()
		return reservation{}, nil, err
	}
	return mine, f, nil
}

// look reads the live reservations other than pid's, under the
// directory's lock.
func (a *admission) look(pid int) ([]reservation, error) {
	unlock, err := a.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return a.scan(pid)
}

// lock waits for the exclusive flock on the directory's lock file, making
// both if they are missing, and returns the function that releases it.
func (a *admission) lock() (func(), error) {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(a.dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	for err == syscall.EINTR {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// scan reads every reservation in the directory but pid's, and removes
// those that do not count: one no run holds, and one whose contents are
// not a valid reservation for its pid (valid). Only files named by a pid
// are reservations. A reservation that cannot be opened or read is an
// error, since it may be a running evaluation's. The caller holds the
// directory's lock.
func (a *admission) scan(pid int) ([]reservation, error) {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return nil, err
	}
	var live []reservation
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil || n <= 0 || n == pid {
			continue
		}
		path := filepath.Join(a.dir, e.Name())
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			// Its run removed it since the directory was read.
			continue
		}
		if err != nil {
			return nil, err
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) == nil {
			os.Remove(path)
			f.Close()
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		r, ok := valid(data, n)
		if !ok {
			os.Remove(path)
			continue
		}
		live = append(live, r)
	}
	return live, nil
}

// valid reads a reservation file's contents and says whether they are a
// reservation attest could have written for pid: its own pid, a place in
// line from 1 that a later reservation can still follow, and an estimate
// of at least one byte. Numbers too large for 64 bits do not parse.
func valid(data []byte, pid int) (reservation, bool) {
	var r reservation
	if json.Unmarshal(data, &r) != nil {
		return reservation{}, false
	}
	return r, r.PID == pid && r.Order >= 1 && r.Order < math.MaxInt64 && r.Estimate >= 1
}

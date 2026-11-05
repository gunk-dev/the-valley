package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// A batch's evaluation is admitted by memory. The pass has a budget, the
// memory every evaluation it runs at once may hold together, and an
// estimate of each evaluation's peak. Every attest it starts is handed
// the reservations directory evaluations/ in the state directory, the
// budget and its estimate, and attest admits its own evaluation there
// (attest/admit.go): the evaluation starts when its estimate fits the
// budget beside those admitted before it.
//
// The budget is --eval-budget when given. Otherwise it is --eval-share of
// the most memory the worker may use: the smallest memory.max of its
// cgroup and every ancestor, such as the build slice the worker, attest
// and the nix daemon share, and never more than the host's memory
// (memoryLimit). When that limit cannot be found, the pass has no budget:
// it hands each attest a budget equal to its own estimate, so each
// evaluation runs alone, and it says once how to give a budget.
//
// A project's estimate is the largest of the peaks its last peakHistory
// successful evaluations measured, as attest reports them. An evaluation
// that failed or was killed stopped short of its high-water mark, as one
// the kernel's out-of-memory killer ended did, so its peak is not kept:
// the project's estimate becomes unknown, and its next evaluation runs
// alone, until one succeeds. A project with no peak yet counts as the
// largest peak recorded for any project, or as the whole budget when there
// is none, so it evaluates alone. The record is evaluations.json in the
// state directory. An estimate larger than the whole budget cannot fit
// beside anything, so that evaluation runs alone, and the pass reports it
// for each such batch.

// defaultEvalShare is the share of the worker's memory limit its
// evaluations get together when neither --eval-budget nor --eval-share
// says: the host module's own default. The rest is left to the builds of
// the batches that are past their evaluations, to the nix daemon and to
// the worker itself.
const defaultEvalShare = 0.6

// peakHistory is how many of a project's latest evaluation peaks its
// estimate is the largest of.
const peakHistory = 5

// evaluations is a pass's evaluation budget and its record of peaks.
type evaluations struct {
	dir    string // the reservations directory attest admits evaluations through
	path   string // the record of peaks
	budget int64  // the memory the evaluations running at once may hold together, in bytes, or 0 when none was found
	source string // where the budget came from, or why none was found, for a report
	peaks  peakRecord
}

// peakRecord is evaluations.json: each project's latest successful
// evaluation peaks, in bytes, oldest first, and the projects whose latest
// evaluation failed or was killed, whose estimate is unknown.
type peakRecord struct {
	Projects map[string][]int64 `json:"projects"`
	Unknown  map[string]bool    `json:"unknown,omitempty"`
}

// openEvaluations works out the pass's budget and reads the record of
// peaks under state. A budget of 0 means none was given, and share of the
// worker's memory limit is used; when that limit cannot be found, the
// pass's budget is 0 and its source says why. Peak files a killed pass left behind are
// removed: no attest of an earlier pass runs once this one holds the
// state directory's lock.
func openEvaluations(state string, budget int64, share float64) (*evaluations, error) {
	e := &evaluations{dir: filepath.Join(state, "evaluations"), path: filepath.Join(state, "evaluations.json")}
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		return nil, err
	}
	if left, err := filepath.Glob(filepath.Join(e.dir, "*.peak")); err == nil {
		for _, f := range left {
			os.Remove(f)
		}
	}
	e.budget, e.source = budget, "--eval-budget"
	if budget == 0 {
		limit, of, err := memoryLimit()
		if err != nil {
			e.budget, e.source = 0, err.Error()
		} else {
			e.budget = int64(float64(limit) * share)
			e.source = fmt.Sprintf("%s of %s, %s", strconv.FormatFloat(share, 'f', -1, 64), of, bytesText(limit))
			if e.budget < 1 {
				return nil, fmt.Errorf("an evaluation budget of %d bytes holds no evaluation; give --eval-budget", e.budget)
			}
		}
	} else if e.budget < 1 {
		return nil, fmt.Errorf("an evaluation budget of %d bytes holds no evaluation; give --eval-budget", e.budget)
	}
	if data, err := os.ReadFile(e.path); err == nil {
		json.Unmarshal(data, &e.peaks)
	}
	if e.peaks.Projects == nil {
		e.peaks.Projects = map[string][]int64{}
	}
	if e.peaks.Unknown == nil {
		e.peaks.Unknown = map[string]bool{}
	}
	return e, nil
}

// estimate is the peak memory a project's next evaluation is expected to
// need, in bytes, and never less than one. A project whose estimate is
// unknown counts as the whole budget, or its own largest peak when that
// is more, so it evaluates alone.
func (e *evaluations) estimate(project string) int64 {
	largest := func(peaks []int64) int64 {
		var m int64
		for _, p := range peaks {
			m = max(m, p)
		}
		return m
	}
	own := largest(e.peaks.Projects[project])
	if e.peaks.Unknown[project] {
		return max(own, e.budget, 1)
	}
	if own > 0 {
		return own
	}
	var m int64
	for _, peaks := range e.peaks.Projects {
		m = max(m, largest(peaks))
	}
	if m == 0 {
		return max(e.budget, 1)
	}
	return m
}

// handed is the budget a batch's attest is handed beside estimate: the
// pass's budget, or, when none was found, the estimate itself, so the
// evaluation fits only when no other is admitted before it.
func (e *evaluations) handed(estimate int64) int64 {
	if e.budget == 0 {
		return estimate
	}
	return e.budget
}

// record keeps what a project's evaluation measured, as attest reported
// it, and writes the record. A peak counts only when the evaluation's
// outcome is "ok". Any other outcome, such as "failed" or "killed",
// makes the project's estimate unknown until an evaluation succeeds. An
// ok evaluation that measured nothing changes nothing. It reports why the
// record could not be written.
func (e *evaluations) record(project string, peak int64, outcome string) error {
	switch {
	case outcome == "ok" && peak > 0:
		peaks := append(e.peaks.Projects[project], peak)
		if len(peaks) > peakHistory {
			peaks = peaks[len(peaks)-peakHistory:]
		}
		e.peaks.Projects[project] = peaks
		delete(e.peaks.Unknown, project)
	case outcome == "ok":
		return nil
	default:
		e.peaks.Unknown[project] = true
	}
	data, err := json.MarshalIndent(e.peaks, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.path + ".new"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.path)
}

// readPeak reads the file attest run --eval-peak wrote: the evaluation's
// peak in bytes and its outcome, on one line. Anything else says nothing.
func readPeak(text string) (int64, string) {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return 0, ""
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || n < 0 {
		return 0, ""
	}
	return n, fields[1]
}

// endedBySignal says whether a process's error is that a signal ended it.
func endedBySignal(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled()
}

// memoryLimit is the most memory this process may use, in bytes, and what
// sets it: the smallest memory.max of its cgroup and every ancestor up to
// the root of the cgroup2 mount, and never more than the host's memory. It
// finds the mount in /proc/self/mountinfo and the cgroup in
// /proc/self/cgroup (cgroupDir). An error says why the limit could not be
// found: a file it needs is missing or unreadable, no cgroup2 mount holds
// the process's cgroup, or a memory.max is unreadable or says neither a
// number nor "max". A memory.max that is missing is no limit: the root
// cgroup has none, and neither has a cgroup whose parent does not enable
// the memory controller for it.
func memoryLimit() (int64, string, error) {
	host, err := hostMemory()
	if err != nil {
		return 0, "", fmt.Errorf("the host's memory could not be read: %v", err)
	}
	cgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0, "", fmt.Errorf("the worker's cgroup could not be read: %v", err)
	}
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return 0, "", fmt.Errorf("the mounts could not be read: %v", err)
	}
	dir, top, path, err := cgroupDir(string(cgroup), string(mountinfo))
	if err != nil {
		return 0, "", err
	}
	return cgroupLimit(dir, top, path, host)
}

// cgroupLimit is the smallest memory.max of the cgroup directory dir, at
// path in the hierarchy, and of each ancestor up to top, and never more
// than host, the host's memory; and what sets it.
func cgroupLimit(dir, top, path string, host int64) (int64, string, error) {
	limit, of := host, "the host's memory"
	for {
		file := filepath.Join(dir, "memory.max")
		text, err := os.ReadFile(file)
		value := strings.TrimSpace(string(text))
		n, perr := strconv.ParseInt(value, 10, 64)
		switch {
		case os.IsNotExist(err), err == nil && value == "max":
		case err != nil:
			return 0, "", fmt.Errorf("%s could not be read: %v", file, err)
		case perr != nil || n < 0:
			return 0, "", fmt.Errorf("%s says %q, neither a number of bytes nor max", file, value)
		case n < limit:
			limit, of = n, "the memory.max of the cgroup "+path
		}
		if dir == top {
			return limit, of, nil
		}
		dir, path = filepath.Dir(dir), filepath.Dir(path)
	}
}

// cgroupDir finds this process's cgroup directory from the contents of
// /proc/self/cgroup and /proc/self/mountinfo: the cgroup2 mount whose root
// holds the cgroup's path, and the cgroup's directory under it. It
// returns that directory, the mount point, which is the last ancestor
// whose memory.max can be read, and the cgroup's path.
func cgroupDir(cgroup, mountinfo string) (dir, top, path string, err error) {
	for _, line := range strings.Split(cgroup, "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			path = p
		}
	}
	if !strings.HasPrefix(path, "/") || slices.Contains(strings.Split(path, "/"), "..") {
		return "", "", "", fmt.Errorf("/proc/self/cgroup names no cgroup2 cgroup for the worker that it can see")
	}
	path = filepath.Clean(path)
	for _, line := range strings.Split(mountinfo, "\n") {
		// A line is: id, parent, device, root, mount point, options,
		// optional fields, "-", file system type, source, options.
		fields := strings.Fields(line)
		sep := slices.Index(fields, "-")
		if sep < 6 || sep+1 >= len(fields) || fields[sep+1] != "cgroup2" {
			continue
		}
		root, point := unescapeMount(fields[3]), unescapeMount(fields[4])
		rel, ok := strings.CutPrefix(path, root)
		if root == "/" {
			rel, ok = path, true
		}
		if !ok || rel != "" && !strings.HasPrefix(rel, "/") {
			continue
		}
		return filepath.Join(point, rel), point, path, nil
	}
	return "", "", "", fmt.Errorf("no cgroup2 mount in /proc/self/mountinfo holds the worker's cgroup %s", path)
}

// unescapeMount undoes mountinfo's escapes: a space, tab, newline or
// backslash in a path is written as a backslash and three octal digits.
func unescapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseSize reads a size in bytes as systemd's MemoryMax does: a whole
// number, optionally followed by K, M, G or T for powers of 1024.
func parseSize(text string) (int64, error) {
	shift := 0
	if i := strings.IndexAny(text, "KMGT"); i >= 0 && i == len(text)-1 {
		shift = 10 * (1 + strings.IndexByte("KMGT", text[i]))
		text = text[:i]
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < 1 || n > (1<<62)>>shift {
		return 0, fmt.Errorf("not a size, such as 12G or 512M")
	}
	return n << shift, nil
}

// bytesText says a number of bytes in GiB or MiB.
func bytesText(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
}

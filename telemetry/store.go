package telemetry

// A stream is a directory of records, numbered in the order they were
// stored, and bounded: records go into segment files of SegmentBytes, and
// only the newest Segments of them are kept. So a stream never holds more
// than about SegmentBytes × Segments, and its oldest records are the ones
// it loses.
//
//	epoch                      random, made once: a stream made again is
//	                           a different stream
//	lock                       flock'd by every writer while it appends
//	seg-<first seq>.jsonl      records, one JSON line each
//	logs/<run>/<check>.log     build output, each file bounded
//
// Writers in more than one process share a stream: each append holds the
// lock, reads the last number stored and stores the next one. A reader
// takes no lock. It reads whole lines only, so a line being written is
// read on the next pass.
//
// A stream a producer exports for the collector is group-readable
// (Shared): its files are made 0640, and its directories inherit the
// collector's group from the exported directory the host makes (mkdir).
// Nothing else in the producer's state is.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Stream is one stream directory and its bounds.
type Stream struct {
	Dir          string
	SegmentBytes int64 // default 1 MiB
	Segments     int   // default 8
	Shared       bool  // group-readable, for the collector
	LockWait     time.Duration
}

func (s *Stream) segmentBytes() int64 {
	if s.SegmentBytes > 0 {
		return s.SegmentBytes
	}
	return 1 << 20
}

func (s *Stream) segments() int {
	if s.Segments > 0 {
		return s.Segments
	}
	return 8
}

func (s *Stream) dirMode() os.FileMode {
	if s.Shared {
		return 0o750
	}
	return 0o700
}

func (s *Stream) fileMode() os.FileMode {
	if s.Shared {
		return 0o640
	}
	return 0o600
}

// mkdir makes a directory of the stream. A private stream's is made
// owner-only whatever the umask. A shared stream's takes its group, and the
// setgid bit that passes the group on, from the exported directory above
// it, and its mode from the producer's umask, which the host sets to keep
// group read: a chmod by a producer outside that group would drop the
// setgid bit, and every file made after it would be unreadable to the
// collector.
func (s *Stream) mkdir(dir string) error {
	if err := os.MkdirAll(dir, s.dirMode()); err != nil {
		return err
	}
	if s.Shared {
		return nil
	}
	return os.Chmod(dir, s.dirMode())
}

// create opens a file of the stream for appending, made with its mode.
func (s *Stream) create(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, s.fileMode())
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm() != s.fileMode() {
		if err := f.Chmod(s.fileMode()); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

// Epoch is the stream's random name, made the first time it is asked for.
func (s *Stream) Epoch() (string, error) {
	path := filepath.Join(s.Dir, "epoch")
	if data, err := readSmall(path); err == nil {
		if e := strings.TrimSpace(string(data)); epochRE.MatchString(e) {
			return e, nil
		}
		return "", fmt.Errorf("%s does not hold an epoch", path)
	}
	if err := s.mkdir(s.Dir); err != nil {
		return "", err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	e := hex.EncodeToString(b[:])
	// Written whole under another name and linked into place, so no
	// reader sees an epoch file without its epoch, and of two writers
	// making it at once one wins and the other reads its epoch.
	tmp, err := os.CreateTemp(s.Dir, ".epoch-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(e + "\n")
	if err == nil {
		err = tmp.Chmod(s.fileMode())
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if err := os.Link(tmp.Name(), path); errors.Is(err, fs.ErrExist) {
		return s.Epoch()
	} else if err != nil {
		return "", err
	}
	return e, nil
}

// lock takes the stream's lock, waiting at most LockWait for it.
func (s *Stream) lock() (*os.File, error) {
	f, err := s.create(filepath.Join(s.Dir, "lock"))
	if err != nil {
		return nil, err
	}
	wait := s.LockWait
	if wait == 0 {
		wait = 2 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("the lock of %s: %w", s.Dir, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type segment struct {
	path  string
	first int64
}

// list is the stream's segments, oldest first.
func (s *Stream) list() ([]segment, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var segs []segment
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "seg-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(name, "seg-"), ".jsonl"), 10, 64)
		if err != nil || n < 1 {
			continue
		}
		segs = append(segs, segment{filepath.Join(s.Dir, name), n})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].first < segs[j].first })
	return segs, nil
}

func segmentName(first int64) string { return fmt.Sprintf("seg-%016d.jsonl", first) }

// lastSeq is the number of the last whole record in a segment, or one
// before its first when it holds none.
func lastSeq(seg segment) (int64, bool, error) {
	f, err := os.OpenFile(seg.path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	size := info.Size()
	if size == 0 {
		return seg.first - 1, true, nil
	}
	from := max(size-64<<10, 0)
	buf := make([]byte, size-from)
	if _, err := f.ReadAt(buf, from); err != nil && !errors.Is(err, io.EOF) {
		return 0, false, err
	}
	whole := buf[len(buf)-1] == '\n'
	lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var r struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(lines[i], &r) == nil && r.Seq >= seg.first {
			return r.Seq, whole, nil
		}
	}
	return seg.first - 1, whole, nil
}

// Append stores a record as the stream's next, and returns its number.
// The record is checked first (Validate), and its Stream and Seq are the
// stream's to set.
func (s *Stream) Append(r *Record) (int64, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}
	if _, err := s.Epoch(); err != nil {
		return 0, err
	}
	lock, err := s.lock()
	if err != nil {
		return 0, err
	}
	defer lock.Close()

	segs, err := s.list()
	if err != nil {
		return 0, err
	}
	seq := int64(1)
	var cur segment
	whole := true
	if len(segs) > 0 {
		cur = segs[len(segs)-1]
		last, w, err := lastSeq(cur)
		if err != nil {
			return 0, err
		}
		seq, whole = last+1, w
		if info, err := os.Stat(cur.path); err != nil || info.Size() >= s.segmentBytes() {
			cur = segment{}
		}
	}
	total := len(segs)
	if cur.path == "" {
		cur = segment{filepath.Join(s.Dir, segmentName(seq)), seq}
		whole = true
		total++
	}
	r.Stream, r.Seq = "", seq
	line, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	if !whole {
		// A writer that stopped half way through a line left it
		// unterminated; the line is ended, and readers skip it.
		line = append([]byte("\n"), line...)
	}
	f, err := s.create(cur.path)
	if err != nil {
		return 0, err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	for _, old := range segs[:max(total-s.segments(), 0)] {
		os.Remove(old.path)
	}
	return seq, nil
}

// Range is what a stream holds: its epoch, and the numbers of its oldest
// and newest records. An empty stream has First = Last + 1.
type Range struct {
	Epoch string `json:"epoch"`
	First int64  `json:"first"`
	Last  int64  `json:"last"`
}

// Range reads the stream's range without reading its records.
func (s *Stream) Range() (Range, error) {
	data, err := readSmall(filepath.Join(s.Dir, "epoch"))
	if err != nil {
		return Range{}, err
	}
	rng := Range{Epoch: strings.TrimSpace(string(data)), First: 1}
	segs, err := s.list()
	if err != nil {
		return rng, err
	}
	if len(segs) == 0 {
		return rng, nil
	}
	rng.First = segs[0].first
	rng.Last, _, err = lastSeq(segs[len(segs)-1])
	return rng, err
}

// Read returns the stream's records numbered after after, oldest first, at
// most limit of them and about maxBytes of them, and the stream's range.
// A line that does not read as a record is skipped.
func (s *Stream) Read(after int64, limit int, maxBytes int) ([]Record, Range, error) {
	rng, err := s.Range()
	if err != nil {
		return nil, rng, err
	}
	segs, err := s.list()
	if err != nil {
		return nil, rng, err
	}
	var out []Record
	size := 0
	for i, seg := range segs {
		if i+1 < len(segs) && segs[i+1].first <= after+1 {
			continue
		}
		f, err := os.OpenFile(seg.path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if errors.Is(err, fs.ErrNotExist) {
			continue // pruned since it was listed
		}
		if err != nil {
			return out, rng, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), MaxRecordBytes*4)
		for sc.Scan() {
			var r Record
			if json.Unmarshal(sc.Bytes(), &r) != nil || r.Seq <= after {
				continue
			}
			if len(out) >= limit || (maxBytes > 0 && size+len(sc.Bytes()) > maxBytes) {
				f.Close()
				return out, rng, nil
			}
			size += len(sc.Bytes())
			out = append(out, r)
		}
		f.Close()
	}
	return out, rng, nil
}

// Logs: build output a producer keeps beside its records. Each check's
// output in a run is one file, cut off at a bound; logs/ keeps the newest
// keptRuns runs.
const keptRuns = 16

// LogFile is one check's output as it is written. It keeps at most max
// bytes, and says whether it dropped any. Nothing it does can hold up the
// build whose output it keeps: the file is made and written by a goroutine
// of its own, through a bounded queue, and output that finds the queue
// full, because the disk under the stream has stalled, is dropped and
// counted as cut off.
type LogFile struct {
	mu        sync.Mutex
	path      string // "" when the run or check is no name a log can have
	max       int64
	written   int64
	truncated bool
	queue     chan []byte
	done      chan struct{}
	failed    chan struct{} // closed once the file cannot be made or written
	closed    bool
}

// logQueue is how many writes a log holds while its file is written, and
// logCloseWait how long Close waits for them.
const (
	logQueue     = 256
	logCloseWait = time.Second
)

// Log opens the log of check in run, for writing as the check runs.
func (s *Stream) Log(run, check string, max int64) *LogFile {
	l := &LogFile{max: max, queue: make(chan []byte, logQueue), done: make(chan struct{}), failed: make(chan struct{})}
	if runRE.MatchString(run) && nameRE.MatchString(check) && !strings.Contains(check, "/") {
		l.path = filepath.Join(s.Dir, "logs", run, check+".log")
	}
	go l.drain(s)
	return l
}

// drain makes the log's file and writes what the queue brings. A log
// whose file cannot be made or written drops the rest.
func (l *LogFile) drain(s *Stream) {
	defer close(l.done)
	var f *os.File
	if l.path != "" {
		logs, dir := filepath.Dir(filepath.Dir(l.path)), filepath.Dir(l.path)
		_, statErr := os.Stat(dir)
		fresh := errors.Is(statErr, fs.ErrNotExist)
		if err := s.mkdir(logs); err == nil {
			if err := s.mkdir(dir); err == nil {
				if fresh {
					s.pruneLogs(logs)
				}
				f, _ = s.create(l.path)
			}
		}
	}
	if f == nil {
		close(l.failed)
	}
	for p := range l.queue {
		if f == nil {
			continue
		}
		if _, err := f.Write(p); err != nil {
			f.Close()
			f = nil
			close(l.failed)
		}
	}
	if f != nil {
		f.Close()
	}
}

// pruneLogs removes all but the newest keptRuns run directories.
func (s *Stream) pruneLogs(logs string) {
	entries, err := os.ReadDir(logs)
	if err != nil || len(entries) <= keptRuns {
		return
	}
	type run struct {
		path string
		mod  time.Time
	}
	var runs []run
	for _, e := range entries {
		if info, err := e.Info(); err == nil && e.IsDir() {
			runs = append(runs, run{filepath.Join(logs, e.Name()), info.ModTime()})
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].mod.After(runs[j].mod) })
	for _, r := range runs[min(keptRuns, len(runs)):] {
		os.RemoveAll(r.path)
	}
}

// Write keeps what fits under the bound and drops the rest. It never
// fails and never waits, so a full or stalled disk or a dropped log never
// fails or holds up the build it records.
func (l *LogFile) Write(p []byte) (int, error) {
	if l == nil {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := p
	if room := l.max - l.written; int64(len(keep)) > room {
		keep = keep[:max(room, 0)]
		l.truncated = true
	}
	l.written += int64(len(p))
	if len(keep) == 0 || l.closed {
		return len(p), nil
	}
	select {
	case l.queue <- bytes.Clone(keep):
	default:
		l.truncated = true
	}
	return len(p), nil
}

// Close ends the log, waits at most logCloseWait for its writes, and says
// how many bytes it was given and whether it kept fewer.
func (l *LogFile) Close() (written int64, truncated bool) {
	if l == nil {
		return 0, false
	}
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.queue)
	}
	l.mu.Unlock()
	timer := time.NewTimer(logCloseWait)
	defer timer.Stop()
	select {
	case <-l.done:
	case <-timer.C:
		l.mu.Lock()
		l.truncated = true
		l.mu.Unlock()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-l.failed:
		if l.written > 0 {
			l.truncated = true
		}
	default:
	}
	return l.written, l.truncated
}

// LogEntry is one log file a stream holds.
type LogEntry struct {
	Run, Check, Path string
	Size             int64
	Mod              time.Time
}

// Logs lists the stream's log files, newest run first.
func (s *Stream) Logs() []LogEntry {
	logs := filepath.Join(s.Dir, "logs")
	runs, _ := os.ReadDir(logs)
	var out []LogEntry
	for _, r := range runs {
		if !r.IsDir() || !runRE.MatchString(r.Name()) {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(logs, r.Name()))
		for _, f := range files {
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() || !strings.HasSuffix(f.Name(), ".log") {
				continue
			}
			out = append(out, LogEntry{r.Name(), strings.TrimSuffix(f.Name(), ".log"), filepath.Join(logs, r.Name(), f.Name()), info.Size(), info.ModTime()})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mod.After(out[j].Mod) })
	return out
}

// readSmall reads a small file of the stream, never through a link.
func readSmall(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 4096))
}

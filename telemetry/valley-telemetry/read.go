package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"the-valley/telemetry"
)

type recordLine struct {
	Kind string `json:"kind"`
	telemetry.Record
}

// selection is what a read names: every stream, or one source's stream
// and units.
func (c *collector) selection(name string) (map[string]*telemetry.Stream, []string, error) {
	all := c.streams()
	if name == "" {
		return all, nil, nil
	}
	if st, ok := all[name]; ok && strings.HasPrefix(name, "report.") {
		return map[string]*telemetry.Stream{name: st}, nil, nil
	}
	for _, s := range c.manifest.Sources {
		if s.Name != name {
			continue
		}
		if !s.Enabled {
			return nil, nil, fmt.Errorf("%s is not enabled on this host", name)
		}
		picked := map[string]*telemetry.Stream{}
		if sn := c.streamOf(s); sn != "" && all[sn] != nil {
			picked[sn] = all[sn]
		}
		return picked, s.Units, nil
	}
	return nil, nil, fmt.Errorf("no source is named %s; valley telemetry sources lists them", name)
}

func sortedNames(m map[string]*telemetry.Stream) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// read answers a read: the selected units' lifecycle from the journal
// (and their own output, with logs), each selected stream's records after
// the cursor, and with logs the tail of each stream's newest build logs.
// A stream the cursor does not name is read from its newest records.
func (c *collector) read(req telemetry.Request) (telemetry.Cursor, error) {
	cur, err := telemetry.ParseCursor(req.Cursor)
	if err != nil {
		return nil, err
	}
	streams, units, err := c.selection(req.Source)
	if err != nil {
		return nil, err
	}
	if len(units) > 0 {
		entries, err := c.journal(units, req.Limit, req.Logs)
		if err != nil {
			c.out.send(map[string]any{"kind": "unavailable", "what": "journal", "error": err.Error()})
		}
		for _, e := range entries {
			if e.Lifecycle {
				c.out.send(map[string]any{"kind": "lifecycle", "time": e.Time, "unit": e.Unit, "message": e.Message})
			} else {
				c.out.send(map[string]any{"kind": "log", "untrusted": true, "time": e.Time, "unit": e.Unit, "text": e.Message})
			}
		}
	}
	for _, name := range sortedNames(streams) {
		c.readStream(name, streams[name], cur, req.Limit, false, req.Logs)
	}
	if req.Logs {
		for _, name := range sortedNames(streams) {
			for i, l := range streams[name].Logs() {
				if i == 8 {
					break
				}
				c.sendLog(name, l, max(l.Size-telemetry.LogTailBytes, 0), l.Size)
			}
		}
	}
	return cur, nil
}

// readStream sends a stream's records after the cursor's position, and
// moves the cursor past them. A position in another epoch, or older than
// the stream still holds, is a gap, and is said. With fromEnd, a stream
// the cursor does not name starts after its newest record; otherwise
// with its newest limit records. A record of a check's own output is
// sent only with logs, made safe to show and marked untrusted.
func (c *collector) readStream(name string, st *telemetry.Stream, cur telemetry.Cursor, limit int, fromEnd, logs bool) {
	rng, err := st.Range()
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		c.out.send(map[string]any{"kind": "unavailable", "what": name, "error": err.Error()})
		return
	}
	pos, known := cur[name]
	after := pos.Seq
	switch {
	case !known && fromEnd:
		after = rng.Last
	case !known:
		after = max(rng.Last-int64(limit), rng.First-1)
	case pos.Epoch != rng.Epoch:
		c.out.send(map[string]any{"kind": "gap", "stream": name, "reason": "reset",
			"detail": "the stream was made again since the cursor was given; it is read from its start"})
		after = rng.First - 1
	case after+1 < rng.First:
		c.out.send(map[string]any{"kind": "gap", "stream": name, "reason": "retention", "from": after + 1, "to": rng.First - 1,
			"detail": "these records were removed to keep the stream within its bound"})
		after = rng.First - 1
	case after > rng.Last:
		c.out.send(map[string]any{"kind": "gap", "stream": name, "reason": "ahead",
			"detail": "the cursor is past the stream's newest record"})
		after = rng.Last
	}
	recs, _, err := st.Read(after, limit, telemetry.MaxResponseBytes/2)
	if err != nil {
		c.out.send(map[string]any{"kind": "unavailable", "what": name, "error": err.Error()})
	}
	cur[name] = telemetry.Position{Epoch: rng.Epoch, Seq: after}
	for _, r := range recs {
		r.Stream = name
		if r.Untrusted() && !logs {
			cur[name] = telemetry.Position{Epoch: rng.Epoch, Seq: r.Seq}
			continue
		}
		if r.Untrusted() {
			r.Data["text"] = telemetry.Sanitize(fmt.Sprint(r.Data["text"]))
			r.Data["untrusted"] = true
		}
		if !c.out.send(recordLine{"record", r}) {
			return
		}
		cur[name] = telemetry.Position{Epoch: rng.Epoch, Seq: r.Seq}
	}
}

// sendLog sends the bytes of a build log from one offset to another. It
// is untrusted text: the check's own output.
func (c *collector) sendLog(stream string, l telemetry.LogEntry, from, to int64) bool {
	f, err := os.OpenFile(l.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, min(to-from, telemetry.LogTailBytes))
	n, _ := f.ReadAt(buf, from)
	return c.out.send(map[string]any{"kind": "log", "untrusted": true, "stream": stream, "run": l.Run, "check": l.Check,
		"from": from, "to": from + int64(n), "size": l.Size, "capped": l.Size >= telemetry.MaxLogBytes,
		"text": telemetry.Sanitize(string(buf[:n]))})
}

// follow sends what the selected streams store from now on, and with
// logs the build output written from now on, until the request's seconds
// are up. A heartbeat every heartbeatEvery carries the cursor, so a
// reader that loses the connection resumes from it.
func (c *collector) follow(req telemetry.Request) telemetry.Cursor {
	cur, err := telemetry.ParseCursor(req.Cursor)
	if err != nil {
		c.out.fail("%v", err)
		return nil
	}
	streams, _, err := c.selection(req.Source)
	if err != nil {
		c.out.fail("%v", err)
		return nil
	}
	deadline := c.now().Add(time.Duration(req.Seconds) * time.Second)
	offsets := map[string]int64{}
	if req.Logs {
		for _, st := range streams {
			for _, l := range st.Logs() {
				// What a run still writing has written so far, up to the
				// last 4 KiB of it, and nothing of the logs of runs that
				// have long ended.
				if c.now().Sub(l.Mod) < time.Minute {
					offsets[l.Path] = max(l.Size-4096, 0)
				} else {
					offsets[l.Path] = l.Size
				}
			}
		}
	}
	beat := c.now()
	for {
		for _, name := range sortedNames(streams) {
			c.readStream(name, streams[name], cur, telemetry.DefaultReadLimit, true, req.Logs)
		}
		if req.Logs {
			for _, name := range sortedNames(streams) {
				for _, l := range streams[name].Logs() {
					if from, seen := offsets[l.Path]; !seen || l.Size > from {
						if c.sendLog(name, l, from, l.Size) {
							offsets[l.Path] = min(l.Size, from+telemetry.LogTailBytes)
						}
					}
				}
			}
		}
		if c.out.err != nil || c.out.truncated || !c.now().Before(deadline) {
			return cur
		}
		if c.now().Sub(beat) >= heartbeatEvery {
			beat = c.now()
			c.out.send(map[string]any{"kind": "heartbeat", "time": telemetry.Stamp(beat), "cursor": cur.String()})
		}
		time.Sleep(pollEvery)
	}
}

var (
	pollEvery      = time.Second
	heartbeatEvery = 10 * time.Second
)

var clientIDRE = regexp.MustCompile(`^([0-9a-f]{16}):([0-9]{1,18})$`)

// watermark is the newest record of one sender's spool the collector has
// stored. A record at or below it is a retry, and is not stored again.
type watermark struct {
	Seq     int64  `json:"seq"`
	Touched string `json:"touched"`
}

const maxWatermarks = 64

// report stores the records a reporter sends: each checked against the
// format and the types a client may send, stamped with the principal and
// the time it was received, and stored in the principal's own bounded
// stream. A record the sender already delivered is counted and dropped.
// The answer is one ack line saying what happened to each.
func (c *collector) report(principal string, in *bufio.Reader) error {
	st := c.reportStream(principal)
	if err := os.MkdirAll(st.Dir, 0o700); err != nil {
		return err
	}
	wmPath := filepath.Join(c.state, "reports", principal+".watermarks.json")
	wm := map[string]watermark{}
	if data, err := os.ReadFile(wmPath); err == nil {
		json.Unmarshal(data, &wm)
	}
	body := bufio.NewReader(io.LimitReader(in, telemetry.MaxReportBytes+1))
	accepted, duplicates, rejected, n, size := 0, 0, 0, 0, 0
	var problems []string
	reject := func(err error) {
		rejected++
		if len(problems) < 8 {
			problems = append(problems, fmt.Sprintf("record %d: %v", n, err))
		}
	}
	now := telemetry.Stamp(c.now())
	for {
		line, err := readLine(body, telemetry.MaxRecordBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		n++
		if err != nil {
			reject(err)
			break
		}
		size += len(line) + 1
		if size > telemetry.MaxReportBytes || n > telemetry.MaxReportRecords {
			reject(fmt.Errorf("a report holds at most %d records and %d bytes; the rest is refused", telemetry.MaxReportRecords, telemetry.MaxReportBytes))
			break
		}
		r, err := telemetry.DecodeRecord(line)
		if err != nil {
			reject(err)
			continue
		}
		m := clientIDRE.FindStringSubmatch(r.ClientID)
		switch {
		case m == nil:
			err = errors.New("no client_id of the form epoch:seq")
		case r.Stream != "" || r.Seq != 0 || r.Principal != "" || r.Received != "":
			err = errors.New("stream, seq, principal and received are the collector's to set")
		case !telemetry.ClientType(r.Type):
			err = fmt.Errorf("a client may not report %q", r.Type)
		default:
			err = r.Validate()
		}
		if err != nil {
			reject(err)
			continue
		}
		epoch := m[1]
		seq, _ := strconv.ParseInt(m[2], 10, 64)
		w, known := wm[epoch]
		if seq <= w.Seq {
			duplicates++
			continue
		}
		if seq > w.Seq+1 {
			gap := telemetry.Record{Type: "report.gap", Time: now, Principal: principal, Received: now,
				Data: map[string]any{"epoch": epoch, "from": w.Seq + 1, "to": seq - 1, "known": known}}
			st.Append(&gap)
		}
		r.Principal, r.Received = principal, now
		if _, err := st.Append(&r); err != nil {
			reject(err)
			break
		}
		accepted++
		wm[epoch] = watermark{seq, now}
	}
	if len(wm) > maxWatermarks {
		type kv struct {
			k string
			w watermark
		}
		var all []kv
		for k, w := range wm {
			all = append(all, kv{k, w})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].w.Touched > all[j].w.Touched })
		for _, e := range all[maxWatermarks:] {
			delete(wm, e.k)
		}
	}
	if err := writeAtomic(wmPath, wm); err != nil {
		return err
	}
	acked := map[string]int64{}
	for k, w := range wm {
		acked[k] = w.Seq
	}
	c.out.send(map[string]any{"kind": "ack", "accepted": accepted, "duplicates": duplicates,
		"rejected": rejected, "problems": problems, "acked": acked, "received": now})
	c.out.end(nil)
	// What a refused report still had to send is read and dropped: a unix
	// socket closed with unread data resets its peer, and the sender would
	// lose the answer above.
	io.Copy(io.Discard, io.LimitReader(in, telemetry.MaxReportBytes+1))
	return nil
}

func writeAtomic(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

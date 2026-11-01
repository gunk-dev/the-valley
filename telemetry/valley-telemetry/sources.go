package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"the-valley/telemetry"
)

// unitProperties are the properties read of every unit, and no others: an
// unrestricted show would print the unit's environment, which can carry a
// credential.
var unitProperties = []string{
	"Id", "LoadState", "ActiveState", "SubState", "Result", "UnitFileState",
	"ExecMainStatus", "ExecMainCode", "ExecMainStartTimestamp", "ExecMainExitTimestamp",
	"StateChangeTimestamp", "NRestarts", "MemoryCurrent", "MemoryPeak", "CPUUsageNSec",
	"InvocationID", "LastTriggerUSec", "NextElapseUSecRealtime",
}

type unitState map[string]string

const commandDeadline = 5 * time.Second

func systemctlShow(units []string) ([]unitState, error) {
	if len(units) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandDeadline)
	defer cancel()
	args := append([]string{"show", "--property=" + strings.Join(unitProperties, ","), "--"}, units...)
	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("systemctl show: %v", err)
	}
	var states []unitState
	for _, block := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
		s := unitState{}
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				s[k] = v
			}
		}
		states = append(states, s)
	}
	if len(states) != len(units) {
		return nil, fmt.Errorf("systemctl show answered for %d of %d units", len(states), len(units))
	}
	return states, nil
}

// journalEntry is one journal entry of a source's units. An entry systemd
// itself wrote (PID 1) is lifecycle: a start, an exit and its status, a
// kill. Every other entry is what the unit's own processes printed, which
// is untrusted text.
type journalEntry struct {
	Time      string `json:"time"`
	Unit      string `json:"unit"`
	Lifecycle bool   `json:"-"`
	Message   string `json:"message"`
	Cursor    string `json:"-"`
}

func journalArgs(units []string) []string {
	var args []string
	for _, u := range units {
		args = append(args, "--unit="+u)
	}
	return args
}

func journalRead(units []string, n int, logs bool) ([]journalEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandDeadline)
	defer cancel()
	args := append([]string{"--no-pager", "--quiet", "--output=json", "--lines=" + strconv.Itoa(n),
		"--output-fields=MESSAGE,_PID,UNIT,_SYSTEMD_UNIT"}, journalArgs(units)...)
	if !logs {
		args = append(args, "_PID=1")
	}
	out, err := exec.CommandContext(ctx, "journalctl", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("journalctl: %v", err)
	}
	var entries []journalEntry
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var raw map[string]any
		if json.Unmarshal(sc.Bytes(), &raw) != nil {
			continue
		}
		e := journalEntry{Lifecycle: raw["_PID"] == "1", Message: journalText(raw["MESSAGE"])}
		e.Unit, _ = raw["UNIT"].(string)
		if e.Unit == "" {
			e.Unit, _ = raw["_SYSTEMD_UNIT"].(string)
		}
		if us, ok := raw["__REALTIME_TIMESTAMP"].(string); ok {
			if n, err := strconv.ParseInt(us, 10, 64); err == nil {
				e.Time = telemetry.Stamp(time.UnixMicro(n))
			}
		}
		e.Cursor, _ = raw["__CURSOR"].(string)
		entries = append(entries, e)
	}
	return entries, nil
}

// journalText is a journal field's text: a string, or the bytes journalctl
// writes as an array when the field is not UTF-8. It is cut to 2 KiB and
// made safe to show.
func journalText(v any) string {
	var s string
	switch v := v.(type) {
	case string:
		s = v
	case []any:
		b := make([]byte, 0, len(v))
		for _, x := range v {
			if f, ok := x.(float64); ok {
				b = append(b, byte(f))
			}
		}
		s = string(b)
	}
	if len(s) > 2048 {
		s = s[:2048] + "…"
	}
	return telemetry.Sanitize(s)
}

// journalHorizon is the time of the oldest journal entry the host still
// holds for the units: how far back their lifecycle and logs reach. The
// host's journal settings decide it, not the collector.
func journalHorizon(units []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), commandDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, "journalctl", append([]string{"--no-pager", "--quiet", "--output=json", "--output-fields=_PID"}, journalArgs(units)...)...)
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return ""
	}
	defer func() { cancel(); cmd.Wait() }()
	line, err := bufio.NewReader(out).ReadBytes('\n')
	if err != nil {
		return ""
	}
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		return ""
	}
	us, _ := raw["__REALTIME_TIMESTAMP"].(string)
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return ""
	}
	return telemetry.Stamp(time.UnixMicro(n))
}

// streams are every stream the collector reads, by name: each source's
// exported stream, and each reporter's reports.
func (c *collector) streams() map[string]*telemetry.Stream {
	out := map[string]*telemetry.Stream{}
	for _, s := range c.manifest.Sources {
		if s.Stream != "" && s.Enabled {
			out[s.Name] = &telemetry.Stream{Dir: s.Stream}
		}
	}
	for _, p := range c.manifest.Reporters {
		if telemetry.ValidPrincipal(p) {
			out["report."+p] = c.reportStream(p)
		}
	}
	return out
}

func (c *collector) reportStream(principal string) *telemetry.Stream {
	return &telemetry.Stream{Dir: c.state + "/reports/" + principal, SegmentBytes: 512 << 10, Segments: 8}
}

// streamOf is the stream a source reads from: its own, or for a remote
// source, its reporter's reports.
func (c *collector) streamOf(s source) string {
	if s.Stream != "" {
		return s.Name
	}
	if s.Reporter != "" {
		return "report." + s.Reporter
	}
	return ""
}

// runSummary is one run, as a stream's recent records tell it.
type runSummary struct {
	Run      string            `json:"run"`
	Producer string            `json:"producer,omitempty"`
	First    string            `json:"first"`
	Last     string            `json:"last"`
	LastType string            `json:"last_type"`
	Open     bool              `json:"open"`
	Project  string            `json:"project,omitempty"`
	Change   string            `json:"change,omitempty"`
	Version  int               `json:"version,omitempty"`
	Head     string            `json:"head,omitempty"`
	Tree     string            `json:"tree,omitempty"`
	Checks   map[string]string `json:"checks,omitempty"`
	Received string            `json:"received,omitempty"`
}

// ends are the record types that end a run.
var ends = map[string]bool{"pass.end": true, "run.end": true, "approve.end": true, "decision": true}

// runs summarises the runs among a stream's newest records, newest first.
func runs(recs []telemetry.Record) []*runSummary {
	byRun := map[string]*runSummary{}
	var order []*runSummary
	for _, r := range recs {
		if r.Run == "" {
			continue
		}
		s := byRun[r.Run]
		if s == nil {
			s = &runSummary{Run: r.Run, First: r.Time, Open: true, Checks: map[string]string{}}
			if r.Producer != nil {
				s.Producer = r.Producer.Name
			}
			byRun[r.Run] = s
			order = append(order, s)
		}
		s.Last, s.LastType, s.Received = r.Time, r.Type, r.Received
		for _, f := range []struct {
			to   *string
			from string
		}{{&s.Project, r.Project}, {&s.Change, r.Change}, {&s.Head, r.Head}, {&s.Tree, r.Tree}} {
			if f.from != "" {
				*f.to = f.from
			}
		}
		if r.Version != 0 {
			s.Version = r.Version
		}
		if r.Check != "" {
			switch r.Type {
			case "check.queued":
				s.Checks[r.Check] = "queued"
			case "check.build-start", "check.start":
				s.Checks[r.Check] = "running"
			case "check.build-end":
				s.Checks[r.Check] = "built"
			case "check.result":
				res, _ := r.Data["result"].(string)
				s.Checks[r.Check] = res
			}
		}
		if ends[r.Type] {
			s.Open = false
		}
	}
	slices.Reverse(order)
	return order
}

// sources writes one line per source: its units' states, its stream's
// range and latest runs, and how far its journal reaches. With snapshot,
// each source's exported state and open runs come with it.
func (c *collector) sources(snapshot bool) {
	streams := c.streams()
	for _, s := range c.manifest.Sources {
		line := map[string]any{"kind": "source", "name": s.Name, "description": s.Description,
			"enabled": s.Enabled, "location": s.Location}
		for k, v := range map[string]any{"units": s.Units, "configured": s.Configured, "instruments": s.Instruments, "not_instrumented": s.Missing, "reporter": s.Reporter} {
			if v != nil && v != "" {
				line[k] = v
			}
		}
		state, detail := c.sourceState(s, streams, line, snapshot)
		line["state"], line["detail"] = state, detail
		if !c.out.send(line) {
			return
		}
	}
	for _, p := range c.manifest.Reporters {
		name := "report." + p
		line := map[string]any{"kind": "source", "name": name, "description": "reports from " + p,
			"enabled": true, "location": "client", "reporter": p}
		state, detail := c.streamState(name, streams[name], nil, line, snapshot, 0)
		line["state"], line["detail"] = state, detail
		if !c.out.send(line) {
			return
		}
	}
}

// sourceState decides a source's state: disabled, unavailable, failed,
// stale, active or idle, and says why.
func (c *collector) sourceState(s source, streams map[string]*telemetry.Stream, line map[string]any, snapshot bool) (string, string) {
	if !s.Enabled {
		return "disabled", "not enabled in this host's configuration"
	}
	var units []unitState
	var unitErr error
	if len(s.Units) > 0 {
		units, unitErr = c.systemctl(s.Units)
		line["unit_states"] = units
		if h := c.horizon(s.Units); h != "" {
			line["journal_since"] = h
		}
	}
	if s.StateFile != "" && snapshot {
		if data, err := readBounded(s.StateFile, 256<<10); err != nil {
			line["exported_state_error"] = err.Error()
		} else if json.Valid(data) {
			line["exported_state"] = json.RawMessage(data)
		}
	}
	running := false
	for _, u := range units {
		if strings.HasSuffix(u["Id"], ".service") && (u["ActiveState"] == "active" || u["ActiveState"] == "activating" || u["ActiveState"] == "deactivating" || u["ActiveState"] == "reloading") {
			running = true
		}
	}
	name := c.streamOf(s)
	state, detail := "idle", "nothing is running"
	if name != "" {
		state, detail = c.streamState(name, streams[name], &running, line, snapshot, s.StaleAfter)
	}
	switch {
	case unitErr != nil:
		return "unavailable", unitErr.Error()
	case s.Reporter != "" && line["stream"] == nil:
		return "unavailable", fmt.Sprintf("%s has reported nothing: this host has no observation of the source", s.Reporter)
	case state == "unavailable" || state == "stale":
		return state, detail
	}
	for _, u := range units {
		if u["LoadState"] != "loaded" {
			return "unavailable", fmt.Sprintf("%s is %s", u["Id"], u["LoadState"])
		}
		if u["ActiveState"] == "failed" || (u["Result"] != "" && u["Result"] != "success") {
			return "failed", fmt.Sprintf("%s: %s, result %s, status %s", u["Id"], u["ActiveState"], u["Result"], u["ExecMainStatus"])
		}
	}
	if running {
		return "active", "a unit is running"
	}
	return state, detail
}

// streamState reads a stream's range and recent runs into line. running,
// when the source has units, says whether one of its services is running:
// a run the stream holds open while none is was interrupted, since a
// killed process records no end of its own.
func (c *collector) streamState(name string, st *telemetry.Stream, running *bool, line map[string]any, snapshot bool, staleAfter int) (string, string) {
	if st == nil {
		return "unavailable", "the stream is not configured"
	}
	rng, err := st.Range()
	if os.IsNotExist(err) {
		return "idle", "no record has been stored yet"
	}
	if err != nil {
		return "unavailable", err.Error()
	}
	recs, _, err := st.Read(max(rng.Last-2000, 0), 2000, 2<<20)
	if err != nil {
		return "unavailable", err.Error()
	}
	info := map[string]any{"name": name, "epoch": rng.Epoch, "first": rng.First, "last": rng.Last}
	if len(recs) > 0 {
		last := recs[len(recs)-1]
		info["last_time"], info["last_type"] = last.Time, last.Type
		if last.Received != "" {
			info["last_received"] = last.Received
		}
	}
	line["stream"] = info
	rs := runs(recs)
	var open []*runSummary
	for _, r := range rs {
		if r.Open {
			open = append(open, r)
		}
	}
	if snapshot {
		line["open_runs"] = open
		if len(rs) > 0 {
			line["latest_runs"] = rs[:min(len(rs), 5)]
		}
	}
	if staleAfter > 0 && len(recs) > 0 {
		last := recs[len(recs)-1]
		at := last.Received
		if at == "" {
			at = last.Time
		}
		if t, err := time.Parse(time.RFC3339, at); err == nil && c.now().Sub(t) > time.Duration(staleAfter)*time.Second {
			return "stale", fmt.Sprintf("nothing received since %s", at)
		}
	}
	if len(open) > 0 {
		if running != nil && !*running {
			// Only a run the unit's own producer holds open says the
			// unit was interrupted: the newest open run.
			return "stale", fmt.Sprintf("run %s recorded no end and no unit of the source is running: it was interrupted", open[0].Run)
		}
		return "active", fmt.Sprintf("%d open runs", len(open))
	}
	return "idle", "no open run"
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, max)
	}
	return buf, nil
}

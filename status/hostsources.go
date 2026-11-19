package main

// The host's unhealthy automations for valley pending, read from the
// telemetry collector's sources operation concurrently with origin and
// worker check costs (checkcost.go).
//
// Each source the collector reports as failed, stale or unavailable
// prints as one host entry above needs you, beside main's state lines,
// once for the host whatever projects the listing covers:
//
//	host  mirrors  failed · valley-publish@app.service: activating, result exit-code, status 1
//	      see: valley telemetry read --source mirrors --logs
//
// Healthy (idle, active) and disabled sources print nothing. When several
// units of a source are failing, each failing unit is named on its own
// line under the source, up to maxHostUnits, then "and N more".
//
// A caller without the observe grant, a host with no collector, or a
// collector that times out or sends an incomplete answer leaves pending
// with no host entry and no new warning. No read outlives the command
// that started it.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"the-valley/telemetry"
)

// hostSourcesTimeout bounds how long valley pending waits for the
// collector's sources answer.
var hostSourcesTimeout = 10 * time.Second

// maxHostUnits is the most failing unit names listed under one host
// source before summarising the rest as "and N more".
const maxHostUnits = 5

// jsonHostSource is one unhealthy host automation in valley-pending/v1's
// host_sources array: a telemetry source whose state is failed, stale or
// unavailable, with the collector's own reason and any failing units.
type jsonHostSource struct {
	Source string   `json:"source"`
	State  string   `json:"state"`
	Reason string   `json:"reason"`
	Units  []string `json:"units"`
}

// hostSources reads the serving host's telemetry sources once in the
// background until close.
type hostSources struct {
	done    chan struct{}
	entries []jsonHostSource
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// startHostSources starts one bounded read of the serving host's
// telemetry sources, or returns immediately with none when no source
// reaches a host over ssh.
func startHostSources(g git, list []source) *hostSources {
	dest, ok := hostTelemetryDest(g, list)
	if !ok {
		done := make(chan struct{})
		close(done)
		return &hostSources{done: done, entries: []jsonHostSource{}, cancel: func() {}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostSourcesTimeout)
	h := &hostSources{done: make(chan struct{}), entries: []jsonHostSource{}, cancel: cancel}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer close(h.done)
		if entries, err := readHostSources(ctx, dest); err == nil && len(entries) > 0 {
			h.entries = entries
		}
	}()
	return h
}

// hostTelemetryDest resolves the serving host's ssh destination for
// telemetry: g's own telemetry remote first (honouring
// valley.telemetry.remote in the current checkout), else the first
// readable repository in list that resolves to ssh.
func hostTelemetryDest(g git, list []source) (sshDest, bool) {
	if dest, err := telemetryRemote(g, ""); err == nil {
		return dest, true
	}
	for _, s := range list {
		if s.err != nil {
			continue
		}
		if dest, err := telemetryRemote(s.g, ""); err == nil {
			return dest, true
		}
	}
	return sshDest{}, false
}

// wait returns the unhealthy host sources once the read ends, or an empty
// slice when all sources are healthy or the collector could not be read.
func (h *hostSources) wait() []jsonHostSource {
	if h == nil {
		return []jsonHostSource{}
	}
	waited := phase("telemetry")
	<-h.done
	waited()
	if h.entries == nil {
		return []jsonHostSource{}
	}
	return h.entries
}

// close cancels the read if it is still running and waits for its ssh to
// end.
func (h *hostSources) close() {
	if h == nil {
		return
	}
	h.cancel()
	h.wg.Wait()
}

// readHostSources runs valley-telemetry sources on dest and parses its
// answer. Any error, refusal, or incomplete answer is returned so the
// caller drops the read without warning.
func readHostSources(ctx context.Context, dest sshDest) ([]jsonHostSource, error) {
	req := telemetry.Request{V: 1, Op: telemetry.OpSources}
	ssh := sshCommand(ctx, dest, req.Command())
	out, err := ssh.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer context.AfterFunc(ctx, func() { out.Close() })()
	if err := ssh.Start(); err != nil {
		return nil, err
	}
	entries, err := parseHostSources(out)
	io.Copy(io.Discard, out)
	waitErr := ssh.Wait()
	if err != nil {
		return nil, err
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return entries, nil
}

// parseHostSources reads one sources answer, which must end, and keeps
// every source whose state is failed, stale or unavailable.
func parseHostSources(r io.Reader) ([]jsonHostSource, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), telemetry.MaxResponseBytes)
	entries := []jsonHostSource{}
	for sc.Scan() {
		var m struct {
			Kind       string           `json:"kind"`
			Name       string           `json:"name"`
			State      string           `json:"state"`
			Detail     string           `json:"detail"`
			UnitStates []map[string]any `json:"unit_states"`
			Error      string           `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			return nil, errors.New("an answer line is not JSON")
		}
		switch m.Kind {
		case "error":
			return nil, errors.New(telemetry.Sanitize(m.Error))
		case "end":
			return entries, nil
		case "source":
			switch m.State {
			case "failed", "stale", "unavailable":
			default:
				continue
			}
			name := readable(telemetry.Sanitize(m.Name))
			if name == "" {
				continue
			}
			entries = append(entries, jsonHostSource{
				Source: name,
				State:  m.State,
				Reason: readable(telemetry.Sanitize(m.Detail)),
				Units:  failingUnits(m.UnitStates),
			})
		}
	}
	return nil, errors.New("the answer did not end")
}

// failingUnits returns the unit IDs in states that are failing by the
// collector's own rule (telemetry/valley-telemetry/sources.go): active
// state failed, or a non-empty result other than success.
func failingUnits(states []map[string]any) []string {
	units := []string{}
	for _, u := range states {
		active, _ := u["ActiveState"].(string)
		result, _ := u["Result"].(string)
		if active == "failed" || (result != "" && result != "success") {
			id, _ := u["Id"].(string)
			if id = readable(telemetry.Sanitize(id)); id != "" {
				units = append(units, id)
			}
		}
	}
	return units
}

// hostLines renders the unhealthy host sources as valley pending's host
// entries, beside main's state lines.
func (t terminal) hostLines(entries []jsonHostSource) []string {
	var lines []string
	for _, h := range entries {
		colour := colourRefused
		if h.State == "stale" {
			colour = colourWorking
		}
		line := "host  " + t.strong(h.Source) + "  " + t.tint(colour, h.State)
		if h.Reason != "" {
			line += " · " + h.Reason
		}
		lines = append(lines, line)
		if len(h.Units) > 1 {
			shown := min(len(h.Units), maxHostUnits)
			for _, u := range h.Units[:shown] {
				lines = append(lines, "      "+u)
			}
			if rest := len(h.Units) - shown; rest > 0 {
				lines = append(lines, fmt.Sprintf("      and %d more", rest))
			}
		}
		see := "valley telemetry sources"
		if telemetry.ValidSource(h.Source) {
			see = "valley telemetry read --source " + h.Source + " --logs"
		}
		lines = append(lines, "      see: "+see)
	}
	return lines
}

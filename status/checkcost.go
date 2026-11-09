package main

// What each check of a version cost the host to build, against what the
// same check recently cost, for valley changes --checks, valley pending
// --checks and valley review.
//
// The numbers are the host's check worker's own: each attest run a pass
// starts records a check.result for each check it builds, in the worker's
// telemetry stream (workertelemetry.go), naming the project, change,
// version and tree. Its build_ms is how long the check's nix build took,
// and its eval_ms how long the run's shared evaluation took. These are
// diagnostics, never evidence: nothing here is signed, and nothing judges
// a change by them.
//
// A listing reads the worker source once for each host it lists, with the
// same command valley telemetry read runs (telemetry.go). The newest page
// is read first, and then the older pages up to costRecords records, at
// once, each from a cursor made from the newest page's. The reads run
// while the listing reads the policy, so they add little to its time. A
// host that does not answer, or refuses, leaves every version it serves
// without build times, and stderr says why, once.
//
// For each required nix check of a version:
//
//   - build_ms is the newest build of the check for this version, and
//     cached says whether its output was already in the host's store;
//   - baseline_ms is the median build time of the check's newest
//     baselineBuilds uncached, passing builds in the same project and on
//     the same capacity as the version's own build, the
//     builds of every version of this change left out;
//   - new says no such build is among the records read, and
//     other_capacity says there are builds, but only on other capacity:
//     the host was resized, so the check starts afresh and is not grown;
//   - grown says the build took longer than its baseline by more than
//     both grownShare of it and grownBy (grew).
//
// A build's capacity is the line the worker gave attest (capacity.go).
// Records with none compare only with each other.
//
// A record from an attest that does not say whether the output was
// cached counts as cached when its build wrote no log: nix writes the
// builder's output to the log, and building nothing writes none.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"the-valley/telemetry"
)

// workerSource is the telemetry source the host module gives the check
// worker.
const workerSource = "worker"

// costRecords bounds how many of the worker's newest records a listing
// reads from one host: five of the collector's largest pages. Each batch
// of checks writes about five records a check, so this holds the last
// few hundred check builds.
const costRecords = 5 * telemetry.MaxReadLimit

// baselineBuilds is how many of a check's recent builds its baseline is
// the median of.
const baselineBuilds = 20

// A check has grown when its build took longer than its baseline by more
// than both of these. Batches build side by side on one host, so a build
// can take a few seconds longer, or a few percent, for reasons of the
// host's and not the change's. Only growth past both is shown.
const (
	grownShare = 0.20
	grownBy    = 30 * time.Second
)

// grew reports whether a build of build ms has grown against a baseline
// of baseline ms.
func grew(build, baseline int64) bool {
	d := build - baseline
	return d > grownBy.Milliseconds() && float64(d) > grownShare*float64(baseline)
}

// median is the median of ms, which must not be empty.
func median(ms []int64) int64 {
	s := append([]int64(nil), ms...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// checkBuild is one check.result record of the worker's: one check built
// for one version or main's tip, or on main's tip one that did not
// evaluate.
type checkBuild struct {
	time, run       string
	project, change string
	version         int
	tree, check     string
	result          string
	buildMS, evalMS int64
	cached          bool
	capacity        string
}

// costs reads the worker's telemetry of each host a listing serves, once
// a host, in the background.
type costs struct {
	mu    sync.Mutex
	reads map[string]*costRead // by destination identity
	said  map[string]bool      // the reasons already said on stderr
}

type costRead struct {
	done   chan struct{}
	builds []checkBuild // newest first
	mains  []mainRun    // the batches run on main tips (mainstate.go)
	why    string       // why there are none, when the read failed
}

func newCosts() *costs {
	return &costs{reads: map[string]*costRead{}, said: map[string]bool{}}
}

// start begins the read of the host g's telemetry remote names
// (telemetryRemote), unless it has begun.
func (c *costs) start(g git) *costRead {
	dest, err := telemetryRemote(g, "")
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		r := &costRead{done: make(chan struct{}), why: err.Error()}
		close(r.done)
		return r
	}
	if r := c.reads[dest.identity()]; r != nil {
		return r
	}
	r := &costRead{done: make(chan struct{})}
	c.reads[dest.identity()] = r
	go func() {
		defer close(r.done)
		r.builds, r.mains, r.why = readBuilds(dest)
	}()
	return r
}

// wait is what a read found, once it has ended, and whether it read. The
// reason a read failed is said on stderr the first time it is met.
func (c *costs) wait(r *costRead, warn func(format string, args ...any)) ([]checkBuild, bool) {
	<-r.done
	if r.why == "" {
		return r.builds, true
	}
	c.mu.Lock()
	said := c.said[r.why]
	c.said[r.why] = true
	c.mu.Unlock()
	if !said {
		warn("no check lists its build times: %s", r.why)
	}
	return nil, false
}

// readBuilds reads up to costRecords of the newest records of the worker
// source at dest, and returns the check builds among them, newest first,
// and the starts and ends of the batches run on main tips.
func readBuilds(dest sshDest) ([]checkBuild, []mainRun, string) {
	first, err := readPage(dest, "")
	if err != nil {
		return nil, nil, err.Error()
	}
	pages := []workerPage{first}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for p := 1; p*telemetry.MaxReadLimit < costRecords; p++ {
		// Page p holds, for each stream, the records up to p pages
		// before the newest one the first page read.
		var parts []string
		for name, pos := range first.cursor {
			if after := pos.Seq - int64((p+1)*telemetry.MaxReadLimit); after+telemetry.MaxReadLimit > 0 {
				parts = append(parts, fmt.Sprintf("%s=%s:%d", name, pos.Epoch, max(after, 0)))
			}
		}
		if len(parts) == 0 {
			break
		}
		sort.Strings(parts)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// An older page that cannot be read leaves the baselines
			// shorter, and nothing else.
			if page, err := readPage(dest, strings.Join(parts, ",")); err == nil {
				mu.Lock()
				pages = append(pages, page)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	var builds []checkBuild
	var mains []mainRun
	for _, page := range pages {
		mains = append(mains, page.mains...)
		for _, b := range page.builds {
			if key := b.key; !seen[key] {
				seen[key] = true
				builds = append(builds, b.checkBuild)
			}
		}
	}
	sort.SliceStable(builds, func(i, j int) bool { return builds[i].time > builds[j].time })
	return builds, mains, ""
}

// workerPage is one answer to a read of the worker source.
type workerPage struct {
	cursor telemetry.Cursor
	builds []keyedBuild
	mains  []mainRun
}

type keyedBuild struct {
	checkBuild
	key string // the record's stream and number
}

// readPage reads one page of the worker source: the records after the
// cursor, or the newest without one.
func readPage(dest sshDest, cursor string) (workerPage, error) {
	req := telemetry.Request{V: 1, Op: telemetry.OpRead, Source: workerSource, Cursor: cursor, Limit: telemetry.MaxReadLimit}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ssh := sshCommand(ctx, dest, req.Command())
	out, err := ssh.StdoutPipe()
	if err != nil {
		return workerPage{}, err
	}
	var sshErr strings.Builder
	ssh.Stderr = &sshErr
	if err := ssh.Start(); err != nil {
		return workerPage{}, fmt.Errorf("could not run ssh: %v", err)
	}
	page, refused, err := parsePage(out)
	io.Copy(io.Discard, out)
	waitErr := ssh.Wait()
	switch {
	case refused != "":
		return workerPage{}, fmt.Errorf("%s refused to read the worker's telemetry: %s", dest.host, refused)
	case err == nil:
		return page, nil
	}
	if why := strings.TrimSpace(telemetry.Sanitize(sshErr.String())); why != "" {
		err = fmt.Errorf("%v: %s", err, why)
	} else if waitErr != nil {
		err = fmt.Errorf("%v: %v", err, waitErr)
	}
	return workerPage{}, fmt.Errorf("the worker's telemetry on %s did not read: %v", dest.host, err)
}

// parsePage reads one answer, which must end, or says why the host
// refused it.
func parsePage(r io.Reader) (page workerPage, refused string, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), telemetry.MaxResponseBytes)
	for sc.Scan() {
		var m struct {
			Kind    string         `json:"kind"`
			Stream  string         `json:"stream"`
			Seq     int64          `json:"seq"`
			Type    string         `json:"type"`
			Time    string         `json:"time"`
			Run     string         `json:"run"`
			Project string         `json:"project"`
			Change  string         `json:"change"`
			Version int            `json:"version"`
			Tree    string         `json:"tree"`
			Check   string         `json:"check"`
			Data    map[string]any `json:"data"`
			Error   string         `json:"error"`
			What    string         `json:"what"`
			Cursor  string         `json:"cursor"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			return workerPage{}, "", fmt.Errorf("an answer line is not JSON")
		}
		switch m.Kind {
		case "error":
			return workerPage{}, telemetry.Sanitize(m.Error), nil
		case "unavailable":
			// The journal says nothing a build time needs. A stream the
			// host could not read may be missing records, so the page is
			// not complete.
			if m.What == "journal" {
				continue
			}
			return workerPage{}, "", fmt.Errorf("the host could not read %s: %s", telemetry.Sanitize(m.What), telemetry.Sanitize(m.Error))
		case "end":
			cur, err := telemetry.ParseCursor(m.Cursor)
			if err != nil {
				return workerPage{}, "", err
			}
			page.cursor = cur
			return page, "", nil
		case "record":
			if m.Change == "" && (m.Type == "batch.start" || m.Type == "batch.end") {
				run := mainRun{project: m.Project, tree: m.Tree, ended: m.Type == "batch.end"}
				run.run, _ = m.Data["attest_run"].(string)
				checks, _ := m.Data["checks"].([]any)
				for _, c := range checks {
					if name, ok := c.(string); ok {
						run.checks = append(run.checks, name)
					}
				}
				page.mains = append(page.mains, run)
				continue
			}
			if m.Type != "check.result" {
				continue
			}
			build, ok := m.Data["build_ms"].(float64)
			if !ok && m.Change != "" {
				continue // a check that did not evaluate built nothing
			}
			// A check on main that did not evaluate is kept as a run that
			// failed, which main's state reads (mainstate.go). It passed
			// no build, so no cost counts it.
			b := checkBuild{time: m.Time, run: m.Run, project: m.Project, change: m.Change, version: m.Version,
				tree: m.Tree, check: m.Check, buildMS: int64(build)}
			b.result, _ = m.Data["result"].(string)
			if eval, ok := m.Data["eval_ms"].(float64); ok {
				b.evalMS = int64(eval)
			}
			b.capacity, _ = m.Data["capacity"].(string)
			if cached, ok := m.Data["cached"].(bool); ok {
				b.cached = cached
			} else {
				logged, _ := m.Data["log_bytes"].(float64)
				b.cached = logged == 0
			}
			page.builds = append(page.builds, keyedBuild{b, fmt.Sprintf("%s#%d", m.Stream, m.Seq)})
		}
	}
	return workerPage{}, "", fmt.Errorf("the answer did not end")
}

// addCosts gives a version's nix checks what they cost to build, from
// builds, newest first: the version's own, and the recent builds of each
// check in its project.
func addCosts(out *jsonVersionChecks, required []jsonRequired, project, changeID string, n int, builds []checkBuild) {
	runner := map[string]string{}
	for _, q := range required {
		runner[q.Name] = q.Runner
	}
	var total int64
	evals := map[string]int64{} // each attest run's evaluation
	found := false
	for i := range out.Required {
		s := &out.Required[i]
		if runner[s.Name] != "nix" {
			continue
		}
		var own *checkBuild
		for j := range builds {
			b := &builds[j]
			if b.project == project && b.check == s.Name && b.change == changeID && b.version == n && (b.tree == "" || b.tree == out.Tree) {
				own = b
				break
			}
		}
		var recent []int64
		other := false
		for j := range builds {
			b := &builds[j]
			if b.project != project || b.check != s.Name {
				continue
			}
			if b.change == changeID || b.cached || b.result != checkPassed {
				continue
			}
			if own != nil && b.capacity != own.capacity {
				other = true
			} else if len(recent) < baselineBuilds {
				recent = append(recent, b.buildMS)
			}
		}
		if own != nil {
			found = true
			build, cached := own.buildMS, own.cached
			s.BuildMS, s.Cached = &build, &cached
			total += build
			evals[own.run] = max(evals[own.run], own.evalMS)
		}
		if len(recent) == 0 {
			s.New, s.OtherCapacity = true, other
			continue
		}
		base := median(recent)
		s.BaselineMS = &base
		s.Grown = own != nil && !own.cached && grew(own.buildMS, base)
	}
	if !found {
		return
	}
	var eval int64
	for _, ms := range evals {
		eval += ms
	}
	out.BuildMS, out.EvalMS = &total, &eval
}

// costLine is what checksLine adds about a version's build times: its
// total and its evaluation, then each check that grew against its
// baseline, most first: " in 9m10s (eval 24s); worker-e2e 431s, +68s
// against its recent 363s". A check with no baseline is no news, and is
// left out.
func costLine(c *jsonVersionChecks) string {
	if c.BuildMS == nil {
		return ""
	}
	line := fmt.Sprintf(" in %s (eval %s)", duration(*c.BuildMS), duration(*c.EvalMS))
	var grown []jsonCheckState
	for _, q := range c.Required {
		if q.Grown {
			grown = append(grown, q)
		}
	}
	sort.SliceStable(grown, func(i, j int) bool {
		return *grown[i].BuildMS-*grown[i].BaselineMS > *grown[j].BuildMS-*grown[j].BaselineMS
	})
	for _, q := range grown {
		line += fmt.Sprintf("; %s %s, +%s against its recent %s", q.Name, seconds(*q.BuildMS), seconds(*q.BuildMS-*q.BaselineMS), seconds(*q.BaselineMS))
	}
	return line
}

// duration is ms as 9m10s, to the second.
func duration(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).Round(time.Second).String()
}

// seconds is ms as 431s, to the second.
func seconds(ms int64) string {
	return fmt.Sprintf("%ds", (ms+500)/1000)
}

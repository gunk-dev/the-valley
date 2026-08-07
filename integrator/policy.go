package main

// The policy bridge. The integrator does not derive required checks; it
// asks the deriver, which is `valley checks` (dcr-f41f718). The whole of
// the mandatory-versus-overridable distinction is CUE unification, so a
// second implementation of the composition here would be a second copy of
// the decision.
//
// Both layers are resolved from the integrator's own side and never from
// the submitted tree (bd-eaefe82): the project layer from a checkout of the
// target tip, the instance layer from where the integrator is configured to
// find it. A change therefore never supplies the policy that gates it.

import (
	"the-valley/integrator/verdict"

	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// instanceLayer is where the group's floor comes from. The floor is the
// instance repository's, read at the tip that has been integrated
// (dcr-f41f718), and the instance repository is the group's own
// (dcr-b87f6e8). A host that serves that repository is therefore already
// holding the authoritative copy, and reading it there is reading it where
// it is authoritative: no checkout to keep in step, no refresh to schedule,
// and no way for the floor a controller composes against to differ from the
// floor the group last landed.
//
// The consequence is worth stating, because it is a property and not a
// limitation. A change to the floor takes effect at the first pass after it
// lands on that ref, and never before — an edit sitting in a branch, a
// worktree, or a request that has not been integrated governs nothing.
//
// path is the fallback: a directory of `*.cue` on disk, for a host that
// serves no instance repository and must be given the layer some other way.
type instanceLayer struct {
	repo string // the bare repository carrying the floor, read and never written
	ref  string // the ref whose tip is authoritative
	dir  string // the floor's directory in that tip's tree
	path string // a materialized layer, when no repository carries one
}

// open resolves the layer to a directory on disk for the length of one pass,
// and returns what ends it.
//
// The tip is taken apart with ls-tree and show rather than checked out. A
// worktree would write into the instance repository — git keeps worktree
// metadata under $GIT_DIR — and a controller has no business writing a
// repository it does not serve. Reading is all this needs, and reading is
// all it is granted.
func (l instanceLayer) open() (string, func(), error) {
	noop := func() {}
	if l.repo == "" {
		return l.path, noop, nil
	}
	tree := l.ref + ":" + l.dir
	listing, err := git(l.repo, "ls-tree", "--name-only", tree)
	if err != nil {
		return "", noop, fmt.Errorf("no instance policy layer: %s has no %s/ at %s: %w", l.repo, l.dir, l.ref, err)
	}
	dir, err := scratch("valley-integrator-floor")
	if err != nil {
		return "", noop, err
	}
	done := func() { _ = os.RemoveAll(dir) }
	found := 0
	for _, name := range strings.Split(strings.TrimSpace(listing), "\n") {
		// The deriver and the composer both read the layer as the `*.cue`
		// documents directly in it, so those are what is taken.
		if !strings.HasSuffix(name, ".cue") {
			continue
		}
		body, err := git(l.repo, "show", tree+"/"+name)
		if err != nil {
			done()
			return "", noop, fmt.Errorf("reading %s/%s from %s at %s: %w", l.dir, name, l.repo, l.ref, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			done()
			return "", noop, err
		}
		found++
	}
	if found == 0 {
		done()
		return "", noop, fmt.Errorf("no instance policy layer: %s holds no *.cue in %s/ at %s", l.repo, l.dir, l.ref)
	}
	return dir, done, nil
}

// policySource is where the two layers are read from, and the schema they
// are composed under.
type policySource struct {
	schema   string // the verification schema
	layer    instanceLayer
	instance string // the instance layer as resolved on disk, for one pass
	project  string // the project layer, relative to the target tip's tree
	valley   string // the deriver
	cue      string // the composer, for the catalogue the deriver does not print
}

// open resolves the instance layer and returns a source that reads it from
// disk, together with what ends the pass. Held for the whole pass rather
// than resolved per call, so the deriver and the composer are given the same
// bytes: they are two readings of one policy, not two policies.
func (p policySource) open() (policySource, func(), error) {
	dir, done, err := p.layer.open()
	if err != nil {
		return p, done, err
	}
	p.instance = dir
	return p, done, nil
}

// derived is one run of the deriver over a diff: which classes the diff
// matched, and which checks that made required.
type derived struct {
	classes  map[string]bool
	requires map[string][]string // check name -> the classes requiring it
}

// derive runs the deriver over a diff, in a checkout of the target tip. The
// checkout supplies the project layer and the commits the diff names; the
// instance layer comes from outside it.
func (p policySource) derive(tip *worktree, from, to string) (derived, error) {
	cmd := exec.Command(p.valley, "checks",
		"--schema", p.schema,
		"--instance", p.instance,
		"--project", filepath.Join(tip.dir, p.project),
		from, to)
	cmd.Dir = tip.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return derived{}, fmt.Errorf("deriving required checks for %s..%s: %w\n%s", verdict.Short(from), verdict.Short(to), err, strings.TrimSpace(string(out)))
	}
	return parseDerived(string(out))
}

// parseDerived reads what `valley checks` printed. The deriver reports for
// a person, so this is the one place the two programs are coupled by
// output; the coupling is pinned by the integrator's own checks rather
// than assumed.
func parseDerived(out string) (derived, error) {
	d := derived{classes: map[string]bool{}, requires: map[string][]string{}}
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "classes matched:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "classes matched:"))
			if rest == "none" {
				continue
			}
			for _, c := range strings.Split(rest, ",") {
				if c = strings.TrimSpace(c); c != "" {
					d.classes[c] = true
				}
			}
		case line == "required checks:":
			for i++; i < len(lines); i++ {
				body := lines[i]
				if !strings.HasPrefix(body, "  ") || strings.TrimSpace(body) == "" {
					i--
					break
				}
				// "  <name>  <mandatory|default>  <class>[, <class>…]"
				fields := strings.Fields(body)
				if len(fields) < 2 {
					return derived{}, fmt.Errorf("the deriver printed %q, which is not a required-check line", body)
				}
				name := fields[0]
				var classes []string
				for _, c := range strings.Split(strings.Join(fields[2:], " "), ",") {
					if c = strings.TrimSpace(c); c != "" {
						classes = append(classes, c)
					}
				}
				d.requires[name] = classes
			}
		case strings.HasPrefix(line, "required checks: none"):
			// Nothing owed; the map stays empty.
		}
	}
	return d, nil
}

// catalogueEntry is one check as the composed policy defines it: how it is
// run, and how long an observation of it stays good.
type catalogueEntry struct {
	Runner    string `json:"runner"`
	Attribute string `json:"attribute"`
	Command   string `json:"command"`
	Validity  string `json:"validity"`
}

// catalogue reads the composed policy's check definitions. The deriver
// prints which checks are required and not what a check is, and the
// integrator needs the latter: a pure check's attribute is what it
// evaluates a closure over, and an effectful check's window is Δ.
//
// This is the same composition the deriver ran, over the same layers and
// the same schema — `cue export` of a field the deriver does not print,
// not a second opinion about what the policy says.
func (p policySource) catalogue(tip *worktree) (map[string]catalogueEntry, string, error) {
	args := []string{"export", "--out", "json", "-e", "policy", p.schema}
	for _, dir := range []string{p.instance, filepath.Join(tip.dir, p.project)} {
		docs, err := filepath.Glob(filepath.Join(dir, "*.cue"))
		if err != nil || len(docs) == 0 {
			return nil, "", fmt.Errorf("no policy layer at %s", dir)
		}
		args = append(args, docs...)
	}
	cmd := exec.Command(p.cue, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("composing the policy: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	var composed struct {
		Checks map[string]catalogueEntry `json:"checks"`
	}
	if err := json.Unmarshal(out, &composed); err != nil {
		return nil, "", fmt.Errorf("reading the composed policy: %w", err)
	}
	return composed.Checks, sha256hex(out), nil
}

// required turns a derivation and a catalogue into the check set the
// verdict is computed over.
func (p policySource) required(d derived, cat map[string]catalogueEntry) ([]verdict.Required, error) {
	var out []verdict.Required
	for name, classes := range d.requires {
		entry, ok := cat[name]
		if !ok {
			return nil, fmt.Errorf("the policy requires %q and defines no check by that name", name)
		}
		window, err := parseWindow(entry.Validity)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", name, err)
		}
		out = append(out, verdict.Required{
			Name:      name,
			Classes:   classes,
			Runner:    entry.Runner,
			Attribute: entry.Attribute,
			Validity:  window,
		})
	}
	return out, nil
}

// parseWindow reads a #Duration from the policy. The schema admits whole
// seconds, minutes, hours and days; an empty window is no window, which is
// what makes the integrator re-demand the check.
func parseWindow(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	unit := s[len(s)-1]
	if unit == 'd' {
		d, err := time.ParseDuration(s[:len(s)-1] + "h")
		return d * 24, err
	}
	return time.ParseDuration(s)
}

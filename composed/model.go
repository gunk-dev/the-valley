package composed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Pin says whether a requirement is mandatory or a default. The form of
// the composed value is the bit. A concrete `true`, which the floor writes,
// cannot be turned off: a project writing `false` conflicts. `bool |
// *true`, which a template writes, is a default a project may turn off.
type Pin string

const (
	Mandatory Pin = "mandatory"
	Default   Pin = "default"
)

// Class is one path class of the composed policy.
type Class struct {
	// Paths are the globs the class covers: each pattern whose value is
	// true, sorted. A pattern a project turned off covers nothing.
	Paths []string

	// Requires are the checks the class requires: each check whose value
	// is true, with whether that requirement is mandatory.
	Requires map[string]Pin

	// Carry is the class's carry mode as the schema resolves it (_mode):
	// exact, same-class or same-paths.
	Carry string
}

// Model is the whole composed policy, as the deriver reads it: the policy
// Compose reads, every class, and what a path no class covers requires.
type Model struct {
	Policy

	Classes map[string]Class

	// Unclassified are the checks a path no class covers requires: each
	// check of the policy's `unclassified` set whose value is true, with
	// whether that requirement is mandatory.
	Unclassified map[string]Pin
}

// The values the schema gives a check's runner and cost, and a class's
// carry mode. A schema older than this reader lacks one of them, and is
// refused rather than read as the loosest value.
var (
	runners = map[string]bool{"nix": true, "command": true}
	costs   = map[string]bool{"quick": true, "slow": true}
	modes   = map[string]bool{"exact": true, "same-class": true, "same-paths": true}
)

// Read composes the policy as Compose does and reads all of it: each
// class's paths, requirements and carry mode, what the `unclassified` set
// requires, and whether each requirement is mandatory. The digest is the
// one Compose gives, of the same export.
//
// It reads as `valley checks` does. The layers are vetted concretely
// first, so a composition with an error outside `policy` is refused too. A
// class's carry mode is the schema's own resolution (_mode). Whether a
// requirement is mandatory is asked of cue, one requirement at a time: a
// document setting it to `false` is vetted with the layers, and a
// requirement that then fails to vet is mandatory. That is the test a
// project writing `false` would meet. A probe that fails for any reason
// reads as mandatory, the strict answer, and the bit never changes which
// checks are required.
//
// Read refuses a policy that requires a check it does not define. The
// schema's cross-check refuses one in a layer, but not in a class added by
// refining `policy` directly, and a required check that nobody can run is
// the one failure a policy must never have.
func Read(ctx context.Context, cue, schema, instance, project string) (*Model, error) {
	if info, err := os.Stat(schema); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("no policy schema at %s", schema)
	}
	docs, err := documents(schema, instance, project)
	if err != nil {
		return nil, err
	}
	vet := command(ctx, cue, append([]string{"vet", "-c"}, docs...)...)
	if out, err := vet.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("the policy layers do not compose: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	pol, out, err := compose(ctx, cue, docs)
	if err != nil {
		return nil, err
	}
	var read struct {
		Classes map[string]struct {
			Paths    map[string]bool `json:"paths"`
			Requires map[string]bool `json:"requires"`
		} `json:"classes"`
		Unclassified map[string]bool `json:"unclassified"`
	}
	if err := json.Unmarshal(out, &read); err != nil {
		return nil, fmt.Errorf("reading the composed policy: %w", err)
	}
	modeOut, err := export(ctx, cue, docs, "_mode")
	if err != nil {
		return nil, fmt.Errorf("reading the classes' carry modes: %w", err)
	}
	var resolved map[string]string
	if err := json.Unmarshal(modeOut, &resolved); err != nil {
		return nil, fmt.Errorf("reading the classes' carry modes: %w", err)
	}

	for name, c := range pol.Checks {
		if !runners[c.Runner] {
			return nil, fmt.Errorf("check %s has no runner this deriver knows (%q)", name, c.Runner)
		}
		if !costs[c.Cost] {
			return nil, fmt.Errorf("check %s has no cost this deriver knows (%q)", name, c.Cost)
		}
	}
	m := &Model{Policy: *pol, Classes: map[string]Class{}, Unclassified: map[string]Pin{}}
	var probes []probe
	for name, c := range read.Classes {
		mode := resolved[name]
		if !modes[mode] {
			return nil, fmt.Errorf("class %s has no carry mode this deriver knows (%q)", name, mode)
		}
		class := Class{Paths: trueKeys(c.Paths), Requires: map[string]Pin{}, Carry: mode}
		for _, check := range trueKeys(c.Requires) {
			if _, ok := pol.Checks[check]; !ok {
				return nil, fmt.Errorf("class %s requires check %s, which the composed policy does not define", name, check)
			}
			probes = append(probes, probe{class: name, check: check})
		}
		m.Classes[name] = class
	}
	for name := range resolved {
		if _, ok := read.Classes[name]; !ok {
			return nil, fmt.Errorf("the schema resolved a carry mode for class %s, which the composed policy does not have", name)
		}
	}
	for _, check := range trueKeys(read.Unclassified) {
		if _, ok := pol.Checks[check]; !ok {
			return nil, fmt.Errorf("the unclassified set requires check %s, which the composed policy does not define", check)
		}
		probes = append(probes, probe{unclassified: true, check: check})
	}

	pins, err := pinAll(ctx, cue, docs, probes)
	if err != nil {
		return nil, err
	}
	for i, p := range probes {
		if p.unclassified {
			m.Unclassified[p.check] = pins[i]
		} else {
			m.Classes[p.class].Requires[p.check] = pins[i]
		}
	}
	return m, nil
}

// trueKeys are the keys of a set written as a struct whose value is true,
// sorted.
func trueKeys(set map[string]bool) []string {
	var out []string
	for k, on := range set {
		if on {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// probe is one requirement whose pin is asked of cue: a check a class
// requires, or one the unclassified set requires.
type probe struct {
	class        string
	unclassified bool
	check        string
}

// document is the probe's one field: the requirement set to false.
func (p probe) document() (string, error) {
	check, err := json.Marshal(p.check)
	if err != nil {
		return "", err
	}
	if p.unclassified {
		return fmt.Sprintf("package verification\npolicy: unclassified: %s: false\n", check), nil
	}
	class, err := json.Marshal(p.class)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("package verification\npolicy: classes: %s: requires: %s: false\n", class, check), nil
}

// pinAll asks cue the pin of every probe, several at a time, and returns
// them in the probes' order. Each probe is its own document, vetted with
// the layers alone, so one probe never answers for another.
func pinAll(ctx context.Context, cue string, docs []string, probes []probe) ([]Pin, error) {
	if len(probes) == 0 {
		return nil, nil
	}
	dir, err := scratch("valley-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	pins := make([]Pin, len(probes))
	errs := make([]error, len(probes))
	slots := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			pins[i], errs[i] = pin(ctx, cue, docs, filepath.Join(dir, fmt.Sprintf("probe-%d.cue", i)), p)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return pins, nil
}

// pin vets the layers with one probe written at path. A vet that fails, for
// whatever reason, is a requirement no project can turn off. A cue that
// cannot be run at all, or a context that is done, answers nothing.
func pin(ctx context.Context, cue string, docs []string, path string, p probe) (Pin, error) {
	doc, err := p.document()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		return "", fmt.Errorf("writing a probe of whether %s is mandatory: %w", p.check, err)
	}
	cmd := command(ctx, cue, append(append([]string{"vet"}, docs...), path)...)
	var discard bytes.Buffer
	cmd.Stdout, cmd.Stderr = &discard, &discard
	err = cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("probing whether %s is mandatory: %w", p.check, ctx.Err())
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return Default, nil
	case errors.As(err, &exit):
		return Mandatory, nil
	default:
		return "", fmt.Errorf("probing whether %s is mandatory: %w", p.check, err)
	}
}

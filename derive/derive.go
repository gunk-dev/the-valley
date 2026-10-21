// Package derive says which checks a change requires. It takes the whole
// composed policy, as composed.Read reads it, and every path the change's
// diff touches (Diff), and returns each required check with the classes
// requiring it, whether it is mandatory, and how it is run; the change's
// carry mode; and the paths no class covers.
//
// Matching is a set operation, never first-match. A change requires the
// union of the checks of every class one of its paths matches, and a path
// no class matches requires the checks of the policy's `unclassified` set.
// A class's paths are globs: `*` matches within one path segment, and a
// `**` segment matches any number of segments, none included (Match).
//
// bin/valley's deriver (derive_required) answers the same question, and
// the tests beside this file hold the two to the same answers. Where they
// differ, the difference is a known fault of bin/valley's, and those tests
// name each one.
package derive

import (
	"fmt"
	"sort"

	"the-valley/composed"
)

// Result is what a change requires.
type Result struct {
	Paths        []string   // every side of the diff, sorted bytewise, each once
	Classes      []string   // the classes at least one path matched, sorted
	Unclassified []string   // the paths no class covers, sorted
	Carry        string     // the change's carry mode: exact, same-class or same-paths
	Required     []Required // the required checks, sorted by name
}

// Required is one check a change requires.
type Required struct {
	Name string

	// Classes are the matched classes requiring it, sorted.
	Classes []string

	// Unclassified is whether the unclassified set requires it, because a
	// path no class covers is among the change's paths.
	Unclassified bool

	// Pin is Mandatory when any requirement of it is mandatory, and
	// Default when every one is a default a project may turn off.
	Pin composed.Pin

	// How the check is run, how long an observation of it stays good, and
	// what running it costs, as the policy defines it.
	composed.Check
}

// strictness orders the carry modes, loosest first.
var strictness = map[string]int{"same-paths": 0, "same-class": 1, "exact": 2}

// Derive says what a change touching paths requires under m. paths need
// not be sorted or unique.
//
// The carry mode is the strictest among the matched classes. A path no
// class covers is same-paths, so it can make no change stricter.
func Derive(m *composed.Model, paths []string) (*Result, error) {
	type compiled struct {
		name  string
		globs []glob
	}
	names := make([]string, 0, len(m.Classes))
	for name := range m.Classes {
		names = append(names, name)
	}
	sort.Strings(names)
	classes := make([]compiled, len(names))
	for i, name := range names {
		classes[i].name = name
		for _, p := range m.Classes[name].Paths {
			classes[i].globs = append(classes[i].globs, compile(p))
		}
	}

	r := &Result{Paths: sortedSet(paths), Classes: []string{}, Unclassified: []string{}, Carry: "same-paths", Required: []Required{}}
	matched := map[string]bool{}
	for _, path := range r.Paths {
		hit := false
		for _, c := range classes {
			for _, g := range c.globs {
				if g.match(path) {
					matched[c.name] = true
					hit = true
					break
				}
			}
		}
		if !hit {
			r.Unclassified = append(r.Unclassified, path)
		}
	}

	required := map[string]*Required{}
	need := func(check string, pin composed.Pin) (*Required, error) {
		q := required[check]
		if q == nil {
			def, ok := m.Checks[check]
			if !ok {
				return nil, fmt.Errorf("the policy requires %s and defines no check by that name", check)
			}
			q = &Required{Name: check, Classes: []string{}, Pin: composed.Default, Check: def}
			required[check] = q
		}
		switch pin {
		case composed.Mandatory:
			q.Pin = composed.Mandatory
		case composed.Default:
		default:
			return nil, fmt.Errorf("the policy's requirement of %s is neither mandatory nor a default (%q)", check, pin)
		}
		return q, nil
	}
	for _, c := range classes {
		if !matched[c.name] {
			continue
		}
		r.Classes = append(r.Classes, c.name)
		class := m.Classes[c.name]
		mode, ok := strictness[class.Carry]
		if !ok {
			return nil, fmt.Errorf("class %s has no carry mode this deriver knows (%q)", c.name, class.Carry)
		}
		if mode > strictness[r.Carry] {
			r.Carry = class.Carry
		}
		for check, pin := range class.Requires {
			q, err := need(check, pin)
			if err != nil {
				return nil, err
			}
			q.Classes = append(q.Classes, c.name)
		}
	}
	if len(r.Unclassified) > 0 {
		for check, pin := range m.Unclassified {
			q, err := need(check, pin)
			if err != nil {
				return nil, err
			}
			q.Unclassified = true
		}
	}

	for _, q := range required {
		r.Required = append(r.Required, *q)
	}
	sort.Slice(r.Required, func(i, j int) bool { return r.Required[i].Name < r.Required[j].Name })
	return r, nil
}

// sortedSet is paths sorted bytewise, each once.
func sortedSet(paths []string) []string {
	out := append([]string{}, paths...)
	sort.Strings(out)
	n := 0
	for i, p := range out {
		if i == 0 || p != out[n-1] {
			out[n] = p
			n++
		}
	}
	return out[:n]
}

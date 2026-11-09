package main

// The policy bridge. The integrator derives required checks in-process:
// cue composes the two layers under the schema into one model, as the
// composed-policy reader (../composed) reads it, and the deriver
// (../derive) matches a diff's paths against that model. Composition is
// cue's alone, so the integrator holds no second copy of it.
//
// Both layers are resolved from the integrator's own side and never from
// the submitted tree (bd-eaefe82): the project layer from the target tip,
// the instance layer from where the integrator is configured to find it. A change therefore never supplies the policy that gates it.
//
// Each layer is a directory of its own under `policy/`, which is the layout
// dcr-f41f718's worked example lays out and the layout the defaults name:
// the floor at `policy/instance` in the instance repository's tip, the
// project's own layer at `policy/project` in the target tip. A repository
// that keeps either somewhere else says so with --instance-policy or
// --project-policy.
//
// A layer that is not there defaults in the safe direction of that layer's
// job (dcr-f41f718), and the two directions are opposite: an absent project
// layer composes the floor alone, and an absent floor is refused.

import (
	"the-valley/composed"
	"the-valley/derive"
	"the-valley/integrator/verdict"

	"context"
	"fmt"
	"time"
)

// The floor is the instance repository's, read at the tip that has been
// integrated (dcr-f41f718), and the instance repository is the group's own
// (dcr-b87f6e8). A host that serves that repository is therefore already
// holding the authoritative copy, and reading it there is reading it where
// it is authoritative: no checkout to keep in step, no refresh to schedule,
// and no way for the floor a controller composes against to differ from the
// floor the group last landed. composed.Floor reads it with ls-tree and
// show rather than a worktree, because a worktree would write into the
// instance repository, and a controller has no business writing a
// repository it does not serve.
//
// The consequence is worth stating, because it is a property and not a
// limitation. A change to the floor takes effect at the first pass after it
// lands on that ref, and never before — an edit sitting in a branch, a
// worktree, or a request that has not been integrated governs nothing.
//
// A host that serves no instance repository is given a materialized
// directory of `*.cue` instead.

// policySource is where the two layers are read from, and the schema they
// are composed under.
type policySource struct {
	schema     string // the verification schema
	layer      composed.Floor
	projectDir string // the project layer's directory in the target tip's tree
	cue        string // the composer

	// diff reads every side of a diff in a repository; derive.Diff when
	// nil.
	diff func(ctx context.Context, dir string, env []string, base, head string) ([]string, error)

	// Both layers as directories of *.cue on disk, and the policy they
	// compose, resolved and read by open and good for one request.
	instance string
	project  string
	model    *composed.Model

	// note is the one thing a pass has to say about how the layers
	// resolved, when that is not what the configuration reads as. Empty
	// when both were where they are configured to be.
	note string
}

// open resolves both layers, reads the policy they compose, and returns a
// source that derives under it, together with what ends the request's use
// of it. The policy is read once per request, so the request's candidate
// derivation, its intervening derivation, the check definitions and the
// digest its landing records are all of one model.
func (p policySource) open(tip *worktree) (policySource, func(), error) {
	dir, done, err := p.layer.Open(context.Background(), "valley-integrator-floor")
	if err != nil {
		return p, done, err
	}
	p.instance = dir
	endProject, err := p.openProject(tip)
	if err != nil {
		done()
		return p, func() {}, err
	}
	if err := p.read(); err != nil {
		endProject()
		done()
		return p, func() {}, err
	}
	return p, func() { endProject(); done() }, nil
}

// read composes the resolved layers under the schema into the model. The
// integrator never reads whether a requirement is mandatory, so it reads
// the model without asking cue that of every requirement.
func (p *policySource) read() error {
	m, err := composed.ReadUnpinned(context.Background(), p.cue, p.schema, p.instance, p.project)
	if err != nil {
		return err
	}
	p.model = m
	return nil
}

// openProject resolves the project's own layer at the target tip, read
// from git as composed.Project reads it, and returns what ends it. The
// layer is read from the tip's commit and not from the checkout's files,
// so presence is decided as approve decides it: a directory with a *.cue
// entry is there, one with none is not, and one that cannot be read is
// refused.
//
// A target that carries no layer of its own is judged under the floor
// alone. The project layer only ever adds (dcr-f41f718), and the identity
// of "adds" is "adds nothing", so that is what absence means. Refusing the
// target instead would make a repository unlandable until its first policy
// commit, which cannot land either: a change never supplies the policy that
// gates it.
func (p *policySource) openProject(tip *worktree) (func(), error) {
	dir, note, done, err := composed.Project(context.Background(), tip.repo, tip.rev, p.projectDir, "valley-integrator-project")
	if err != nil {
		return done, err
	}
	p.project, p.note = dir, note
	return done, nil
}

// derived is one derivation over a diff: how many changed paths it read,
// which classes the diff matched, which checks that made required, and the
// change's carry mode.
//
// A path no class covers puts the name `unclassified` in classes, and a
// check the policy's `unclassified` set requires names it among the classes
// requiring it. A class the policy itself names `unclassified` folds into
// that one name. That only widens what an intervening change reads as
// touching, so it can stop an observation transferring and never let one
// through.
type derived struct {
	paths    int
	classes  map[string]bool
	requires map[string][]string // check name -> the classes requiring it
	carry    string              // how far an approval carries across a rebase
}

// derive derives the checks a diff requires under the request's model, in
// a checkout of the target tip. The checkout supplies the commits the diff
// names; the policy comes from outside it.
func (p policySource) derive(tip *worktree, from, to string) (derived, error) {
	fail := func(err error) (derived, error) {
		return derived{}, fmt.Errorf("deriving required checks for %s..%s: %w", verdict.Short(from), verdict.Short(to), err)
	}
	diff := p.diff
	if diff == nil {
		diff = derive.Diff
	}
	paths, err := diff(context.Background(), tip.dir, nil, from, to)
	if err != nil {
		return fail(err)
	}
	r, err := derive.Derive(p.model, paths)
	if err != nil {
		return fail(err)
	}
	d := derived{paths: len(r.Paths), classes: map[string]bool{}, requires: map[string][]string{}, carry: r.Carry}
	for _, c := range r.Classes {
		d.classes[c] = true
	}
	if len(r.Unclassified) > 0 {
		d.classes["unclassified"] = true
	}
	for _, q := range r.Required {
		by := append([]string{}, q.Classes...)
		if q.Unclassified {
			by = append(by, "unclassified")
		}
		d.requires[q.Name] = by
	}
	// A diff misread as empty requires nothing, so no changed paths are
	// believed only when git agrees that the two trees are the same.
	if d.paths == 0 {
		if _, err := git(tip.dir, "diff-tree", "--quiet", from, to); err != nil {
			return derived{}, fmt.Errorf("the deriver read no changed paths in %s..%s, and git does not agree: %w", verdict.Short(from), verdict.Short(to), err)
		}
	}
	return d, nil
}

// required turns a derivation into the check set the verdict is computed
// over, with each check as the model defines it: a pure check's attribute
// is what the integrator evaluates a closure over, and an effectful
// check's window is Δ.
func (p policySource) required(d derived) ([]verdict.Required, error) {
	var out []verdict.Required
	for name, classes := range d.requires {
		entry, ok := p.model.Checks[name]
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

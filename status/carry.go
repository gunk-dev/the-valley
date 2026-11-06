// valley-go carry: whether review's answer about the version of a change
// it showed carries to a later version the change gained while the
// review was open (bin/valley's moved). It is information only: it files
// no request, and the integrator judges what approve files as it judges
// any request.
//
// The later version is set against the version shown as compare sets a
// version against its approved one (compare.go), with main's tip on
// origin. The answer carries when the two have the same file delta, the
// same commit messages and authors, and the change's carry mode allows
// the landings between their bases: exact allows none, same-class none
// that touched a path in a class the change touches, and same-paths any.
// The mode is derived as the integrator derives it, under the policy at
// main's tip (derive.Derive).
//
// The output is one line, the verdict, carries or review, and a short
// reason, separated by a tab, and then the comparison's text, which
// review pages when it asks the operator to look again.

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"the-valley/change"
	"the-valley/derive"
)

const carryUsage = "usage: valley-go carry <change ID> <version shown> <later version>"

func runCarry(g git, args []string, w io.Writer) error {
	var n [2]int
	if len(args) != 3 || !change.ValidID(args[0]) {
		return failure(carryUsage)
	}
	for i, s := range args[1:] {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 || strconv.Itoa(v) != s {
			return failure(carryUsage)
		}
		n[i] = v
	}
	from, to := n[0], n[1]
	if from >= to {
		return failure(carryUsage)
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	h, err := readHistory(g, args[0])
	if err != nil {
		return err
	}
	if to > len(h.versions) {
		return failure(fmt.Sprintf("change %s has no v%d", h.handle, to))
	}
	if h.tip == "" {
		return failure("origin has no main to compare the versions against")
	}
	if err := fetch(g, h.tip, h.head(from), h.head(to)); err != nil {
		return err
	}
	c, err := compareWith(g, h, h.tip, reference{version: from}, to)
	if err != nil {
		return err
	}
	verdict, reason, err := carryVerdict(g, h.tip, c, from)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\t%s\n%s", verdict, reason, c.text)
	return err
}

// carryVerdict says whether an answer about version from carries to the
// version c compared with it, and why.
func carryVerdict(g git, tip string, c comparison, from int) (verdict, reason string, err error) {
	switch {
	case c.kind == compareNone:
		return "review", fmt.Sprintf("it could not be set against v%d", from), nil
	case c.kind == compareChanged:
		return "review", fmt.Sprintf("its changes differ from v%d's", from), nil
	case c.meta:
		return "review", fmt.Sprintf("its commit messages or authors differ from v%d's", from), nil
	case len(c.landings) == 1:
		return "carries", fmt.Sprintf("same changes as v%d", from), nil
	}
	mode, shared, err := carryMode(g, tip, c)
	if err != nil {
		return "", "", err
	}
	if !carryAllows(mode, shared) {
		return "review", fmt.Sprintf("a restack of v%d, and carry mode %s holds it over what landed since", from, mode), nil
	}
	return "carries", fmt.Sprintf("a restack of v%d; same changes", from), nil
}

// carryMode is the change's carry mode under the policy at main's tip,
// and whether a landing between the two bases touched a path in a class
// the change touches.
func carryMode(g git, tip string, c comparison) (mode string, shared bool, err error) {
	instance, project, done, err := policyLayers(g, tip)
	defer done()
	if err != nil {
		return "", false, failure(fmt.Sprintf("the policy at main did not read: %v", err))
	}
	schema, err := schemaFile(g, "")
	if err != nil {
		return "", false, err
	}
	model, err := policyModel(schema, instance, project, true)
	if err != nil {
		return "", false, err
	}
	ctx := context.Background()
	paths, err := derive.Diff(ctx, g.dir, g.env, c.base, c.head)
	if err != nil {
		return "", false, failure(err.Error())
	}
	own, err := derive.Derive(model, paths)
	if err != nil {
		return "", false, failure(err.Error())
	}
	if own.Carry != "same-class" {
		return own.Carry, false, nil
	}
	var landed []string
	for i := 0; i+1 < len(c.landings); i++ {
		p, err := derive.Diff(ctx, g.dir, g.env, c.landings[i+1], c.landings[i])
		if err != nil {
			return "", false, failure(err.Error())
		}
		landed = append(landed, p...)
	}
	theirs, err := derive.Derive(model, landed)
	if err != nil {
		return "", false, failure(err.Error())
	}
	classes := map[string]bool{}
	for _, class := range own.Classes {
		classes[class] = true
	}
	for _, class := range theirs.Classes {
		if classes[class] {
			return own.Carry, true, nil
		}
	}
	return own.Carry, false, nil
}

// carryAllows says whether a change's carry mode lets an answer carry
// over at least one landing, given whether a landing touched one of the
// change's classes.
func carryAllows(mode string, shared bool) bool {
	switch mode {
	case "same-paths":
		return true
	case "same-class":
		return !shared
	}
	return false
}

// carry: whether review's answer about the version of a change
// it showed carries to a later version the change gained while the
// review was open (review.go's moved). It is information only: it files
// no request, and the integrator judges what approve files as it judges
// any request. valley pending reads a request at an earlier version's
// head through the same rules (carriedApproval, pending.go).
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
// review pages when it asks the operator to look again. A reason is one
// plain clause about the later version: for carries, why the answer
// covers it. A review line has a third field, when review offers to
// approve the later version in place: approve, at once, since the
// change's commits are unchanged; view, once the operator has paged the
// comparison, since a commit changed; or none, when the versions could
// not be set side by side.

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"the-valley/change"
	"the-valley/composed"
	"the-valley/derive"
)

// carryUsage is what runCarry, which review calls, takes.
const carryUsage = "carry takes <change ID> <version shown> <later version>"

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
	c, verdict, reason, offer, err := carryBetween(g, h, from, to, func() (*composed.Model, error) { return tipModel(g, h.tip) })
	if err != nil {
		return err
	}
	if offer != "" {
		reason += "\t" + offer
	}
	_, err = fmt.Fprintf(w, "%s\t%s\n%s", verdict, reason, c.text)
	return err
}

// carryBetween sets version to of the change h holds against version
// from, at main's tip as h read it, and says whether an answer about from
// carries to it, and why (carryVerdict). model is the policy at that tip,
// read only when the change's carry mode decides.
func carryBetween(g git, h *history, from, to int, model func() (*composed.Model, error)) (c comparison, verdict, reason, offer string, err error) {
	if to > len(h.versions) {
		return c, "", "", "", failure(fmt.Sprintf("change %s has no v%d", h.handle, to))
	}
	if h.tip == "" {
		return c, "", "", "", failure("origin has no main to compare the versions against")
	}
	if err := fetch(g, h.tip, h.head(from), h.head(to)); err != nil {
		return c, "", "", "", err
	}
	if c, err = compareWith(g, h, h.tip, reference{version: from}, to, false); err != nil {
		return c, "", "", "", err
	}
	verdict, reason, offer, err = carryVerdict(g, c, from, to, model)
	return c, verdict, reason, offer, err
}

// tipModel is the policy at main's tip, as carry reads it on its own. A
// carry verdict reads the classes and carry mode alone, so it asks cue no
// requirement's pin.
func tipModel(g git, tip string) (*composed.Model, error) {
	instance, project, done, err := policyLayers(g, tip)
	defer done()
	if err != nil {
		return nil, failure(fmt.Sprintf("the policy at main did not read: %v", err))
	}
	schema, err := schemaFile(g, "")
	if err != nil {
		return nil, err
	}
	return policyModel(schema, instance, project, false)
}

// carryVerdict says whether an answer about version from carries to
// version to, which c compared with it, and why, and for review, when the
// later version is offered for approval in place.
func carryVerdict(g git, c comparison, from, to int, model func() (*composed.Model, error)) (verdict, reason, offer string, err error) {
	switch {
	case c.kind == compareNone:
		return "review", fmt.Sprintf("it could not be set against v%d", from), "none", nil
	case c.kind == compareChanged && c.verb == "rebased":
		return "review", rebasedReason(c, from), "approve", nil
	case c.kind == compareChanged:
		return "review", editedReason(c, from), "view", nil
	case c.meta:
		return "review", "its commit messages or authors differ", "view", nil
	case len(c.landings) == 1:
		return "carries", fmt.Sprintf("v%d makes the same changes as v%d on the same main", to, from), "", nil
	}
	mode, shared, err := carryMode(g, c, model)
	if err != nil {
		return "", "", "", err
	}
	if !carryAllows(mode, shared) {
		return "review", fmt.Sprintf("a restack; carry mode %s holds it over what landed since", mode), "approve", nil
	}
	return "carries", fmt.Sprintf("v%d is v%d rebased onto the new main with no other changes", to, from), "", nil
}

// rebasedReason says that a rebase changed the change's files and not its
// commits, and names the files main also edited, the first three. A path
// is quoted when it holds what is not printable, so that it can neither
// split review's tab-separated line nor drive the terminal.
func rebasedReason(c comparison, from int) string {
	commits := "its commit is"
	if c.has != 1 {
		commits = fmt.Sprintf("its %d commits are", c.has)
	}
	files := c.mainEdited
	if len(files) == 0 {
		return fmt.Sprintf("rebased onto main; %s unchanged, but its file changes differ from v%d's", commits, from)
	}
	quoted := make([]string, min(len(files), 3))
	for i := range quoted {
		quoted[i] = quotedPath(files[i])
	}
	named := strings.Join(quoted, ", ")
	if len(files) > 3 {
		named += ", …"
	}
	return fmt.Sprintf("rebased onto main; %s unchanged, but main also edited %d of its files (%s)", commits, len(files), named)
}

// editedReason says how many of the change's commits changed.
func editedReason(c comparison, from int) string {
	switch {
	case c.had != c.has:
		return fmt.Sprintf("its commits changed (%s, where v%d had %d)", counted(c.has, "commit"), from, c.had)
	case c.has == 1:
		return "its commit changed"
	}
	return fmt.Sprintf("its commits changed (%d of %d)", c.edited, c.has)
}

// carryMode is the change's carry mode under the policy at main's tip,
// which model reads, and whether a landing between the two bases touched
// a path in a class the change touches.
func carryMode(g git, c comparison, policy func() (*composed.Model, error)) (mode string, shared bool, err error) {
	model, err := policy()
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

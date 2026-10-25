package main

import (
	"strings"

	"the-valley/knot"
)

// mk is a knot of type t titled title, as `valley knot new` would write
// it, with each option applied after.
func mk(t, title string, options ...func(*knot.Knot)) *knot.Knot {
	ty, _ := knot.TypeNamed(t)
	slug := knot.Slug(title)
	k := &knot.Knot{Type: t, ID: knot.DeriveID(ty, slug), Slug: slug, Status: ty.Initial(),
		Title: title, Created: "2026-10-15", CreatedBy: "operator"}
	if t == "outcome" || t == "bug" {
		k.BlockedBy = []string{}
	}
	for _, o := range options {
		o(k)
	}
	return k
}

// files writes knots, each at its path.
func files(knots ...*knot.Knot) map[string]string {
	out := map[string]string{}
	for _, k := range knots {
		out[k.Path()] = string(knot.Render(k))
	}
	return out
}

// with is fs and a comment by author on the knot with id.
func with(fs map[string]string, id, author, body string) map[string]string {
	fs[knot.CommentsDirectory+"/"+id+"/20261015T005800Z-0123456789abcdef.md"] = string(knot.RenderComment(&knot.Comment{Author: author, Body: body}))
	return fs
}

// Fixtures are the shared fixtures: graphs both validators accept, each
// holding the shapes the live graph uses, and one graph for each way of
// breaking a shared rule.
func Fixtures() []Case {
	milestone := mk("outcome", "Slice one lands")
	question := mk("question", "Where does the graph live", func(k *knot.Knot) {
		k.Status, k.Owner = "answered", "operator"
		k.Changes = []string{"valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr"}
	})
	bug := mk("bug", "The hook reads too much", func(k *knot.Knot) { k.Parent = milestone.ID })
	idea := mk("idea", "Knots as cords", func(k *knot.Knot) {
		k.Status, k.GraduatedInto, k.Parent = "graduated", question.ID, milestone.ID
	})
	work := mk("outcome", "Write the hook case", func(k *knot.Knot) {
		k.Status, k.Parent, k.Owner = "in-progress", milestone.ID, "alice"
		k.BlockedBy = []string{bug.ID, "app:oc-0123456"}
		k.ClaimedBy, k.ClaimedAt = "20261015-0058-feb0a926", "2026-10-15T00:58:00Z"
		k.Changes = []string{"valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr", "https://github.com/example/site/pull/12"}
		k.Target, k.Source = "site", "2026-10-15, the plan-graph design"
		k.Body = "Some \"quoted\" text, and a line with <angle> brackets & more.\n\n- a list\n"
	})
	// A milestone waiting on its own work item is no cycle.
	waiting := mk("outcome", "Slice one ships", func(k *knot.Knot) { k.BlockedBy = []string{work.ID} })
	work2 := mk("outcome", "Write the hook case")
	work2.Slug = "write-the-hook-case-2"
	work2.ID = knot.DeriveID(work2.TypeOf(), work2.Slug)

	valid := []Case{
		{Name: "every-shape", Accept: true, Files: with(files(milestone, question, bug, idea, work, waiting, work2), work.ID, "alice", "Claimed for the run.\n")},
		{Name: "one-knot", Accept: true, Files: files(mk("bug", "A lone bug"))},
		// A graduated idea moves to work taken on, or to a question to
		// settle.
		{Name: "graduated-into-an-outcome", Accept: true, Files: files(milestone, mk("idea", "Slices", func(k *knot.Knot) {
			k.Status, k.GraduatedInto = "graduated", milestone.ID
		}))},
		{Name: "graduated-into-a-question", Accept: true, Files: files(question, mk("idea", "One ref", func(k *knot.Knot) {
			k.Status, k.GraduatedInto = "graduated", question.ID
		}))},
	}
	for _, t := range knot.Types {
		for _, s := range t.Statuses() {
			k := mk(t.Name, "A knot that is "+s, func(k *knot.Knot) { k.Status = s })
			fs := files(k)
			if s == "graduated" {
				k.GraduatedInto = question.ID
				fs = files(k, question)
			}
			valid = append(valid, Case{Name: "status-" + t.Name + "-" + s, Accept: true, Files: fs})
		}
	}

	// Each rejected graph breaks one shared rule and nothing else.
	reject := func(name string, rule knot.Rule, fs map[string]string) Case {
		return Case{Name: name, Rule: rule, Files: fs}
	}
	edit := func(k *knot.Knot, f func(string) string) map[string]string {
		return map[string]string{k.Path(): f(string(knot.Render(k)))}
	}
	set := func(k *knot.Knot, f func(*knot.Knot)) *knot.Knot {
		c := k.Clone()
		f(c)
		return c
	}
	a, b := mk("outcome", "Alpha"), mk("outcome", "Beta")
	moved := mk("bug", "A bug filed as an outcome")
	misfiled := map[string]string{"outcomes/" + moved.ID + "-" + moved.Slug + ".md": string(knot.Render(moved))}
	twin := files(a)
	twin["outcomes/"+a.ID+"-alpha-twin.md"] = string(knot.Render(a))
	asked := mk("question", "Asked")
	other := mk("idea", "Another idea")
	cases := []Case{
		reject("unknown-type", knot.RuleType, map[string]string{a.Path(): string(knot.Render(set(a, func(k *knot.Knot) { k.Type = "task" })))}),
		reject("type-decision", knot.RuleType, map[string]string{asked.Path(): string(knot.Render(set(asked, func(k *knot.Knot) { k.Type = "decision" })))}),
		reject("unknown-status", knot.RuleStatus, files(set(a, func(k *knot.Knot) { k.Status = "blocked" }))),
		reject("status-of-another-type", knot.RuleStatus, files(set(mk("bug", "Bug"), func(k *knot.Knot) { k.Status = "done" }))),
		reject("id-not-hex", knot.RuleIDShape, edit(a, func(s string) string { return strings.Replace(s, "id: "+a.ID, "id: oc-xyz", 1) })),
		reject("id-of-another-type", knot.RuleIDShape, edit(a, func(s string) string { return strings.Replace(s, "id: oc-", "id: bd-", 1) })),
		reject("id-prefix-dcr", knot.RuleIDShape, edit(asked, func(s string) string { return strings.Replace(s, "id: qn-", "id: dcr-", 1) })),
		reject("id-not-derived", knot.RuleIDDerived, files(set(a, func(k *knot.Knot) { k.ID = "oc-0000000" }))),
		reject("filename-disagrees", knot.RuleFilename, map[string]string{a.Path(): string(knot.Render(set(a, func(k *knot.Knot) { k.ID = b.ID })))}),
		reject("knot-in-another-directory", knot.RuleDirectory, misfiled),
		reject("duplicate-id", knot.RuleUniqueID, twin),
		reject("unknown-field", knot.RuleFieldOnType, edit(a, func(s string) string {
			return strings.Replace(s, "blocked_by: []\n", "blocked_by: []\npriority: \"high\"\n", 1)
		})),
		reject("blocked-by-on-an-idea", knot.RuleFieldOnType, edit(mk("idea", "Idea"), func(s string) string {
			return strings.Replace(s, "created_by: \"operator\"\n", "created_by: \"operator\"\nblocked_by: []\n", 1)
		})),
		reject("changes-on-an-idea", knot.RuleFieldOnType, files(set(mk("idea", "Idea"), func(k *knot.Knot) {
			k.Changes = []string{"valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr"}
		}))),
		reject("claim-on-a-question", knot.RuleFieldOnType, edit(asked, func(s string) string {
			return strings.Replace(s, "created_by: \"operator\"\n", "created_by: \"operator\"\nclaimed_by: \"run\"\nclaimed_at: \"2026-10-15T00:58:00Z\"\n", 1)
		})),
		reject("blocked-by-on-a-question", knot.RuleFieldOnType, edit(asked, func(s string) string {
			return strings.Replace(s, "created_by: \"operator\"\n", "created_by: \"operator\"\nblocked_by: []\n", 1)
		})),
		reject("creator-not-a-name", knot.RuleFieldShape, files(set(a, func(k *knot.Knot) { k.CreatedBy = "the operator" }))),
		reject("owner-not-a-name", knot.RuleFieldShape, files(set(a, func(k *knot.Knot) { k.Owner = "-dash" }))),
		reject("blocker-not-a-reference", knot.RuleFieldShape, files(set(a, func(k *knot.Knot) { k.BlockedBy = []string{"app/oc-0123456"} }))),
		reject("change-not-a-link", knot.RuleFieldShape, files(set(a, func(k *knot.Knot) { k.Changes = []string{"https://example.com/pull/1"} }))),
		reject("claim-time-not-a-time", knot.RuleFieldShape, files(set(a, func(k *knot.Knot) { k.ClaimedBy, k.ClaimedAt = "run", "yesterday" }))),
		reject("created-not-a-date", knot.RuleFieldShape, files(set(a, func(k *knot.Knot) { k.Created = "2026-10-15T00:00" }))),
		reject("target-not-a-project", knot.RuleFieldShape, files(set(a, func(k *knot.Knot) { k.Target = "two words" }))),
		reject("outcome-without-blocked-by", knot.RuleRequired, edit(a, func(s string) string { return strings.Replace(s, "blocked_by: []\n", "", 1) })),
		reject("empty-title", knot.RuleRequired, files(set(a, func(k *knot.Knot) { k.Title = "" }))),
		reject("no-status", knot.RuleRequired, edit(a, func(s string) string { return strings.Replace(s, "status: open\n", "", 1) })),
		reject("parent-not-in-the-graph", knot.RuleEdgeResolves, files(set(a, func(k *knot.Knot) { k.Parent = b.ID }))),
		reject("blocker-not-in-the-graph", knot.RuleEdgeResolves, files(set(a, func(k *knot.Knot) { k.BlockedBy = []string{b.ID} }))),
		reject("graduated-into-no-knot", knot.RuleEdgeResolves, files(set(idea, func(k *knot.Knot) { k.Parent = "" }))),
		reject("graduated-into-a-bug", knot.RuleFieldShape, files(set(idea, func(k *knot.Knot) { k.Parent, k.GraduatedInto = "", bug.ID }), set(bug, func(k *knot.Knot) { k.Parent = "" }))),
		reject("graduated-into-an-idea", knot.RuleFieldShape, files(set(idea, func(k *knot.Knot) { k.Parent, k.GraduatedInto = "", other.ID }), other)),
		reject("parent-a-bug", knot.RuleParentOutcome, files(set(a, func(k *knot.Knot) { k.Parent = bug.ID }), set(bug, func(k *knot.Knot) { k.Parent = "" }))),
		reject("graduated-without-destination", knot.RuleGraduated, files(mk("idea", "Gone", func(k *knot.Knot) { k.Status = "graduated" }))),
		reject("destination-while-raw", knot.RuleGraduated, files(mk("idea", "Raw", func(k *knot.Knot) { k.GraduatedInto = question.ID }), question)),
	}
	return append(valid, cases...)
}

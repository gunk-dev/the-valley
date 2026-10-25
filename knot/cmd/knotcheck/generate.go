package main

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"

	"the-valley/knot"
)

var (
	words      = []string{"plan", "graph", "hook", "ref", "knot", "cord", "lease", "claim", "slice", "seed", "view", "push", "tree", "walk", "bound"}
	principals = []string{"operator", "alice", "integrator", "a.b_c"}
)

// Generate is n graphs from seed: each valid graph the generator makes,
// and, after it, the same graph broken by one shared rule. A broken graph
// is kept only when the Go validator refuses it under a shared rule, so
// every case is one the CUE schema and the lint must refuse too.
func Generate(n int, seed uint64) []Case {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	var cases []Case
	for i := 0; len(cases) < n; i++ {
		knots := graph(r)
		name := fmt.Sprintf("generated-%03d", i)
		valid := Case{Name: name, Accept: true, Files: withComments(r, knots)}
		if err := validate(asFiles(valid.Files)); err != nil {
			panic(fmt.Sprintf("generated graph %s is invalid: %v", name, err))
		}
		cases = append(cases, valid)
		if len(cases) == n {
			break
		}
		broken, rule := mutate(r, knots)
		err := validate(asFiles(broken))
		e, ok := err.(*knot.Error)
		if err == nil || !ok {
			continue
		}
		if !slices.Contains(knot.SharedRules, e.Rule) {
			continue
		}
		cases = append(cases, Case{Name: name + "-" + string(rule), Rule: e.Rule, Files: broken})
	}
	return cases
}

func asFiles(fs map[string]string) map[string]file {
	out := map[string]file{}
	for p, s := range fs {
		out[p] = file{mode: "100644", data: []byte(s)}
	}
	return out
}

// graph is a random valid graph of 2 to 24 knots. Every edge points at a
// knot made earlier, so neither parent nor blocked_by edges form a cycle.
func graph(r *rand.Rand) []*knot.Knot {
	var knots []*knot.Knot
	taken := map[string]bool{}
	pick := func(list []string) string { return list[r.IntN(len(list))] }
	for range 2 + r.IntN(23) {
		t := knot.Types[r.IntN(len(knot.Types))]
		var title []string
		for range 1 + r.IntN(4) {
			title = append(title, pick(words))
		}
		title[0] = strings.ToUpper(title[0][:1]) + title[0][1:]
		id, slug := knot.NewID(t, strings.Join(title, " "), func(id string) bool { return taken[id] })
		taken[id] = true
		k := &knot.Knot{Type: t.Name, ID: id, Slug: slug, Status: pick(t.Statuses()), Title: strings.Join(title, " "),
			Created: fmt.Sprintf("2026-%02d-%02d", 9+r.IntN(3), 1+r.IntN(28)), CreatedBy: pick(principals)}
		var outcomes, destinations, earlier []string
		for _, e := range knots {
			earlier = append(earlier, e.ID)
			switch e.Type {
			case "outcome":
				outcomes = append(outcomes, e.ID)
				destinations = append(destinations, e.ID)
			case "question":
				destinations = append(destinations, e.ID)
			}
		}
		if k.Status == "graduated" {
			if len(destinations) == 0 {
				k.Status = "raw"
			} else {
				k.GraduatedInto = pick(destinations)
			}
		}
		if len(outcomes) > 0 && r.IntN(2) == 0 {
			k.Parent = pick(outcomes)
		}
		if r.IntN(3) == 0 {
			k.Owner = pick(principals)
		}
		if r.IntN(4) == 0 {
			k.Source = "2026-10-15, a generated graph"
		}
		if t.Name == "outcome" || t.Name == "bug" {
			k.BlockedBy = []string{}
			for range r.IntN(3) {
				if len(earlier) > 0 && !slices.Contains(k.BlockedBy, earlier[len(earlier)-1]) {
					b := pick(earlier)
					if !slices.Contains(k.BlockedBy, b) {
						k.BlockedBy = append(k.BlockedBy, b)
					}
				}
			}
			if r.IntN(5) == 0 {
				k.BlockedBy = append(k.BlockedBy, "app:oc-0123456")
			}
			slices.Sort(k.BlockedBy)
			if r.IntN(4) == 0 {
				k.ClaimedBy, k.ClaimedAt = "run-"+fmt.Sprint(r.IntN(100)), "2026-10-15T01:02:03Z"
			}
			if r.IntN(4) == 0 {
				k.Changes = []string{"valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr"}
			}
			if r.IntN(6) == 0 {
				k.Target = pick([]string{"site", "app", "app:bd-0123456"})
			}
		}
		// An answered question links the change that records its answer.
		// This draws nothing, so the graphs a seed makes stay the same.
		if k.Status == "answered" {
			k.Changes = []string{"https://github.com/example/site/pull/12"}
		}
		if r.IntN(2) == 0 {
			k.Body = "A body for " + k.Title + ".\n"
		}
		knots = append(knots, k)
	}
	return knots
}

func withComments(r *rand.Rand, knots []*knot.Knot) map[string]string {
	fs := files(knots...)
	for i := range r.IntN(4) {
		k := knots[r.IntN(len(knots))]
		p := fmt.Sprintf("%s/%s/20261015T0%d0000Z-%016x.md", knot.CommentsDirectory, k.ID, i, r.Uint64())
		fs[p] = string(knot.RenderComment(&knot.Comment{Author: principals[r.IntN(len(principals))], Body: "A comment.\n"}))
	}
	return fs
}

// mutate is the graph broken by one shared rule, picked at random, and the
// rule it means to break.
func mutate(r *rand.Rand, knots []*knot.Knot) (map[string]string, knot.Rule) {
	knots = slices.Clone(knots)
	i := r.IntN(len(knots))
	k := knots[i].Clone()
	knots[i] = k
	t := k.TypeOf()
	at := slices.IndexFunc(knot.Types, func(x knot.Type) bool { return x.Name == t.Name })
	other := knot.Types[(at+1+r.IntN(3))%4]
	rules := []knot.Rule{knot.RuleType, knot.RuleStatus, knot.RuleIDShape, knot.RuleIDDerived, knot.RuleDirectory,
		knot.RuleFieldOnType, knot.RuleFieldShape, knot.RuleRequired, knot.RuleEdgeResolves, knot.RuleParentOutcome, knot.RuleGraduated}
	rule := rules[r.IntN(len(rules))]
	switch rule {
	case knot.RuleType:
		k.Type = "task"
		fs := files(knots...)
		delete(fs, knots[i].Path())
		fs[t.Directory+"/"+k.ID+"-"+k.Slug+".md"] = string(knot.Render(k))
		return fs, rule
	case knot.RuleStatus:
		k.Status = other.Terminal[0]
		if slices.Contains(t.Statuses(), k.Status) {
			k.Status = "blocked"
		}
	case knot.RuleIDShape:
		fs := files(knots...)
		fs[k.Path()] = strings.Replace(fs[k.Path()], "id: "+k.ID, "id: "+other.Prefix+k.ID[len(t.Prefix):], 1)
		return fs, rule
	case knot.RuleIDDerived:
		k.ID = t.Prefix + "-0000000"
	case knot.RuleDirectory:
		fs := files(knots...)
		delete(fs, k.Path())
		fs[other.Directory+"/"+k.ID+"-"+k.Slug+".md"] = string(knot.Render(k))
		return fs, rule
	case knot.RuleFieldOnType:
		switch t.Name {
		case "outcome", "bug":
			k.GraduatedInto = "qn-0000000"
		case "idea":
			k.Changes = []string{"valley:the-valley/kkkkllllmmmmnnnnooooppppqqqqrrrr"}
		default:
			k.Target = "site"
		}
	case knot.RuleFieldShape:
		k.Owner = "an owner"
	case knot.RuleRequired:
		k.Title = ""
	case knot.RuleEdgeResolves:
		k.Parent = "oc-0000000"
	case knot.RuleParentOutcome:
		for _, o := range knots {
			if o.Type != "outcome" {
				k.Parent = o.ID
			}
		}
		if k.Parent == "" || strings.HasPrefix(k.Parent, "oc-") {
			k.Parent = "bd-0000000"
		}
	case knot.RuleGraduated:
		if t.Name == "idea" {
			k.Status, k.GraduatedInto = "graduated", ""
		} else {
			k.Title = ""
			rule = knot.RuleRequired
		}
	}
	return files(knots...), rule
}

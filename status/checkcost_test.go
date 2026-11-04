package main

import (
	"fmt"
	"strings"
	"testing"
)

// A build has grown only when it is slower than its baseline by more than
// both 20% and 30 s.
func TestGrew(t *testing.T) {
	for _, c := range []struct {
		build, baseline int64
		grown           bool
	}{
		{100_000, 60_000, true},           // +40 s, +67%
		{500_000, 400_000, true},          // +100 s, +25%
		{431_000, 363_000, false},         // +68 s, but +18.7%
		{80_000, 60_000, false},           // +20 s, +33%
		{1_230_000, 1_200_000 - 1, false}, // +30 s, +2.5%
		{400_000, 400_000, false},
		{10_000, 400_000, false}, // faster
	} {
		if got := grew(c.build, c.baseline); got != c.grown {
			t.Errorf("grew(%d, %d) = %v, want %v", c.build, c.baseline, got, c.grown)
		}
	}
}

func TestMedian(t *testing.T) {
	for _, c := range []struct {
		ms   []int64
		want int64
	}{
		{[]int64{5}, 5},
		{[]int64{9, 1, 5}, 5},
		{[]int64{4, 1, 3, 2}, 2},
		{[]int64{10, 20}, 15},
	} {
		if got := median(c.ms); got != c.want {
			t.Errorf("median(%v) = %d, want %d", c.ms, got, c.want)
		}
	}
}

// The baseline is the median of a check's newest 20 uncached, passing
// builds in the project, with every version of the change left out.
func TestAddCosts(t *testing.T) {
	me := strings.Repeat("k", 32)
	other := func(i int) string { return fmt.Sprintf("%s%02d", strings.Repeat("m", 30), i) }
	var builds []checkBuild // newest first
	add := func(b checkBuild) {
		if b.project == "" {
			b.project = "app"
		}
		if b.result == "" {
			b.result = checkPassed
		}
		builds = append(builds, b)
	}
	// This version's own builds, in one run.
	add(checkBuild{run: "r2", change: me, version: 2, tree: "t2", check: "slow", buildMS: 200_000, evalMS: 24_000})
	add(checkBuild{run: "r2", change: me, version: 2, tree: "t2", check: "fast", buildMS: 120, evalMS: 24_000, cached: true})
	add(checkBuild{run: "r2", change: me, version: 2, tree: "t2", check: "fresh", buildMS: 9_000, evalMS: 23_000})
	// The change's earlier version, never counted.
	add(checkBuild{run: "r1", change: me, version: 1, tree: "t1", check: "slow", buildMS: 1})
	// Other changes: a cached build, a failed one and another project's
	// are left out, then 25 builds of which the newest 20 count.
	add(checkBuild{change: other(98), check: "slow", buildMS: 2, cached: true})
	add(checkBuild{change: other(99), check: "slow", buildMS: 3, result: "failed"})
	add(checkBuild{project: "site", change: other(97), check: "slow", buildMS: 4})
	for i := 0; i < 25; i++ {
		ms := int64(100_000 + i*1_000) // the newest 20: 100 s to 119 s
		if i >= 20 {
			ms = 900_000
		}
		add(checkBuild{change: other(i), check: "slow", buildMS: ms})
		add(checkBuild{change: other(i), check: "fast", buildMS: 5_000})
	}
	out := &jsonVersionChecks{Tree: "t2", Required: []jsonCheckState{{Name: "slow"}, {Name: "fast"}, {Name: "fresh"}, {Name: "manual"}}}
	required := []jsonRequired{{Name: "slow", Runner: "nix"}, {Name: "fast", Runner: "nix"}, {Name: "fresh", Runner: "nix"}, {Name: "manual", Runner: "command"}}
	addCosts(out, required, "app", me, 2, builds)

	slow, fast, fresh, manual := out.Required[0], out.Required[1], out.Required[2], out.Required[3]
	if slow.BuildMS == nil || *slow.BuildMS != 200_000 || *slow.Cached || slow.BaselineMS == nil || *slow.BaselineMS != 109_500 || !slow.Grown || slow.New {
		t.Errorf("slow: %+v", slow)
	}
	if fast.BuildMS == nil || *fast.BuildMS != 120 || !*fast.Cached || *fast.BaselineMS != 5_000 || fast.Grown {
		t.Errorf("fast: %+v", fast)
	}
	if fresh.BuildMS == nil || *fresh.BuildMS != 9_000 || fresh.BaselineMS != nil || !fresh.New || fresh.Grown {
		t.Errorf("fresh: %+v", fresh)
	}
	if manual.BuildMS != nil || manual.New || manual.BaselineMS != nil {
		t.Errorf("manual: %+v", manual)
	}
	if out.BuildMS == nil || *out.BuildMS != 209_120 || *out.EvalMS != 24_000 {
		t.Errorf("totals: build %v, eval %v", out.BuildMS, out.EvalMS)
	}
	out.State = checkPassed
	for i := range out.Required {
		out.Required[i].State = checkPassed
	}
	want := "checks: 4 passed in 3m29s (eval 24s); slow 200s, +91s against its recent 110s; fresh 9s, new"
	if got := checksLine(out); got != want {
		t.Errorf("checksLine:\n got %s\nwant %s", got, want)
	}
}

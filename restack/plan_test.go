package main

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"the-valley/change"
	"the-valley/rebase"
)

var (
	tip  = strings.Repeat("a", 40)
	low  = strings.Repeat("b", 40)
	high = strings.Repeat("c", 40)
	req  = strings.Repeat("d", 40)
	ev   = "0123456789abcdef"
)

// graph is an ancestry over a few named commits: each maps to its
// ancestors, itself included. A commit it does not know is an error.
func graph(g map[string][]string) Ancestry {
	return func(a, b string) (bool, error) {
		anc, ok := g[b]
		if !ok {
			return false, errors.New("missing " + b)
		}
		for _, x := range anc {
			if x == a {
				return true, nil
			}
		}
		return false, nil
	}
}

func TestARequestIsRelatedByNameHeadOrRange(t *testing.T) {
	m := Members{Names: map[string]bool{"c-abc": true, "alice-topic": true}, Heads: []string{low, high}}
	g := graph(map[string][]string{
		tip: {tip},
		req: {req, high, low, tip}, // a commit on top of the family's top
	})
	for _, tc := range []struct {
		what string
		r    Request
		want string
	}{
		{"by c-<id>", Request{"c-abc", tip}, "name"},
		{"by an earlier version's branch", Request{"alice-topic", tip}, "name"},
		{"by an exact head", Request{"other", high}, "head"},
		{"by a range holding a layer head", Request{"other", req}, "range"},
		{"not at all", Request{"other", tip}, ""},
	} {
		by, err := Relate(tc.r, m, tip, g)
		if err != nil || strings.Join(by, ",") != tc.want {
			t.Errorf("%s: related by %v, %v; want %q", tc.what, by, err, tc.want)
		}
	}
}

func TestARangeOnMainDoesNotRelate(t *testing.T) {
	// A layer head the tip already reaches is not in the request's
	// incoming range.
	m := Members{Names: map[string]bool{}, Heads: []string{low}}
	g := graph(map[string][]string{tip: {tip, low}, req: {req, tip, low}})
	if by, err := Relate(Request{"other", req}, m, tip, g); by != nil || err != nil {
		t.Errorf("related by %v, %v; want unrelated", by, err)
	}
}

func TestAnUnreadableRequestCannotBeRelatedOrNot(t *testing.T) {
	m := Members{Names: map[string]bool{}, Heads: []string{low}}
	if _, err := Relate(Request{"other", req}, m, tip, graph(map[string][]string{tip: {tip}})); err == nil {
		t.Error("a request whose head cannot be read was judged unrelated")
	}
	// A name match needs no ancestry, so it answers even then.
	m.Names["c-abc"] = true
	if by, err := Relate(Request{"c-abc", req}, m, tip, graph(nil)); err != nil || len(by) != 1 {
		t.Errorf("related by %v, %v; want by name", by, err)
	}
}

func TestOnlyACurrentStaleOrRejectReleases(t *testing.T) {
	cur := func(d string) rebase.Outcome { return rebase.Outcome{Head: req, Tip: tip, Evidence: ev, Decision: d} }
	for _, tc := range []struct {
		what     string
		o        rebase.Outcome
		found    bool
		current  bool
		releases bool
	}{
		{"no outcome", rebase.Outcome{}, false, false, false},
		{"stale", cur("stale"), true, true, true},
		{"reject", cur("reject"), true, true, true},
		{"malformed", rebase.ParseOutcome("not an outcome"), true, false, false},
		{"another head", rebase.Outcome{Head: high, Tip: tip, Evidence: ev, Decision: "stale"}, true, false, false},
		{"another tip", rebase.Outcome{Head: req, Tip: low, Evidence: ev, Decision: "stale"}, true, false, false},
		{"other evidence", rebase.Outcome{Head: req, Tip: tip, Evidence: "fedcba9876543210", Decision: "stale"}, true, false, false},
		{"failed", rebase.Outcome{Head: req, Tip: tip, Evidence: ev, Decision: "failed", FailedAt: "2026-10-18T00:00:00Z"}, true, true, false},
		{"refused", cur("refused-not-ready"), true, true, false},
		{"refused merge", cur("refused-merge"), true, true, false},
		{"land with the request still filed", cur("land"), true, true, false},
	} {
		current, releases, why := Currency(tc.o, tc.found, req, tip, ev)
		if current != tc.current || releases != tc.releases || why == "" {
			t.Errorf("%s: current %v, releases %v (%q); want %v, %v", tc.what, current, releases, why, tc.current, tc.releases)
		}
	}
}

func record(versions, seq int, uploader string) *change.Record {
	r := &change.Record{Schema: change.Schema, ChangeID: strings.Repeat("k", 32), Target: "main", Owner: "alice", State: change.Ready, Seq: seq, Active: versions}
	for i := 1; i <= versions; i++ {
		r.Versions = append(r.Versions, change.Version{N: i, Head: change.HeadPrefix + low, Title: "t", Branch: "topic", Uploader: uploader})
	}
	return r
}

func TestCapacityCountsTheVersionTheRestackWouldAdd(t *testing.T) {
	// Two uploads by the principal and a cap of 2: the third is over.
	r := record(2, 2, "restacker")
	if c := CapacityOf(r, "restacker", 2, 100); c.Uploads != 3 || len(c.Over) != 1 {
		t.Errorf("uploads %d, over %v; want 3 and the cap", c.Uploads, c.Over)
	}
	// Another principal's uploads do not count against it.
	if c := CapacityOf(r, "other", 2, 100); c.Uploads != 1 || len(c.Over) != 0 {
		t.Errorf("uploads %d, over %v; want 1 and nothing", c.Uploads, c.Over)
	}
	at := func(c *Capacity, word string) bool {
		return len(c.Over) == 1 && strings.Contains(c.Over[0], word)
	}
	if c := CapacityOf(record(maxVersions-1, maxVersions-1, "alice"), "restacker", 64, 100); len(c.Over) != 0 {
		t.Errorf("%d versions is within the limit, over %v", maxVersions, c.Over)
	}
	if c := CapacityOf(record(maxVersions, maxVersions, "alice"), "restacker", 64, 100); !at(c, "versions, over") {
		t.Errorf("%d versions: over %v", maxVersions+1, c.Over)
	}
	if c := CapacityOf(record(1, maxSeq, "alice"), "restacker", 64, 100); !at(c, "update") {
		t.Errorf("update %d: over %v", maxSeq+1, c.Over)
	}
	if c := CapacityOf(record(1, 1, "alice"), "restacker", 64, maxBytes); len(c.Over) != 0 {
		t.Errorf("%d bytes is within the limit, over %v", maxBytes, c.Over)
	}
	if c := CapacityOf(record(1, 1, "alice"), "restacker", 64, maxBytes+1); !at(c, "bytes") {
		t.Errorf("%d bytes: over %v", maxBytes+1, c.Over)
	}
}

// The schema puts no upper bound on the cap, so a cap no int holds is no
// practical cap rather than a refusal. Only what the schema itself refuses
// is refused.
func TestACapTooLargeToCountIsNoPracticalCap(t *testing.T) {
	for _, tc := range []struct {
		stated json.Number
		want   int
	}{
		{"1", 1},
		{"64", 64},
		{"9223372036854775807", math.MaxInt},
		{"9223372036854775808", math.MaxInt},
		{"100000000000000000000000000000", math.MaxInt},
	} {
		if got, err := CapOf(tc.stated); err != nil || got != tc.want {
			t.Errorf("a cap of %s counts as %d, %v; want %d", tc.stated, got, err, tc.want)
		}
	}
	for _, stated := range []json.Number{"", "0", "-1", "-9223372036854775809", "1.5", "1e3"} {
		if got, err := CapOf(stated); err == nil {
			t.Errorf("a cap of %q counts as %d; want it refused", stated, got)
		}
	}
}

func TestTheNextRecordIsMeasuredWithTheNewVersion(t *testing.T) {
	r := record(1, 1, "alice")
	before, err := change.Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	after := nextSize("abc", r, "restacker")
	if after <= len(before) {
		t.Errorf("the next record is %d bytes, not more than the %d it extends", after, len(before))
	}
}

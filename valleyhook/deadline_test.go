package main

import (
	"fmt"
	"io"
	"path"
	"strings"
	"testing"
	"time"

	"the-valley/knot"
)

// git can stop taking requests, or stall partway through an answer, and a
// read blocked on it has to end when the deadline passes, not when git
// next writes. Each case is a stream that stalls at one point.
func TestAStalledReadEndsAtTheDeadline(t *testing.T) {
	const deadline = 100 * time.Millisecond
	id := strings.Repeat("ab", 20)
	header := id + " blob 10\n"
	for _, c := range []struct {
		what   string
		takes  bool   // whether git reads the requests at all
		answer string // what git answers before it stalls
	}{
		{"git takes no request", false, ""},
		{"git answers nothing", true, ""},
		{"git stalls in a header", true, id + " bl"},
		{"git stalls in the content", true, header + header + "01234"},
	} {
		requests, in := io.Pipe()
		out, answers := io.Pipe()
		if c.takes {
			go func() { _, _ = io.Copy(io.Discard, requests) }()
		}
		if c.answer != "" {
			go func() { _, _ = answers.Write([]byte(c.answer)) }()
		}
		b := graphBudget
		b.deadline = deadline
		ctx, cancel := b.context()
		s := newObjectStream(ctx, cancel, in, out, b)
		start := time.Now()
		_, err := s.read(id, "blob", 100)
		elapsed := time.Since(start)
		s.close()
		if want := "could not be read within the 100ms the hook spends reading the plan graph"; err == nil || err.Error() != want {
			t.Errorf("%s: error = %v, want %q", c.what, err, want)
		}
		if elapsed > deadline+time.Second {
			t.Errorf("%s: the read ended %s after it began", c.what, elapsed)
		}
	}
}

// Every read below begins in time, and the one that reads the knot ends
// after the deadline. Nothing else is wrong with the push, so it is the
// last look at the clock, after every rule has passed, that refuses it.
func TestAGraphCheckedPastTheDeadlineIsRefused(t *testing.T) {
	r := newRecords(t)
	ty, _ := knot.TypeNamed("idea")
	slug := knot.Slug("Checked late")
	k := &knot.Knot{Type: ty.Name, ID: knot.DeriveID(ty, slug), Slug: slug, Status: ty.Initial(),
		Title: "Checked late", Created: "2026-10-15", CreatedBy: "operator"}
	ideas := r.git(fmt.Sprintf("100644 blob %s\t%s\n", r.blob(string(knot.Render(k))), path.Base(k.Path())), "mktree")
	root := r.git(fmt.Sprintf("040000 tree %s\t%s\n", ideas, ty.Directory), "mktree")
	commit := r.git(fmt.Sprintf("tree %s\nauthor operator <p@localhost> 1700000000 +0000\ncommitter operator <p@localhost> 1700000000 +0000\n\n%s",
		root, knot.Message("knot new", "0123456789abcdef")), "hash-object", "-t", "commit", "-w", "--stdin")
	u := update{old: zeroID, new: commit, ref: knot.Ref}

	if _, why := (policy{repo: lateRepository{deadline: knot.HookDeadline}}).checkGraph("operator", u); why != "" {
		t.Fatalf("the push read in time: %s", why)
	}
	_, why := (policy{repo: lateRepository{deadline: 500 * time.Millisecond, wait: 600 * time.Millisecond}}).checkGraph("operator", u)
	if want := "the hook could not finish checking it within the 500ms it spends on the plan graph"; why != want {
		t.Errorf("the push read late: %q, want %q", why, want)
	}
}

// lateRepository reads the plan graph within the deadline given, and
// waits after each file it reads, as a read that began in time and ended
// late would.
type lateRepository struct {
	gitRepository
	deadline, wait time.Duration
}

func (l lateRepository) graph() (graphReader, error) {
	b := graphBudget
	b.deadline = l.deadline
	objects, err := openObjectsWithin(b)
	if err != nil {
		return nil, err
	}
	return lateGraph{streamGraph{objects}, l.wait}, nil
}

type lateGraph struct {
	streamGraph
	wait time.Duration
}

func (g lateGraph) blob(id string, max int) ([]byte, error) {
	data, err := g.streamGraph.blob(id, max)
	time.Sleep(g.wait)
	return data, err
}

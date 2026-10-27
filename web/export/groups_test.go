package export_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// stopInPhase is a context that counts the checks of Err made within a
// function whose name ends with phase, and is cancelled at the check
// numbered at. With at 0 it is never cancelled, and counts alone.
type stopInPhase struct {
	context.Context
	cancel context.CancelFunc
	phase  string
	at     int
	checks int
}

func (c *stopInPhase) Err() error {
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, c.phase) {
			c.checks++
			if c.checks == c.at {
				c.cancel()
			}
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

// stopsAtEveryCheck runs f once to count the checks it makes within
// phase, and then once cancelled at each of them. Each cancelled run
// returns context.Canceled, and makes at most one check after the one
// that saw the cancellation, the one that reports it. It returns how
// many checks a whole run makes.
func stopsAtEveryCheck(t *testing.T, phase string, f func(context.Context) error) int {
	t.Helper()
	count := &stopInPhase{Context: context.Background(), phase: phase}
	if err := f(count); err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= count.checks; at++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &stopInPhase{Context: base, cancel: cancel, phase: phase, at: at}
		err := f(ctx)
		cancel()
		if !errors.Is(err, context.Canceled) || ctx.checks > at+1 {
			t.Errorf("cancelled at check %d of %d in %s, it returned %v after %d checks", at, count.checks, phase, err, ctx.checks)
		}
	}
	return count.checks
}

// CheckNames stops when its context is cancelled, inside each loop: over
// the groups and their aliases, and over the projects and their web
// names, former names and aliases. It checks before each name it reads,
// so a long list of names keeps no cancelled reader at work.
func TestCheckNamesStopsWhenCancelled(t *testing.T) {
	var groups []export.Group
	var placed []export.Placed
	names := 0
	for g := range 3 {
		id := fmt.Sprintf("g%d", g)
		groups = append(groups, export.Group{ID: id, Name: id + "-name", Aliases: []string{id + "-a", id + "-b"}})
		names += 4
		for p := range 3 {
			project := fmt.Sprintf("%s-p%d", id, p)
			placed = append(placed, export.Placed{Project: project, Placement: export.Placement{
				Group: id, Name: fmt.Sprint("web", p), FormerNames: []string{fmt.Sprint("old", p), fmt.Sprint("older", p)},
				Aliases: []string{project + "-a", project + "-b"},
			}})
			names += 6
		}
	}
	placed = append(placed, export.Placed{Project: "z-unplaced"})
	names++
	checks := stopsAtEveryCheck(t, ".CheckNames", func(ctx context.Context) error {
		return export.CheckNames(ctx, groups, placed)
	})
	if checks < names {
		t.Errorf("CheckNames read %d names and checked its context %d times", names, checks)
	}
}

// Reading an export checks its groups and the places of its projects
// last, and stops when its context is cancelled anywhere in that check.
func TestValidatingGroupsStopsWhenCancelled(t *testing.T) {
	f := exporttest.New(t)
	e, err := export.Decode(f.ExportJSON)
	if err != nil {
		t.Fatal(err)
	}
	site := e.Projects[0]
	site.Name, site.Group, site.WebName, site.FormerNames, site.Aliases = "site", "web", "app", []string{"pages"}, []string{"site"}
	e.Projects = append(e.Projects, site)
	e.Groups = append(e.Groups, export.Group{ID: "web", Name: "docs", Aliases: []string{"d"}})
	checks := stopsAtEveryCheck(t, ".(*Export).checkGroups", e.ValidateContext)
	// Each group's identifier, name and alias, and each project's
	// identifier, web name, former name and alias.
	if names := 2*3 + 2*4; checks < names {
		t.Errorf("checking the export's groups read %d names, and checked the context %d times", names, checks)
	}
}

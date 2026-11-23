package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"the-valley/flow/internal/config"
	"the-valley/flow/internal/git"
	"the-valley/flow/internal/location"
	"the-valley/flow/internal/project"
	"the-valley/flow/internal/run"
	"the-valley/flow/internal/selfupdate"
	"the-valley/flow/internal/tmux"
)

var namingCmd = &cobra.Command{
	Use:   "naming",
	Short: "Which names flow gives new state, and where its state is",
}

var namingStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report the naming generation, where state is, and what blocks advancing",
	Long: `Reports the host's naming_generation and what this build gives new state
under it: legacy names under legacy, and bridge names under bridge and flow,
since this build does not build the flow generation's writers. Bridge names
put new sessions, the sync log, consult snapshots, scaffolded declarations,
and single-valued state that is in neither place under the flow names
(~/.valley/flow), and keep the legacy names (~/.klaus) for the rest.

It also reports where each kind of state is, under either names, and any
kind found in both; the live sessions, with the build and generation that
created each (legacy when a session records none); for each registered
project, whether its worktrees' commit hooks are the current ones;
self-update's current and previous builds; and what blocks each generation
past the one set, each with its reason. It changes nothing.

It sees only this machine's flow state: other machines, and timers or
helpers that run outside flow, are for the operator to check. A running
dashboard keeps the environment it started with, so it takes a changed
setting at its next restart.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		l, err := location.Host()
		if err != nil {
			return err
		}
		// The host config's generation: Load refuses one that is none.
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		l.Gen = cfg.Generation()
		r := buildNamingReport(cmd.Context(), namingInputs{
			layout: l,
			tmux:   run.TmuxDepsFor(cmd.Context(), tmux.NewExecClient()),
		})
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(r)
		}
		printNamingReport(cmd.OutOrStdout(), r, l.Home)
		return nil
	},
}

// namingSupportCmd is how a build says which generations it supports: the
// operator asks a rollback target with it, since naming status runs no
// program the self-update state names.
var namingSupportCmd = &cobra.Command{
	Use:    "_naming-support",
	Short:  "Print the naming generations this build operates and allocates, as JSON",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(thisBuildSupport())
	},
}

func init() {
	namingStatusCmd.Flags().Bool("json", false, "Print the report as JSON")
	namingCmd.AddCommand(namingStatusCmd)
	rootCmd.AddCommand(namingCmd)
	rootCmd.AddCommand(namingSupportCmd)
}

// namingSupport is which generations a build operates (finds, reads and
// writes state of) and, for each it can be set to, which generation's
// names it gives new state.
type namingSupport struct {
	Operates  []location.Generation                       `json:"operates"`
	Allocates map[location.Generation]location.Generation `json:"allocates"`
}

func thisBuildSupport() namingSupport {
	s := namingSupport{Operates: location.Operates(), Allocates: map[location.Generation]location.Generation{}}
	for _, g := range location.Generations {
		s.Allocates[g] = location.Allocates(g)
	}
	return s
}

type namingInputs struct {
	layout location.Layout
	tmux   run.TmuxDeps
}

type namingReport struct {
	Generation location.Generation   `json:"generation"`
	Allocates  location.Generation   `json:"allocates"` // the names this build gives new state under Generation
	Operates   []location.Generation `json:"operates"`
	Places     []namingPlace         `json:"places"`
	Sessions   []namingSession       `json:"live_sessions"`
	Projects   []namingProject       `json:"projects"`
	SelfUpdate namingSelfUpdate      `json:"self_update"`
	Next       location.Generation   `json:"next,omitempty"`
	Blockers   []namingBlocker       `json:"blockers"`
	Unseen     string                `json:"unseen"`
}

// namingPlace is where one kind of state is.
type namingPlace struct {
	Kind   string   `json:"kind"`
	Paths  []string `json:"paths,omitempty"` // where it is, or where it goes
	Exists bool     `json:"exists"`
	Error  string   `json:"error,omitempty"`
}

type namingSession struct {
	ID         string `json:"id"`
	Dir        string `json:"dir"`
	CreatedBy  string `json:"created_by,omitempty"` // empty: a build that recorded none
	Generation string `json:"generation"`
	Error      string `json:"error,omitempty"` // its runs could not be read, so it may be live
}

type namingProject struct {
	Name         string              `json:"name"`
	Path         string              `json:"path"`
	Declarations []string            `json:"declarations,omitempty"`
	Hooks        []git.WorktreeHooks `json:"hooks"`
	Error        string              `json:"error,omitempty"`
}

type namingSelfUpdate struct {
	State    string              `json:"state,omitempty"`
	Current  *selfupdate.Install `json:"current,omitempty"`
	Previous *selfupdate.Install `json:"previous,omitempty"`
	Error    string              `json:"error,omitempty"`
}

type namingBlocker struct {
	What   string              `json:"what"`
	Reason string              `json:"reason"`
	Blocks location.Generation `json:"blocks"` // the first generation it keeps the host from
}

// namingUnseen is what the report can't see, among it the environment of a
// running dashboard, which a process sets up as it starts
// (flowenv.NormalizeProcess).
const namingUnseen = "Not seen from here: other machines, and timers or helpers outside flow.\n" +
	"A running dashboard keeps the environment it started with until it restarts."

func buildNamingReport(ctx context.Context, in namingInputs) namingReport {
	l := in.layout
	gen, _ := l.Generation()
	r := namingReport{
		Generation: gen,
		Allocates:  location.Allocates(gen),
		Operates:   location.Operates(),
		Next:       gen.Next(),
		Sessions:   []namingSession{},
		Projects:   []namingProject{},
		Blockers:   []namingBlocker{},
		Unseen:     namingUnseen,
	}

	place := func(kind string, resolve func() (string, error)) {
		p := namingPlace{Kind: kind}
		path, err := resolve()
		var conflict *location.ConflictError
		switch {
		case errors.As(err, &conflict):
			p.Paths, p.Exists, p.Error = conflict.Paths, true, err.Error()
		case err != nil:
			p.Error = err.Error()
		default:
			_, statErr := os.Lstat(path)
			p.Paths, p.Exists = []string{path}, statErr == nil
		}
		r.Places = append(r.Places, p)
	}
	place("host config", l.HostConfig)
	place("project registry", l.Registry)
	place("shared memory", l.Memory)
	place("self-update state", l.SelfUpdateState)

	sessions, sessErr := l.Sessions()
	sp := namingPlace{Kind: "sessions"}
	for _, root := range l.SessionRoots() {
		if _, err := os.Stat(root); err == nil {
			sp.Paths, sp.Exists = append(sp.Paths, root), true
		}
	}
	if sessErr != nil {
		sp.Error = sessErr.Error()
	}
	if len(sp.Paths) == 0 {
		if root, err := l.NewSessionsRoot(); err == nil {
			sp.Paths = []string{root}
		}
	}
	r.Places = append(r.Places, sp)

	// State of the new rule is listed wherever it exists, else where it goes.
	newRule := func(kind string, existing []string, resolve func() (string, error)) {
		if len(existing) > 0 {
			r.Places = append(r.Places, namingPlace{Kind: kind, Paths: existing, Exists: true})
		} else {
			place(kind, resolve)
		}
	}
	newRule("sync log", l.SyncLogs(), l.SyncLog)
	newRule("consult snapshots", l.ConsultSnapshotDirs(), l.ConsultSnapshots)
	locks := namingPlace{Kind: "global review locks", Paths: l.ReviewLockDirs()}
	for _, d := range locks.Paths {
		if _, err := os.Stat(d); err == nil {
			locks.Exists = true
		}
	}
	r.Places = append(r.Places, locks)

	for _, s := range sessions {
		live, err := sessionLive(s.Dir, in.tmux)
		if err != nil {
			// A session whose runs can't be read may be live: say so.
			r.Sessions = append(r.Sessions, namingSession{ID: s.ID, Dir: s.Dir, Generation: string(location.Legacy), Error: err.Error()})
			continue
		}
		if !live {
			continue
		}
		ns := namingSession{ID: s.ID, Dir: s.Dir, Generation: string(location.Legacy)}
		if st, err := run.NewHomeDirStoreFromPath(s.Dir).Load(s.ID); err == nil {
			ns.CreatedBy = st.CreatedBy
			if st.NamingGeneration != "" {
				ns.Generation = st.NamingGeneration
			}
		}
		r.Sessions = append(r.Sessions, ns)
	}

	if regPath, err := l.Registry(); err == nil {
		reg, err := project.LoadFrom(regPath)
		if err != nil {
			for i := range r.Places {
				if r.Places[i].Kind == "project registry" {
					r.Places[i].Error = err.Error()
				}
			}
		} else {
			names := make([]string, 0, len(reg.Projects))
			paths := reg.List() // ~ expanded
			for name := range paths {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				r.Projects = append(r.Projects, namingProjectOf(ctx, name, paths[name]))
			}
		}
	}

	r.SelfUpdate = namingSelfUpdateOf(l)
	r.Blockers = namingBlockers(r)
	return r
}

// namingTarget is the generation a report's state checks are for: the next
// one, or the one set when this build doesn't build it; "" when neither.
func namingTarget(r namingReport) location.Generation {
	if r.Next != "" {
		return r.Next
	}
	if r.Allocates != r.Generation {
		return r.Generation
	}
	return ""
}

// unbuilt is the first generation from g on whose names this build doesn't
// give new state, or "".
func unbuilt(g location.Generation) location.Generation {
	for x := g; x != ""; x = x.Next() {
		if location.Allocates(x) != x {
			return x
		}
	}
	return ""
}

// sessionLive reports whether a session's coordinator, or any of its runs'
// agents, is still running in tmux.
func sessionLive(dir string, td run.TmuxDeps) (bool, error) {
	states, unparsed, unreadable, err := run.NewHomeDirStoreFromPath(dir).Audit()
	if err != nil {
		return false, err
	}
	if len(unparsed) > 0 {
		return false, fmt.Errorf("run records %s don't parse", strings.Join(unparsed, ", "))
	}
	if len(unreadable) > 0 {
		msgs := make([]string, len(unreadable))
		for i, e := range unreadable {
			msgs[i] = e.Error()
		}
		return false, fmt.Errorf("run records can't be read: %s", strings.Join(msgs, "; "))
	}
	for _, s := range states {
		if s.Type == "session" {
			for _, p := range []*string{s.CoordinatorPane, s.DashboardPane} {
				if p != nil && td.PaneExists(*p) && !td.PaneIsIdle(*p) {
					return true, nil
				}
			}
			continue
		}
		if s.IsAgentRunningWith(td) {
			return true, nil
		}
	}
	return false, nil
}

func namingProjectOf(ctx context.Context, name, path string) namingProject {
	p := namingProject{Name: name, Path: path, Hooks: []git.WorktreeHooks{}}
	for _, file := range location.ProjectFiles {
		if f, err := location.ProjectFile(path, file); err == nil {
			if _, err := os.Lstat(f); err == nil {
				p.Declarations = append(p.Declarations, f)
			}
		}
	}
	hooks, err := git.InstalledHooks(ctx, path)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	p.Hooks = hooks
	return p
}

func namingSelfUpdateOf(l location.Layout) namingSelfUpdate {
	var su namingSelfUpdate
	path, err := l.SelfUpdateState()
	if err != nil {
		su.Error = err.Error()
		return su
	}
	su.State = path
	st, err := selfupdate.LoadFile(path)
	if err != nil {
		su.Error = err.Error()
		return su
	}
	su.Current, su.Previous = st.Current, st.Previous
	return su
}

// namingBlockers is what keeps the host from each generation past what it
// has, each with its reason and the first generation it blocks: this
// build's own limit blocks the first generation it doesn't build, and the
// state found here blocks namingTarget.
func namingBlockers(r namingReport) []namingBlocker {
	b := []namingBlocker{}
	if g := unbuilt(r.Generation); g != "" {
		b = append(b, namingBlocker{
			What:   "this build",
			Reason: fmt.Sprintf("the %s generation's writers are not built", g),
			Blocks: g,
		})
	}
	next := namingTarget(r)
	if next == "" {
		return b
	}
	add := func(what, reason string) {
		b = append(b, namingBlocker{What: what, Reason: reason, Blocks: next})
	}
	for _, p := range r.Places {
		if p.Error != "" {
			add(p.Kind, p.Error)
		}
	}
	for _, s := range r.Sessions {
		if s.Error != "" {
			add("session "+s.ID, "its runs could not be read, so it may be live: "+s.Error)
			continue
		}
		if s.CreatedBy == "" {
			add("session "+s.ID, "it was created by a build that records no naming generation; let it finish")
		}
	}
	for _, p := range r.Projects {
		var stale int
		for _, h := range p.Hooks {
			if !h.Current {
				stale++
			}
		}
		if p.Error != "" {
			add("project "+p.Name, "its commit hooks could not be read: "+p.Error)
		} else if stale > 0 {
			add("project "+p.Name, fmt.Sprintf("%d of its %d worktrees with flow's hooks have older ones; a run or session resume there installs the current ones", stale, len(p.Hooks)))
		}
	}
	if su := r.SelfUpdate; su.Error != "" && !placeFailed(r, "self-update state") {
		add("self-update state", "the rollback target could not be read from it: "+su.Error)
	}
	// The report runs no program the state file names, so what the
	// rollback target supports is for the operator to ask it.
	if prev := r.SelfUpdate.Previous; prev != nil {
		add("self-update's previous build "+installLabel(prev),
			fmt.Sprintf("it is the rollback target, and must operate %s state: %s _naming-support lists what it operates",
				next, filepath.Join(prev.StorePath, "bin", "klaus")))
	}
	return b
}

// placeFailed reports whether r's place of kind already carries an error,
// which is a blocker of its own.
func placeFailed(r namingReport, kind string) bool {
	for _, p := range r.Places {
		if p.Kind == kind && p.Error != "" {
			return true
		}
	}
	return false
}

func installLabel(i *selfupdate.Install) string {
	if i.Commit != "" {
		return shortSHA(i.Commit)
	}
	return i.StorePath
}

// tildePath is p as the operator writes it: ~ for home when p is in it.
func tildePath(home, p string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + p[len(home):]
	}
	return p
}

func printNamingReport(w io.Writer, r namingReport, home string) {
	tilde := func(p string) string { return tildePath(home, p) }
	tildes := func(ps []string) string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = tilde(p)
		}
		return strings.Join(out, ", ")
	}

	head := fmt.Sprintf("naming generation %s · new state gets %s names", r.Generation, r.Allocates)
	target := namingTarget(r)
	blocking := map[location.Generation][]namingBlocker{}
	for _, b := range r.Blockers {
		blocking[b.Blocks] = append(blocking[b.Blocks], b)
	}
	switch n := len(blocking[target]); {
	case target == "":
		fmt.Fprintf(w, "✓ %s\n", head)
	case n == 0:
		fmt.Fprintf(w, "✓ %s · nothing here blocks %s\n", head, target)
	default:
		fmt.Fprintf(w, "◇ %s · %d %s %s\n", head, n, plural(n, "blocks", "block"), target)
	}

	fmt.Fprintln(w, "\nWhere state is")
	for _, p := range r.Places {
		line := tildes(p.Paths)
		if !p.Exists {
			line += " (none yet)"
		}
		if p.Error != "" && len(p.Paths) == 0 {
			line = p.Error
		}
		fmt.Fprintf(w, "  %-20s %s\n", p.Kind, line)
		if p.Error != "" && len(p.Paths) > 0 {
			fmt.Fprintf(w, "  %-20s ✗ %s\n", "", p.Error)
		}
	}

	fmt.Fprintln(w, "\nLive sessions")
	if len(r.Sessions) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, s := range r.Sessions {
		by := "a build that recorded none"
		if s.CreatedBy != "" {
			by = s.CreatedBy
		}
		if s.Error != "" {
			fmt.Fprintf(w, "  %s · ✗ %s\n", s.ID, s.Error)
			continue
		}
		fmt.Fprintf(w, "  %s · created by %s · %s names\n", s.ID, by, s.Generation)
	}

	fmt.Fprintln(w, "\nCommit hooks")
	if len(r.Projects) == 0 {
		fmt.Fprintln(w, "  no registered projects")
	}
	for _, p := range r.Projects {
		var stale int
		for _, h := range p.Hooks {
			if !h.Current {
				stale++
			}
		}
		switch {
		case p.Error != "":
			fmt.Fprintf(w, "  %s · ✗ %s\n", p.Name, p.Error)
		case len(p.Hooks) == 0:
			fmt.Fprintf(w, "  %s · no worktrees with flow's hooks\n", p.Name)
		case stale == 0:
			fmt.Fprintf(w, "  %s · ✓ %d %s current\n", p.Name, len(p.Hooks), plural(len(p.Hooks), "worktree", "worktrees"))
		default:
			fmt.Fprintf(w, "  %s · ◇ %d of %d worktrees older\n", p.Name, stale, len(p.Hooks))
		}
	}

	fmt.Fprintln(w, "\nSelf-update")
	switch su := r.SelfUpdate; {
	case su.Error != "":
		fmt.Fprintf(w, "  ✗ %s\n", su.Error)
	case su.Current == nil && su.Previous == nil:
		fmt.Fprintln(w, "  nothing installed by self-update")
	default:
		for _, row := range []struct {
			label string
			i     *selfupdate.Install
		}{{"current", su.Current}, {"previous", su.Previous}} {
			if row.i != nil {
				fmt.Fprintf(w, "  %-9s %s %s\n", row.label, installLabel(row.i), tilde(row.i.StorePath))
			}
		}
	}

	// A section for each generation from the one set on that something
	// blocks, and for the target even when nothing does.
	for g := r.Generation; g != ""; g = g.Next() {
		if bs := blocking[g]; len(bs) > 0 || g == target {
			if g == r.Generation {
				fmt.Fprintf(w, "\nBlocks %s\n", g)
			} else {
				fmt.Fprintf(w, "\nBlocks advancing to %s\n", g)
			}
			if len(bs) == 0 {
				fmt.Fprintln(w, "  nothing found here")
			}
			for _, b := range bs {
				fmt.Fprintf(w, "  ◇ %s: %s\n", b.What, b.Reason)
			}
		}
	}
	fmt.Fprintf(w, "\n%s\n", r.Unseen)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

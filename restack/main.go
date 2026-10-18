// valley-restacker rebases ready changes onto main when main moves under
// them, as its own principal, so their evidence stays fresh without their
// owners rebasing and approving again by hand.
//
// This slice is the planner: `valley-restacker once --report-only` reads
// one snapshot of origin and says which families it would restack, which
// it would hold and why, and what capacity allows. It pushes nothing.
//
// It talks to origin only over git, as a client, from a scratch repository
// under its state directory. Hooks and external filters are off there:
// the scratch repository's hooks path is /dev/null, and git reads neither
// the system's nor the user's configuration, so no filter driver is
// defined. A service that needs ssh settings passes them in
// GIT_SSH_COMMAND.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"the-valley/change"
	"the-valley/composed"
	"the-valley/rebase"
)

const usage = `valley-restacker: rebase ready changes onto main when main moves.

usage: valley-restacker once --report-only [flags]

  once --report-only
        read one snapshot of origin and print what a pass would do: each
        family, restack or hold with the reason, its capacity, and the
        integration requests it considered. Pushes nothing.

flags:
  --repo URL            origin: the repository the restacker serves (required)
  --target NAME         the target changes are rebased onto (only main)
  --state DIR           where the scratch repository lives (required)
  --principal NAME      the principal the restacker pushes as (required)
  --known-signers FILE  verifier keys, for landings proved by integrator evidence
  --integrator-name N   the name the integrator signs under
  --attest PATH         the attest helper that checks a note (default: attest on PATH)
  --instance-repo URL   the repository carrying the group's floor
  --instance-ref REF    the ref whose tip carries the floor (refs/heads/main)
  --instance-policy DIR the floor's directory in that tip (policy/instance)
  --instance DIR        a materialized floor, for a host that serves none
  --project-policy DIR  the project layer, in the target tip (policy/project)
  --schema FILE         the verification schema (VALLEY_VERIFICATION_SCHEMA)
  --json                print the plan as JSON
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "once":
		if err := once(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "valley-restacker: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "valley-restacker: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// config is a pass's configuration, from flags.
type config struct {
	repo, target, state, principal string
	verify                         rebase.Verifier
	floor                          composed.Floor
	instanceRepo, projectPolicy    string
	schema, cue                    string
	json                           bool
}

func once(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("valley-restacker once", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	var c config
	reportOnly := fs.Bool("report-only", false, "")
	fs.StringVar(&c.repo, "repo", "", "")
	fs.StringVar(&c.target, "target", rebase.Target, "")
	fs.StringVar(&c.state, "state", "", "")
	fs.StringVar(&c.principal, "principal", "", "")
	fs.StringVar(&c.verify.Keys, "known-signers", "", "")
	fs.StringVar(&c.verify.Signer, "integrator-name", "", "")
	fs.StringVar(&c.verify.Attest, "attest", "", "")
	fs.StringVar(&c.instanceRepo, "instance-repo", "", "")
	fs.StringVar(&c.floor.Ref, "instance-ref", "refs/heads/main", "")
	fs.StringVar(&c.floor.Dir, "instance-policy", "policy/instance", "")
	fs.StringVar(&c.floor.Path, "instance", "", "")
	fs.StringVar(&c.projectPolicy, "project-policy", "policy/project", "")
	fs.StringVar(&c.schema, "schema", os.Getenv("VALLEY_VERIFICATION_SCHEMA"), "")
	fs.BoolVar(&c.json, "json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case fs.NArg() > 0:
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case !*reportOnly:
		return errors.New("once needs --report-only: this restacker plans and does not yet push")
	case c.target != rebase.Target:
		return fmt.Errorf("--target %s: a change may name only %s as its target", c.target, rebase.Target)
	}
	for _, f := range []struct{ flag, value string }{
		{"--repo", c.repo}, {"--state", c.state}, {"--principal", c.principal}, {"--schema", c.schema},
	} {
		if f.value == "" {
			return fmt.Errorf("%s is required", f.flag)
		}
	}
	if !change.ValidPrincipal(c.principal) {
		return fmt.Errorf("--principal %q is not a principal", c.principal)
	}
	// The floor comes from the instance repository's tip, or from a
	// directory on disk, and never from both, as for the integrator.
	if (c.instanceRepo == "") == (c.floor.Path == "") {
		return errors.New("exactly one of --instance-repo and --instance is required: the floor is read from the instance repository's tip, or from a materialized directory")
	}
	if c.verify.Attest == "" {
		c.verify.Attest, _ = exec.LookPath("attest")
	}
	var err error
	if c.cue, err = exec.LookPath("cue"); err != nil {
		return errors.New("cue is not on the path; the restacker composes the policy with it")
	}
	ctx := context.Background()
	g, err := scratchRepo(ctx, c)
	if err != nil {
		return err
	}
	p, err := planOnce(ctx, g, c)
	if err != nil {
		return err
	}
	if c.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	printPlan(out, p)
	return nil
}

// scratchRepo makes, or reuses, the bare repository a pass reads origin
// into, and returns git run in it with hooks and configured filters off.
func scratchRepo(ctx context.Context, c config) (rebase.Git, error) {
	dir := filepath.Join(c.state, "scratch.git")
	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	g := rebase.Git{Dir: dir, Env: env, Stderr: os.Stderr}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return g, err
		}
		if err := run(ctx, g, "init", "--quiet", "--bare"); err != nil {
			return g, err
		}
	}
	for _, kv := range [][2]string{{"core.hooksPath", "/dev/null"}, {"core.fsmonitor", "false"}} {
		if err := run(ctx, g, "config", kv[0], kv[1]); err != nil {
			return g, err
		}
	}
	// The remote is set every pass, so a changed --repo takes effect.
	_ = run(ctx, g, "remote", "remove", "origin")
	return g, run(ctx, g, "remote", "add", "origin", c.repo)
}

func run(ctx context.Context, g rebase.Git, args ...string) error {
	_, err := gitOut(ctx, g, args...)
	return err
}

func gitOut(ctx context.Context, g rebase.Git, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = g.Dir, g.Env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(b), nil
}

// ancestry is merge-base --is-ancestor, telling "no" (exit 1) apart from a
// question git could not answer.
func ancestry(ctx context.Context, g rebase.Git) Ancestry {
	return func(a, b string) (bool, error) {
		cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", a, b)
		cmd.Dir, cmd.Env = g.Dir, g.Env
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return true, nil
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return false, nil
		}
		return false, fmt.Errorf("could not tell whether %s is in %s", short(a), short(b))
	}
}

// policyCap composes the policy at the target tip and reads the cap on
// automatic versions: the floor from the instance repository's tip, read
// over git into the scratch repository, or from --instance; the project
// layer from the tip.
func policyCap(ctx context.Context, g rebase.Git, c config, tip string) (int, error) {
	floor := c.floor
	if c.instanceRepo != "" {
		out, err := gitOut(ctx, g, "ls-remote", c.instanceRepo, floor.Ref)
		oid, _, _ := strings.Cut(out, "\t")
		if err != nil || !rebase.IsObjectID(oid) {
			return 0, fmt.Errorf("no instance policy layer: %s has no %s: %s", c.instanceRepo, floor.Ref, composed.Floorless)
		}
		if err := run(ctx, g, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", c.instanceRepo, oid); err != nil {
			return 0, err
		}
		floor.Repo, floor.Ref = g.Dir, oid
	}
	instance, done, err := floor.Open("valley-restacker-floor")
	if err != nil {
		return 0, err
	}
	defer done()
	project, _, doneProject, err := composed.Project(g.Dir, tip, c.projectPolicy, "valley-restacker-project")
	if err != nil {
		return 0, err
	}
	defer doneProject()
	pol, err := composed.Compose(c.cue, c.schema, instance, project)
	if err != nil {
		return 0, err
	}
	return CapOf(pol.MaxAutomaticVersions)
}

// planOnce reads one snapshot and plans every family in it.
func planOnce(ctx context.Context, g rebase.Git, c config) (*Plan, error) {
	s, err := rebase.ReadSnapshot(ctx, g, c.verify)
	if err != nil {
		return nil, err
	}
	if s.Tip == "" {
		return nil, fmt.Errorf("origin has no %s", rebase.Target)
	}
	cap, err := policyCap(ctx, g, c, s.Tip)
	if err != nil {
		return nil, err
	}
	var attestations []string
	var requests []Request
	for ref, oid := range s.Refs {
		switch {
		case strings.HasPrefix(ref, rebase.AttestationRefs):
			attestations = append(attestations, ref+" "+oid)
		case strings.HasPrefix(ref, rebase.RequestPrefix):
			requests = append(requests, Request{Name: strings.TrimPrefix(ref, rebase.RequestPrefix), Head: oid})
		}
	}
	p := &Plan{Target: rebase.Target, Tip: s.Tip, Evidence: change.EvidenceFingerprint(attestations), Cap: cap}

	// The snapshot lists request heads and does not fetch them. They are
	// fetched here, before any ancestry is read, all at once and then one
	// by one when that fails, so one head origin cannot serve leaves the
	// others readable.
	var heads []string
	for _, r := range requests {
		heads = append(heads, r.Head)
	}
	if rebase.Fetch(ctx, g, heads...) != nil {
		for _, h := range heads {
			_ = rebase.Fetch(ctx, g, h)
		}
	}

	seen := map[string]bool{}
	for _, n := range s.Changes {
		if seen[n.ID] || !s.Unlanded(n) {
			continue
		}
		f, err := rebase.Capture(s, rebase.Selection{ID: n.ID, Whole: true})
		if err != nil {
			return nil, err
		}
		for _, l := range f.Layers {
			if l.ID != "" {
				seen[l.ID] = true
			}
		}
		p.Families = append(p.Families, judge(f, s, c.principal, p, requests, ancestry(ctx, g)))
	}
	return p, nil
}

// judge decides what a pass would do with one family.
func judge(f *rebase.Family, s *rebase.Snapshot, principal string, p *Plan, requests []Request, is Ancestry) FamilyPlan {
	fp := FamilyPlan{Reasons: []string{}, Notes: []string{}, Requests: []RequestPlan{}}
	ready := false
	for _, l := range f.Layers {
		lp := LayerPlan{Name: l.Name, ID: l.ID, Branch: l.Branch, Head: l.Head, State: string(l.State())}
		if l.Below != nil {
			lp.Below = l.Below.Name
		}
		if l.State() == rebase.Ready {
			ready = true
		} else {
			fp.Reasons = append(fp.Reasons, stateLine(l))
		}
		fp.Layers = append(fp.Layers, lp)
	}
	switch {
	case !ready:
		fp.Action = leave
		return fp
	case f.FastForwards():
		fp.Action, fp.Reasons = current, []string{"it already descends from " + rebase.Target + "'s tip"}
		return fp
	}
	if err := f.Check(); err != nil {
		fp.Reasons = append(fp.Reasons, err.Error())
	}

	m := membersOf(f)
	for _, r := range requests {
		by, err := Relate(r, m, p.Tip, is)
		if err != nil {
			fp.Requests = append(fp.Requests, RequestPlan{Name: r.Name, Head: r.Head, By: []string{}, Why: err.Error()})
			fp.Reasons = append(fp.Reasons, fmt.Sprintf("request %s: whether it is related cannot be determined: %v", r.Name, err))
			continue
		}
		if by == nil {
			continue
		}
		o, found := s.OutcomeOf(r.Name)
		rp := RequestPlan{Name: r.Name, Head: r.Head, By: by}
		if found {
			rp.Decision = o.Decision
		}
		rp.Current, rp.Releases, rp.Why = Currency(o, found, r.Head, p.Tip, p.Evidence)
		switch {
		case !rp.Releases:
			fp.Reasons = append(fp.Reasons, fmt.Sprintf("request %s might still land: %s", r.Name, rp.Why))
		case o.Decision == "reject":
			fp.Notes = append(fp.Notes, fmt.Sprintf("request %s was rejected at this head, tip and evidence", r.Name))
		}
		fp.Requests = append(fp.Requests, rp)
	}
	sortRequests(fp.Requests)

	for i, l := range f.Layers {
		if l.ID == "" || l.Record == nil {
			continue
		}
		cp := CapacityOf(l.Record, principal, p.Cap, nextSize(l.Name, l.Record, principal))
		fp.Layers[i].Capacity = cp
		fp.Reasons = append(fp.Reasons, cp.Over...)
	}
	fp.Action = restack
	if len(fp.Reasons) > 0 {
		fp.Action = hold
	}
	return fp
}

func printPlan(w io.Writer, p *Plan) {
	fmt.Fprintf(w, "%s at %s, evidence %s, at most %d automatic versions a change\n", p.Target, short(p.Tip), p.Evidence, p.Cap)
	if len(p.Families) == 0 {
		fmt.Fprintln(w, "no change to restack")
	}
	for _, f := range p.Families {
		fmt.Fprintf(w, "\n%s: %s\n", f.Layers[0].Name, f.Action)
		for _, l := range f.Layers {
			fmt.Fprintf(w, "  %-24s %-10s %s", l.Name, l.State, short(l.Head))
			if l.Below != "" {
				fmt.Fprintf(w, " on %s", l.Below)
			}
			if c := l.Capacity; c != nil {
				fmt.Fprintf(w, "; %d/%d automatic, %d/%d versions, update %d/%d, %d/%d bytes",
					c.Uploads, c.Cap, c.Versions, c.MaxVersions, c.Seq, c.MaxSeq, c.Bytes, c.MaxBytes)
			}
			fmt.Fprintln(w)
		}
		for _, r := range f.Requests {
			fmt.Fprintf(w, "  request %s at %s, by %s: %s\n", r.Name, short(r.Head), strings.Join(r.By, ", "), r.Why)
		}
		for _, why := range f.Reasons {
			fmt.Fprintf(w, "  - %s\n", why)
		}
		for _, n := range f.Notes {
			fmt.Fprintf(w, "  note: %s\n", n)
		}
	}
}

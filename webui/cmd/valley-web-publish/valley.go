package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"the-valley/webui/export"
)

// readAttempts is how many times a pass reads the valley before it gives
// up on inputs that keep changing under it.
const readAttempts = 3

// The bounds on what a pass holds of a program's output. A program that
// prints more than its bound to stdout fails, and what it prints to stderr
// past its bound is dropped.
const (
	maxValleyOutput = 64 << 20
	maxGitOutput    = 4 << 20
	maxStderr       = 64 << 10
)

// reading is what a pass read: the export, each exported project's mirror,
// and the projects it keeps private.
type reading struct {
	export  *export.Export
	json    []byte
	mirrors map[string]string
	private []string
}

// collect reads the valley and builds the export. Each knot's detail is
// read after the plan, so a write to the graph in between makes the two
// disagree; export.Build says so with ErrInconsistent, and collect reads
// everything again. It does the same when the instance repository moved
// during a read.
func (p *publisher) collect(ctx context.Context) (*reading, error) {
	if err := p.prepareOrigin(ctx); err != nil {
		return nil, err
	}
	if err := checkSchema(ctx, p.cfg.schema); err != nil {
		return nil, err
	}
	var err error
	for attempt := 1; attempt <= readAttempts; attempt++ {
		var r *reading
		r, err = p.read(ctx)
		if err == nil {
			return r, nil
		}
		if !errors.Is(err, export.ErrInconsistent) {
			return nil, err
		}
		p.warn("%v; reading the valley again", err)
	}
	return nil, fmt.Errorf("the valley changed under every one of %d reads: %w", readAttempts, err)
}

// planListing and pendingListing are what the publisher reads of the
// valley's JSON itself: which projects there are, each project's knots,
// and where each can be read. export.Build reads the rest.
type (
	planListing struct {
		Projects []struct {
			Project string `json:"project"`
			Knots   []struct {
				ID string `json:"id"`
			} `json:"knots"`
		} `json:"projects"`
	}
	pendingListing struct {
		Repos []pendingRepo `json:"repos"`
	}
	pendingRepo struct {
		Repo  string  `json:"repo"`
		URL   string  `json:"url"`
		Error *string `json:"error"`
	}
)

// read is one read of the valley. A project is private when the instance
// does not allow it, or when its own declaration is missing, invalid or
// private. Any other failure to read a project the instance allows ends
// the pass: an export without it would take it off the web.
func (p *publisher) read(ctx context.Context) (*reading, error) {
	inst, err := p.fetchInstance(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	decl, err := readDeclaration(ctx, p.origin, inst.tip, p.cfg.schema)
	var bad *invalidDeclaration
	switch {
	case errors.Is(err, errNoDeclaration):
	case errors.As(err, &bad):
		p.warn("the instance's policy/web at %s is not a valid declaration, so every project is private: %v", short(inst.tip), err)
	case err != nil:
		return nil, fmt.Errorf("could not read the instance's policy/web at %s: %w", short(inst.tip), err)
	case decl.Instance != nil:
		for name, d := range decl.Instance.Projects {
			allowed[name] = d.Visibility == string(export.Public)
		}
	}

	planJSON, err := p.valley(ctx, "plan", "--all", "--json")
	if err != nil {
		return nil, err
	}
	pendingJSON, err := p.valley(ctx, "pending", "--all", "--json")
	if err != nil {
		return nil, err
	}
	var plan planListing
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return nil, fmt.Errorf("valley plan --all --json: %w", err)
	}
	var pending pendingListing
	if err := json.Unmarshal(pendingJSON, &pending); err != nil {
		return nil, fmt.Errorf("valley pending --all --json: %w", err)
	}
	// The CLI lists what the instance repository publishes, and nothing
	// else. When discovery fails it lists the repository it runs in alone,
	// which would publish an export without the other projects.
	var planned, listed []string
	for _, proj := range plan.Projects {
		planned = append(planned, proj.Project)
	}
	repos := map[string]pendingRepo{}
	for _, r := range pending.Repos {
		listed = append(listed, r.Repo)
		repos[r.Repo] = r
	}
	for _, l := range []struct {
		command string
		names   []string
	}{{"valley plan --all", planned}, {"valley pending --all", listed}} {
		sort.Strings(l.names)
		if !slices.Equal(l.names, inst.projects) {
			return nil, fmt.Errorf("%s lists %s, and the instance repository publishes %s at %s, so nothing is published",
				l.command, listNames(l.names), listNames(inst.projects), projectsRef)
		}
	}

	r := &reading{mirrors: map[string]string{}}
	in := export.Inputs{
		Plan: planJSON, Pending: pendingJSON,
		Visibility: map[string]export.Visibility{}, SourceTips: map[string]string{},
		Principals: p.principals,
	}
	for _, proj := range plan.Projects {
		name := proj.Project
		public := false
		if allowed[name] {
			if public, err = p.projectVisibility(ctx, name, repos[name], in.SourceTips, r.mirrors); err != nil {
				return nil, err
			}
		}
		if !public {
			r.private = append(r.private, name)
			continue
		}
		in.Visibility[name] = export.Public
		for _, k := range proj.Knots {
			detail, err := p.valley(ctx, "knot", "show", "--project", name, k.ID, "--json")
			if err != nil {
				return nil, err
			}
			in.Details = append(in.Details, detail)
		}
	}
	if err := p.confirmInstance(ctx, inst); err != nil {
		return nil, err
	}
	sort.Strings(r.private)
	if r.export, err = export.Build(in); err != nil {
		return nil, err
	}
	if r.json, err = r.export.Encode(); err != nil {
		return nil, err
	}
	return r, nil
}

// listNames is a list of projects for a message.
func listNames(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// projectVisibility decides whether a project the instance allows is
// public: its own policy/web on main says public. For a public project it
// records main's tip and the mirror it was fetched into. A project with no
// main, or whose declaration is missing, invalid or private, is private,
// and an invalid declaration is said. A project that cannot be read is an
// error.
func (p *publisher) projectVisibility(ctx context.Context, name string, repo pendingRepo,
	tips, mirrors map[string]string) (bool, error) {
	if !export.ValidName(name) {
		p.warn("%q is not a project name, so it is private", name)
		return false, nil
	}
	if repo.Error != nil {
		return false, fmt.Errorf("%s: the pending listing could not read it: %s", name, *repo.Error)
	}
	if repo.URL == "" {
		return false, fmt.Errorf("%s: the pending listing gives no URL to read it from", name)
	}
	mirror, tip, err := p.fetchMain(ctx, name, repo.URL)
	if errors.Is(err, errNoMain) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s: could not read main: %w", name, err)
	}
	decl, err := readDeclaration(ctx, mirror, tip, p.cfg.schema)
	var bad *invalidDeclaration
	switch {
	case errors.Is(err, errNoDeclaration):
		return false, nil
	case errors.As(err, &bad):
		p.warn("%s: its policy/web at %s is not a valid declaration, so it is private: %v", name, short(tip), err)
		return false, nil
	case err != nil:
		return false, fmt.Errorf("%s: could not read its policy/web at %s: %w", name, short(tip), err)
	case decl.Project == nil || decl.Project.Visibility != string(export.Public):
		return false, nil
	}
	tips[name], mirrors[name] = tip, mirror
	return true, nil
}

// prepareOrigin makes the publisher's bare repository, with origin set to
// the instance repository, or sets origin again when it names another.
func (p *publisher) prepareOrigin(ctx context.Context) error {
	if err := os.MkdirAll(p.cache, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(p.origin, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if err := os.RemoveAll(p.origin); err != nil {
			return err
		}
		if _, err := git(ctx, "", "init", "--quiet", "--bare", "--template=", p.origin); err != nil {
			return err
		}
	}
	got, err := git(ctx, p.origin, "config", "--get", "remote.origin.url")
	if err == nil && strings.TrimSpace(string(got)) == p.cfg.instanceRepo {
		return nil
	}
	_, _ = git(ctx, p.origin, "remote", "remove", "origin")
	_, err = git(ctx, p.origin, "remote", "add", "--end-of-options", "origin", p.cfg.instanceRepo)
	return err
}

// The instance repository's project list, and where the publisher's
// repository keeps what it fetched of the instance.
const (
	projectsRef    = "refs/the-valley/projects"
	originMain     = "refs/remotes/origin/main"
	originProjects = "refs/remotes/origin/projects"
)

// instanceRead is the instance repository as a read found it: main's tip,
// the project list it publishes, and the projects that list names.
type instanceRead struct {
	tip, list string
	projects  []string
}

// fetchInstance fetches the instance repository's main and its project
// list into the publisher's repository. A repository whose main carries no
// policy/instance is not an instance repository. One whose main carries
// policy/valley is refused: the valley CLI would follow it to the instance
// it names, for the projects and for the signers, and the publisher reads
// through no instance but the one it is given.
func (p *publisher) fetchInstance(ctx context.Context) (instanceRead, error) {
	var r instanceRead
	if _, err := git(ctx, p.origin, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin",
		"+refs/heads/main:"+originMain, "+"+projectsRef+":"+originProjects); err != nil {
		return r, fmt.Errorf("could not read main and %s in the instance repository: %w", projectsRef, err)
	}
	var err error
	if r.tip, err = revParse(ctx, p.origin, originMain+"^{commit}"); err != nil {
		return r, err
	}
	if _, err := git(ctx, p.origin, "cat-file", "-e", r.tip+":policy/instance"); err != nil {
		return r, fmt.Errorf("%s holds no policy/instance on main, so it is not an instance repository", p.cfg.instanceRepo)
	}
	if _, err := git(ctx, p.origin, "cat-file", "-e", r.tip+":policy/valley"); err == nil {
		return r, fmt.Errorf("%s holds policy/valley on main, which would point the valley CLI at another instance; an instance repository the publisher reads holds policy/instance and no policy/valley", p.cfg.instanceRepo)
	}
	if r.list, err = revParse(ctx, p.origin, originProjects); err != nil {
		return r, err
	}
	list, err := git(ctx, p.origin, "cat-file", "blob", r.list)
	if err != nil {
		return r, fmt.Errorf("%s in the instance repository is not a list of projects: %w", projectsRef, err)
	}
	for _, name := range strings.Split(string(list), "\n") {
		if name != "" {
			r.projects = append(r.projects, name)
		}
	}
	sort.Strings(r.projects)
	return r, nil
}

// confirmInstance fetches the instance repository again once a read is
// done. Each valley command reads main and the project list afresh, so the
// read holds only when both are where it began; a main that moved may have
// carried a policy/valley the CLI followed.
func (p *publisher) confirmInstance(ctx context.Context, began instanceRead) error {
	now, err := p.fetchInstance(ctx)
	if err != nil {
		return err
	}
	if now.tip != began.tip || now.list != began.list {
		return fmt.Errorf("%w: the instance repository's main or project list moved during the read", export.ErrInconsistent)
	}
	return nil
}

// errNoMain is fetchMain's error for a project with no main.
var errNoMain = errors.New("no main")

// fetchMain brings a project's main into its mirror, a bare repository
// under the cache that holds main alone, and returns the mirror and main's
// tip, or errNoMain when the project has none. A mirror is first made by a clone, so it has the project's object
// format, and the clone is renamed into place only once it is whole. The
// clone copies objects through the transport, as a fetch does, and never
// links the project's own files.
func (p *publisher) fetchMain(ctx context.Context, name, url string) (string, string, error) {
	heads, err := git(ctx, p.origin, "ls-remote", "--end-of-options", url, "refs/heads/main")
	if err != nil {
		return "", "", err
	}
	if !listsRef(heads, "refs/heads/main") {
		return "", "", errNoMain
	}
	mirror := filepath.Join(p.cache, "repos", name+".git")
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err == nil {
		if _, err := git(ctx, mirror, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head",
			"--end-of-options", url, "+refs/heads/main:refs/heads/main"); err != nil {
			return "", "", err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
			return "", "", err
		}
		made, err := os.MkdirTemp(filepath.Dir(mirror), "."+name+".new-")
		if err != nil {
			return "", "", err
		}
		defer os.RemoveAll(made)
		clone := filepath.Join(made, "repo.git")
		if _, err := git(ctx, "", "clone", "--quiet", "--bare", "--no-local", "--no-tags", "--single-branch",
			"--branch", "main", "--template=", "--end-of-options", url, clone); err != nil {
			return "", "", err
		}
		if err := os.RemoveAll(mirror); err != nil {
			return "", "", err
		}
		if err := os.Rename(clone, mirror); err != nil {
			return "", "", err
		}
	}
	tip, err := revParse(ctx, mirror, "refs/heads/main^{commit}")
	return mirror, tip, err
}

// listsRef reports whether ls-remote's output lists the ref. ls-remote
// matches a pattern against the tail of a ref name, so each name listed
// is compared whole.
func listsRef(out []byte, ref string) bool {
	for _, line := range strings.Split(string(out), "\n") {
		if _, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return true
		}
	}
	return false
}

func revParse(ctx context.Context, repo, rev string) (string, error) {
	out, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// valley runs one of the CLI's JSON commands in the publisher's repository
// and returns what it printed, up to maxValleyOutput. What it says on
// stderr is passed on.
func (p *publisher) valley(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "valley", args...)
	cmd.Dir = p.origin
	cmd.Env = valleyEnvironment(os.Environ())
	stdout := &bounded{max: maxValleyOutput, strict: true}
	cmd.Stdout = stdout
	cmd.Stderr = p.stderr
	err := cmd.Run()
	if stdout.over {
		return nil, fmt.Errorf("valley %s: %w", strings.Join(args, " "), errTooLong)
	}
	if err != nil {
		return nil, fmt.Errorf("valley %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// valleyEnvironment is the environment the valley CLI runs in: the
// caller's, without the variables that would point git at another
// repository, and without the ones that would give the CLI verification
// material of their own. The CLI then reads the signers the host publishes,
// as it reads the projects.
func valleyEnvironment(environ []string) []string {
	var env []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if redirectsGit(name) || name == "VALLEY_KNOWN_SIGNERS" || name == "VALLEY_INTEGRATOR_SIGNER" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// git runs git in repo, unless it is "", with replacement refs off, git
// held to the transports a URL from data may use, and no prompt, and
// returns what it printed, up to maxGitOutput. It keeps the caller's
// GIT_CONFIG_* variables, which carry the safe.directory entries a host
// gives the publisher for repositories it does not own.
func git(ctx context.Context, repo string, args ...string) ([]byte, error) {
	if repo != "" {
		args = append([]string{"-C", repo}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects"}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	stdout := &bounded{max: maxGitOutput, strict: true}
	stderr := &bounded{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	verb := args[0]
	if verb == "-C" && len(args) > 2 {
		verb = args[2]
	}
	if stdout.over {
		return nil, fmt.Errorf("git %s: %w", verb, errTooLong)
	}
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", verb, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// errTooLong is the error for a program that printed more than its bound.
var errTooLong = errors.New("it printed more than the publisher holds of it")

// bounded holds what a program prints, up to max bytes. Past max, a write
// fails when strict is set, which ends the program at its next write, and
// is dropped when it is not. It is no io.ReaderFrom, so os/exec copies
// through Write.
type bounded struct {
	buf    bytes.Buffer
	max    int
	strict bool
	over   bool
}

func (b *bounded) Write(p []byte) (int, error) {
	room := max(b.max-b.buf.Len(), 0)
	if len(p) <= room {
		return b.buf.Write(p)
	}
	b.over = true
	b.buf.Write(p[:room])
	if b.strict {
		return room, errTooLong
	}
	return len(p), nil
}

func (b *bounded) Bytes() []byte  { return b.buf.Bytes() }
func (b *bounded) String() string { return b.buf.String() }

func gitEnvironment(environ []string) []string {
	var env []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if redirectsGit(name) || name == "GIT_ALLOW_PROTOCOL" || name == "GIT_TERMINAL_PROMPT" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_ALLOW_PROTOCOL=ssh:git:https:file",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_REPLACE_OBJECTS=1",
	)
}

// redirectsGit reports whether the variable points git at another
// repository, another object store or another work tree than the one it
// is run in.
func redirectsGit(name string) bool {
	switch name {
	case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_PREFIX",
		"GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
		return true
	}
	return false
}

// readPrincipals reads the display mapping: a JSON object from principal
// names to the names an export shows. No file is an empty mapping, under
// which every principal is shown as export.Unmapped.
func readPrincipals(path string) (export.Principals, error) {
	if path == "" {
		return export.Principals{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var m export.Principals
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("the display mapping %s is not a JSON object of names: %w", path, err)
	}
	if err := dec.Decode(&json.RawMessage{}); err != io.EOF {
		return nil, fmt.Errorf("the display mapping %s holds more than its one object", path)
	}
	if m == nil {
		m = export.Principals{}
	}
	return m, nil
}

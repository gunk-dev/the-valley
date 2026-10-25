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
	"sort"
	"strings"

	"the-valley/webui/export"
)

// readAttempts is how many times a pass reads the valley before it gives
// up on inputs that keep changing under it.
const readAttempts = 3

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
// everything again.
func (p *publisher) collect(ctx context.Context) (*reading, error) {
	if err := p.prepareOrigin(ctx); err != nil {
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
// valley's JSON itself: which projects there are, which could be read,
// each project's knots and its URL. export.Build reads the rest.
type (
	planListing struct {
		Projects []struct {
			Project string  `json:"project"`
			Error   *string `json:"error"`
			Knots   []struct {
				ID string `json:"id"`
			} `json:"knots"`
		} `json:"projects"`
	}
	pendingListing struct {
		Repos []struct {
			Repo  string  `json:"repo"`
			URL   string  `json:"url"`
			Error *string `json:"error"`
		} `json:"repos"`
	}
)

// read is one read of the valley.
func (p *publisher) read(ctx context.Context) (*reading, error) {
	instanceTip, err := p.fetchInstance(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	decl, err := readDeclaration(ctx, p.origin, instanceTip, p.cfg.schema)
	switch {
	case errors.Is(err, errNoDeclaration):
	case err != nil && ctx.Err() != nil:
		return nil, err
	case err != nil:
		p.warn("the instance's policy/web at %s is not a valid declaration, so every project is private: %v", short(instanceTip), err)
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
	urls := map[string]string{}
	for _, r := range pending.Repos {
		if r.Error == nil {
			urls[r.Repo] = r.URL
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
		public, err := p.projectVisibility(ctx, name, allowed[name], urls[name], in.SourceTips, r.mirrors)
		if err != nil {
			return nil, err
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
	sort.Strings(r.private)
	if r.export, err = export.Build(in); err != nil {
		return nil, err
	}
	if r.json, err = r.export.Encode(); err != nil {
		return nil, err
	}
	return r, nil
}

// projectVisibility decides whether one project is public: the instance
// allows it, and its own policy/web on main says public. For a public
// project it records main's tip and the mirror it was fetched into. A
// project that cannot be read, or whose declaration is missing or invalid,
// is private; the reason is said when it is a fault rather than a choice.
func (p *publisher) projectVisibility(ctx context.Context, name string, allowed bool, url string,
	tips, mirrors map[string]string) (bool, error) {
	if !allowed {
		return false, nil
	}
	if !export.ValidName(name) {
		p.warn("%q is not a project name, so it is private", name)
		return false, nil
	}
	if url == "" {
		p.warn("%s: the pending listing gives no URL to read it from, so it is private", name)
		return false, nil
	}
	mirror, tip, err := p.fetchMain(ctx, name, url)
	if err != nil {
		if ctx.Err() != nil {
			return false, err
		}
		p.warn("%s: main could not be read, so it is private: %v", name, err)
		return false, nil
	}
	decl, err := readDeclaration(ctx, mirror, tip, p.cfg.schema)
	switch {
	case errors.Is(err, errNoDeclaration):
		return false, nil
	case err != nil:
		if ctx.Err() != nil {
			return false, err
		}
		p.warn("%s: its policy/web at %s is not a valid declaration, so it is private: %v", name, short(tip), err)
		return false, nil
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

// fetchInstance fetches the instance repository's main into the
// publisher's repository, and returns its tip. A repository whose main
// carries no policy/instance is not an instance repository.
func (p *publisher) fetchInstance(ctx context.Context) (string, error) {
	const ref = "refs/remotes/origin/main"
	if _, err := git(ctx, p.origin, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head",
		"origin", "+refs/heads/main:"+ref); err != nil {
		return "", fmt.Errorf("could not read the instance repository's main: %w", err)
	}
	tip, err := revParse(ctx, p.origin, ref)
	if err != nil {
		return "", err
	}
	if _, err := git(ctx, p.origin, "cat-file", "-e", tip+":policy/instance"); err != nil {
		return "", fmt.Errorf("%s holds no policy/instance on main, so it is not an instance repository", p.cfg.instanceRepo)
	}
	return tip, nil
}

// fetchMain brings a project's main into its mirror, a bare repository
// under the cache that holds main alone, and returns the mirror and main's
// tip. A mirror is first made by a clone, so it has the project's object
// format, and the clone is renamed into place only once it is whole. The
// clone copies objects through the transport, as a fetch does, and never
// links the project's own files.
func (p *publisher) fetchMain(ctx context.Context, name, url string) (string, string, error) {
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
	tip, err := revParse(ctx, mirror, "refs/heads/main")
	return mirror, tip, err
}

func revParse(ctx context.Context, repo, ref string) (string, error) {
	out, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// valley runs one of the CLI's JSON commands in the publisher's repository
// and returns what it printed. What it says on stderr is passed on.
func (p *publisher) valley(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "valley", args...)
	cmd.Dir = p.origin
	cmd.Env = valleyEnvironment(os.Environ())
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = p.stderr
	if err := cmd.Run(); err != nil {
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
// held to the transports a URL from data may use, and no prompt. It keeps
// the caller's GIT_CONFIG_* variables, which carry the safe.directory
// entries a host gives the publisher for repositories it does not own.
func git(ctx context.Context, repo string, args ...string) ([]byte, error) {
	if repo != "" {
		args = append([]string{"-C", repo}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects"}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		verb := args[0]
		if verb == "-C" && len(args) > 2 {
			verb = args[2]
		}
		return nil, fmt.Errorf("git %s: %w: %s", verb, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

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

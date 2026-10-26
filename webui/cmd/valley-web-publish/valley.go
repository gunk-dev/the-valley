package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
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
	"time"

	"the-valley/readaccess"
	"the-valley/webui/export"
)

// readAttempts is how many times a pass reads the valley before it gives
// up on inputs that keep changing under it.
const readAttempts = 3

// cueTimeout is how long a cue run may take. A run past it fails the pass:
// how long cue takes depends on the host as much as on what it reads.
var cueTimeout = time.Minute

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

// collect reads the valley and builds the export. It fetches the instance
// repository once, and every read of the pass reads the instance at that
// fetch. It reads the read grants first, at the registry commit it fetched,
// and prepares the CLI's clones of the projects a grant to anyone names
// alone: the CLI can make no other clone, so it reads no other project,
// and one that cannot be read fails no pass. Each knot's detail is read
// after the plan, so a write to the graph in between makes the two
// disagree; export.Build says so with ErrInconsistent, and collect reads
// everything again.
func (p *publisher) collect(ctx context.Context) (*reading, error) {
	if err := p.access.CheckSchemas(ctx); err != nil {
		return nil, err
	}
	inst, err := p.fetchInstance(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := p.readGrants(ctx, inst)
	if err != nil {
		return nil, err
	}
	var selected []string
	for _, name := range inst.projects {
		if grants.Selects(readaccess.Anyone, name) {
			selected = append(selected, name)
		}
	}
	if err := p.prepareCLICaches(ctx, selected); err != nil {
		return nil, err
	}
	if err := p.preparePointer(ctx); err != nil {
		return nil, err
	}
	for attempt := 1; attempt <= readAttempts; attempt++ {
		var r *reading
		r, err = p.read(ctx, inst, grants)
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

// readGrants reads the read grants at the destination from the identity
// registry at the commit fetchInstance fetched of --registry-ref. A
// registry that is missing or invalid grants nothing, so every project is
// private. One that cannot be read ends the pass.
func (p *publisher) readGrants(ctx context.Context, inst instanceRead) (readaccess.Grants, error) {
	if inst.registry == "" {
		p.warn("the instance repository holds no %s, so it has no identity registry, and every project is private", p.cfg.registryRef)
		return nil, nil
	}
	grants, err := p.access.Grants(ctx, p.origin, inst.registry, p.cfg.registryDir, p.cfg.destination, p.now())
	var bad *readaccess.InvalidError
	switch {
	case errors.Is(err, readaccess.ErrMissing):
		return nil, nil
	case errors.As(err, &bad):
		p.warn("the instance's identity registry at %s is not valid, so every project is private: %v", short(inst.registry), err)
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("could not read the instance's identity registry at %s: %w", short(inst.registry), err)
	}
	return grants, nil
}

// read is one read of the valley, with the instance as fetchInstance read
// it and the grants readGrants read. A project is public when anyone may
// read it at the destination, by the read grants in the instance's
// registry and the project's own policy/read. It is private when no grant
// to anyone names it, and when its policy/read is missing, invalid, or
// does not let anyone read it. Any other failure to read a project a grant
// names ends the pass: an export without it would take it off the web.
func (p *publisher) read(ctx context.Context, inst instanceRead, grants readaccess.Grants) (*reading, error) {
	planJSON, err := p.valley(ctx, p.pointer, "plan", "--all", "--json")
	if err != nil {
		return nil, err
	}
	pendingJSON, err := p.valley(ctx, p.pointer, "pending", "--all", "--json")
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
		if grants.Selects(readaccess.Anyone, name) {
			if public, err = p.readableByAnyone(ctx, name, repos[name], grants, in.SourceTips, r.mirrors); err != nil {
				return nil, err
			}
		}
		if !public {
			r.private = append(r.private, name)
			continue
		}
		in.Visibility[name] = export.Public
		for _, k := range proj.Knots {
			detail, err := p.valley(ctx, p.origin, "knot", "show", "--project", name, k.ID, "--json")
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

// listNames is a list of projects for a message.
func listNames(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// readableByAnyone decides whether a project a read grant to anyone names
// is public: its own policy/read on main lets anyone read it, under that
// grant. For a public project it records main's tip and the mirror it was
// fetched into. A project with no main, or whose policy/read is missing,
// invalid or does not let anyone read it, is private, and an invalid
// policy is said. A project that cannot be read is an error.
func (p *publisher) readableByAnyone(ctx context.Context, name string, repo pendingRepo,
	grants readaccess.Grants, tips, mirrors map[string]string) (bool, error) {
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
	consent, err := p.access.Consent(ctx, mirror, tip, name)
	var bad *readaccess.InvalidError
	switch {
	case errors.Is(err, readaccess.ErrMissing):
		return false, nil
	case errors.As(err, &bad):
		p.warn("%s: its policy/read at %s is not valid, so it is private: %v", name, short(tip), err)
		return false, nil
	case err != nil:
		return false, fmt.Errorf("%s: could not read its policy/read at %s: %w", name, short(tip), err)
	case !grants.May(readaccess.Anyone, consent):
		return false, nil
	}
	tips[name], mirrors[name] = tip, mirror
	return true, nil
}

// preparePointer makes the repository valley plan and valley pending run
// in: a bare repository whose origin is itself, and whose main holds one
// file, policy/valley, naming the instance repository. The CLI finds the
// instance at policy/valley on origin's main, as it does from any project,
// and follows it no further. So it reads the projects and the signers the
// instance repository publishes, and never reads the instance repository's
// main, where a policy/valley could land and be taken away again while the
// pass reads. Only the publisher writes to this repository, and main's
// commit is the same in every pass for the same --instance-repo.
func (p *publisher) preparePointer(ctx context.Context) error {
	files, err := sharedFiles(ctx, p.cfg.instanceRepo, nil)
	if err != nil {
		return err
	}
	// Build the four small Git objects in memory so their compressed sizes
	// are known before any cache file is written.
	object := func(kind string, data []byte) []byte {
		raw := append([]byte(fmt.Sprintf("%s %d\x00", kind, len(data))), data...)
		h := sha1.New()
		if strings.Contains(files["config"], "objectformat = sha256") {
			h = sha256.New()
		}
		h.Write(raw)
		digest := h.Sum(nil)
		name := hex.EncodeToString(digest)
		var compressed bytes.Buffer
		w := zlib.NewWriter(&compressed)
		_, _ = w.Write(raw)
		_ = w.Close()
		files["objects/"+name[:2]+"/"+name[2:]] = compressed.String()
		return digest
	}
	blob := object("blob", []byte(p.cfg.instanceRepo+"\n"))
	policy := object("tree", append([]byte("100644 valley\x00"), blob...))
	tree := object("tree", append([]byte("40000 policy\x00"), policy...))
	commit := object("commit", []byte(fmt.Sprintf("tree %x\nauthor valley-web-publish <operator@example.org> 0 +0000\ncommitter valley-web-publish <operator@example.org> 0 +0000\n\npolicy/valley: the instance the publisher reads\n", tree)))
	files["refs/heads/main"] = hex.EncodeToString(commit) + "\n"
	files["config"] = strings.Replace(files["config"], quoteGit(p.cfg.instanceRepo), quoteGit(p.pointer), 1)
	return p.stageFiles(p.pointer, files)
}

// prepareCLICaches uses the CLI's URL hash and .git layout (status/list.go).
// Both the project and instance objects are borrowed: the CLI reads the
// instance's signers while inspecting each project's records.
func (p *publisher) prepareCLICaches(ctx context.Context, names []string) error {
	instance, err := sharedFiles(ctx, p.cfg.instanceRepo, nil)
	if err != nil {
		return err
	}
	reposDir := p.cliReposDir()
	if err := os.MkdirAll(reposDir, 0o700); err != nil {
		return err
	}
	// A killed pass may leave the parent sealed. Only the publisher
	// stages repositories; the CLI may only use the current staged set.
	if err := os.Chmod(reposDir, 0o700); err != nil {
		return err
	}
	urls := make(map[string]string, len(names))
	staged := make(map[string]bool, len(names))
	for _, name := range names {
		if !export.ValidName(name) {
			return fmt.Errorf("invalid project name %q", name)
		}
		u := strings.TrimRight(p.cfg.instanceRepo, "/")
		cut := strings.LastIndex(u, "/") + 1
		suffix := ""
		if strings.HasSuffix(u[cut:], ".git") {
			suffix = ".git"
		}
		url := u[:cut] + name + suffix
		urls[name] = url
		sum := sha256.Sum256([]byte(url))
		staged[hex.EncodeToString(sum[:8])+"-"+name] = true
	}
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !staged[entry.Name()] {
			if err := os.RemoveAll(filepath.Join(reposDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	for _, name := range names {
		url := urls[name]
		files, err := sharedFiles(ctx, url, nil)
		if err != nil {
			return fmt.Errorf("%s: could not prepare CLI cache: %w", name, err)
		}
		files["config"] = strings.Replace(files["config"], "bare = true", "bare = false", 1) + "[core]\n\tlogallrefupdates = false\n"
		files["objects/info/alternates"] += instance["objects/info/alternates"]
		wrapped := map[string]string{}
		for path, data := range files {
			wrapped[".git/"+path] = data
		}
		sum := sha256.Sum256([]byte(url))
		dir := filepath.Join(reposDir, hex.EncodeToString(sum[:8])+"-"+name)
		if err := p.stageFiles(dir, wrapped); err != nil {
			return err
		}
	}
	// Each CLI cache can write main and its lock file. Leave room for
	// those bounded metadata writes before invoking any collection command.
	p.collectionRoom = int64(len(names)+1) * 4096
	return p.reserveStorage(p.collectionRoom)
}

func (p *publisher) cliReposDir() string {
	return filepath.Join(filepath.Dir(p.cache), "valley", "repos")
}

// The instance repository's project list, and where the publisher's
// repository keeps what it fetched of the instance.
const (
	projectsRef    = "refs/the-valley/projects"
	originMain     = "refs/remotes/origin/main"
	originProjects = "refs/remotes/origin/projects"
	originRegistry = "refs/remotes/origin/registry"
)

// instanceRead is the instance repository as a pass found it: main's tip,
// the commit of --registry-ref, the project list it publishes, and the
// projects that list names. registry is "" when the instance repository
// holds no --registry-ref.
type instanceRead struct {
	tip, registry, list string
	projects            []string
}

// fetchInstance fetches the instance repository's main, its project list
// and --registry-ref into the publisher's repository, once a pass. The
// registry is read at the commit fetched here for the whole pass, as the
// identity compiler reads it at the commit it resolved. A repository whose
// main carries no policy/instance is not an instance repository. One whose
// main carries policy/valley is refused too: it names another instance as
// its own.
func (p *publisher) fetchInstance(ctx context.Context) (instanceRead, error) {
	var r instanceRead
	refs := map[string]string{originMain: "refs/heads/main", originProjects: projectsRef}
	read := "main and " + projectsRef
	fromMain := p.cfg.registryRef == "refs/heads/main"
	if !fromMain {
		held, err := hasRef(ctx, p.cfg.instanceRepo, p.cfg.registryRef)
		if err != nil {
			return r, fmt.Errorf("could not read %s in the instance repository: %w", p.cfg.registryRef, err)
		}
		if held {
			refs[originRegistry] = p.cfg.registryRef
			read = "main, " + projectsRef + " and " + p.cfg.registryRef
		}
	}
	if err := p.shareRepository(ctx, p.origin, p.cfg.instanceRepo, refs); err != nil {
		return r, fmt.Errorf("could not read %s in the instance repository: %w", read, err)
	}
	var err error
	if r.tip, err = revParse(ctx, p.origin, originMain+"^{commit}"); err != nil {
		return r, err
	}
	switch {
	case fromMain:
		r.registry = r.tip
	case refs[originRegistry] != "":
		if r.registry, err = revParse(ctx, p.origin, originRegistry+"^{commit}"); err != nil {
			return r, fmt.Errorf("%s in the instance repository is not a commit: %w", p.cfg.registryRef, err)
		}
	}
	if _, err := git(ctx, p.origin, "cat-file", "-e", r.tip+":policy/instance"); err != nil {
		return r, fmt.Errorf("%s holds no policy/instance on main, so it is not an instance repository", p.cfg.instanceRepo)
	}
	if _, err := git(ctx, p.origin, "cat-file", "-e", r.tip+":policy/valley"); err == nil {
		return r, fmt.Errorf("%s holds policy/valley on main, which names another instance; an instance repository the publisher reads holds policy/instance and no policy/valley", p.cfg.instanceRepo)
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

// errNoMain is fetchMain's error for a project with no main.
var errNoMain = errors.New("no main")

// fetchMain reads main into a cache that borrows the local repository's
// objects. Borrowing keeps repository history outside the storage budget:
// the publisher can read the host's objects but cannot write them.
func (p *publisher) fetchMain(ctx context.Context, name, url string) (string, string, error) {
	if !filepath.IsAbs(url) {
		return "", "", fmt.Errorf("repository must be an absolute local path: %q", url)
	}
	heads, err := git(ctx, url, "ls-remote", "--end-of-options", url, "refs/heads/main")
	if err != nil {
		return "", "", err
	}
	if !listsRef(heads, "refs/heads/main") {
		return "", "", errNoMain
	}

	mirror := filepath.Join(p.cache, "repos", name+".git")
	if err := p.shareRepository(ctx, mirror, url, map[string]string{"refs/heads/main": "refs/heads/main"}); err != nil {
		return "", "", err
	}
	tip, err := revParse(ctx, mirror, "refs/heads/main^{commit}")
	return mirror, tip, err
}

// shareRepository replaces a disposable cache with refs and an alternate.
// No fetch transport runs, so even objects added concurrently cannot be
// copied. All bytes to be written are known before staging starts. Old
// caches remain counted until the replacement is complete.
func (p *publisher) shareRepository(ctx context.Context, dir, source string, refs map[string]string) error {
	files, err := sharedFiles(ctx, source, refs)
	if err != nil {
		return err
	}
	return p.stageFiles(dir, files)
}

func quoteGit(s string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\t", "\\t").Replace(s)
}

func sharedFiles(ctx context.Context, source string, refs map[string]string) (map[string]string, error) {
	if !filepath.IsAbs(source) || strings.ContainsAny(source, "\n\r") {
		return nil, fmt.Errorf("repository must be an absolute local path: %q", source)
	}
	objects, err := git(ctx, source, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return nil, err
	}
	format, err := git(ctx, source, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, err
	}
	config := "[core]\n\tbare = true\n\trepositoryformatversion = 0\n"
	switch strings.TrimSpace(string(format)) {
	case "sha1":
	case "sha256":
		config = "[core]\n\tbare = true\n\trepositoryformatversion = 1\n[extensions]\n\tobjectformat = sha256\n"
	default:
		return nil, fmt.Errorf("unsupported object format %q", format)
	}
	quoted := quoteGit(source)
	config += "[remote \"origin\"]\n\turl = \"" + quoted + "\"\n"
	files := map[string]string{
		"HEAD": "ref: refs/heads/main\n", "config": config,
		"objects/info/alternates": strings.TrimSpace(string(objects)) + "\n",
	}
	for dest, ref := range refs {
		tip, err := revParse(ctx, source, ref)
		if err != nil {
			return nil, err
		}
		files[dest] = tip + "\n"
	}
	return files, nil
}

func (p *publisher) reserveStorage(added int64) error {
	used, err := p.storageUsed()
	if err != nil {
		return err
	}
	if used > int64(p.cfg.maxStorage)-added {
		return p.overStorage(used + added)
	}
	return nil
}

func (p *publisher) stageFiles(dir string, files map[string]string) error {
	used, err := p.storageUsed()
	if err != nil {
		return err
	}
	var added int64
	for _, content := range files {
		added += int64(len(content))
	}
	if used > int64(p.cfg.maxStorage)-added {
		return p.overStorage(used + added)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".shared-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	prefix := stage
	if _, ok := files[".git/HEAD"]; ok {
		prefix = filepath.Join(stage, ".git")
	}
	if err := os.MkdirAll(filepath.Join(prefix, "refs"), 0o700); err != nil {
		return err
	}
	for name, content := range files {
		path := filepath.Join(stage, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	if err := p.checkStorage(); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(stage, dir)
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

// hasRef reports whether repo holds ref. An error is a repository git
// could not read.
func hasRef(ctx context.Context, repo, ref string) (bool, error) {
	_, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

func revParse(ctx context.Context, repo, rev string) (string, error) {
	out, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// valley runs one of the CLI's JSON commands in dir, one of the
// publisher's repositories, and returns what it printed, up to
// maxValleyOutput. What it says on stderr is passed on.
func (p *publisher) valley(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if err := p.reserveStorage(p.collectionRoom); err != nil {
		return nil, err
	}
	// Discovery can see a newly listed project, and knot details can name
	// an unlisted project. Deny creation in the parent before either can
	// make an unshared cache and fetch objects. Staged .git directories
	// remain writable for their bounded ref updates.
	reposDir := p.cliReposDir()
	if err := os.Chmod(reposDir, 0o500); err != nil {
		return nil, err
	}
	defer os.Chmod(reposDir, 0o700)
	cmd := exec.CommandContext(ctx, "valley", args...)
	cmd.Dir = dir
	cmd.Env = valleyEnvironment(os.Environ())
	stdout := &bounded{max: maxValleyOutput, strict: true}
	cmd.Stdout = stdout
	cmd.Stderr = p.stderr
	err := cmd.Run()
	if budget := p.checkStorage(); budget != nil {
		return nil, budget
	}
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

// git runs git in repo, unless it is "", and returns what it printed, up
// to maxGitOutput.
func git(ctx context.Context, repo string, args ...string) ([]byte, error) {
	return gitInput(ctx, repo, "", args...)
}

// gitInput is git with input on its stdin. With no input, stdin is the
// null device.
func gitInput(ctx context.Context, repo, input string, args ...string) ([]byte, error) {
	var stdin io.Reader
	if input != "" {
		stdin = strings.NewReader(input)
	}
	stdout := &bounded{max: maxGitOutput, strict: true}
	err := gitStream(ctx, repo, stdin, stdout, args...)
	if stdout.over {
		return nil, fmt.Errorf("git %s: %w", gitVerb(args), errTooLong)
	}
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// gitStream runs git in repo, unless it is "", with stdin and stdout
// given: with replacement refs off, git held to the transports a URL from
// data may use, no prompt, and a fixed date for any commit it writes. It
// keeps the caller's GIT_CONFIG_* variables, which carry the
// safe.directory entries a host gives the publisher for repositories it
// does not own.
func gitStream(ctx context.Context, repo string, stdin io.Reader, stdout io.Writer, args ...string) error {
	if repo != "" {
		args = append([]string{"-C", repo}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects"}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	stderr := &bounded{max: maxStderr}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", gitVerb(args), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitVerb is the git command args run, for messages: the first argument
// after -C and the options git takes before its command.
func gitVerb(args []string) string {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-C", "-c":
			i++
		default:
			return args[i]
		}
	}
	return "git"
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
		switch {
		case redirectsGit(name), name == "GIT_ALLOW_PROTOCOL", name == "GIT_TERMINAL_PROMPT",
			name == "GIT_AUTHOR_DATE", name == "GIT_COMMITTER_DATE":
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_ALLOW_PROTOCOL=ssh:git:https:file",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_AUTHOR_DATE=@0 +0000",
		"GIT_COMMITTER_DATE=@0 +0000",
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

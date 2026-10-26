package readaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// PolicyDir is where a project keeps its consent: the *.cue documents
// directly in this directory of its main.
const PolicyDir = "policy/read"

// ErrMissing is the error for a commit that holds no document where one is
// read: no *.cue file directly in the directory, or no directory.
var ErrMissing = errors.New("no document")

// InvalidError is the error for a document that is there and is not valid:
// cue refused it under its schema, or it is past the bounds, malformed, or
// for another project. An invalid document consents to nothing. Every
// other error of a Reader's is a failure to read, which consents to
// nothing either, and which a caller reports rather than acting on.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

func invalid(format string, args ...any) error {
	return &InvalidError{fmt.Errorf(format, args...)}
}

// Reader reads the two consents out of git commits, with git and cue. It
// reads a document's *.cue files with git ls-tree and cat-file, never a
// checkout, copies them into a scratch directory with no cue.mod, so cue
// resolves no import beyond its standard library, and exports them as JSON
// with cue under the document's schema. Its Go reading then holds the JSON
// to the schema's fields and names again, so a document that redeclares a
// definition of the schema widens nothing.
type Reader struct {
	// ReadSchema is schema/read.cue, and IdentitySchema is
	// schema/identity.cue.
	ReadSchema, IdentitySchema string

	// CueTimeout bounds each cue run. Zero is a minute.
	CueTimeout time.Duration
}

// The bounds on a document: how many files it may have, how large each may
// be, and how much cue may print for it. A document past them is invalid.
// A registry carries keys and attestations, so its bounds are larger.
type bounds struct {
	files  int
	file   int64
	output int
}

var (
	policyBounds   = bounds{files: 32, file: 64 << 10, output: 1 << 20}
	registryBounds = bounds{files: 64, file: 1 << 20, output: 8 << 20}
)

// The bounds on what git and cue print. git's output past maxGitOutput
// fails the call, and what a program prints on stderr past maxStderr is
// dropped.
const (
	maxGitOutput = 4 << 20
	maxStderr    = 64 << 10
)

// cueRefused is the exit status cue ends with when it reports an error in
// what it was given, as when a schema refuses a document. A Go program that
// panics exits 2, and one the kernel kills has no status.
const cueRefused = 1

// CheckSchemas holds each schema to loading on its own, before any
// document is read under it. Without it a schema that cannot be read would
// make every document invalid, and so withdraw every consent.
func (r *Reader) CheckSchemas(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "readaccess-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for _, schema := range []string{r.ReadSchema, r.IdentitySchema} {
		if schema == "" {
			return errors.New("no schema given")
		}
		if _, err := r.cue(ctx, dir, 0, "vet", "-c=false", schema); err != nil {
			return fmt.Errorf("the schema %s does not load: %w", schema, err)
		}
	}
	return nil
}

// policyDoc is a policy/read as cue exports it under schema/read.cue.
type policyDoc struct {
	Project  string          `json:"project"`
	Identity string          `json:"identity"`
	Readers  map[string]bool `json:"readers"`
}

// Consent reads the consent of the project at a commit of repo: its
// policy/read under ReadSchema. A policy that names another project than
// the one it is read from is invalid.
func (r *Reader) Consent(ctx context.Context, repo, commit, project string) (Consent, error) {
	var doc policyDoc
	if err := r.read(ctx, repo, commit, PolicyDir, r.ReadSchema, policyBounds, &doc); err != nil {
		return Consent{}, err
	}
	switch {
	case !validProject(doc.Project):
		return Consent{}, invalid("%s names the project %q, which is not a project name", PolicyDir, doc.Project)
	case doc.Project != project:
		return Consent{}, invalid("%s is for the project %q, and was read from %q", PolicyDir, doc.Project, project)
	case !identityPattern.MatchString(doc.Identity):
		return Consent{}, invalid("%s declares the identity %q, which is not a publication identity", PolicyDir, doc.Identity)
	}
	c := Consent{Project: doc.Project, Identity: doc.Identity, Readers: []string{}}
	for reader, ok := range doc.Readers {
		if !ok || !namePattern.MatchString(reader) {
			return Consent{}, invalid("%s names the reader %q, which is not a principal name", PolicyDir, reader)
		}
		c.Readers = append(c.Readers, reader)
	}
	slices.Sort(c.Readers)
	return c, nil
}

// registryDoc is the identity registry as cue exports it under
// schema/identity.cue. The parts the read grants do not depend on are
// taken whole, and every other field is held to the schema's.
type registryDoc struct {
	Boundaries map[string]struct {
		Kind        string `json:"kind"`
		Destination string `json:"destination"`
	} `json:"boundaries"`
	Principals map[string]struct {
		Kind     string              `json:"kind"`
		Keys     json.RawMessage     `json:"keys"`
		Grants   map[string]grantDoc `json:"grants"`
		External json.RawMessage     `json:"external"`
		Expires  string              `json:"expires"`
	} `json:"principals"`
	Anyone *struct {
		Grants map[string]grantDoc `json:"grants"`
	} `json:"anyone"`
	Genesis            string          `json:"genesis"`
	GenesisGovernance  json.RawMessage `json:"genesisGovernance"`
	ExternalGovernance json.RawMessage `json:"externalGovernance"`
	ReadScopes         json.RawMessage `json:"readScopes"`
}

type grantDoc struct {
	Boundary string          `json:"boundary"`
	Projects json.RawMessage `json:"projects"`
}

// edgeRead is the boundary kind that checks read grants.
const edgeRead = "edge-read"

// Grants reads the instance's read grants at the destination: the grants
// at every edge read boundary that names it, from the registry in dir at a
// commit of the instance repository, under IdentitySchema. A principal
// whose expiry has arrived on day holds none, as the identity compiler
// omits it from every artifact.
func (r *Reader) Grants(ctx context.Context, repo, commit, dir, destination string, day time.Time) (Grants, error) {
	if !validDir(dir) {
		return nil, fmt.Errorf("%q is not a directory a commit can hold", dir)
	}
	if !namePattern.MatchString(destination) {
		return nil, fmt.Errorf("%q is not a destination name", destination)
	}
	var doc registryDoc
	if err := r.read(ctx, repo, commit, dir, r.IdentitySchema, registryBounds, &doc); err != nil {
		return nil, err
	}
	y, m, d := day.UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)

	g := Grants{}
	add := func(principal, name string, grant grantDoc) error {
		b, ok := doc.Boundaries[grant.Boundary]
		switch {
		case !ok:
			return invalid("the grant %s.%s names the boundary %q, which the registry does not declare", principal, name, grant.Boundary)
		case b.Kind != edgeRead && principal == Anyone:
			return invalid("anyone holds the grant %s at a boundary of kind %q; anyone holds read grants alone", name, b.Kind)
		case b.Kind != edgeRead && grant.Projects != nil:
			return invalid("the grant %s.%s names projects at a boundary of kind %q", principal, name, b.Kind)
		case b.Kind != edgeRead:
			return nil
		case !namePattern.MatchString(b.Destination):
			return invalid("the boundary %s names the destination %q, which is not a name", grant.Boundary, b.Destination)
		}
		s, err := parseScope(grant.Projects)
		if err != nil {
			return invalid("the grant %s.%s: %w", principal, name, err)
		}
		if b.Destination == destination {
			g[principal] = append(g[principal], s)
		}
		return nil
	}
	for principal, p := range doc.Principals {
		if principal == Anyone || !namePattern.MatchString(principal) {
			return nil, invalid("the registry names the principal %q, which is reserved or not a principal name", principal)
		}
		if p.Expires != "" {
			end, err := time.Parse(time.DateOnly, p.Expires)
			if err != nil {
				return nil, invalid("%s expires %q, which is not a calendar day", principal, p.Expires)
			}
			if !today.Before(end) {
				continue
			}
		}
		for name, grant := range p.Grants {
			if err := add(principal, name, grant); err != nil {
				return nil, err
			}
		}
	}
	if doc.Anyone != nil {
		for name, grant := range doc.Anyone.Grants {
			if err := add(Anyone, name, grant); err != nil {
				return nil, err
			}
		}
	}
	return g, nil
}

// parseScope reads a read grant's projects: "all", or an object of project
// names and publication identities.
func parseScope(raw json.RawMessage) (Scope, error) {
	var all string
	if err := json.Unmarshal(raw, &all); err == nil {
		if all != "all" {
			return Scope{}, fmt.Errorf("its projects are %q, which is neither \"all\" nor a set of projects", all)
		}
		return Scope{All: true}, nil
	}
	s := Scope{Projects: map[string]string{}}
	if err := json.Unmarshal(raw, &s.Projects); err != nil || s.Projects == nil {
		return Scope{}, errors.New("its projects are neither \"all\" nor a set of projects")
	}
	for project, identity := range s.Projects {
		if !validProject(project) || !identityPattern.MatchString(identity) {
			return Scope{}, fmt.Errorf("it names the project %q with the identity %q, and not a project name with a publication identity", project, identity)
		}
	}
	return s, nil
}

var (
	namePattern     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	identityPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// validProject reports whether name is a project name: the principal-name
// pattern, at most 64 bytes, as schema/read.cue holds it.
func validProject(name string) bool {
	return len(name) <= 64 && namePattern.MatchString(name)
}

// validDir reports whether dir is a relative path to a directory below a
// commit's root, written plainly.
func validDir(dir string) bool {
	return dir != "" && dir != "." && path.Clean(dir) == dir && !path.IsAbs(dir) &&
		dir != ".." && !strings.HasPrefix(dir, "../")
}

// read reads the document in dir at a commit of repo, exports it with cue
// under schema, and decodes the JSON into doc, which must take every field
// cue exported. Its error is ErrMissing when the commit holds no document
// there, an *InvalidError when the document is invalid, and any other
// error when git or cue could not be run over it.
func (r *Reader) read(ctx context.Context, repo, commit, dir, schema string, lim bounds, doc any) error {
	entry, err := git(ctx, repo, "ls-tree", "-z", commit, "--", dir)
	if err != nil {
		return err
	}
	if len(entry) == 0 {
		return ErrMissing
	}
	meta, _, _ := strings.Cut(string(entry), "\t")
	f := strings.Fields(meta)
	if len(f) != 3 || f[1] != "tree" {
		return invalid("%s is not a directory", dir)
	}
	listing, err := git(ctx, repo, "ls-tree", "-z", f[2])
	if errors.Is(err, errTooLong) {
		return invalid("%s lists more than %d bytes of entries", dir, maxGitOutput)
	} else if err != nil {
		return err
	}

	// A tree git wrote itself never names an entry with a directory part,
	// nor two entries alike, but one written literally can. Such a
	// directory is no document: copied out by name, an entry like ../x.cue
	// would land outside the scratch directory.
	type document struct{ name, oid string }
	var docs []document
	seen := map[string]bool{}
	for _, e := range strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00") {
		if e == "" {
			continue
		}
		meta, name, _ := strings.Cut(e, "\t")
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			return invalid("%s holds an entry named %q, which is not a plain file name", dir, name)
		}
		if seen[name] {
			return invalid("%s holds two entries named %q", dir, name)
		}
		seen[name] = true
		fields := strings.Fields(meta)
		if !strings.HasSuffix(name, ".cue") || len(fields) != 3 || fields[1] != "blob" ||
			(fields[0] != "100644" && fields[0] != "100755") {
			continue
		}
		docs = append(docs, document{name, fields[2]})
	}
	if len(docs) == 0 {
		return ErrMissing
	}
	if len(docs) > lim.files {
		return invalid("%s holds %d documents, more than the %d it may have", dir, len(docs), lim.files)
	}

	scratch, err := os.MkdirTemp("", "readaccess-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	files := []string{"export", "--out", "json", schema}
	for _, d := range docs {
		out, err := git(ctx, repo, "cat-file", "-s", d.oid)
		if err != nil {
			return err
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return fmt.Errorf("git cat-file -s %s: %w", d.oid, err)
		}
		if size > lim.file {
			return invalid("%s/%s is %d bytes, more than the %d a document may be", dir, d.name, size, lim.file)
		}
		body, err := git(ctx, repo, "cat-file", "blob", d.oid)
		if err != nil {
			return err
		}
		file := filepath.Join(scratch, d.name)
		if err := os.WriteFile(file, body, 0o600); err != nil {
			return err
		}
		files = append(files, file)
	}

	out, err := r.cue(ctx, scratch, lim.output, files...)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(doc); err != nil {
		return invalid("%s is not what its schema exports: %w", dir, err)
	}
	if err := dec.Decode(&json.RawMessage{}); err != io.EOF {
		return invalid("cue exported more than one document for %s", dir)
	}
	return nil
}

// cue runs cue with args in dir, and returns what it printed, up to limit
// bytes, or maxGitOutput when limit is zero. Only what the files decide is
// an *InvalidError: a run that ends with cueRefused, and a run that prints
// more than limit. Every other failure is an error of its own: a cue that
// could not be started, that crashed, that was killed or that ran past the
// timeout, and a read that was cancelled.
func (r *Reader) cue(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	timeout := r.CueTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	cueCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cueCtx, "cue", args...)
	cmd.Dir = dir
	cmd.Env = cueEnvironment(os.Environ())
	if limit == 0 {
		limit = maxGitOutput
	}
	stdout := &bounded{max: limit, strict: true}
	stderr := &bounded{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	said := fmt.Sprint(err)
	if first, _, _ := strings.Cut(strings.TrimSpace(stderr.buf.String()), "\n"); first != "" {
		said += ": " + first
	}
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case stdout.over:
		return nil, invalid("cue exported more than %d bytes", limit)
	case cueCtx.Err() != nil:
		return nil, fmt.Errorf("cue took longer than %v, so the document was not read", timeout)
	case errors.As(err, &exit) && exit.ExitCode() == cueRefused:
		return nil, invalid("cue %s: %s", args[0], said)
	case errors.As(err, &exit):
		return nil, fmt.Errorf("cue did not finish reading the document: %s", said)
	case err != nil:
		return nil, fmt.Errorf("could not run cue: %w", err)
	}
	return stdout.buf.Bytes(), nil
}

// cueEnvironment is the caller's environment without CUE's own variables,
// so a document is read the same way whoever reads it.
func cueEnvironment(environ []string) []string {
	var env []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "CUE_") {
			env = append(env, kv)
		}
	}
	return env
}

// errTooLong is the error for a program that printed more than its bound.
var errTooLong = errors.New("it printed more than the reader holds of it")

// git runs git in repo with replacement refs off, pathspecs taken
// literally and no prompt, and returns what it printed, up to
// maxGitOutput. It keeps the caller's GIT_CONFIG_* variables, which carry
// the safe.directory entries a host gives for repositories the caller does
// not own, and drops the ones that would point git at another repository.
func git(ctx context.Context, repo string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "--literal-pathspecs", "-C", repo}, args...)...)
	var env []string
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_PREFIX",
			"GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_TERMINAL_PROMPT", "GIT_NO_REPLACE_OBJECTS":
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	stdout := &bounded{max: maxGitOutput, strict: true}
	stderr := &bounded{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.over {
		return nil, fmt.Errorf("git %s: %w", args[0], errTooLong)
	}
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.buf.String()))
	}
	return stdout.buf.Bytes(), nil
}

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

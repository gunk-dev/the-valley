package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"the-valley/exactjson"
)

// webDir is where a repository keeps its web visibility declaration.
const webDir = "policy/web"

// The bounds on a declaration: how many documents it may have, how large
// each may be, and how much cue may print for it. A declaration past them
// is invalid, so the project is private.
const (
	maxDeclarationFiles = 32
	maxDeclarationFile  = 64 << 10
	maxCueOutput        = 1 << 20
)

// cueTimeout is how long a cue run may take. A run past it fails the pass:
// how long cue takes depends on the host as much as on the declaration.
var cueTimeout = time.Minute

// cueRefused is the exit status cue ends with when it reports an error in
// what it was given, as when the schema refuses a declaration. A Go
// program that panics exits 2, and one the kernel kills has no status.
const cueRefused = 1

// errNoDeclaration is readDeclaration's error for a commit with no
// policy/web/*.cue.
var errNoDeclaration = errors.New("no policy/web declaration")

// invalidDeclaration is readDeclaration's error for a declaration that is
// there and is not one: cue ran over it to the end and refused it, or it
// is past the bounds. A project whose declaration is invalid is private.
// Any other error of readDeclaration's is a failure to read the
// declaration, which ends the pass.
type invalidDeclaration struct{ err error }

func (e *invalidDeclaration) Error() string { return e.err.Error() }
func (e *invalidDeclaration) Unwrap() error { return e.err }

func invalid(format string, args ...any) error {
	return &invalidDeclaration{fmt.Errorf(format, args...)}
}

// declaration is a policy/web declaration as cue exports it under
// schema/web.cue.
type declaration struct {
	Project  *visibility `json:"project"`
	Instance *struct {
		Projects map[string]visibility `json:"projects"`
	} `json:"instance"`
}

type visibility struct {
	Visibility string `json:"visibility"`
}

// readDeclaration reads the policy/web declaration at a commit of repo: the
// *.cue documents directly in policy/web/, taken out with ls-tree and
// cat-file rather than a checkout, and exported by cue under the schema.
// Only regular files are read; a symbolic link or a submodule is not a
// document. Its error is errNoDeclaration when the commit has none, an
// *invalidDeclaration when the declaration is invalid, and any other error
// when git or cue could not be run over it.
func readDeclaration(ctx context.Context, repo, commit, schema string) (*declaration, error) {
	entry, err := git(ctx, repo, "ls-tree", "-z", commit, "--", webDir)
	if err != nil {
		return nil, err
	}
	if len(entry) == 0 {
		return nil, errNoDeclaration
	}
	meta, _, _ := strings.Cut(string(entry), "\t")
	if f := strings.Fields(meta); len(f) != 3 || f[1] != "tree" {
		return nil, invalid("%s is not a directory", webDir)
	} else if entry, err = git(ctx, repo, "ls-tree", "-z", f[2]); errors.Is(err, errTooLong) {
		return nil, invalid("%s lists more than %d bytes of entries", webDir, maxGitOutput)
	} else if err != nil {
		return nil, err
	}
	type document struct{ name, oid string }
	var docs []document
	for _, e := range strings.Split(string(entry), "\x00") {
		meta, name, ok := strings.Cut(e, "\t")
		if !ok || !strings.HasSuffix(name, ".cue") {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			continue
		}
		docs = append(docs, document{name, fields[2]})
	}
	if len(docs) == 0 {
		return nil, errNoDeclaration
	}
	if len(docs) > maxDeclarationFiles {
		return nil, invalid("%s holds %d documents, more than the %d a declaration may have", webDir, len(docs), maxDeclarationFiles)
	}

	scratch, err := os.MkdirTemp("", "valley-web-publish-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	files := []string{schema}
	for _, d := range docs {
		out, err := git(ctx, repo, "cat-file", "-s", d.oid)
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("git cat-file -s %s: %w", d.oid, err)
		}
		if size > maxDeclarationFile {
			return nil, invalid("%s/%s is %d bytes, more than the %d a document may be", webDir, d.name, size, maxDeclarationFile)
		}
		body, err := git(ctx, repo, "cat-file", "blob", d.oid)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(scratch, d.name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return nil, err
		}
		files = append(files, path)
	}

	out, err := cueExport(ctx, scratch, files)
	if err != nil {
		return nil, err
	}
	var d declaration
	if err := exactjson.Decode(out, &d); err != nil {
		return nil, invalid("the declaration is not what schema/web.cue exports: %w", err)
	}
	return &d, nil
}

// cueExport runs cue export --out json over the files, in dir. Only what
// the files decide is an *invalidDeclaration: a run that ends with
// cueRefused, and a run that prints more than maxCueOutput. Every other
// failure is an error of its own, which ends the pass: a cue that could
// not be started, that crashed, that was killed or that ran past
// cueTimeout, and a pass that was cancelled.
func cueExport(ctx context.Context, dir string, files []string) ([]byte, error) {
	cueCtx, cancel := context.WithTimeout(ctx, cueTimeout)
	defer cancel()
	cmd := exec.CommandContext(cueCtx, "cue", append([]string{"export", "--out", "json"}, files...)...)
	cmd.Dir = dir
	cmd.Env = cueEnvironment(os.Environ())
	stdout := &bounded{max: maxCueOutput, strict: true}
	stderr := &bounded{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	said := fmt.Sprint(err)
	if first, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); first != "" {
		said += ": " + first
	}
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case stdout.over:
		return nil, invalid("cue exported more than %d bytes", maxCueOutput)
	case cueCtx.Err() != nil:
		return nil, fmt.Errorf("cue took longer than %v, so the declaration was not read", cueTimeout)
	case errors.As(err, &exit) && exit.ExitCode() == cueRefused:
		return nil, invalid("cue export: %s", said)
	case errors.As(err, &exit):
		return nil, fmt.Errorf("cue did not finish reading the declaration: %s", said)
	case err != nil:
		return nil, fmt.Errorf("could not run cue: %w", err)
	}
	return stdout.Bytes(), nil
}

// checkSchema holds the schema to exporting on its own, as schema/web.cue
// does, before any declaration is read under it. Without it a schema that
// cannot be read would make every declaration invalid, and every project
// private.
func checkSchema(ctx context.Context, schema string) error {
	dir, err := os.MkdirTemp("", "valley-web-publish-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if _, err := cueExport(ctx, dir, []string{schema}); err != nil {
		return fmt.Errorf("the schema %s does not load: %w", schema, err)
	}
	return nil
}

// cueEnvironment is the caller's environment without CUE's own variables,
// so a declaration is read the same way whoever runs the publisher. The
// documents are copied into a scratch directory with no cue.mod, so cue
// resolves no import beyond its standard library.
func cueEnvironment(environ []string) []string {
	var env []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "CUE_") {
			env = append(env, kv)
		}
	}
	return env
}

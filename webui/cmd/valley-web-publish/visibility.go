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
	"strconv"
	"strings"
	"time"
)

// webDir is where a repository keeps its web visibility declaration.
const webDir = "policy/web"

// The bounds on a declaration: how many documents it may have, how large
// each may be, and how long cue may take over them. A declaration past
// them is invalid, so the project is private.
const (
	maxDeclarationFiles = 32
	maxDeclarationFile  = 64 << 10
	cueTimeout          = time.Minute
)

// errNoDeclaration is readDeclaration's error for a commit with no
// policy/web/*.cue.
var errNoDeclaration = errors.New("no policy/web declaration")

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
// document. Its error is errNoDeclaration when the commit has none, and
// another error when the declaration is invalid.
func readDeclaration(ctx context.Context, repo, commit, schema string) (*declaration, error) {
	listing, err := git(ctx, repo, "ls-tree", "-z", commit+":"+webDir)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errNoDeclaration // no such directory
	}
	type document struct{ name, oid string }
	var docs []document
	for _, entry := range strings.Split(string(listing), "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
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
		return nil, fmt.Errorf("%s holds %d documents, more than the %d a declaration may have", webDir, len(docs), maxDeclarationFiles)
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
			return nil, fmt.Errorf("%s/%s is %d bytes, more than the %d a document may be", webDir, d.name, size, maxDeclarationFile)
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

	cueCtx, cancel := context.WithTimeout(ctx, cueTimeout)
	defer cancel()
	cmd := exec.CommandContext(cueCtx, "cue", append([]string{"export", "--out", "json"}, files...)...)
	cmd.Dir = scratch
	cmd.Env = cueEnvironment(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		first, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		return nil, fmt.Errorf("cue export: %v: %s", err, first)
	}
	dec := json.NewDecoder(&stdout)
	dec.DisallowUnknownFields()
	var d declaration
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("the declaration is not what schema/web.cue exports: %w", err)
	}
	if err := dec.Decode(&json.RawMessage{}); err != io.EOF {
		return nil, errors.New("cue exported more than one document")
	}
	return &d, nil
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

package main

// checks prints which checks a change requires, as the valley-checks/v1
// JSON document: the Go deriver's answer (../derive) over the composed
// policy (../composed).
//
// It selects checks over two commits, a base and a head, and never over a
// working tree: what is derived is what a commit holds. The policy is
// three things named on the command line, the schema and the floor's and
// the project layer's directories of *.cue, so whoever calls it decides
// where each layer is read from. `valley checks` resolves the layers
// itself and does not run this verb.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"the-valley/composed"
	"the-valley/derive"
)

const checksUsage = "usage: valley-go checks [--schema <file>] --instance <dir> --project <dir> <base> <head>"

func runChecks(g git, args []string, w io.Writer) error {
	var schema, instance, project string
	var rest []string
	for i := 0; i < len(args); i++ {
		opt, value, inline := strings.Cut(args[i], "=")
		var dest *string
		switch opt {
		case "--schema":
			dest = &schema
		case "--instance":
			dest = &instance
		case "--project":
			dest = &project
		}
		switch {
		case dest == nil && strings.HasPrefix(args[i], "-"):
			return failure(checksUsage)
		case dest == nil:
			rest = append(rest, args[i])
		case inline:
			*dest = value
		case i+1 < len(args):
			*dest = args[i+1]
			i++
		default:
			return failure(checksUsage)
		}
	}
	if len(rest) != 2 || instance == "" || project == "" {
		return failure(checksUsage)
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	// The schema ships with the package, which names it in
	// VALLEY_VERIFICATION_SCHEMA; only the-valley's own tree carries one,
	// as `valley checks` reads it.
	if schema == "" {
		schema = os.Getenv("VALLEY_VERIFICATION_SCHEMA")
	}
	if schema == "" {
		root, err := g.output(true, "rev-parse", "--show-toplevel")
		if err != nil {
			return failure("no schema: name one with --schema or VALLEY_VERIFICATION_SCHEMA")
		}
		schema = filepath.Join(root, "schema", "verification.cue")
	}
	var commits [2]string
	for i, rev := range rest {
		oid, err := g.output(true, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
		if err != nil || !isObjectID(oid) {
			return failure("no such commit: " + rev)
		}
		commits[i] = oid
	}
	base, head := commits[0], commits[1]
	if _, err := exec.LookPath("cue"); err != nil {
		return failure("this verb needs the cue CLI; nix run .#valley bundles it")
	}

	ctx := context.Background()
	model, err := composed.Read(ctx, "cue", schema, instance, project)
	if err != nil {
		return failure(err.Error() + "; nothing derived")
	}
	paths, err := derive.Diff(ctx, g.dir, g.env, base, head)
	if err != nil {
		return failure(err.Error() + "; nothing derived")
	}
	res, err := derive.Derive(model, paths)
	if err != nil {
		return failure(err.Error() + "; nothing derived")
	}

	// Encoded whole before anything is written, so a reader gets the
	// document or nothing.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(checksDocument(base, head, model.Digest, res)); err != nil {
		return fmt.Errorf("could not encode the checks: %w", err)
	}
	_, err = w.Write(buf.Bytes())
	return err
}

// The valley-checks/v1 document. docs/architecture.md documents it, and a
// change to any field's name or meaning is a new schema version. Every
// list is a list, empty rather than null.
type jsonChecks struct {
	Schema            string         `json:"schema"`
	Base              string         `json:"base"`
	Head              string         `json:"head"`
	PolicyDigest      string         `json:"policy_digest"`
	Paths             []jsonPath     `json:"paths"`
	Classes           []string       `json:"classes"`
	UnclassifiedPaths []jsonPath     `json:"unclassified_paths"`
	Carry             string         `json:"carry"`
	Required          []jsonRequired `json:"required"`
}

// jsonRequired is one required check. attribute is given for the nix
// runner, command for the command runner, and validity only when the
// policy declares a window.
type jsonRequired struct {
	Name         string   `json:"name"`
	Classes      []string `json:"classes"`
	Unclassified bool     `json:"unclassified"`
	Pin          string   `json:"pin"`
	Cost         string   `json:"cost"`
	Runner       string   `json:"runner"`
	Attribute    string   `json:"attribute,omitempty"`
	Command      string   `json:"command,omitempty"`
	Validity     string   `json:"validity,omitempty"`
}

func checksDocument(base, head, digest string, r *derive.Result) jsonChecks {
	doc := jsonChecks{
		Schema:            "valley-checks/v1",
		Base:              base,
		Head:              head,
		PolicyDigest:      digest,
		Paths:             jsonPaths(r.Paths),
		Classes:           append([]string{}, r.Classes...),
		UnclassifiedPaths: jsonPaths(r.Unclassified),
		Carry:             r.Carry,
		Required:          []jsonRequired{},
	}
	for _, q := range r.Required {
		doc.Required = append(doc.Required, jsonRequired{
			Name:         q.Name,
			Classes:      append([]string{}, q.Classes...),
			Unclassified: q.Unclassified,
			Pin:          string(q.Pin),
			Cost:         q.Cost,
			Runner:       q.Runner,
			Attribute:    q.Attribute,
			Command:      q.Command,
			Validity:     q.Validity,
		})
	}
	return doc
}

// jsonPath is a path as git gave it, which is bytes that need not be
// UTF-8. JSON holds only UTF-8, and json.Marshal writes each byte that is
// not as U+FFFD, so such a path would not read back as git gave it. A path
// that is UTF-8 is written as {"path": "…"}, and any other as {"base64":
// "…"}, in standard base64, which reads back byte for byte.
type jsonPath string

func jsonPaths(paths []string) []jsonPath {
	out := make([]jsonPath, len(paths))
	for i, p := range paths {
		out[i] = jsonPath(p)
	}
	return out
}

func (p jsonPath) MarshalJSON() ([]byte, error) {
	var v any = struct {
		Path string `json:"path"`
	}{string(p)}
	if !utf8.ValidString(string(p)) {
		v = struct {
			Base64 string `json:"base64"`
		}{base64.StdEncoding.EncodeToString([]byte(p))}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

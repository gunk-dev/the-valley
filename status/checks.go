package main

// checks prints which checks a change requires: the Go deriver's answer
// (../derive) over the composed policy (../composed). It is the whole of
// `valley checks`.
//
// It selects checks over two commits, a base and a head, and never over a
// working tree: what is derived is what a commit holds. A layer named with
// --instance or --project is read as given, and nothing is fetched for it.
// A layer left unnamed is read at the target's tip: main on origin as this
// clone last fetched it, or else the local main. The base defaults to the
// merge base of that tip and the head.
//
// It prints a report for a person, the required names alone with --names,
// or the valley-checks/v1 JSON document with --json.

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
	"sort"
	"strings"
	"unicode/utf8"

	"the-valley/composed"
	"the-valley/derive"
)

const checksUsage = "usage: valley checks [--schema <file>] [--instance <dir>] [--project <dir>] [--names [--cost quick|slow] | --json] [<base> [<head>]]"

// checksTargets are the refs whose tip an unnamed layer and an unnamed
// base are read at, first found first.
var checksTargets = []string{"refs/remotes/origin/main", "refs/heads/main"}

func runChecks(g git, args []string, w io.Writer) error {
	var schema, instance, project, cost string
	var names, asJSON, costed bool
	needs := map[string]string{"--schema": "a file", "--instance": "a directory", "--project": "a directory", "--cost": "quick or slow"}
	dests := map[string]*string{"--schema": &schema, "--instance": &instance, "--project": &project, "--cost": &cost}
	i := 0
options:
	for ; i < len(args); i++ {
		opt, value, inline := strings.Cut(args[i], "=")
		switch {
		case args[i] == "--":
			i++
			break options
		case args[i] == "--names":
			names = true
		case args[i] == "--json":
			asJSON = true
		case dests[opt] != nil:
			if !inline {
				if i+1 >= len(args) {
					return failure(opt + " needs " + needs[opt])
				}
				i++
				value = args[i]
			}
			*dests[opt] = value
			costed = costed || opt == "--cost"
		case strings.HasPrefix(args[i], "-"):
			return failure("unknown option: " + args[i])
		default:
			break options
		}
	}
	revs := args[i:]
	if len(revs) > 2 || (asJSON && (names || costed)) {
		return failure(checksUsage)
	}
	if costed {
		if cost != "quick" && cost != "slow" {
			return failure("--cost takes quick or slow, not '" + cost + "'")
		}
		if !names {
			return failure("--cost filters the --names list; pass --names too")
		}
	}
	root, err := g.output(true, "rev-parse", "--show-toplevel")
	if err != nil {
		return failure("not a git repository")
	}
	if schema, err = schemaFile(g, schema); err != nil {
		return err
	}
	headRev := "HEAD"
	if len(revs) == 2 {
		headRev = revs[1]
	}
	head, err := commitOf(g, headRev)
	if err != nil {
		return err
	}

	// The target is what the layers are read out of, so it is needed
	// whenever either layer is left unnamed, and it is what an unnamed
	// base is the merge base with.
	var target, tip string
	if instance == "" || project == "" || len(revs) == 0 {
		// Only a ref that is not there falls through to the next. A read
		// of origin's that failed ends here: falling through would read
		// the policy out of the local main, which may be older.
		for _, ref := range checksTargets {
			oid, ok, err := g.ref(ref)
			if err != nil {
				return failure("could not read the main branch; nothing derived")
			} else if ok {
				target, tip = ref, oid
				break
			}
		}
		if target == "" {
			return failure("no main branch: the target's tip is where the policy layers are read from, and there is none here")
		}
	}
	var base string
	if len(revs) > 0 {
		if base, err = commitOf(g, revs[0]); err != nil {
			return err
		}
	} else if base, err = g.output(true, "merge-base", tip, head); err != nil || !isObjectID(base) {
		return failure(fmt.Sprintf("%s and %s do not share history — name a base", head, strings.TrimPrefix(target, "refs/")))
	}

	src, done, err := resolveLayers(g, tip, instance, project)
	defer done()
	if err != nil {
		return failure(err.Error() + "; nothing derived")
	}
	model, err := policyModel(schema, src.instance, src.project, !names)
	if err != nil {
		return err
	}
	ctx := context.Background()
	paths, err := derive.Diff(ctx, g.dir, g.env, base, head)
	if err != nil {
		return failure(err.Error() + "; nothing derived")
	}
	res, err := derive.Derive(model, paths)
	if err != nil {
		return failure(err.Error() + "; nothing derived")
	}

	// Each form is written whole once it is made, so a reader gets the
	// answer or nothing.
	var buf bytes.Buffer
	switch {
	case asJSON:
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(checksDocument(base, head, model.Digest, res)); err != nil {
			return fmt.Errorf("could not encode the checks: %w", err)
		}
	case names:
		for _, q := range res.Required {
			if cost == "" || costOf(q) == cost {
				fmt.Fprintln(&buf, q.Name)
			}
		}
	default:
		at := ""
		if tip != "" {
			short, err := g.output(true, "rev-parse", "--short", tip)
			if err != nil {
				return failure("could not read " + target + "; nothing derived")
			}
			at = strings.TrimPrefix(strings.TrimPrefix(target, "refs/remotes/"), "refs/heads/") + " " + short
		}
		label, plabel := src.labels(root, instance, project, at)
		shortBase, err1 := g.output(true, "rev-parse", "--short", base)
		shortHead, err2 := g.output(true, "rev-parse", "--short", head)
		if err1 != nil || err2 != nil {
			return failure("could not read the commits; nothing derived")
		}
		checksReport(&buf, shortBase+".."+shortHead, label+" + "+plabel, res)
	}
	_, err = w.Write(buf.Bytes())
	return err
}

// commitOf is the object ID of the commit rev names.
func commitOf(g git, rev string) (string, error) {
	oid, err := g.output(true, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil || !isObjectID(oid) {
		return "", failure("no such commit: " + rev)
	}
	return oid, nil
}

// costOf is a required check's cost as the report groups it: slow, or
// else quick.
func costOf(q derive.Required) string {
	if q.Cost == "slow" {
		return "slow"
	}
	return "quick"
}

// labels say where each layer was read from, as a reader can check it: a
// layer named is named as given, relative to the top level when it is
// under it, and a layer read at the target names the ref and the commit
// it stood at.
func (src layerSource) labels(root, instance, project, at string) (label, plabel string) {
	switch {
	case instance != "":
		label = strings.TrimPrefix(instance, root+"/")
	case src.ownValley:
		label = instanceLayer + "@" + at + " (this repository is its own valley)"
	default:
		label = src.valley + " " + instanceLayer + "@refs/heads/main"
	}
	switch {
	case project != "":
		plabel = strings.TrimPrefix(project, root+"/")
	case src.noProject:
		plabel = "no " + projectLayer + "@" + at + " — the floor alone"
	default:
		plabel = projectLayer + "@" + at
	}
	return label, plabel
}

// checksReport writes the report for a person: the change, the policy it
// was read under, what it matched, and each required check with its pin
// and the classes requiring it. A class requiring a check is listed with
// the unclassified set, which requires checks of a path no class covers.
func checksReport(w io.Writer, change, policy string, r *derive.Result) {
	fmt.Fprintf(w, "%s: %d changed path(s)\n", change, len(r.Paths))
	fmt.Fprintf(w, "policy: %s\n", policy)
	matched := append([]string{}, r.Classes...)
	if len(r.Unclassified) > 0 {
		matched = append(matched, "unclassified")
	}
	sort.Strings(matched)
	fmt.Fprintf(w, "classes matched: %s\n", joinOrNone(matched))
	// How far an approval of this change carries across a rebase. Nothing
	// here acts on it.
	fmt.Fprintf(w, "carry mode: %s\n\n", r.Carry)

	width, unclassified := 0, false
	var quick, slow []string
	for _, q := range r.Required {
		width = max(width, len(q.Name))
		unclassified = unclassified || q.Unclassified
		if costOf(q) == "slow" {
			slow = append(slow, q.Name)
		} else {
			quick = append(quick, q.Name)
		}
	}
	if len(r.Required) == 0 {
		fmt.Fprintln(w, "required checks: none")
	} else {
		fmt.Fprintln(w, "required checks:")
		for _, q := range r.Required {
			by := append([]string{}, q.Classes...)
			if q.Unclassified {
				by = append(by, "unclassified")
			}
			sort.Strings(by)
			fmt.Fprintf(w, "  %-*s  %-9s  %s\n", width, q.Name, q.Pin, strings.Join(by, ", "))
		}
	}

	if len(r.Unclassified) > 0 {
		fmt.Fprintf(w, "\nunclassified: %d path(s) matched no class\n", len(r.Unclassified))
		for _, p := range r.Unclassified {
			fmt.Fprintf(w, "  %s\n", p)
		}
		// The failure the unclassified set exists to prevent: paths
		// outside every class that the policy then asks nothing of.
		if !unclassified {
			fmt.Fprintln(w, "  the policy requires nothing of them — it is behind the tree")
		}
	}

	// Below every row, and unindented, so neither line reads as a
	// required check. The grouping changes nothing required: approve runs
	// every required check, and the integrator requires evidence for every
	// one, whatever it costs.
	if len(r.Required) > 0 {
		fmt.Fprintf(w, "\nquick: %s\n", joinOrNone(quick))
		fmt.Fprintf(w, "slow: %s\n", joinOrNone(slow))
	}
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// derivedChecks is the valley-checks/v1 document for the change between
// two commits, under the schema and the two layers named. An empty schema
// is the packaged one, or else the one in this repository's tree. The
// check worker (worker.go) calls it for the checks to build, and reads no
// requirement's pin, so each pin is unknown.
func derivedChecks(g git, schema, instance, project, baseRev, headRev string) (jsonChecks, error) {
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return jsonChecks{}, failure("not a git repository")
	}
	schema, err := schemaFile(g, schema)
	if err != nil {
		return jsonChecks{}, err
	}
	var commits [2]string
	for i, rev := range []string{baseRev, headRev} {
		oid, err := g.output(true, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
		if err != nil || !isObjectID(oid) {
			return jsonChecks{}, failure("no such commit: " + rev)
		}
		commits[i] = oid
	}
	model, err := policyModel(schema, instance, project, false)
	if err != nil {
		return jsonChecks{}, err
	}
	return deriveUnder(g, model, commits[0], commits[1])
}

// schemaFile is the verification schema named, or when none is, the
// packaged one, or else the one in this repository's tree.
func schemaFile(g git, schema string) (string, error) {
	// The schema ships with the package, which names it in
	// VALLEY_VERIFICATION_SCHEMA; only the-valley's own tree carries one,
	// as `valley checks` reads it.
	if schema == "" {
		schema = os.Getenv("VALLEY_VERIFICATION_SCHEMA")
	}
	if schema == "" {
		root, err := g.output(true, "rev-parse", "--show-toplevel")
		if err != nil {
			return "", failure("no schema: name one with --schema or VALLEY_VERIFICATION_SCHEMA")
		}
		schema = filepath.Join(root, "schema", "verification.cue")
	}
	return schema, nil
}

// policyModel is the policy the schema and the two layers compose, read
// once for every change derived under it. pinned asks cue which
// requirements are mandatory (composed.Read), one cue vet each, for the
// readers that print a pin: valley checks' report and its document. Every
// other reader leaves them unknown (composed.ReadUnpinned).
func policyModel(schema, instance, project string, pinned bool) (*composed.Model, error) {
	if _, err := exec.LookPath("cue"); err != nil {
		return nil, failure("this verb needs the cue CLI; nix run .#valley bundles it")
	}
	read := composed.Read
	if !pinned {
		read = composed.ReadUnpinned
	}
	model, err := read(context.Background(), "cue", schema, instance, project)
	if err != nil {
		return nil, failure(err.Error() + "; nothing derived")
	}
	return model, nil
}

// deriveUnder is the valley-checks/v1 document for the change between two
// commits, given as object IDs, under a policy read.
func deriveUnder(g git, model *composed.Model, base, head string) (jsonChecks, error) {
	ctx := context.Background()
	paths, err := derive.Diff(ctx, g.dir, g.env, base, head)
	if err != nil {
		return jsonChecks{}, failure(err.Error() + "; nothing derived")
	}
	res, err := derive.Derive(model, paths)
	if err != nil {
		return jsonChecks{}, failure(err.Error() + "; nothing derived")
	}
	return checksDocument(base, head, model.Digest, res), nil
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
	Follows           []jsonFollow   `json:"follows"`
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

// jsonFollow is one check the change sets due on main without requiring
// it, with the matched classes it follows.
type jsonFollow struct {
	Name      string   `json:"name"`
	Classes   []string `json:"classes"`
	Cost      string   `json:"cost"`
	Runner    string   `json:"runner"`
	Attribute string   `json:"attribute,omitempty"`
	Command   string   `json:"command,omitempty"`
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
		Follows:           []jsonFollow{},
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
	for _, f := range r.Follows {
		doc.Follows = append(doc.Follows, jsonFollow{
			Name:      f.Name,
			Classes:   append([]string{}, f.Classes...),
			Cost:      f.Cost,
			Runner:    f.Runner,
			Attribute: f.Attribute,
			Command:   f.Command,
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

package change

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A restack report is what the host restacker observed when it tried to
// rebase a ready change onto its target: the inputs it read, what came of
// the attempt, and what it published. It lives at
// refs/the-valley/restack/<target>/<change ID>. The ref points at a report
// commit whose tree holds one file, report.json, in the schema
// valley-restack-report/v1:
//
//	{
//	  "schema": "valley-restack-report/v1",
//	  "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
//	  "target": "main",
//	  "target_tip": "git-sha1:<40 hex>",
//	  "operation": "20261017T203500Z-1",
//	  "result": "conflict",
//	  "layers": [
//	    {"change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
//	     "record": "git-sha1:<40 hex>", "version": 2, "branch": "topic",
//	     "head": "git-sha1:<40 hex>", "published": null}
//	  ],
//	  "failing_layer": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
//	  "conflict_paths": ["src/main.go"],
//	  "blocking_requests": [],
//	  "refusal": null
//	}
//
// The layers are the changes the attempt rebased together, bottom first,
// each as it was read: its record commit, its active version, and that
// version's branch and head. published is null for a layer whose branch
// the attempt did not move. Otherwise it is the head the branch was moved
// to, and the version and record commit that recorded it, both null when
// no version was added.
//
// Each report commit has the report it replaces as its one parent, and the
// first has none, so the ref holds the change's whole history of reports.
//
// A report is an observation and grants nothing. It is not evidence that
// a version's content is unchanged or approved. A reader decides whether
// it still applies by comparing what it observed with what origin holds
// now.
//
// It is read as strictly as a record: valid UTF-8, no key twice, no field
// the schema does not have, every field of exactly its type, and every
// string and list within its bound. The push hook reads a pushed report
// with ParseRestackReport, and valley status reads it back the same way.

// RestackSchema names the report format.
const RestackSchema = "valley-restack-report/v1"

// RestackPrefix opens every restack report's ref.
const RestackPrefix = "refs/the-valley/restack/"

// RestackRef is the ref a change's restack report lives at.
func RestackRef(target, id string) string {
	return RestackPrefix + target + "/" + id
}

// ParseRestackRef splits a report's ref into its target and change ID. It
// reports false for a ref that is not refs/the-valley/restack/<target>/<ID>
// with a branch name as the target and a change ID as the ID.
func ParseRestackRef(ref string) (target, id string, ok bool) {
	rest, found := strings.CutPrefix(ref, RestackPrefix)
	if !found {
		return "", "", false
	}
	slash := strings.LastIndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	target, id = rest[:slash], rest[slash+1:]
	if !ValidBranch(target) || !ValidID(id) {
		return "", "", false
	}
	return target, id, true
}

// What came of an attempt. A conflict and a block move no branch and add
// no version. partial is an attempt that moved its branches and did not
// add every version; complete is one that did both. superseded is an
// attempt given up because its inputs moved while it ran.
const (
	RestackConflict   = "conflict"
	RestackBlocked    = "blocked"
	RestackPartial    = "partial"
	RestackComplete   = "complete"
	RestackSuperseded = "superseded"
)

// The bounds a report is held to.
const (
	MaxRestackLayers   = 64   // layers in one report
	MaxConflictPaths   = 100  // conflict paths in one report
	MaxConflictPath    = 1024 // bytes in one conflict path
	MaxBlockingRequest = 64   // blocking requests in one report
	MaxRequestRef      = 256  // bytes in one blocking request's ref
	MaxRefusal         = 2048 // bytes in a refusal
	MaxOperation       = 64   // bytes in an operation identifier
)

// The bounds on the objects that hold reports, which every reader holds a
// report to before it reads anything past them. A report commit names a
// tree, at most one parent, an author, a committer and a short message,
// and its tree names one file.
const (
	MaxReportCommit  = 4 << 10  // bytes in a report commit
	MaxReportTree    = 1 << 10  // bytes in its tree
	MaxReportBlob    = 64 << 10 // bytes in its report.json
	MaxReportUpdates = 64       // report updates in one push
)

// RestackReport is one restack report.
type RestackReport struct {
	Schema    string         `json:"schema"`
	ChangeID  string         `json:"change_id"`
	Target    string         `json:"target"`
	TargetTip string         `json:"target_tip"`
	Operation string         `json:"operation"`
	Result    string         `json:"result"`
	Layers    []RestackLayer `json:"layers"`
	Failing   *string        `json:"failing_layer"`
	Conflicts []string       `json:"conflict_paths"`
	Requests  []string       `json:"blocking_requests"`
	Refusal   *string        `json:"refusal"`
}

// RestackLayer is one change an attempt rebased, as the attempt read it,
// and what it published for it.
type RestackLayer struct {
	ChangeID  string            `json:"change_id"`
	Record    string            `json:"record"`
	Version   int               `json:"version"`
	Branch    string            `json:"branch"`
	Head      string            `json:"head"`
	Published *RestackPublished `json:"published"`
}

// RestackPublished is what an attempt published for a layer: the head its
// branch was moved to, and the version and record commit that recorded
// that head, both nil when no version was added.
type RestackPublished struct {
	Head    string  `json:"head"`
	Version *int    `json:"version"`
	Record  *string `json:"record"`
}

// Layer is the layer of the change given, if the report has one.
func (r *RestackReport) Layer(id string) (RestackLayer, bool) {
	for _, l := range r.Layers {
		if l.ChangeID == id {
			return l, true
		}
	}
	return RestackLayer{}, false
}

// ParseRestackReport reads report.json, strictly.
func ParseRestackReport(data []byte) (*RestackReport, error) {
	v, err := readJSON(data)
	if err != nil {
		return nil, err
	}
	o, err := fieldsOf(v, "the report", "schema", "change_id", "target", "target_tip", "operation", "result",
		"layers", "failing_layer", "conflict_paths", "blocking_requests", "refusal")
	if err != nil {
		return nil, err
	}
	r := &RestackReport{}
	for _, f := range []struct {
		name string
		into *string
	}{
		{"schema", &r.Schema}, {"change_id", &r.ChangeID}, {"target", &r.Target},
		{"target_tip", &r.TargetTip}, {"operation", &r.Operation}, {"result", &r.Result},
	} {
		if *f.into, err = stringOf(o[f.name], f.name); err != nil {
			return nil, err
		}
	}
	if r.Failing, err = optionalString(o["failing_layer"], "failing_layer"); err != nil {
		return nil, err
	}
	if r.Refusal, err = optionalString(o["refusal"], "refusal"); err != nil {
		return nil, err
	}
	if r.Conflicts, err = stringsOf(o["conflict_paths"], "conflict_paths", MaxConflictPaths); err != nil {
		return nil, err
	}
	if r.Requests, err = stringsOf(o["blocking_requests"], "blocking_requests", MaxBlockingRequest); err != nil {
		return nil, err
	}
	layers := o["layers"]
	if layers.kind != kindArray {
		return nil, fmt.Errorf("its layers is %s, where an array is expected", layers.kind)
	}
	if len(layers.items) > MaxRestackLayers {
		return nil, fmt.Errorf("it holds %d layers, more than the %d a report may", len(layers.items), MaxRestackLayers)
	}
	r.Layers = []RestackLayer{}
	for i, item := range layers.items {
		l, err := layerOf(item, fmt.Sprintf("layers[%d]", i))
		if err != nil {
			return nil, err
		}
		r.Layers = append(r.Layers, l)
	}
	if err := r.Check(); err != nil {
		return nil, err
	}
	return r, nil
}

func layerOf(v *value, where string) (RestackLayer, error) {
	o, err := fieldsOf(v, where, "change_id", "record", "version", "branch", "head", "published")
	if err != nil {
		return RestackLayer{}, err
	}
	var l RestackLayer
	for _, f := range []struct {
		name string
		into *string
	}{
		{"change_id", &l.ChangeID}, {"record", &l.Record}, {"branch", &l.Branch}, {"head", &l.Head},
	} {
		if *f.into, err = stringOf(o[f.name], where+"."+f.name); err != nil {
			return RestackLayer{}, err
		}
	}
	if l.Version, err = integerOf(o["version"], where+".version", MaxVersions); err != nil {
		return RestackLayer{}, err
	}
	p := o["published"]
	if isNull(p) {
		return l, nil
	}
	where += ".published"
	po, err := fieldsOf(p, where, "head", "version", "record")
	if err != nil {
		return RestackLayer{}, err
	}
	l.Published = &RestackPublished{}
	if l.Published.Head, err = stringOf(po["head"], where+".head"); err != nil {
		return RestackLayer{}, err
	}
	if !isNull(po["version"]) {
		n, err := integerOf(po["version"], where+".version", MaxVersions)
		if err != nil {
			return RestackLayer{}, err
		}
		l.Published.Version = &n
	}
	if l.Published.Record, err = optionalString(po["record"], where+".record"); err != nil {
		return RestackLayer{}, err
	}
	return l, nil
}

// Check holds a report to everything the schema says beyond the types of
// its fields: the formats and bounds of its strings and lists, and what
// its fields say about one another. The change it is about is one of its
// layers, no layer appears twice, and a failing layer is one of them. A
// conflict names its failing layer. A conflict or a block published
// nothing, and a complete attempt published a version for every layer. A
// published version comes with the record commit that holds it.
func (r *RestackReport) Check() error {
	switch {
	case r.Schema != RestackSchema:
		return fmt.Errorf("its schema is %q, where %q is expected", r.Schema, RestackSchema)
	case !ValidID(r.ChangeID):
		return fmt.Errorf("its change_id %q is not a change ID: 32 letters from k to z", r.ChangeID)
	case !ValidBranch(r.Target) || !utf8.ValidString(r.Target):
		return fmt.Errorf("its target %q is not a branch name in UTF-8", r.Target)
	case !ValidHead(r.TargetTip):
		return fmt.Errorf("its target_tip %q is not %s and 40 lowercase hex digits", r.TargetTip, HeadPrefix)
	case !operationName.MatchString(r.Operation) || len(r.Operation) > MaxOperation:
		return fmt.Errorf("its operation %q is not 1 to %d letters, digits, dots, colons, dashes and underscores", r.Operation, MaxOperation)
	case !validResult(r.Result):
		return fmt.Errorf("its result is %q, where %q, %q, %q, %q or %q is expected", r.Result,
			RestackConflict, RestackBlocked, RestackPartial, RestackComplete, RestackSuperseded)
	case len(r.Layers) == 0:
		return errors.New("it holds no layer, and a report holds at least the change it is about")
	case len(r.Layers) > MaxRestackLayers:
		return fmt.Errorf("it holds %d layers, more than the %d a report may", len(r.Layers), MaxRestackLayers)
	case len(r.Conflicts) > MaxConflictPaths:
		return fmt.Errorf("it holds %d conflict paths, more than the %d a report may", len(r.Conflicts), MaxConflictPaths)
	case len(r.Requests) > MaxBlockingRequest:
		return fmt.Errorf("it holds %d blocking requests, more than the %d a report may", len(r.Requests), MaxBlockingRequest)
	case r.Refusal != nil && !validText(*r.Refusal, MaxRefusal):
		return fmt.Errorf("its refusal is not 1 to %d bytes free of control characters", MaxRefusal)
	}
	seen := map[string]bool{}
	published, versioned := false, true
	for i, l := range r.Layers {
		where := fmt.Sprintf("layers[%d]", i)
		switch {
		case !ValidID(l.ChangeID):
			return fmt.Errorf("its %s.change_id %q is not a change ID: 32 letters from k to z", where, l.ChangeID)
		case seen[l.ChangeID]:
			return fmt.Errorf("its %s names the change %s, which an earlier layer names", where, l.ChangeID)
		case !ValidHead(l.Record):
			return fmt.Errorf("its %s.record %q is not %s and 40 lowercase hex digits", where, l.Record, HeadPrefix)
		case l.Version < 1 || l.Version > MaxVersions:
			return fmt.Errorf("its %s.version is %d, outside 1 to %d", where, l.Version, MaxVersions)
		case !ValidBranch(l.Branch) || !utf8.ValidString(l.Branch):
			return fmt.Errorf("its %s.branch %q is not a branch name in UTF-8", where, l.Branch)
		case !ValidHead(l.Head):
			return fmt.Errorf("its %s.head %q is not %s and 40 lowercase hex digits", where, l.Head, HeadPrefix)
		}
		seen[l.ChangeID] = true
		p := l.Published
		if p == nil {
			versioned = false
			continue
		}
		published = true
		switch {
		case !ValidHead(p.Head):
			return fmt.Errorf("its %s.published.head %q is not %s and 40 lowercase hex digits", where, p.Head, HeadPrefix)
		case (p.Version == nil) != (p.Record == nil):
			return fmt.Errorf("its %s.published names a version without its record, or a record without its version, and the two come together", where)
		case p.Version != nil && (*p.Version < 1 || *p.Version > MaxVersions):
			return fmt.Errorf("its %s.published.version is %d, outside 1 to %d", where, *p.Version, MaxVersions)
		case p.Record != nil && !ValidHead(*p.Record):
			return fmt.Errorf("its %s.published.record %q is not %s and 40 lowercase hex digits", where, *p.Record, HeadPrefix)
		}
		versioned = versioned && p.Version != nil
	}
	for i, path := range r.Conflicts {
		if !validText(path, MaxConflictPath) {
			return fmt.Errorf("its conflict_paths[%d] is not 1 to %d bytes free of control characters", i, MaxConflictPath)
		}
	}
	for i, ref := range r.Requests {
		name, ok := strings.CutPrefix(ref, "refs/the-valley/integration-requests/"+r.Target+"/")
		if !ok || !ValidBranch(name) || len(ref) > MaxRequestRef {
			return fmt.Errorf("its blocking_requests[%d] %q is not an integration request's ref on %s of at most %d bytes", i, ref, r.Target, MaxRequestRef)
		}
	}
	switch {
	case !seen[r.ChangeID]:
		return fmt.Errorf("no layer names the change %s, and a report is about one of its layers", r.ChangeID)
	case r.Failing != nil && !seen[*r.Failing]:
		return fmt.Errorf("its failing_layer %q is none of its layers", *r.Failing)
	case r.Result == RestackConflict && r.Failing == nil:
		return errors.New("its result is conflict, and a conflict names its failing_layer")
	case (r.Result == RestackConflict || r.Result == RestackBlocked) && published:
		return fmt.Errorf("its result is %s, and a %s publishes nothing", r.Result, r.Result)
	case r.Result == RestackComplete && !versioned:
		return errors.New("its result is complete, and a complete attempt published a version for every layer")
	}
	return nil
}

// EncodeRestackReport writes a report as report.json. It refuses a report
// ParseRestackReport would refuse, so what it writes always reads back as
// the same report.
func EncodeRestackReport(r *RestackReport) ([]byte, error) {
	if err := r.Check(); err != nil {
		return nil, fmt.Errorf("the report cannot be written: %w", err)
	}
	out := *r
	if out.Conflicts == nil {
		out.Conflicts = []string{}
	}
	if out.Requests == nil {
		out.Requests = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// operationName is the shape of an operation identifier: a local name the
// restacker gives one attempt.
var operationName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]*$`)

func validResult(s string) bool {
	switch s {
	case RestackConflict, RestackBlocked, RestackPartial, RestackComplete, RestackSuperseded:
		return true
	}
	return false
}

// validText reports whether s is 1 to max bytes of UTF-8 holding no
// control character.
func validText(s string, max int) bool {
	if len(s) < 1 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

func isNull(v *value) bool {
	return v.kind == kindLiteral && v.text == "null"
}

// optionalString reads a string or null.
func optionalString(v *value, name string) (*string, error) {
	if isNull(v) {
		return nil, nil
	}
	s, err := stringOf(v, name)
	if err != nil {
		return nil, fmt.Errorf("its %s is %s, where a string or null is expected", name, v.kind)
	}
	return &s, nil
}

// stringsOf reads an array of at most max strings.
func stringsOf(v *value, name string, max int) ([]string, error) {
	if v.kind != kindArray {
		return nil, fmt.Errorf("its %s is %s, where an array is expected", name, v.kind)
	}
	if len(v.items) > max {
		return nil, fmt.Errorf("its %s holds %d entries, more than the %d it may", name, len(v.items), max)
	}
	list := []string{}
	for i, item := range v.items {
		s, err := stringOf(item, fmt.Sprintf("%s[%d]", name, i))
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, nil
}

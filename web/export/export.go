package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"unicode"
	"unicode/utf8"

	"the-valley/exactjson"
	"the-valley/knot"
)

// Schema is the export.json document's schema. A change to any field's
// name or meaning is a new schema version.
const Schema = "valley-web-export/v2"

// Export is export.json: the projects of one valley that the publisher
// exports, as a reader may see them. It says nothing of who may read
// them. Each project carries its publication identity, and a signed
// authorization (see Authorization) says which readers may read which
// project and identity. A server serves a project only as its current
// authorization allows.
type Export struct {
	Schema string `json:"schema"`
	// Collected is when the plan was read (valley-plan/v1's now), in
	// RFC 3339. It is display-time: the content digest leaves it out.
	Collected string `json:"collected"`
	// Groups are the groups the projects belong to, by identifier, and
	// no other. An export with none omits it, as v1 exports before
	// groups did.
	Groups   []Group   `json:"groups,omitempty"`
	Projects []Project `json:"projects"`
}

// Project is one exported project. Its two tips are different refs: the
// graph tip is refs/the-valley/knots, which its knots were read at, and
// the source tip is main, which its bundle carries.
type Project struct {
	Name string `json:"project"`
	// Identity is the project's publication identity, as its policy/read
	// declares it: 32 lowercase hex digits. An authorization names a
	// project by its name and this identity together.
	Identity string `json:"identity"`
	// Group is the identifier of the project's group. WebName is its name
	// within the group, the second segment of its web URLs, which Name,
	// its identifier, need not be. FormerNames are its former web names
	// within the group, and Aliases its short aliases. A project with no
	// group, as in a v1 export from before groups, has no web URL, and a
	// server serves it as one the export does not hold.
	Group       string   `json:"group,omitempty"`
	WebName     string   `json:"name,omitempty"`
	FormerNames []string `json:"former_names,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	// GraphTip is "" for a project with no plan graph.
	GraphTip  string `json:"graph_tip"`
	SourceTip string `json:"source_tip"`
	// Roots are the outcomes with no parent, in tree order.
	Roots []string `json:"roots"`
	// Knots are every knot, in tree order, each with its body and comments.
	Knots []Knot `json:"knots"`
	// InFlight are the active knots that are claimed, the oldest claim
	// first, then the active knots whose status is in-progress and that
	// nobody claims, in tree order.
	InFlight      []string    `json:"in_flight"`
	OpenResiduals []Residuals `json:"open_residuals"`
	// Changes are the changes ready for approval: ready, with an active
	// version that has not landed.
	Changes []Change `json:"changes"`
}

// Knot is one knot: its fields, what the graph derives of it, its edges to
// other projects' knots, and its body and comments. Every principal name
// in it has passed through the publisher's display mapping.
type Knot struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Status        string   `json:"status"`
	Active        bool     `json:"active"`
	Title         string   `json:"title"`
	Created       string   `json:"created"`
	CreatedBy     string   `json:"created_by"`
	Source        string   `json:"source,omitempty"`
	Parent        string   `json:"parent,omitempty"`
	Owner         string   `json:"owner,omitempty"`
	BlockedBy     []string `json:"blocked_by"`
	Changes       []string `json:"changes"`
	Target        string   `json:"target,omitempty"`
	GraduatedInto string   `json:"graduated_into,omitempty"`

	// State is valley-plan/v1's: ready, in-progress, blocked, active,
	// done or abandoned.
	State     string   `json:"state"`
	Ready     bool     `json:"ready"`
	WaitingOn []string `json:"waiting_on"`
	// WaitingOnHidden is set when the knot also waits on one or more
	// knots of projects the export does not hold, which WaitingOn leaves
	// out. It says neither which nor how many. An export with no such
	// knot omits it everywhere, as v1 exports before it did.
	WaitingOnHidden bool      `json:"waiting_on_hidden,omitempty"`
	ClearedByEnding []string  `json:"cleared_by_ending"`
	Dangling        []string  `json:"dangling"`
	Unresolved      []string  `json:"unresolved"`
	Children        []string  `json:"children"`
	Blocks          []string  `json:"blocks"`
	Progress        *Progress `json:"progress,omitempty"`
	Claim           *Claim    `json:"claim,omitempty"`
	Edges           []Edge    `json:"edges"`

	Body         string    `json:"body"`
	Comments     []Comment `json:"comments"`
	CommentCount int       `json:"comment_count"`
}

// Progress is a parent's finished descendants, out of those not abandoned.
type Progress struct {
	Done int `json:"done"`
	Of   int `json:"of"`
}

// Claim is a knot's claim. AgeSeconds and Stale are as of Collected; they
// are display-time, and a reader derives a claim's age from At.
type Claim struct {
	By         string `json:"by"`
	At         string `json:"at"`
	AgeSeconds int64  `json:"age_seconds"`
	Stale      bool   `json:"stale"`
}

// Edge is a reference to another project's knot, from blocked_by or
// target. State is resolved or dangling, as valley-plan/v1 says, or hidden
// when the project is not exported. A hidden edge carries the reference
// alone: no status, no title and no why.
type Edge struct {
	Field   string  `json:"field"`
	Ref     string  `json:"ref"`
	Project string  `json:"project"`
	ID      string  `json:"id"`
	State   string  `json:"state"`
	Why     *string `json:"why"`
	Status  *string `json:"status"`
	Title   *string `json:"title"`
}

// Edge states.
const (
	EdgeResolved = "resolved"
	EdgeDangling = "dangling"
	EdgeHidden   = "hidden"
)

// Comment is one comment on a knot, oldest first. Op is the operation that
// wrote it, which with the knot names the comment. A comment that has been
// edited says when it was last edited, in Edited, and by whom, in
// EditedBy; one never edited has neither.
type Comment struct {
	Author   string `json:"author"`
	At       string `json:"at"`
	Op       string `json:"op"`
	Edited   string `json:"edited,omitempty"`
	EditedBy string `json:"edited_by,omitempty"`
	Body     string `json:"body"`
}

// Residuals are the open bugs under one parent, or under none.
type Residuals struct {
	Parent *string  `json:"parent"`
	Bugs   []string `json:"bugs"`
}

// Change is a change ready for approval, with its active version alone.
type Change struct {
	ChangeID string  `json:"change_id"`
	Handle   string  `json:"handle"`
	Target   string  `json:"target"`
	Owner    string  `json:"owner"`
	Version  Version `json:"version"`
	// Includes are the changes this one includes that the export carries.
	Includes []string  `json:"includes"`
	Knots    []KnotRef `json:"knots"`
}

// Version is a change's active version.
type Version struct {
	N        int    `json:"n"`
	Head     string `json:"head"`
	Title    string `json:"title"`
	Uploader string `json:"uploader"`
	// Landing is not-landed or uncertain: a ready change's active version
	// has not landed.
	Landing string   `json:"landing"`
	Request *Request `json:"request"`
	Outcome *Outcome `json:"outcome"`
}

// Request is the integration request that approves the version's head.
type Request struct {
	Commit string `json:"commit"`
}

// Outcome is the integrator's last decision about the version.
type Outcome struct {
	Decision string  `json:"decision"`
	Head     string  `json:"head"`
	Tip      *string `json:"tip"`
}

// KnotRef is a knot of the same project that links a change.
type KnotRef struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Title  string `json:"title"`
}

// Encode writes the export as export.json's bytes: compact JSON and a
// closing newline.
func (e *Export) Encode() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Decode reads export.json strictly: a key that is not the name of a
// field the schema names, spelled exactly, a key twice in one object,
// anything but white space after the document, or an export Validate
// refuses, fails the read.
func Decode(data []byte) (*Export, error) {
	return DecodeContext(context.Background(), bytes.NewReader(data))
}

// DecodeContext reads and validates an export while ctx remains active.
// It reads the document twice, as exactjson.Decode does: first through
// exactjson.Check, which refuses a key not spelled exactly as a field's
// name, a key twice in one object, an array or object where the field
// holds none, and nesting past exactjson.MaxDepth, and then into the
// export. Both reads check ctx between tokens and within long strings.
func DecodeContext(ctx context.Context, r io.Reader) (*Export, error) {
	var data bytes.Buffer
	dec := newTokenDecoder(ctx, io.TeeReader(r, &data))
	if err := exactjson.Check(dec, reflect.TypeFor[Export]()); err != nil {
		if stopped := ctx.Err(); stopped != nil {
			return nil, stopped
		}
		return nil, fmt.Errorf("export.json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("export.json holds more than its one document")
	}
	var e Export
	if err := decodeValue(ctx, newTokenDecoder(ctx, &data), reflect.ValueOf(&e).Elem()); err != nil {
		return nil, fmt.Errorf("export.json: %w", err)
	}
	if err := e.ValidateContext(ctx); err != nil {
		return nil, err
	}
	return &e, nil
}

// Validate holds an export to what a server may route by and show without
// further checks: its schema, project names that are safe as file names,
// one entry per project in name order, object IDs for tips, and knot IDs
// that are unique within their project. Its groups and the places of its
// projects pass CheckNames, and each group is some project's. Text fields
// are valid UTF-8 with no control character other than a newline or a
// tab.
func (e *Export) Validate() error {
	return e.ValidateContext(context.Background())
}

// ValidateContext checks the export while ctx remains active.
func (e *Export) ValidateContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.Schema != Schema {
		return fmt.Errorf("export.json has schema %q, not %q", e.Schema, Schema)
	}
	for i, p := range e.Projects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ValidName(p.Name) {
			return fmt.Errorf("export.json names a project %q, which is not a project name", p.Name)
		}
		if i > 0 && e.Projects[i-1].Name >= p.Name {
			return fmt.Errorf("export.json lists project %s out of order or twice", p.Name)
		}
		if !ValidIdentity(p.Identity) {
			return fmt.Errorf("project %s has identity %q, which is not 32 lowercase hex digits", p.Name, p.Identity)
		}
		if p.GraphTip != "" && !ValidObjectID(p.GraphTip) {
			return fmt.Errorf("project %s has graph tip %q, which is not an object ID", p.Name, p.GraphTip)
		}
		if !ValidObjectID(p.SourceTip) {
			return fmt.Errorf("project %s has source tip %q, which is not an object ID", p.Name, p.SourceTip)
		}
		seen := map[string]bool{}
		for _, k := range p.Knots {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !knot.IsLocalID(k.ID) {
				return fmt.Errorf("project %s holds a knot %q, which is not a knot ID", p.Name, k.ID)
			}
			if seen[k.ID] {
				return fmt.Errorf("project %s holds knot %s twice", p.Name, k.ID)
			}
			seen[k.ID] = true
			for _, s := range []string{k.Title, k.Body, k.Source, k.CreatedBy, k.Owner} {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !validTextContext(ctx, s) {
					if err := ctx.Err(); err != nil {
						return err
					}
					return fmt.Errorf("knot %s:%s holds text that is not valid UTF-8 or holds a control character", p.Name, k.ID)
				}
			}
			for _, c := range k.Comments {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !validTextContext(ctx, c.Body) || !validTextContext(ctx, c.Author) || !validTextContext(ctx, c.EditedBy) || !validTextContext(ctx, c.Edited) {
					if err := ctx.Err(); err != nil {
						return err
					}
					return fmt.Errorf("a comment on knot %s:%s holds text that is not valid UTF-8 or holds a control character", p.Name, k.ID)
				}
			}
		}
		for _, c := range p.Changes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !ValidObjectID(c.Version.Head) {
				return fmt.Errorf("change %s of project %s has head %q, which is not an object ID", c.ChangeID, p.Name, c.Version.Head)
			}
		}
	}
	return e.checkGroups(ctx)
}

// checkGroups holds the export's groups and the places of its projects,
// which are in project order already, to CheckNames, and each group to
// having a project, so an export names no group that only private projects
// belong to. It checks ctx before each project and each group.
func (e *Export) checkGroups(ctx context.Context) error {
	var placed []Placed
	named := map[string]bool{}
	for _, p := range e.Projects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.Group != "" || p.WebName != "" || len(p.FormerNames) > 0 || len(p.Aliases) > 0 {
			placed = append(placed, Placed{p.Name, Placement{Group: p.Group, Name: p.WebName, FormerNames: p.FormerNames, Aliases: p.Aliases}})
			named[p.Group] = true
		}
	}
	if err := CheckNames(ctx, e.Groups, placed); err != nil {
		if stopped := ctx.Err(); stopped != nil {
			return stopped
		}
		return fmt.Errorf("export.json: %w", err)
	}
	for _, g := range e.Groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !named[g.ID] {
			return fmt.Errorf("export.json lists group %s, which none of its projects belongs to", g.ID)
		}
	}
	return nil
}

// ValidName reports whether s is a project's name: the plan graph's rule
// for a project's or a principal's name. Such a name holds no slash and
// cannot be a dot segment, so it is safe as a file name.
func ValidName(s string) bool { return knot.IsName(s) }

// ValidIdentity reports whether s is a publication identity: 32 lowercase
// hex digits, as schema/read.cue's #Identity.
func ValidIdentity(s string) bool { return len(s) == 32 && isHex(s) }

// ValidObjectID reports whether s is a full git object ID, SHA-1 or
// SHA-256, in lowercase hex.
func ValidObjectID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && isHex(s)
}

func isHex(s string) bool {
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validText is valid UTF-8 holding no control character but a newline or
// a tab.
func validText(s string) bool {
	return validTextContext(context.Background(), s)
}

func validTextContext(ctx context.Context, s string) bool {
	nextCheck := 0
	for i := 0; i < len(s); {
		if i >= nextCheck {
			if ctx.Err() != nil {
				return false
			}
			nextCheck = i + 4096
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && n == 1) || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			return false
		}
		i += n
	}
	return ctx.Err() == nil
}

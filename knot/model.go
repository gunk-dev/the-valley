package knot

import (
	"slices"
	"strings"
)

// Type is one of the four kinds of knot. Each fixes its ID prefix, its
// directory, and the statuses it can be in, split into active and
// terminal. A knot starts in its type's first active status.
type Type struct {
	Name      string   `json:"name"`
	Prefix    string   `json:"prefix"`
	Directory string   `json:"directory"`
	Active    []string `json:"active"`
	Terminal  []string `json:"terminal"`
}

// Types are the four types, in the order the schema lists them.
var Types = []Type{
	{Name: "outcome", Prefix: "oc", Directory: "outcomes",
		Active: []string{"open", "in-progress"}, Terminal: []string{"done", "abandoned"}},
	{Name: "bug", Prefix: "bd", Directory: "bugs",
		Active: []string{"open"}, Terminal: []string{"closed"}},
	{Name: "idea", Prefix: "ida", Directory: "ideas",
		Active: []string{"raw", "exploring", "adopted"}, Terminal: []string{"graduated", "superseded", "discarded"}},
	{Name: "decision", Prefix: "dcr", Directory: "decisions",
		Active: []string{"proposed"}, Terminal: []string{"decided", "superseded"}},
}

// CommentsDirectory holds every knot's comments, one directory per knot.
const CommentsDirectory = "comments"

// TypeNamed is the type called name.
func TypeNamed(name string) (Type, bool) {
	for _, t := range Types {
		if t.Name == name {
			return t, true
		}
	}
	return Type{}, false
}

// typeOfPrefix is the type whose IDs open with prefix.
func typeOfPrefix(prefix string) (Type, bool) {
	for _, t := range Types {
		if t.Prefix == prefix {
			return t, true
		}
	}
	return Type{}, false
}

// typeOfDirectory is the type whose knots live in dir.
func typeOfDirectory(dir string) (Type, bool) {
	for _, t := range Types {
		if t.Directory == dir {
			return t, true
		}
	}
	return Type{}, false
}

// TypeNames lists the four types' names.
func TypeNames() []string {
	var names []string
	for _, t := range Types {
		names = append(names, t.Name)
	}
	return names
}

// Statuses are every status of the type, active ones first.
func (t Type) Statuses() []string {
	return append(append([]string(nil), t.Active...), t.Terminal...)
}

// Initial is the status a new knot of the type starts in.
func (t Type) Initial() string { return t.Active[0] }

// IsTerminal reports whether status is one of the type's terminal ones.
func (t Type) IsTerminal(status string) bool { return slices.Contains(t.Terminal, status) }

// Field is one key a knot's frontmatter may hold.
type Field struct {
	Name string `json:"name"`
	// On are the types that carry it.
	On []string `json:"on"`
	// Kind is how it is written: "token" bare, "string" as a JSON
	// string, "list" as a flow list.
	Kind string `json:"kind"`
	// Required is "always", "never", or the condition under which it is
	// required.
	Required string `json:"required"`
	// Set says how the valley command changes it: "set", "link",
	// "claim", or "" for a field no command changes after `new`.
	Set     string `json:"set,omitempty"`
	Meaning string `json:"meaning"`
}

var (
	allTypes  = []string{"outcome", "bug", "idea", "decision"}
	workTypes = []string{"outcome", "bug"}
)

// Fields are every key a knot may hold, in the order the canonical form
// writes them.
var Fields = []Field{
	{Name: "type", On: allTypes, Kind: "token", Required: "always",
		Meaning: "the knot's type: outcome, bug, idea or decision"},
	{Name: "id", On: allTypes, Kind: "token", Required: "always",
		Meaning: "the type's prefix and the first 7 hex digits of the SHA-256 of the slug"},
	{Name: "status", On: allTypes, Kind: "token", Required: "always", Set: "set",
		Meaning: "the knot's state, from its type's statuses"},
	{Name: "title", On: allTypes, Kind: "string", Required: "always", Set: "set",
		Meaning: "one line naming the knot"},
	{Name: "created", On: allTypes, Kind: "token", Required: "always",
		Meaning: "the date the knot was created, YYYY-MM-DD"},
	{Name: "created_by", On: allTypes, Kind: "string", Required: "always",
		Meaning: "the principal that created the knot; the host checks it names the pusher"},
	{Name: "source", On: allTypes, Kind: "string", Required: "never", Set: "set",
		Meaning: "where the content came from: a date and a venue"},
	{Name: "parent", On: allTypes, Kind: "token", Required: "never", Set: "link",
		Meaning: "the outcome this belongs to: a milestone, a work item, or a residual's work item"},
	{Name: "owner", On: allTypes, Kind: "string", Required: "never", Set: "set",
		Meaning: "the principal it waits on: responsibility, not authority"},
	{Name: "blocked_by", On: workTypes, Kind: "list", Required: "always", Set: "link",
		Meaning: "the knots this waits on: local IDs, or <project>:<id> for another project's"},
	{Name: "claimed_by", On: workTypes, Kind: "string", Required: "with claimed_at", Set: "claim",
		Meaning: "the run working on it"},
	{Name: "claimed_at", On: workTypes, Kind: "string", Required: "with claimed_by", Set: "claim",
		Meaning: "when the run claimed it, as a UTC time"},
	{Name: "changes", On: workTypes, Kind: "list", Required: "never", Set: "link",
		Meaning: "the changes that do the work: valley:<project>/<change ID>, or a GitHub pull request URL"},
	{Name: "target", On: workTypes, Kind: "string", Required: "never", Set: "set",
		Meaning: "the project where the work lands, or <project>:<id>, when that is not this project"},
	{Name: "graduated_into", On: []string{"idea"}, Kind: "token", Required: "when status is graduated", Set: "set",
		Meaning: "the decision a graduated idea's thinking moved to"},
}

// FieldNamed is the field called name.
func FieldNamed(name string) (Field, bool) {
	for _, f := range Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// carries reports whether a knot of type t may hold field f.
func (f Field) carries(t string) bool { return slices.Contains(f.On, t) }

// FieldNames lists the names of the fields a knot of type t may hold, or
// of every field when t is "".
func FieldNames(t string) []string {
	var names []string
	for _, f := range Fields {
		if t == "" || f.carries(t) {
			names = append(names, f.Name)
		}
	}
	return names
}

// Knot is one knot: its frontmatter, its body, and the slug its filename
// carries.
type Knot struct {
	Type          string
	ID            string
	Status        string
	Title         string
	Created       string
	CreatedBy     string
	Source        string
	Parent        string
	Owner         string
	BlockedBy     []string
	ClaimedBy     string
	ClaimedAt     string
	Changes       []string
	Target        string
	GraduatedInto string

	// Body is the markdown below the frontmatter, ending in a newline,
	// or "" for none.
	Body string
	// Slug is the part of the filename after the ID.
	Slug string
}

// Path is where the knot's file lives in the graph's tree.
func (k *Knot) Path() string {
	t, _ := TypeNamed(k.Type)
	return t.Directory + "/" + k.ID + "-" + k.Slug + ".md"
}

// TypeOf is the knot's type.
func (k *Knot) TypeOf() Type {
	t, _ := TypeNamed(k.Type)
	return t
}

// Active reports whether the knot's status is one of its type's active
// ones.
func (k *Knot) Active() bool { return !k.TypeOf().IsTerminal(k.Status) }

// Get is the value of a field written the way `valley knot set` reads
// it: a list joined with commas, and "" for a field the knot does not
// hold.
func (k *Knot) Get(field string) string {
	switch field {
	case "type":
		return k.Type
	case "id":
		return k.ID
	case "status":
		return k.Status
	case "title":
		return k.Title
	case "created":
		return k.Created
	case "created_by":
		return k.CreatedBy
	case "source":
		return k.Source
	case "parent":
		return k.Parent
	case "owner":
		return k.Owner
	case "blocked_by":
		return strings.Join(k.BlockedBy, ",")
	case "claimed_by":
		return k.ClaimedBy
	case "claimed_at":
		return k.ClaimedAt
	case "changes":
		return strings.Join(k.Changes, ",")
	case "target":
		return k.Target
	case "graduated_into":
		return k.GraduatedInto
	}
	return ""
}

// Clone is a copy of the knot that shares nothing with it.
func (k *Knot) Clone() *Knot {
	c := *k
	c.BlockedBy = slices.Clone(k.BlockedBy)
	c.Changes = slices.Clone(k.Changes)
	return &c
}

// Comment is one comment on a knot: who wrote it, and what it says. Its
// filename says when, and which operation wrote it.
type Comment struct {
	Author string
	Body   string
}

// IsLocalID reports whether s is a knot ID.
func IsLocalID(s string) bool { return localID.MatchString(s) }

// IsQualifiedID reports whether s names a knot in another project,
// <project>:<id>.
func IsQualifiedID(s string) bool { return len(s) <= MaxName+12 && qualifiedID.MatchString(s) }

// IsName reports whether s is a principal's, a project's or a run's name.
func IsName(s string) bool { return len(s) <= MaxName && name.MatchString(s) }

// IsChangeLink reports whether s is an entry changes may hold.
func IsChangeLink(s string) bool {
	return len(s) <= MaxLink && (valleyChange.MatchString(s) || githubPR.MatchString(s))
}

// IsOpID reports whether s is an operation's ID.
func IsOpID(s string) bool { return opID.MatchString(s) }

// IsTimestamp reports whether s is a UTC time as claimed_at holds one.
func IsTimestamp(s string) bool { return timestamp.MatchString(s) }

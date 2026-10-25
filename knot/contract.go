// Package knot is the plan graph: a project's plan kept as data, at one
// ref of the project's repository, refs/the-valley/knots. Each item on the
// ref is a knot: an outcome, a bug, an idea or a question, in the node
// format schema/node.cue states. The ref is a cord, and each knot is tied
// on it.
//
// The ref has its own root history. Its first commit has no parent, and
// every later commit has the previous tip as its one parent. Each commit
// is one operation, and its tree is the whole graph:
//
//	outcomes/<id>-<slug>.md
//	bugs/<id>-<slug>.md
//	ideas/<id>-<slug>.md
//	questions/<id>-<slug>.md
//	comments/<knot id>/<stamp>-<op>.md
//
// This package is the one reading of the graph. The valley command reads
// and writes it through this package, and the push hook validates every
// push to the ref with it, so a graph one of them accepts is a graph the
// other accepts. It depends on the standard library alone.
//
// This file is the admission contract: every limit the graph is held to,
// the shapes of its names and references, and which rules the Go
// validator shares with the CUE schema and the knowledge lint. Each is a
// named constant, and contract_test.go tests each limit at its edge.
package knot

import (
	"regexp"
	"time"
)

// Ref is the one ref the graph lives at. The hook matches it exactly: a
// ref that only begins with this name is no graph.
const Ref = "refs/the-valley/knots"

// The limits. A push that would take the graph past one is refused, and
// the valley command refuses to write it.
const (
	// MaxKnots is the most knots a graph holds.
	MaxKnots = 5000
	// MaxComments is the most comments a graph holds, across every knot.
	MaxComments = 20000
	// MaxKnotSize is the most bytes one knot's file holds.
	MaxKnotSize = 64 << 10
	// MaxCommentSize is the most bytes one comment's file holds.
	MaxCommentSize = 16 << 10
	// MaxChangedPaths is the most files one commit adds or changes. One
	// operation touches one or two, so this bounds what the hook reads
	// for a push without bounding any operation.
	MaxChangedPaths = 32
	// MaxCommitSize is the most bytes a graph commit object holds.
	MaxCommitSize = 16 << 10
	// MaxTitle is the most bytes a title or a source holds.
	MaxTitle = 200
	// MaxSlug is the most bytes a slug holds.
	MaxSlug = 48
	// MaxName is the most bytes a principal, a project or a run ID holds.
	MaxName = 64
	// MaxListItems is the most entries one list field holds.
	MaxListItems = 64
	// MaxLink is the most bytes one entry of changes holds.
	MaxLink = 256
)

// The bounds on the hook's walk of a graph's tree. They follow from the
// limits above: the widest tree is one knot's comments, and the walk
// visits the root's five directories, every knot, one comments directory
// per knot, and every comment. The walk counts a subtree each time a tree
// names it, so a tree that names one subtree many times is refused at
// these bounds before it is read.
const (
	// MaxDepth is how deep trees nest, the root's tree counted as 1:
	// comments/<id>/ is at 3.
	MaxDepth = 3
	// MaxTreeEntries is the most entries one tree holds.
	MaxTreeEntries = MaxComments
	// MaxEntries is the most entries the walk visits in all.
	MaxEntries = 5 + 2*MaxKnots + MaxComments
	// MaxTreeSize is the most bytes one tree object holds: MaxTreeEntries
	// entries of the longest name, with a 32-byte object ID.
	MaxTreeSize = 2 << 20
	// MaxReadBytes is the most bytes the hook reads for one push: trees,
	// the commits and the knots it parses.
	MaxReadBytes = 64 << 20
	// HookDeadline is how long the hook spends on one push's graph before
	// it refuses the push.
	HookDeadline = 5 * time.Second
)

// What the valley command does when a write does not land.
const (
	// Attempts is how many times the command tries one operation: the
	// first try and four retries, each after a random pause.
	Attempts = 5
	// OpSearch is how many of the ref's latest commits the command
	// searches for an operation's ID after a push whose outcome it cannot
	// tell, before it tries the operation again.
	OpSearch = 64
	// StaleClaim is the age past which a claim is flagged as stale.
	StaleClaim = 24 * time.Hour
)

// IDRuleCutoff is the date after which every node's ID is derived from its
// slug (archive/.the-valley/README.md). The knowledge lint checks the
// derivation only for nodes created after it. Every knot is: a knot
// created on or before the cutoff is refused, so the derivation rule is
// the same in both validators for every graph the Go validator accepts.
const IDRuleCutoff = "2026-08-05"

// The shapes of names and references.
var (
	// localID is a knot's ID: a type prefix and the first 7 hex digits of
	// the SHA-256 of its slug.
	localID = regexp.MustCompile(`^(oc|bd|ida|qn)-[0-9a-f]{7}$`)
	// qualifiedID names a knot in another project's graph:
	// <project>:<id>.
	qualifiedID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*:(oc|bd|ida|qn)-[0-9a-f]{7}$`)
	// name is a principal, a project or a run ID, as schema/valley.cue
	// names a principal (#PrincipalName).
	name = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	// slug is the part of a knot's filename after its ID.
	slugShape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// date is a YYYY-MM-DD date, and timestamp a UTC time to the second.
	date      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	timestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
	// valleyChange links a change on a valley host:
	// valley:<project>/<change ID>.
	valleyChange = regexp.MustCompile(`^valley:[a-zA-Z0-9][a-zA-Z0-9._-]*/[k-z]{32}$`)
	// githubPR links a pull request on GitHub.
	githubPR = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[1-9][0-9]*$`)
	// knotFile, commentDir and commentFile are the names the layout
	// allows below the root.
	knotFile    = regexp.MustCompile(`^((oc|bd|ida|qn)-[0-9a-f]{7})-([a-z0-9-]+)\.md$`)
	commentFile = regexp.MustCompile(`^([0-9]{8}T[0-9]{6}Z)-([0-9a-f]{16})\.md$`)
	// commentID is a comment's ID: its file's name without .md.
	commentID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$`)
	// opID is the ID an operation carries in its Valley-Op trailer.
	opID = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// CommentStamp is the layout of the stamp a comment's filename opens with:
// the UTC time the comment was written.
const CommentStamp = "20060102T150405Z"

// Rule names one admission rule. Every refusal the validator gives is
// under one rule, so a test or a check can say which rule a graph breaks.
type Rule string

// The rules both validators enforce: the Go validator in this package,
// and the CUE schema with the knowledge lint (schema/node.cue,
// nix/knowledge-lint.py). The knot-agree check runs both over the same
// graphs and fails when they disagree on any of these.
const (
	RuleType          Rule = "type"           // type is one of the four
	RuleStatus        Rule = "status"         // status is in the type's enum
	RuleIDShape       Rule = "id-shape"       // the ID has its type's prefix and 7 hex digits
	RuleIDDerived     Rule = "id-derived"     // the ID's digits are derived from the slug
	RuleFilename      Rule = "filename"       // the filename's ID is the frontmatter's
	RuleDirectory     Rule = "directory"      // a knot lives in its type's directory
	RuleUniqueID      Rule = "unique-id"      // no two knots share an ID
	RuleFieldOnType   Rule = "field-on-type"  // a field appears only on the types that carry it
	RuleFieldShape    Rule = "field-shape"    // names, references, links, dates and times have their shapes
	RuleRequired      Rule = "required"       // the fields every knot of a type carries are there
	RuleEdgeResolves  Rule = "edge-resolves"  // a local edge names a knot in the graph
	RuleParentOutcome Rule = "parent-outcome" // parent names an outcome
	RuleGraduated     Rule = "graduated"      // graduated_into is there exactly when an idea is graduated
)

// The rules the Go validator alone enforces. The canonical form, the
// layout, cycles and transitions are the live graph's, and the archived
// graph the lint was written for has none of them.
const (
	RuleCanonical  Rule = "canonical"  // the file is exactly as Render writes it
	RuleText       Rule = "text"       // UTF-8, no control character but newline and tab
	RuleLayout     Rule = "layout"     // only the allowed paths, as plain files
	RuleLimit      Rule = "limit"      // within the limits above
	RuleCycle      Rule = "cycle"      // parent and blocked_by edges form no cycle
	RuleTransition Rule = "transition" // what a push may change
	RuleProvenance Rule = "provenance" // created_by, author and edited_by name the pusher
	RuleClaim      Rule = "claim"      // claimed_by and claimed_at come together
	RuleCutoff     Rule = "cutoff"     // created after IDRuleCutoff
)

// SharedRules are the rules both validators enforce, in a fixed order.
var SharedRules = []Rule{
	RuleType, RuleStatus, RuleIDShape, RuleIDDerived, RuleFilename,
	RuleDirectory, RuleUniqueID, RuleFieldOnType, RuleFieldShape,
	RuleRequired, RuleEdgeResolves, RuleParentOutcome, RuleGraduated,
}

// GoRules are the rules the Go validator alone enforces.
var GoRules = []Rule{
	RuleCanonical, RuleText, RuleLayout, RuleLimit, RuleCycle,
	RuleTransition, RuleProvenance, RuleClaim, RuleCutoff,
}

// Error is a refusal: the rule it is under, the path it is about, if any,
// and what is wrong, as a sentence that names the valid values.
type Error struct {
	Rule Rule
	Path string
	Msg  string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return e.Msg
	}
	return e.Path + ": " + e.Msg
}

func refuse(rule Rule, path, msg string) *Error {
	return &Error{Rule: rule, Path: path, Msg: msg}
}

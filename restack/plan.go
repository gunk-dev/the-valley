package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"the-valley/change"
	"the-valley/rebase"
)

// The restacker's own limits on a record it would change. Each is below
// the push hook's (change/record.go), so a record the restacker leaves at
// its limit still has room for its owner's own updates.
const (
	maxVersions = 900      // versions of one change
	maxSeq      = 99000    // record updates in a change's life
	maxBytes    = 48 << 10 // bytes of the record's encoded JSON
)

// What the planner would do with a family.
const (
	restack = "restack" // rebase every layer onto main's tip
	hold    = "hold"    // a layer, a request or a limit stops it; Reasons say which
	current = "current" // the family already descends from main's tip
	leave   = "leave"   // no layer is a ready change, so the restacker has no business with it
	wait    = "wait"    // it shares a change or a branch with a family the journal still holds, or, as one the pass would restack or hold, with another such family
)

// Plan is what one pass would do, from one snapshot of origin.
type Plan struct {
	Target    string       `json:"target"`
	Tip       string       `json:"tip"`
	Evidence  string       `json:"evidence"`             // change.EvidenceFingerprint of every attestation ref
	Cap       int          `json:"maxAutomaticVersions"` // the policy's cap as CapOf counts it
	Operation string       `json:"operation"`            // the name the pass's writes are built under
	Recovery  *Recovery    `json:"recovery"`             // what recovery decided of the journal in the state directory; null for none
	Families  []FamilyPlan `json:"families"`
	Actions   []Action     `json:"actions"` // each write a write pass tried, refused and unpushed ones included; none in --report-only
	Pending   *Recovery    `json:"pending"` // what recovery last decided of a journal the pass leaves open; null for none

	families []*rebase.Family // each family as captured, as Families
}

// FamilyPlan is one family of layers, bottom first, and what would become
// of it.
type FamilyPlan struct {
	Layers   []LayerPlan   `json:"layers"`
	Action   string        `json:"action"`
	Reasons  []string      `json:"reasons"`
	Notes    []string      `json:"notes"`
	Requests []RequestPlan `json:"requests"`
	Writes   *Writes       `json:"writes,omitempty"` // what a pass would push for it; absent for a family left alone or current
}

// LayerPlan is one layer as the snapshot shows it, with the capacity of
// the record a restack would change; Capacity is nil for a plain branch.
type LayerPlan struct {
	Name     string    `json:"name"`
	ID       string    `json:"id,omitempty"`
	Branch   string    `json:"branch"`
	Head     string    `json:"head"`
	State    string    `json:"state"`
	Below    string    `json:"below,omitempty"`
	Capacity *Capacity `json:"capacity,omitempty"`
}

// Capacity is how close a record is to each limit, counted as it would be
// once the restack added a version.
type Capacity struct {
	Uploads     int      `json:"uploads"` // versions the principal uploaded, the new one included
	Cap         int      `json:"maxAutomaticVersions"`
	Versions    int      `json:"versions"`
	MaxVersions int      `json:"maxVersions"`
	Seq         int      `json:"seq"`
	MaxSeq      int      `json:"maxSeq"`
	Bytes       int      `json:"bytes"`
	MaxBytes    int      `json:"maxBytes"`
	Over        []string `json:"over"` // each limit the record would pass, as a sentence
}

// RequestPlan is an integration request related to a family: how it is
// related, and whether its outcome is current.
type RequestPlan struct {
	Name     string   `json:"name"`
	Head     string   `json:"head"`
	By       []string `json:"by"` // name, head, range
	Decision string   `json:"decision,omitempty"`
	Current  bool     `json:"current"`
	Releases bool     `json:"releases"`
	Why      string   `json:"why"`
}

// Request is an integration request on origin: its name under
// rebase.RequestPrefix and the head it asks to land.
type Request struct {
	Name, Head string
}

// Members is what a request may be related to a family by: the request
// names of its changes, and its heads. A change's heads are every version
// head it has ever had, not only the active one, so a request filed for an
// older version under any name still holds the family. A plain branch's
// head is its tip.
type Members struct {
	Names map[string]bool
	Heads []string
}

// membersOf collects a family's request names and heads. A change's names
// are c-<change ID> and the branch-derived name of every version it has
// ever had, since a request filed under an earlier version's branch is
// still about the change. A plain branch's name is its own.
func membersOf(f *rebase.Family) Members {
	m := Members{Names: map[string]bool{}}
	seen := map[string]bool{}
	head := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			m.Heads = append(m.Heads, h)
		}
	}
	for _, l := range f.Layers {
		if l.ID == "" {
			m.Names[rebase.ChangeName(l.Branch)] = true
			head(l.Head)
			continue
		}
		m.Names[change.RequestName(l.ID)] = true
		head(l.Head)
		for _, v := range l.Record.Versions {
			if v.Branch != "" {
				m.Names[rebase.ChangeName(v.Branch)] = true
			}
			head(rebase.HeadOf(v))
		}
	}
	return m
}

// Incoming is the commits a request's head would bring to the target: the
// commits it reaches that the target's tip does not. An error is a head
// whose commits could not be read, as for an object that is missing or is
// not a commit.
type Incoming func(head string) (map[string]bool, error)

// Ancestry answers whether a is an ancestor of b, or b itself. An error is
// an answer that could not be had, as for an object that is missing or is
// not a commit.
type Ancestry func(a, b string) (bool, error)

// Relate says how a request is related to a family: by name, by an exact
// head, and by an incoming range that contains a head of the family. No
// answer and a nil error is unrelated. An error is a relation that could
// not be determined, which the caller must treat as related.
func Relate(r Request, m Members, in Incoming) ([]string, error) {
	var by []string
	if m.Names[r.Name] {
		by = append(by, "name")
	}
	for _, h := range m.Heads {
		if h == r.Head {
			by = append(by, "head")
			break
		}
	}
	if len(by) > 0 {
		return by, nil
	}
	commits, err := in(r.Head)
	if err != nil {
		return nil, err
	}
	for _, h := range m.Heads {
		if commits[h] {
			return []string{"range"}, nil
		}
	}
	return nil, nil
}

// Currency judges a related request's outcome: whether it lets the family
// proceed, and why. An outcome is current when its head, its target tip
// and its evidence fingerprint all equal what the planner sees. A current
// stale releases the family, since the integrator will not land the
// request at that head on that tip. So does a current reject, which the
// caller notes. Anything else holds: no outcome, a record that does not
// read, an outcome about another head, tip or evidence, which the
// integrator will judge again, and a failure or a refusal.
func Currency(o rebase.Outcome, found bool, head, tip, evidence string) (current, releases bool, why string) {
	switch {
	case !found:
		return false, false, "no outcome yet, so the integrator has still to judge it"
	case o.Decision == "unknown":
		return false, false, "its outcome record does not read"
	case o.Head != head:
		return false, false, fmt.Sprintf("its outcome is about head %s, not %s", short(o.Head), short(head))
	case o.Tip != tip:
		return false, false, fmt.Sprintf("its outcome was judged on main at %s, not %s", short(o.Tip), short(tip))
	case o.Evidence != evidence:
		return false, false, fmt.Sprintf("its outcome was judged on evidence %s, not %s", o.Evidence, evidence)
	}
	switch o.Decision {
	case "stale":
		return true, true, "a current stale outcome: it cannot land as it stands"
	case "reject":
		return true, true, "a current reject outcome: the integrator rejected it"
	case "failed":
		return true, false, "the integrator failed on it at " + o.FailedAt
	case "land", "voided-withdrawn":
		return true, false, fmt.Sprintf("a current %s outcome with the request still filed", o.Decision)
	}
	return true, false, "the integrator refused it (" + o.Decision + ")"
}

// CapOf reads the policy's cap on automatic versions as the count the
// planner compares uploads against. The schema admits any whole number of
// at least 1, with no upper bound. A cap larger than an int holds is no
// practical cap: it is counted as the largest int, and no record holds
// that many versions. An absent cap, or one that is not a whole number of
// at least 1, is refused, since the schema admits neither.
func CapOf(stated json.Number) (int, error) {
	if stated == "" {
		return 0, errors.New("the composed policy states no restack.maxAutomaticVersions")
	}
	n, err := strconv.Atoi(string(stated))
	switch {
	case errors.Is(err, strconv.ErrRange) && n > 0:
		return math.MaxInt, nil
	case err != nil || n < 1:
		return 0, fmt.Errorf("the composed policy's restack.maxAutomaticVersions is %s, not a whole number of at least 1", stated)
	}
	return n, nil
}

// CapacityOf counts a record against every limit as it would stand with
// one more version uploaded by principal. size is the length of that
// record's encoded JSON.
func CapacityOf(r *change.Record, principal string, cap, size int) *Capacity {
	c := &Capacity{
		Uploads: 1, Cap: cap,
		Versions: len(r.Versions) + 1, MaxVersions: maxVersions,
		Seq: r.Seq + 1, MaxSeq: maxSeq,
		Bytes: size, MaxBytes: maxBytes,
	}
	for _, v := range r.Versions {
		if v.Uploader == principal {
			c.Uploads++
		}
	}
	if c.Uploads > c.Cap {
		c.Over = append(c.Over, fmt.Sprintf("%s would have %d versions uploaded by %s, over the cap of %d", r.ChangeID, c.Uploads, principal, c.Cap))
	}
	if c.Versions > c.MaxVersions {
		c.Over = append(c.Over, fmt.Sprintf("%s would have %d versions, over the %d the restacker allows", r.ChangeID, c.Versions, c.MaxVersions))
	}
	if c.Seq > c.MaxSeq {
		c.Over = append(c.Over, fmt.Sprintf("%s's record would be at update %d, over the %d the restacker allows", r.ChangeID, c.Seq, c.MaxSeq))
	}
	if c.Bytes > c.MaxBytes {
		c.Over = append(c.Over, fmt.Sprintf("%s's record would be %d bytes, over the %d the restacker allows", r.ChangeID, c.Bytes, c.MaxBytes))
	}
	return c
}

// nextSize is the length of a record's encoded JSON once a restack adds a
// version. The rebased head is not known yet, so the version names a
// stand-in of the same length; the title, the branch and the uploader are
// the ones the restack would write. 0 is a record the engine would not
// extend at all, which the version and update limits already hold.
func nextSize(handle string, r *change.Record, principal string) int {
	active := r.ActiveVersion()
	stand := change.HeadPrefix + strings.Repeat("0", len(rebase.HeadOf(active)))
	next, err := rebase.NextRecord(handle, r, change.Version{Head: stand, Title: active.Title, Branch: active.Branch, Uploader: principal})
	if err != nil {
		return 0
	}
	b, err := change.Encode(next)
	if err != nil {
		return 0
	}
	return len(b)
}

func short(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}

// stateLine names a layer's state for a reason, as a change is ready,
// approved or submitted.
func stateLine(l *rebase.Layer) string {
	switch s := l.State(); s {
	case rebase.Plain:
		return l.Name + " is a plain branch, which no record is on"
	case rebase.Draft:
		return l.Name + " is a draft"
	case rebase.Withdrawn:
		return l.Name + " is withdrawn"
	case rebase.Uncertain:
		return l.Name + " may have landed already, with no evidence either way"
	default:
		return fmt.Sprintf("%s is %s", l.Name, s)
	}
}

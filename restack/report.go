package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"the-valley/change"
	"the-valley/rebase"
)

// A family's reports are cut from one observation of it: everything the
// attempt read and what came of it, in full. The journal keeps the
// observation whole. Each recorded change in the family gets a report of
// its own, in the format change/report.go defines, about every recorded
// change in the family, bottom first. A plain branch is no report layer.
//
// A report holds what the format allows and no more: 64 layers, 100
// conflict paths of at most 1024 bytes, 64 blocking requests whose refs
// are at most 256 bytes, a refusal of at most 2048 bytes, no control
// character in any of them, and 64 KiB of JSON in all. When the
// observation holds more, the report is a fallback. It keeps what fits, in
// the observation's order:
//
//   - when the family has more than 64 recorded changes, only the change
//     the report is about and the layer that failed;
//   - each conflict path and each blocking request that fits on its own,
//     up to the format's count;
//   - its refusal, with each run of control characters made one space,
//     cut to fit.
//
// Its refusal then ends by saying what it leaves out, and naming the
// digest of the whole observation the journal keeps. Two fallbacks that
// show the same are told apart by that digest. If the report is still more
// than 64 KiB, it sheds its conflict paths, then its blocking requests,
// then every layer but the change's own and the failing one.

// Observation is a family as one attempt saw it, in full.
type Observation struct {
	Action    string          `json:"action"`         // the planner's: restack or hold
	Result    string          `json:"result"`         // what the reports say came of it: complete, conflict or blocked
	Layers    []ObservedLayer `json:"layers"`         // every layer, plain branches included, bottom first
	Requests  []RequestPlan   `json:"requests"`       // every related request the planner considered
	Reasons   []string        `json:"reasons"`        // why the planner held it
	Notes     []string        `json:"notes"`          // what the planner noted
	Failing   string          `json:"failing_layer"`  // the change ID of the layer that failed; "" for none
	Conflicts []string        `json:"conflict_paths"` // every path that conflicted
	Refusal   string          `json:"refusal"`        // why nothing was published, in full; "" for nothing
}

// ObservedLayer is one layer as the attempt read it.
type ObservedLayer struct {
	Name      string `json:"name"`       // a change's handle, or a plain branch's name
	ID        string `json:"change_id"`  // "" for a plain branch
	Branch    string `json:"branch"`     // the branch a rebase pushes
	BranchTip string `json:"branch_tip"` // that branch's tip on origin; "" when origin had none
	Record    string `json:"record"`     // the record commit read; "" for a plain branch
	Version   int    `json:"version"`    // the active version; 0 for a plain branch
	Head      string `json:"head"`       // the head rebased
	State     string `json:"state"`
	Below     string `json:"below"` // the layer it sits on, by name; "" for the bottom
}

// observe is the observation of a family the planner judged, before
// anything is tried.
func observe(f *rebase.Family, fp FamilyPlan) Observation {
	o := Observation{Action: fp.Action, Requests: fp.Requests, Reasons: fp.Reasons, Notes: fp.Notes, Conflicts: []string{}}
	for _, l := range f.Layers {
		ol := ObservedLayer{Name: l.Name, ID: l.ID, Branch: l.Branch, BranchTip: l.Tip, Head: l.Head, State: string(l.State())}
		if l.ID != "" {
			ol.Record, ol.Version = l.Commit, l.Version
		}
		if l.Below != nil {
			ol.Below = l.Below.Name
		}
		o.Layers = append(o.Layers, ol)
	}
	return o
}

// Digest is the first 16 hex digits of the SHA-256 of the observation as
// the journal keeps it.
func (o Observation) Digest() string {
	data, _ := json.Marshal(o)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// fullReport is the report about the change id that the observation gives,
// before it is cut to the format's bounds. published is what the attempt
// published for each change, by ID.
func fullReport(op, tip, id string, o Observation, published map[string]*change.RestackPublished) *change.RestackReport {
	r := &change.RestackReport{
		Schema: change.RestackSchema, ChangeID: id, Target: rebase.Target, TargetTip: change.HeadPrefix + tip,
		Operation: op, Result: o.Result, Layers: []change.RestackLayer{}, Conflicts: o.Conflicts, Requests: []change.RestackRequest{},
	}
	for _, l := range o.Layers {
		if l.ID == "" {
			continue
		}
		rl := change.RestackLayer{
			ChangeID: l.ID, Record: change.HeadPrefix + l.Record, Version: l.Version,
			Head: change.HeadPrefix + l.Head, Branch: l.Branch, Published: published[l.ID],
		}
		if l.BranchTip != "" {
			tip := change.HeadPrefix + l.BranchTip
			rl.BranchTip = &tip
		}
		r.Layers = append(r.Layers, rl)
	}
	for _, q := range o.Requests {
		if !q.Releases {
			r.Requests = append(r.Requests, change.RestackRequest{Ref: rebase.RequestPrefix + q.Name, Tip: change.HeadPrefix + q.Head})
		}
	}
	if o.Failing != "" {
		failing := o.Failing
		r.Failing = &failing
	}
	if o.Refusal != "" {
		refusal := o.Refusal
		r.Refusal = &refusal
	}
	return r
}

// bound cuts a report to the format's bounds, as the comment at the top of
// this file describes, and says what it left out: nothing for a report
// that fits whole. digest names the whole observation. It errs only when
// not even the change's own layer fits.
func bound(full *change.RestackReport, digest string) (*change.RestackReport, []string, error) {
	r := *full
	var omitted []string
	if n := len(r.Layers); n > change.MaxRestackLayers {
		r.Layers = essential(&r)
		omitted = append(omitted, fmt.Sprintf("%d of the family's %d recorded changes", n-len(r.Layers), n))
	}
	paths, cut := []string{}, 0
	for _, p := range r.Conflicts {
		if len(paths) < change.MaxConflictPaths && fits(p, change.MaxConflictPath) {
			paths = append(paths, p)
		} else {
			cut++
		}
	}
	if cut > 0 {
		omitted = append(omitted, fmt.Sprintf("%d of its %d conflict paths", cut, len(r.Conflicts)))
	}
	r.Conflicts = paths
	requests, seen, cut := []change.RestackRequest{}, map[string]bool{}, 0
	for _, q := range r.Requests {
		name, ok := strings.CutPrefix(q.Ref, rebase.RequestPrefix)
		if ok && len(requests) < change.MaxBlockingRequest && change.ValidBranch(name) && utf8.ValidString(name) &&
			len(q.Ref) <= change.MaxRequestRef && !seen[q.Ref] && change.ValidHead(q.Tip) {
			seen[q.Ref] = true
			requests = append(requests, q)
		} else {
			cut++
		}
	}
	if cut > 0 {
		omitted = append(omitted, fmt.Sprintf("%d of its %d blocking requests", cut, len(r.Requests)))
	}
	r.Requests = requests
	refusal := ""
	if full.Refusal != nil {
		refusal = plain(*full.Refusal)
	}
	if len(refusal) > change.MaxRefusal-noteRoom {
		refusal = shorten(refusal, change.MaxRefusal-noteRoom)
		omitted = append(omitted, "the end of its refusal")
	}

	for {
		out := r
		out.Refusal = refusalWith(refusal, omitted, digest)
		data, err := change.EncodeRestackReport(&out)
		if err != nil {
			return nil, nil, err
		}
		if len(data) <= change.MaxReportBlob {
			return &out, omitted, nil
		}
		switch {
		case len(r.Conflicts) > 0:
			omitted = append(omitted, fmt.Sprintf("the %d conflict paths left, to fit in %d bytes", len(r.Conflicts), change.MaxReportBlob))
			r.Conflicts = []string{}
		case len(r.Requests) > 0:
			omitted = append(omitted, fmt.Sprintf("the %d blocking requests left, to fit in %d bytes", len(r.Requests), change.MaxReportBlob))
			r.Requests = []change.RestackRequest{}
		case len(essential(&r)) < len(r.Layers):
			kept := essential(&r)
			omitted = append(omitted, fmt.Sprintf("%d more of the family's recorded changes, to fit in %d bytes", len(r.Layers)-len(kept), change.MaxReportBlob))
			r.Layers = kept
		default:
			return nil, nil, fmt.Errorf("no report of %s fits in %d bytes, even with its own layer alone", r.ChangeID, change.MaxReportBlob)
		}
	}
}

// essential is the layers a fallback report keeps: the change's own, and
// the failing one, in the family's order.
func essential(r *change.RestackReport) []change.RestackLayer {
	var keep []change.RestackLayer
	for _, l := range r.Layers {
		if l.ChangeID == r.ChangeID || r.Failing != nil && l.ChangeID == *r.Failing {
			keep = append(keep, l)
		}
	}
	return keep
}

// fits reports whether s is 1 to max bytes of UTF-8 holding no control
// character, as a report's conflict path must be.
func fits(s string, max int) bool {
	return len(s) >= 1 && len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// plain is s as UTF-8 with each run of control characters one space.
func plain(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToValidUTF8(s, "�") {
		if unicode.IsControl(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// refusalWith is a report's refusal: the observation's, and, when the
// report leaves anything out, a note of what and of the digest of the
// whole, cut to fit the format. nil is no refusal.
func refusalWith(refusal string, omitted []string, digest string) *string {
	if len(omitted) == 0 && refusal == "" {
		return nil
	}
	note := ""
	if len(omitted) > 0 {
		note = fmt.Sprintf("This report leaves out %s; the restacker's journal keeps the whole observation, digest %s.",
			strings.Join(omitted, ", "), digest)
	}
	if room := change.MaxRefusal - len(note) - len(" "); len(refusal) > room {
		refusal = shorten(refusal, room)
	}
	out := strings.TrimSpace(refusal + " " + note)
	return &out
}

// noteRoom is what a refusal leaves for the note of what a fallback leaves
// out, which is never longer.
const noteRoom = 600

// shorten is s cut at a character boundary to at most n bytes, an ellipsis
// marking the cut.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n - len("…")
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return strings.TrimSpace(s[:max(end, 0)]) + "…"
}

// sameReport reports whether two reports say the same: the same
// observations and the same result. The operation that wrote each is not
// compared, since an unchanged observation written again by a later
// attempt says nothing new.
func sameReport(a, b *change.RestackReport) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytesOf(a) == bytesOf(b)
}

func bytesOf(r *change.RestackReport) string {
	c := *r
	c.Operation = ""
	if c.Conflicts == nil {
		c.Conflicts = []string{}
	}
	if c.Requests == nil {
		c.Requests = []change.RestackRequest{}
	}
	if c.Layers == nil {
		c.Layers = []change.RestackLayer{}
	}
	data, _ := json.Marshal(c)
	return string(data)
}

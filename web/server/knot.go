package server

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"the-valley/knot"
	"the-valley/web/export"
)

// knotPage is one knot: where it sits, its fields, the knots it links,
// its body and its comments.
type knotPage struct {
	Knot      knotLink
	Type      string
	Status    string
	Trail     []knotLink
	TrailCut  bool
	Facts     []fact
	BlockedBy []reference
	Blocks    []knotLink
	Children  []knotLink
	Changes   []string
	Body      string
	Comments  []comment
}

// fact is one of a knot's fields. A fact with a URL links to what it
// names.
type fact struct {
	Label string
	Text  string
	URL   string
}

// reference is one entry of blocked_by: a knot the page links to, a
// reference it can only name, with what is known of it, or a knot of a
// project the reader may not see, which it does not name at all.
type reference struct {
	Ref  string
	Knot *knotLink
	Note string
}

// comment is one comment as the page shows it. An edited comment says so.
type comment struct {
	Author   string
	At       string
	Edited   bool
	EditedAt string
	EditedBy string
	Body     string
}

func (q *request) knot(project, id string) {
	p, ok := q.project(project)
	if !ok {
		q.notFound()
		return
	}
	knots := q.idx.knots[p.Name]
	k, ok := knots[id]
	if !ok {
		q.notFound()
		return
	}
	page := knotPage{Knot: q.idx.linkOf(p.Name, k), Type: k.Type, Status: k.Status, Body: trimEnd(k.Body)}

	// The trail runs from the milestone down to the knot's parent. It
	// names the nearest MaxPlanDepth parents, and marks any above them.
	seen := map[string]bool{k.ID: true}
	for at := k.Parent; at != "" && !seen[at]; {
		parent, ok := knots[at]
		if !ok {
			break
		}
		if len(page.Trail) == MaxPlanDepth {
			page.TrailCut = true
			break
		}
		seen[at] = true
		page.Trail = append(page.Trail, q.idx.linkOf(p.Name, parent))
		at = parent.Parent
	}
	slices.Reverse(page.Trail)

	add := func(label, text, url string) {
		if text != "" {
			page.Facts = append(page.Facts, fact{label, text, url})
		}
	}
	add("State", stateLabel(k.State), "")
	add("Created", k.Created+" by "+k.CreatedBy, "")
	add("Owner", k.Owner, "")
	add("Source", k.Source, "")
	if c := k.Claim; c != nil {
		add("Claimed", "by "+c.By+", "+stampOf(c.At), "")
	}
	if pr := k.Progress; pr != nil {
		add("Progress", strconv.Itoa(pr.Done)+" of "+strconv.Itoa(pr.Of)+" done", "")
	}
	if k.Target != "" {
		text, url := q.idx.target(k.Target)
		add("Lands in", text, url)
	}
	if k.GraduatedInto != "" {
		text, url := q.idx.graduated(p.Name, k.GraduatedInto)
		add("Graduated into", text, url)
	}
	for _, c := range k.Changes {
		page.Changes = append(page.Changes, q.idx.change(c))
	}

	for _, ref := range k.BlockedBy {
		page.BlockedBy = append(page.BlockedBy, q.reference(p.Name, k, ref))
	}
	for _, id := range k.Blocks {
		if b, ok := knots[id]; ok {
			page.Blocks = append(page.Blocks, q.idx.linkOf(p.Name, b))
		}
	}
	for _, id := range k.Children {
		if c, ok := knots[id]; ok {
			page.Children = append(page.Children, q.idx.linkOf(p.Name, c))
		}
	}
	for _, c := range k.Comments {
		page.Comments = append(page.Comments, comment{Author: c.Author, At: stampOf(c.At),
			Edited: c.Edited != "", EditedAt: stampOf(c.Edited), EditedBy: c.EditedBy, Body: trimEnd(c.Body)})
	}
	q.page(http.StatusOK, "knot", k.ID+" · "+k.Title, page)
}

// The words a page shows for a reference into a project the reader may
// not see, whether the export holds it for another reader or not at all.
// They name nothing of what is referred to: not the project, not the knot,
// and not the reference's text. A page shows them too for a value that has
// none of the shapes its field allows, since such a value names nothing
// the reader may see.
const (
	unpublishedKnot    = "a knot in an unpublished project"
	unpublishedProject = "an unpublished project"
	unpublishedChange  = "a change in an unpublished project"
)

// knotRef reads ref as a reference to a knot, from a knot of project's: a
// knot's ID in project's own graph, or <project>:<id> for a knot of
// another project's, each whole (knot/validate.go). other and id are the
// project and the ID it names. ok is false for any other text, which names
// no knot.
func knotRef(project, ref string) (other, id string, ok bool) {
	if knot.IsLocalID(ref) {
		return project, ref, true
	}
	if !knot.IsQualifiedID(ref) {
		return "", "", false
	}
	other, id, _ = strings.Cut(ref, ":")
	return other, id, true
}

// reference resolves one blocked_by entry of k's, read by knotRef. A knot
// of this project or of another the reader may see is linked. A reference
// into a project the index does not hold is shown as unpublished, with
// none of its text, and so is an entry of neither shape, which names no
// knot. Any other reference is named, and said to be missing.
func (q *request) reference(project string, k *export.Knot, ref string) reference {
	other, id, ok := knotRef(project, ref)
	if !ok {
		return reference{Note: unpublishedKnot}
	}
	if _, ok := q.project(other); !ok {
		return reference{Note: unpublishedKnot}
	}
	if b, ok := q.idx.knots[other][id]; ok {
		link := q.idx.linkOf(other, b)
		return reference{Ref: ref, Knot: &link}
	}
	for _, e := range k.Edges {
		if e.Field == "blocked_by" && e.Ref == ref && e.State == export.EdgeDangling {
			return reference{Ref: ref, Note: "not found in its graph"}
		}
	}
	return reference{Ref: ref, Note: "not found"}
}

// target is how a page names what a knot's target holds, and the page it
// links to. A target is a project's name, or <project>:<id> for a knot of
// one, each whole (knot/validate.go). It is never a knot of the knot's own
// graph, so a project named as a knot's ID is still a project. A project
// the reader may see is linked to the tree of its published tip. A knot of
// one is linked to its page, or named and not linked when the index does
// not hold it. A target into a project the index does not hold is named
// only as unpublished, and not linked, and so is a target of neither
// shape.
func (idx *index) target(ref string) (text, url string) {
	if knot.IsName(ref) {
		if p, ok := idx.project(ref); ok {
			return ref, treeURL(idx.bases[p.Name], p.SourceTip, nil)
		}
		return unpublishedProject, ""
	}
	if !knot.IsQualifiedID(ref) {
		return unpublishedProject, ""
	}
	other, id, _ := strings.Cut(ref, ":")
	if _, ok := idx.project(other); !ok {
		return unpublishedKnot, ""
	}
	if _, ok := idx.knots[other][id]; ok {
		return ref, knotURL(idx.bases[other], id)
	}
	return ref, ""
}

// graduated is how a page names what a knot's graduated_into holds, and
// the page it links to. graduated_into is the ID of an outcome or a
// question in the knot's own graph (knot/validate.go), which the reader
// sees, since they see the knot. It is linked when the index holds it, and
// named and not linked when it does not. Any other value names no knot the
// reader may see, and is named only as unpublished.
func (idx *index) graduated(project, ref string) (text, url string) {
	if !knot.IsLocalID(ref) || !strings.HasPrefix(ref, "oc-") && !strings.HasPrefix(ref, "qn-") {
		return unpublishedKnot, ""
	}
	if _, ok := idx.knots[project][ref]; ok {
		return ref, knotURL(idx.bases[project], ref)
	}
	return ref, ""
}

// change is how a page names one of a knot's change links. A link is
// valley:<project>/<change ID>, or a GitHub pull request's URL, each whole
// (knot/validate.go). A valley link is named only as unpublished unless
// the index holds its project, and so is a link of neither shape.
func (idx *index) change(link string) string {
	if !knot.IsChangeLink(link) {
		return unpublishedChange
	}
	if rest, ok := strings.CutPrefix(link, "valley:"); ok {
		project, _, _ := strings.Cut(rest, "/")
		if _, public := idx.project(project); !public {
			return unpublishedChange
		}
	}
	return link
}

// waitsOn is the knots a blocked knot of project's waits on, as a tree row
// names them: refs, and, when hidden is set, the knots of projects the
// reader may not see that the export or the projection left out of refs.
// Those, any of refs into a project the index does not hold, and any that
// knotRef does not read as a knot's, are named once, as unpublished, after
// the others. A blocked knot that waits on nothing refs names waits on
// something unpublished, so it is named so too, as it is from an export
// that does not carry waiting_on_hidden.
func (idx *index) waitsOn(project string, refs []string, hidden bool) string {
	var out []string
	named := false
	for _, ref := range refs {
		idx.checkBuild()
		other, _, ok := knotRef(project, ref)
		if !ok {
			hidden = true
			continue
		}
		if _, public := idx.project(other); !public {
			hidden = true
			continue
		}
		out = append(out, ref)
		named = true
	}
	if hidden || !named {
		out = append(out, unpublishedKnot)
	}
	return strings.Join(out, ", ")
}

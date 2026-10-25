package server

import (
	"net/http"
	"strconv"
	"strings"

	"the-valley/knot"
	"the-valley/webui/export"
)

// knotPage is one knot: where it sits, its fields, the knots it links,
// its body and its comments.
type knotPage struct {
	Knot      knotLink
	Type      string
	Status    string
	Trail     []knotLink
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
// reference it can only name, with what is known of it, or a knot of an
// unpublished project, which it does not name at all.
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
	page := knotPage{Knot: linkOf(p.Name, k), Type: k.Type, Status: k.Status, Body: trimEnd(k.Body)}

	// The trail runs from the milestone down to the knot's parent.
	seen := map[string]bool{k.ID: true}
	for at := k.Parent; at != "" && !seen[at]; {
		seen[at] = true
		parent, ok := knots[at]
		if !ok {
			break
		}
		page.Trail = append([]knotLink{linkOf(p.Name, parent)}, page.Trail...)
		at = parent.Parent
	}

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
		text, url := q.idx.named(p.Name, k.Target)
		add("Lands in", text, url)
	}
	if k.GraduatedInto != "" {
		text, url := q.idx.named(p.Name, k.GraduatedInto)
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
			page.Blocks = append(page.Blocks, linkOf(p.Name, b))
		}
	}
	for _, id := range k.Children {
		if c, ok := knots[id]; ok {
			page.Children = append(page.Children, linkOf(p.Name, c))
		}
	}
	for _, c := range k.Comments {
		page.Comments = append(page.Comments, comment{Author: c.Author, At: stampOf(c.At),
			Edited: c.Edited != "", EditedAt: stampOf(c.Edited), EditedBy: c.EditedBy, Body: trimEnd(c.Body)})
	}
	q.page(http.StatusOK, "knot", k.ID+" · "+k.Title, page)
}

// The words a page shows for a reference into a project the export does
// not hold as public. They name nothing of what is referred to: not the
// project, not the knot, and not the reference's text.
const (
	unpublishedKnot    = "a knot in an unpublished project"
	unpublishedProject = "an unpublished project"
	unpublishedChange  = "a change in an unpublished project"
)

// reference resolves one blocked_by entry of k's. A knot of this project
// or of another public one is linked. A reference into a project the
// export does not hold as public is shown as unpublished, with none of
// its text. Any other reference is named, and said to be missing.
func (q *request) reference(project string, k *export.Knot, ref string) reference {
	other, id, foreign := strings.Cut(ref, ":")
	if !foreign {
		other, id = project, ref
	}
	if _, ok := q.project(other); !ok {
		return reference{Note: unpublishedKnot}
	}
	if b, ok := q.idx.knots[other][id]; ok {
		link := linkOf(other, b)
		return reference{Ref: ref, Knot: &link}
	}
	for _, e := range k.Edges {
		if e.Field == "blocked_by" && e.Ref == ref && e.State == export.EdgeDangling {
			return reference{Ref: ref, Note: "not found in its graph"}
		}
	}
	return reference{Ref: ref, Note: "not found"}
}

// named is how a page names what a target or graduated_into holds, and
// the page it links to: a knot of a public project, or the tree of a
// public project's published tip. A reference into a project the export
// does not hold as public is named only as unpublished, and not linked. A
// knot of a public project that the export does not hold is named and not
// linked.
func (idx *index) named(project, ref string) (text, url string) {
	other, id, qualified := strings.Cut(ref, ":")
	switch {
	case qualified:
		if _, ok := idx.project(other); !ok {
			return unpublishedKnot, ""
		}
		if _, ok := idx.knots[other][id]; ok {
			return ref, knotURL(other, id)
		}
		return ref, ""
	case knot.IsLocalID(ref):
		if _, ok := idx.knots[project][ref]; ok {
			return ref, knotURL(project, ref)
		}
		return ref, ""
	}
	if p, ok := idx.project(ref); ok {
		return ref, treeURL(p.Name, p.SourceTip, nil)
	}
	return unpublishedProject, ""
}

// change is how a page names one of a knot's change links. A valley
// change of a project the export does not hold as public is named only as
// unpublished.
func (idx *index) change(link string) string {
	if rest, ok := strings.CutPrefix(link, "valley:"); ok {
		if project, _, ok := strings.Cut(rest, "/"); ok {
			if _, public := idx.project(project); !public {
				return unpublishedChange
			}
		}
	}
	return link
}

// waitsOn is the knots a blocked knot waits on, as a tree row names them.
// The knots of projects the export does not hold as public are named once,
// as unpublished.
func (idx *index) waitsOn(refs []string) string {
	var out []string
	hidden := false
	for _, ref := range refs {
		if other, _, ok := strings.Cut(ref, ":"); ok {
			if _, public := idx.project(other); !public {
				if !hidden {
					out = append(out, unpublishedKnot)
				}
				hidden = true
				continue
			}
		}
		out = append(out, ref)
	}
	return strings.Join(out, ", ")
}

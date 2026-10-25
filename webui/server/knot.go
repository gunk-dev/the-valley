package server

import (
	"net/http"
	"strconv"
	"strings"

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

// reference is one entry of blocked_by: a knot the page links to, or a
// reference it can only name, with what is known of it.
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
	page := knotPage{Knot: linkOf(p.Name, k), Type: k.Type, Status: k.Status, Body: trimEnd(k.Body),
		Changes: k.Changes}

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
		add("Lands in", k.Target, q.refURL(p.Name, k.Target))
	}
	if k.GraduatedInto != "" {
		add("Graduated into", k.GraduatedInto, q.refURL(p.Name, k.GraduatedInto))
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

// reference resolves one blocked_by entry of k's. A knot of this project
// or of another public one is linked. A reference to a project the
// export does not hold is named and no more is said of it.
func (q *request) reference(project string, k *export.Knot, ref string) reference {
	other, id, foreign := strings.Cut(ref, ":")
	if !foreign {
		other, id = project, ref
	}
	if _, ok := q.project(other); ok {
		if b, ok := q.idx.knots[other][id]; ok {
			link := linkOf(other, b)
			return reference{Ref: ref, Knot: &link}
		}
	}
	for _, e := range k.Edges {
		if e.Field != "blocked_by" || e.Ref != ref {
			continue
		}
		switch e.State {
		case export.EdgeHidden:
			return reference{Ref: ref, Note: "not published"}
		case export.EdgeDangling:
			return reference{Ref: ref, Note: "not found in its graph"}
		}
	}
	return reference{Ref: ref, Note: "not found"}
}

// refURL is the page of what a target or graduated_into names: a knot of
// a public project, or the tree of a public project's published tip. It
// is "" for anything else.
func (q *request) refURL(project, ref string) string {
	other, id, foreign := strings.Cut(ref, ":")
	if !foreign {
		if _, ok := q.idx.knots[project][ref]; ok {
			return knotURL(project, ref)
		}
		if p, ok := q.project(ref); ok {
			return treeURL(p.Name, p.SourceTip, nil)
		}
		return ""
	}
	if _, ok := q.project(other); ok {
		if _, ok := q.idx.knots[other][id]; ok {
			return knotURL(other, id)
		}
	}
	return ""
}

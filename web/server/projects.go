package server

import "net/http"

// projectGroup is one group on the projects page: its name, its own page,
// and its projects the reader may read.
type projectGroup struct {
	Name     string
	URL      string
	Projects []listedProject
}

// listedProject is one project on the projects page: its path,
// <group>/<project>, and links to its source and its knots.
type listedProject struct {
	Name   string
	Source string
	Knots  string
}

// projectsPage is the list of projects a reader may read, by group: every
// group's on /-/projects, or one group's on its own page. A group on its
// own page has no link to itself.
type projectsPage struct {
	Groups []projectGroup
}

// projects serves /-/projects: every project in the reader's view, by
// group, in the export's order. With none, the page says so, and answers
// 200.
func (q *request) projects() {
	q.page(http.StatusOK, "projects", "projects", projectsPage{Groups: q.idx.listing})
}

// group serves /<group>/ and /<group>: the projects page of the group's
// projects in the reader's view. A group with none answers as a path that
// does not exist, so the page never says that a group exists.
func (q *request) group(name string) {
	i, ok := q.idx.listed[name]
	if !ok {
		q.notFound()
		return
	}
	g := q.idx.listing[i]
	g.URL = ""
	q.page(http.StatusOK, "projects", g.Name, projectsPage{Groups: []projectGroup{g}})
}

// projectHead is the header of every page that belongs to one project: its
// path, <group>/<project>, and its two tabs, source and knots. The tab of
// the page's own part of the project is current.
type projectHead struct {
	Name string
	Tabs []projectTab
}

type projectTab struct {
	Text    string
	URL     string
	Current bool
}

// head is the project header of a page of project's, whose current tab is
// current: "source" for its tree, file and history pages, and "knots" for
// its knots page and each of its knots.
func (idx *index) head(project, current string) projectHead {
	base := idx.bases[project]
	return projectHead{Name: idx.names[project], Tabs: []projectTab{
		{"source", treeURL(base, idx.projects[project].SourceTip, nil), current == "source"},
		{"knots", projectKnotsURL(base), current == "knots"},
	}}
}

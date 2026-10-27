package export

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Group is a group of projects, as an export carries it: its identifier,
// its name, and its aliases. A project's web URLs open with its group's
// name, /<name>/<project>/…, and a URL under an alias redirects to the
// same path under the name. The identifier never changes, and the
// projects of the group name it; no URL shows it.
type Group struct {
	ID      string   `json:"group"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

// Placement is where the instance places a project on the web: the
// identifier of its group, and its short aliases. /<alias>/… redirects to
// the same path under /<group name>/<project>/…. A project with no group
// has no web URL, and no aliases.
type Placement struct {
	Group   string
	Aliases []string
}

// Reserved are the first segments of the web server's own paths. No
// group's name or alias, and no project's alias, is one.
var Reserved = []string{"-"}

// CheckNames holds groups, and the projects placed among them, to the
// rules the web routes by. Each group has an identifier, a name and
// aliases that are names, and no two groups share an identifier. Each
// placed project names a group that is there, and has aliases only when it
// has a group. Group names, group aliases and project aliases share one
// namespace, the first segment of a web URL: two that differ at most in
// ASCII case collide, a name given twice collides with itself, and none is
// a reserved first segment. A project's own name is unique within its
// group, which the project names it is declared under already are.
//
// The instance's declaration is held to these rules when it is read, and
// an export when it is built and read.
func CheckNames(groups []Group, placed map[string]Placement) error {
	ids := map[string]Group{}
	sorted := append([]Group{}, groups...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	claims := map[string]string{}
	for _, r := range Reserved {
		claims[strings.ToLower(r)] = "the web server's own paths"
	}
	claim := func(name, by string) error {
		key := strings.ToLower(name)
		if other, taken := claims[key]; taken {
			return fmt.Errorf("%s and %s are both %q, ignoring case, and one first segment of a web URL names one group or project", other, by, name)
		}
		claims[key] = by
		return nil
	}
	for _, g := range sorted {
		if !ValidName(g.ID) {
			return fmt.Errorf("a group's identifier is %q, which is not a name", g.ID)
		}
		if _, twice := ids[g.ID]; twice {
			return fmt.Errorf("the group %s is declared twice", g.ID)
		}
		ids[g.ID] = g
		names := append([]string{g.Name}, g.Aliases...)
		for i, name := range names {
			by := "the name of group " + g.ID
			if i > 0 {
				by = "alias " + strconv.Itoa(i-1) + " of group " + g.ID
			}
			if !ValidName(name) {
				return fmt.Errorf("%s is %q, which is not a name", by, name)
			}
			if err := claim(name, by); err != nil {
				return err
			}
		}
	}
	projects := make([]string, 0, len(placed))
	for name := range placed {
		projects = append(projects, name)
	}
	sort.Strings(projects)
	for _, project := range projects {
		p := placed[project]
		switch _, ok := ids[p.Group]; {
		case p.Group == "" && len(p.Aliases) > 0:
			return fmt.Errorf("the project %s has aliases and no group, so they have no URL to redirect to", project)
		case p.Group != "" && !ok:
			return fmt.Errorf("the project %s belongs to the group %q, which is not declared", project, p.Group)
		}
		for i, alias := range p.Aliases {
			by := "alias " + strconv.Itoa(i) + " of project " + project
			if !ValidName(alias) {
				return fmt.Errorf("%s is %q, which is not a name", by, alias)
			}
			if err := claim(alias, by); err != nil {
				return err
			}
		}
	}
	return nil
}

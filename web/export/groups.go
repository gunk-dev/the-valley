package export

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Group is a group of projects, as an export carries it: its identifier,
// its name, and its aliases. A project's web URLs open with its group's
// name, /<name>/<project's web name>/…, and a URL under an alias
// redirects to the same path under the name. The identifier never
// changes, and the projects of the group name it; no URL shows it.
type Group struct {
	ID      string   `json:"group"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

// Placement is where the instance places a project on the web: the
// identifier of its group, its web name within the group, its former web
// names there, and its short aliases. Its web URLs are
// /<group name>/<name>/…. /<group name>/<former name>/… and /<alias>/…
// redirect to the same path under them. A project with no group has no
// web URL, and no web name, former names or aliases.
type Placement struct {
	Group       string
	Name        string
	FormerNames []string
	Aliases     []string
}

// Placed is a project, by its identifier, and where the instance places
// it.
type Placed struct {
	Project string
	Placement
}

// Sorted is placements, by project, in project order, as CheckNames takes
// them.
func Sorted(placements map[string]Placement) []Placed {
	out := make([]Placed, 0, len(placements))
	for project, p := range placements {
		out = append(out, Placed{project, p})
	}
	slices.SortFunc(out, func(a, b Placed) int { return strings.Compare(a.Project, b.Project) })
	return out
}

// Reserved are the first segments of the web server's own paths. No
// group's name or alias, and no project's alias, is one.
var Reserved = []string{"-"}

// CheckNames holds groups, and the projects placed among them, to the
// rules the web routes by. groups are in identifier order, and placed in
// project order, each once. Each group's identifier, name and aliases are
// names. Each placed project's identifier is a name, and a project in a
// group has a web name that is a name, while one in no group has no web
// name, no former names and no aliases. Each group a project names is
// there.
//
// The names share two namespaces. Group names, group aliases and project
// aliases share the first segment of a web URL. The web names and former
// names of one group's projects share the second segment of that group's
// URLs. In each namespace, two names that differ at most in ASCII case
// collide, and a name given twice collides with itself. No first segment
// is a reserved one. Two groups' projects never collide, so each group
// may hold a project named app.
//
// CheckNames checks ctx before each name it reads, so the work between
// two checks is bounded by the length of a name. The instance's
// declaration is held to these rules when it is read, and an export when
// it is built and read.
func CheckNames(ctx context.Context, groups []Group, placed []Placed) error {
	first := map[string]string{}
	for _, r := range Reserved {
		first[strings.ToLower(r)] = "the web server's own paths"
	}
	second := map[string]map[string]string{}
	// claim checks ctx, then claims name for by in names. segment says
	// what the namespace is, for the message.
	claim := func(names map[string]string, name, by, segment string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ValidName(name) {
			return fmt.Errorf("%s is %q, which is not a name", by, name)
		}
		key := strings.ToLower(name)
		if other, taken := names[key]; taken {
			return fmt.Errorf("%s and %s are both %q, ignoring case, and %s", other, by, name, segment)
		}
		names[key] = by
		return nil
	}
	const firstSegment = "one first segment of a web URL names one group or project"
	for i, g := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ValidName(g.ID) {
			return fmt.Errorf("a group's identifier is %q, which is not a name", g.ID)
		}
		if i > 0 && groups[i-1].ID >= g.ID {
			return fmt.Errorf("the group %s is listed out of order or twice", g.ID)
		}
		second[g.ID] = map[string]string{}
		if err := claim(first, g.Name, "the name of group "+g.ID, firstSegment); err != nil {
			return err
		}
		for j, alias := range g.Aliases {
			if err := claim(first, alias, "alias "+strconv.Itoa(j)+" of group "+g.ID, firstSegment); err != nil {
				return err
			}
		}
	}
	for i, p := range placed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ValidName(p.Project) {
			return fmt.Errorf("a project's identifier is %q, which is not a name", p.Project)
		}
		if i > 0 && placed[i-1].Project >= p.Project {
			return fmt.Errorf("the project %s is placed out of order or twice", p.Project)
		}
		names, declared := second[p.Group]
		switch {
		case p.Group == "" && (p.Name != "" || len(p.FormerNames) > 0 || len(p.Aliases) > 0):
			return fmt.Errorf("the project %s has a web name, former names or aliases and no group, so they have no URL", p.Project)
		case p.Group == "":
			continue
		case !declared:
			return fmt.Errorf("the project %s belongs to the group %q, which is not declared", p.Project, p.Group)
		}
		segment := "one second segment of a web URL under the group " + p.Group + " names one project"
		if err := claim(names, p.Name, "the web name of project "+p.Project, segment); err != nil {
			return err
		}
		for j, former := range p.FormerNames {
			if err := claim(names, former, "former name "+strconv.Itoa(j)+" of project "+p.Project, segment); err != nil {
				return err
			}
		}
		for j, alias := range p.Aliases {
			if err := claim(first, alias, "alias "+strconv.Itoa(j)+" of project "+p.Project, firstSegment); err != nil {
				return err
			}
		}
	}
	return nil
}

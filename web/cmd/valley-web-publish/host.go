package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"the-valley/exactjson"
	"the-valley/web/export"
)

// hostDoc is the host declaration as cue exports it under
// schema/valley.cue. The publisher reads the groups, and each project's
// group, web name, former names and short aliases. The other fields are
// taken as raw JSON, and exactjson holds every key to the schema's
// spelling, so a Group beside a group is refused rather than read as it.
type hostDoc struct {
	Projects map[string]hostProject `json:"projects"`
	Groups   map[string]hostGroup   `json:"groups"`
	Backup   json.RawMessage        `json:"backup"`
}

type hostProject struct {
	Git         json.RawMessage `json:"git"`
	Mirrors     json.RawMessage `json:"mirrors"`
	Protection  json.RawMessage `json:"protection"`
	Grants      json.RawMessage `json:"grants"`
	Group       string          `json:"group"`
	Name        string          `json:"name"`
	FormerNames []string        `json:"formerNames"`
	Aliases     []string        `json:"aliases"`
}

type hostGroup struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

// webPlaces are where the instance places its projects on the web: its
// groups, in identifier order, and where it places each declared project.
type webPlaces struct {
	groups     []export.Group
	placements map[string]export.Placement
}

// readHost reads the host declaration at path and holds its groups and
// projects to export.CheckNames. A declaration whose project's
// identifier is not a name, whose names collide, or that names a group it
// does not declare, is refused, and no pass runs: a project whose URL is
// in doubt is not published.
func readHost(path string) (webPlaces, error) {
	var w webPlaces
	data, err := os.ReadFile(path)
	if err != nil {
		return w, fmt.Errorf("--host: %w", err)
	}
	refuse := func(err error) (webPlaces, error) {
		return webPlaces{}, fmt.Errorf("--host %s is not a host declaration as cue exports it: %w", path, err)
	}
	var doc hostDoc
	if err := exactjson.Decode(data, &doc); err != nil {
		return refuse(err)
	}
	w.placements = map[string]export.Placement{}
	for name, p := range doc.Projects {
		switch {
		case p.Group == "" && p.Name != "" && p.Name != name:
			return refuse(fmt.Errorf("the project %s is in no group, and its web name is %q", name, p.Name))
		case p.Group == "":
			// cue exports a project's identifier as its web name. A
			// project in no group has none.
			w.placements[name] = export.Placement{FormerNames: p.FormerNames, Aliases: p.Aliases}
		default:
			w.placements[name] = export.Placement{Group: p.Group, Name: p.Name, FormerNames: p.FormerNames, Aliases: p.Aliases}
		}
	}
	for id, g := range doc.Groups {
		w.groups = append(w.groups, export.Group{ID: id, Name: g.Name, Aliases: g.Aliases})
	}
	slices.SortFunc(w.groups, func(a, b export.Group) int { return strings.Compare(a.ID, b.ID) })
	if err := export.CheckNames(context.Background(), w.groups, export.Sorted(w.placements)); err != nil {
		return webPlaces{}, fmt.Errorf("--host %s is refused: %w", path, err)
	}
	return w, nil
}

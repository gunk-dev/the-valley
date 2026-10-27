package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"the-valley/exactjson"
	"the-valley/web/export"
)

// hostDoc is the host declaration as cue exports it under
// schema/valley.cue. The publisher reads the groups and each project's
// group and short aliases. The other fields are taken as raw JSON, and
// exactjson holds every key to the schema's spelling, so a Group beside a
// group is refused rather than read as it.
type hostDoc struct {
	Projects map[string]struct {
		Git        json.RawMessage `json:"git"`
		Mirrors    json.RawMessage `json:"mirrors"`
		Protection json.RawMessage `json:"protection"`
		Grants     json.RawMessage `json:"grants"`
		Group      string          `json:"group"`
		Aliases    []string        `json:"aliases"`
	} `json:"projects"`
	Groups map[string]struct {
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
	} `json:"groups"`
	Backup json.RawMessage `json:"backup"`
}

// webPlaces are where the instance places its projects on the web: its
// groups, in identifier order, and each declared project's group and
// short aliases.
type webPlaces struct {
	groups     []export.Group
	placements map[string]export.Placement
}

// readHost reads the host declaration at path and holds its groups and
// aliases to export.CheckNames. A declaration whose names collide, or
// that names a group it does not declare, is refused, and no pass runs:
// a project whose URL is in doubt is not published.
func readHost(path string) (webPlaces, error) {
	var w webPlaces
	data, err := os.ReadFile(path)
	if err != nil {
		return w, fmt.Errorf("--host: %w", err)
	}
	var doc hostDoc
	if err := exactjson.Decode(data, &doc); err != nil {
		return w, fmt.Errorf("--host %s is not a host declaration as cue exports it: %w", path, err)
	}
	w.placements = map[string]export.Placement{}
	for name, p := range doc.Projects {
		w.placements[name] = export.Placement{Group: p.Group, Aliases: p.Aliases}
	}
	for id, g := range doc.Groups {
		w.groups = append(w.groups, export.Group{ID: id, Name: g.Name, Aliases: g.Aliases})
	}
	sort.Slice(w.groups, func(i, j int) bool { return w.groups[i].ID < w.groups[j].ID })
	if err := export.CheckNames(w.groups, w.placements); err != nil {
		return w, fmt.Errorf("--host %s is refused: %w", path, err)
	}
	return w, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
// cue exports projects and groups always, and each declaration as an
// object. Pointers tell an object from a null or a missing field, which
// the publisher refuses.
type hostDoc struct {
	Projects *map[string]*hostProject `json:"projects"`
	Groups   *map[string]*hostGroup   `json:"groups"`
	Backup   json.RawMessage          `json:"backup"`
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
// projects to export.CheckNames. A declaration that is not an object
// with projects and groups, or whose project or group is not an object,
// is refused, as is one whose project's identifier is not a name, whose
// names collide, or that names a group it does not declare. No pass runs
// then: a pass over a declaration it cannot read would publish an export
// without the projects it lost, and a project whose URL is in doubt is
// not published.
func readHost(path string) (webPlaces, error) {
	var w webPlaces
	data, err := os.ReadFile(path)
	if err != nil {
		return w, fmt.Errorf("--host: %w", err)
	}
	refuse := func(err error) (webPlaces, error) {
		return webPlaces{}, fmt.Errorf("--host %s: invalid host declaration: %w", path, err)
	}
	// encoding/json reads a null into a struct as nothing at all, so the
	// document opens an object, or it is refused.
	if !bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("{")) {
		return refuse(errors.New("the document must be a JSON object"))
	}
	var doc hostDoc
	if err := exactjson.Decode(data, &doc); err != nil {
		return refuse(err)
	}
	if doc.Projects == nil || doc.Groups == nil {
		return refuse(errors.New("projects and groups are required and must be objects"))
	}
	w.placements = map[string]export.Placement{}
	for name, p := range *doc.Projects {
		switch {
		case p == nil:
			return refuse(fmt.Errorf("project %q is null", name))
		case p.Group == "" && p.Name != "" && p.Name != name:
			return refuse(fmt.Errorf("project %s sets web name %q without a group", name, p.Name))
		case p.Group == "":
			// cue exports a project's identifier as its web name. A
			// project in no group has none.
			w.placements[name] = export.Placement{FormerNames: p.FormerNames, Aliases: p.Aliases}
		default:
			w.placements[name] = export.Placement{Group: p.Group, Name: p.Name, FormerNames: p.FormerNames, Aliases: p.Aliases}
		}
	}
	for id, g := range *doc.Groups {
		if g == nil {
			return refuse(fmt.Errorf("group %q is null", id))
		}
		w.groups = append(w.groups, export.Group{ID: id, Name: g.Name, Aliases: g.Aliases})
	}
	slices.SortFunc(w.groups, func(a, b export.Group) int { return strings.Compare(a.ID, b.ID) })
	if err := export.CheckNames(context.Background(), w.groups, export.Sorted(w.placements)); err != nil {
		return webPlaces{}, fmt.Errorf("--host %s is refused: %w", path, err)
	}
	return w, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"the-valley/change"
	"the-valley/rebase"
)

// The journal is every write the restacker intends and has not yet seen
// through, kept in the state directory before the first of them is
// pushed, so a pass that ends part way can tell afterwards what it did. It
// is the restacker's own, local file, journal.json, in the format
// valley-restack-journal/v1:
//
//	{
//	  "schema": "valley-restack-journal/v1",
//	  "repository": "<--repo>", "target": "main", "principal": "<--principal>",
//	  "families": [
//	    {"operation": "20261020T120400Z-1a2b3c4d",
//	     "tip": "<main's tip as captured>",
//	     "evidence": "<the evidence fingerprint as captured>",
//	     "observed": { the whole observation: Observation },
//	     "branches": { the branch push, as rebase.PendingBranches keeps it } or null,
//	     "versions": [ each version, as rebase.PendingVersion keeps it, lower layers first ],
//	     "reports": [ {"pending": { as rebase.PendingReport keeps it },
//	                   "omitted": [ what the report leaves out of the observation ]} ],
//	     "replacements": [ each report of what did happen, in the form of a report ],
//	     "accepted": {"branches": <whether origin took the branch push>,
//	                  "versions": [ the change ID of each version origin added, lower layers first ]}}
//	  ]
//	}
//
// Each family carries the operation that built it, and main's tip and the
// evidence fingerprint that operation captured, since a journal can hold
// families of several operations: a family stays in the journal while it
// is open (publish.go), and a later pass adds the families it builds. No
// two families share a change or a branch, since the planner has a family
// that would wait (waits). A
// family has a branch push and versions only when its operation would
// restack it. A replacement is a report of what did happen to a family
// whose branches moved, built when origin's report says otherwise
// (publish.go). What a read of origin showed it took of a family's writes,
// its branch push and each version it added, is kept as accepted
// (confirm), so no later movement of a branch or a record unmakes it.
// Each version is leased on the record commit the capture read, and each
// report and replacement on the report origin held. Every write names the
// object it would push by its full ID, and every one of those objects is
// kept reachable in the scratch repository, under
// refs/valley-restack/<operation>/<object>, for as long as the journal
// names it (keep).
//
// The journal is read as strictly as it is written: a field missing,
// unknown or named twice, a form that does not agree with itself, or a
// journal another repository, target or principal wrote is refused, and
// nothing it names is decided (loadJournal). A family agrees with itself
// only when every write it holds is about one of its layers. A family
// that would restack adds exactly one version of each of its changes.
// Each report gives the family's result, and says published exactly what
// the family's versions add: nothing, for a family that adds none. Each
// replacement is kept only once origin is known to have taken the branch
// push. It says complete or partial, says each change was published at
// its prepared head, and names a version only as the family adds it. It
// says partial when a change it names has no version, or when it leaves
// out a change, as a fallback may. A
// family accepts a version only as it adds it, and only once its branch
// push is accepted. A
// journal that would not read back as written is never kept (keep). A
// journal that is missing is never inferred: what origin shows of a write
// no journal names is left for the planner, which holds a change whose
// branch is not at its recorded head.

// journalSchema names the journal's format.
const journalSchema = "valley-restack-journal/v1"

// journalFile is the journal's name in the state directory.
const journalFile = "journal.json"

// retainPrefix is where the scratch repository keeps every object a
// journaled operation would push.
const retainPrefix = "refs/valley-restack/"

// Journal is the writes of every family still to be seen through.
type Journal struct {
	Schema     string          `json:"schema"`
	Repository string          `json:"repository"`
	Target     string          `json:"target"`
	Principal  string          `json:"principal"`
	Families   []JournalFamily `json:"families"`
}

// JournalFamily is one family's observation and the writes it would make.
type JournalFamily struct {
	Operation    string                   `json:"operation"` // the operation that built it
	Tip          string                   `json:"tip"`       // main's tip as that operation captured it
	Evidence     string                   `json:"evidence"`  // change.EvidenceFingerprint as captured
	Observed     Observation              `json:"observed"`
	Branches     *rebase.PendingBranches  `json:"branches"`
	Versions     []*rebase.PendingVersion `json:"versions"`
	Reports      []JournalReport          `json:"reports"`
	Replacements []JournalReport          `json:"replacements"` // reports of what did happen, at most one a change
	Accepted     Accepted                 `json:"accepted"`
}

// Accepted is what a read of origin showed it took of a family's writes:
// its branch push, and the version of each change it added. Once the
// journal keeps it, recovery decides those writes done whatever becomes
// of the branches and records after, so an author who rewrites or deletes
// a restacked branch does not unmake the obligation to report what was
// published (confirm).
type Accepted struct {
	Branches bool     `json:"branches"`
	Versions []string `json:"versions"` // change IDs, lower layers first
}

// has reports whether origin is known to have added the change's version.
func (a Accepted) has(id string) bool {
	return slices.Contains(a.Versions, id)
}

// JournalReport is one change's report, and what it leaves out of the
// observation it was cut from: nothing for a report that holds it whole.
type JournalReport struct {
	Pending *rebase.PendingReport `json:"pending"`
	Omitted []string              `json:"omitted"`
}

// operationName is the shape of an operation's name, as a report's
// operation field takes it.
var operationName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]*$`)

// check refuses a journal that does not agree with itself, or that
// another repository, target or principal wrote.
func (j *Journal) check(b binding) error {
	switch {
	case j.Schema != journalSchema:
		return fmt.Errorf("its schema is %q, where %q is expected", j.Schema, journalSchema)
	case j.Repository != b.Repository || j.Target != b.Target || j.Principal != b.Principal:
		return fmt.Errorf("it belongs to %s on %s as %s, and this pass is for %s on %s as %s",
			j.Target, j.Repository, j.Principal, b.Target, b.Repository, b.Principal)
	}
	whose := map[string]int{} // the family each change and branch is in
	for i, f := range j.Families {
		if err := f.check(j.Target); err != nil {
			return fmt.Errorf("its family %d: %w", i, err)
		}
		for _, l := range f.Observed.Layers {
			for _, key := range layerKeys(l.ID, l.Branch) {
				if k, found := whose[key]; found && k != i {
					return fmt.Errorf("its families %d and %d share the %s", k, i, key)
				}
				whose[key] = i
			}
		}
	}
	return nil
}

// layerKeys names what a layer is, for telling whether two families
// share it: "change <ID>" for a change, and "branch <name>" for the
// branch it is on.
func layerKeys(id, branch string) []string {
	var keys []string
	if id != "" {
		keys = append(keys, "change "+id)
	}
	if branch != "" {
		keys = append(keys, "branch "+branch)
	}
	return keys
}

func (f *JournalFamily) check(target string) error {
	o := f.Observed
	switch {
	case !operationName.MatchString(f.Operation) || len(f.Operation) > change.MaxOperation:
		return fmt.Errorf("its operation %q is not a report's operation name", f.Operation)
	case !rebase.IsObjectID(f.Tip):
		return fmt.Errorf("its tip %q is not a full object ID", f.Tip)
	case !hexOf(f.Evidence, 16):
		return fmt.Errorf("its evidence %q is not an evidence fingerprint", f.Evidence)
	case o.Action != restack && o.Action != hold:
		return fmt.Errorf("its action is %q, where %q or %q is expected", o.Action, restack, hold)
	case o.Result != change.RestackComplete && o.Result != change.RestackConflict && o.Result != change.RestackBlocked:
		return fmt.Errorf("its result is %q", o.Result)
	case len(o.Layers) == 0:
		return errors.New("it observed no layer")
	}
	layers := map[string]ObservedLayer{} // change layers by ID
	order := map[string]int{}
	branches := map[string]ObservedLayer{}
	for i, l := range o.Layers {
		switch {
		case l.Name == "":
			return fmt.Errorf("its layer %d has no name", i)
		case l.ID != "" && !change.ValidID(l.ID):
			return fmt.Errorf("its layer %s names the change ID %q", l.Name, l.ID)
		case l.ID != "" && (!rebase.IsObjectID(l.Record) || l.Version < 1):
			return fmt.Errorf("its layer %s names no record commit and version", l.Name)
		case l.ID == "" && (l.Record != "" || l.Version != 0):
			return fmt.Errorf("its plain branch %s names a record", l.Name)
		case !rebase.IsObjectID(l.Head):
			return fmt.Errorf("its layer %s names the head %q", l.Name, l.Head)
		case l.Branch != "" && !change.ValidBranch(l.Branch):
			return fmt.Errorf("its layer %s names the branch %q", l.Name, l.Branch)
		case l.BranchTip != "" && !rebase.IsObjectID(l.BranchTip):
			return fmt.Errorf("its layer %s names the branch tip %q", l.Name, l.BranchTip)
		case l.ID != "" && layers[l.ID].ID != "":
			return fmt.Errorf("it observed %s twice", l.ID)
		}
		if l.ID != "" {
			layers[l.ID], order[l.ID] = l, i
		}
		if l.Branch != "" {
			branches[l.Branch] = l
		}
	}
	if o.Failing != "" && layers[o.Failing].ID == "" {
		return fmt.Errorf("its failing layer %s is none of its changes", o.Failing)
	}

	if (f.Branches != nil) != (o.Result == change.RestackComplete) || (f.Branches == nil) != (len(f.Versions) == 0) {
		return errors.New("it has a branch push and versions exactly when it would restack")
	}
	news := map[string]string{}
	if b := f.Branches; b != nil {
		if b.Tip != f.Tip {
			return fmt.Errorf("its branch push was prepared on %s, not the captured tip %s", b.Tip, f.Tip)
		}
		for _, u := range b.Updates {
			l, ok := branches[u.Branch]
			if !ok || l.BranchTip != u.Old {
				return fmt.Errorf("its branch push moves %s from %s, which it did not observe", u.Branch, u.Old)
			}
			news[u.Branch] = u.New
		}
		if len(b.Updates) != len(o.Layers) {
			return errors.New("its branch push does not move every layer")
		}
	}
	last := -1
	written := map[string]*rebase.PendingVersion{} // each version by change ID
	for _, v := range f.Versions {
		if v == nil {
			return errors.New("it holds an empty version")
		}
		l, ok := layers[v.ID]
		switch {
		case !ok:
			return fmt.Errorf("it adds a version to %s, which is none of its changes", v.ID)
		case !v.Restack():
			return fmt.Errorf("its version of %s is not a restack's", v.ID)
		case v.Old != l.Record || v.Branch != l.Branch || v.Head != news[l.Branch]:
			return fmt.Errorf("its version of %s is not leased on the record it observed, or does not record the head prepared for %s", v.ID, l.Branch)
		case order[v.ID] <= last:
			return fmt.Errorf("its versions are not lower layers first")
		}
		last = order[v.ID]
		written[v.ID] = v
	}
	if f.Branches != nil && len(written) != len(layers) {
		return fmt.Errorf("it would restack %d changes and adds a version to %d of them, where each change gets exactly one", len(layers), len(written))
	}
	if f.Accepted.Branches && f.Branches == nil {
		return errors.New("origin took a branch push it does not have")
	}
	last = -1
	for _, id := range f.Accepted.Versions {
		switch {
		case !f.Accepted.Branches:
			return fmt.Errorf("origin added its version of %s, and is not known to have taken its branch push", id)
		case written[id] == nil:
			return fmt.Errorf("origin added a version of %s, which the family does not add", id)
		case order[id] <= last:
			return errors.New("its accepted versions are not lower layers first, once each")
		}
		last = order[id]
	}
	reported := map[string]bool{}
	for _, r := range f.Reports {
		p := r.Pending
		switch {
		case p == nil:
			return errors.New("it holds an empty report")
		case layers[p.ID].ID == "":
			return fmt.Errorf("it reports on %s, which is none of its changes", p.ID)
		case reported[p.ID]:
			return fmt.Errorf("it reports on %s twice", p.ID)
		case !f.ours(p.Report, target):
			return fmt.Errorf("its report on %s is not this operation's, on %s at %s", p.ID, target, f.Tip)
		case p.Report.Result != o.Result:
			return fmt.Errorf("its report on %s says %s, and the family's result is %s", p.ID, p.Report.Result, o.Result)
		}
		if err := checkClaims(p.Report, layers, written); err != nil {
			return fmt.Errorf("its report on %s %w", p.ID, err)
		}
		reported[p.ID] = true
	}
	replaced := map[string]bool{}
	for _, r := range f.Replacements {
		p := r.Pending
		switch {
		case p == nil:
			return errors.New("it holds an empty replacement")
		case !f.Accepted.Branches:
			return fmt.Errorf("it replaces the report on %s, and origin is not known to have taken its branch push", p.ID)
		case layers[p.ID].ID == "":
			return fmt.Errorf("it replaces a report on %s, which is none of its changes", p.ID)
		case replaced[p.ID]:
			return fmt.Errorf("it replaces the report on %s twice", p.ID)
		case !f.ours(p.Report, target):
			return fmt.Errorf("its replacement on %s is not this operation's, on %s at %s", p.ID, target, f.Tip)
		}
		if err := checkHappened(p.Report, layers, written, news); err != nil {
			return fmt.Errorf("its replacement on %s %w", p.ID, err)
		}
		replaced[p.ID] = true
	}
	return nil
}

// ours reports whether a report is the family's operation's, on the
// target at the tip the operation captured.
func (f *JournalFamily) ours(r *change.RestackReport, target string) bool {
	return r.Operation == f.Operation && r.Target == target && r.TargetTip == change.HeadPrefix+f.Tip
}

// checkHappened refuses a replacement that says other than what could
// have happened to a family whose branches moved: each of its layers is
// one of the family's changes, published at the head prepared for its
// branch, with a version only as the family adds it. Whether it is
// complete is a matter of the whole family, which a fallback names only
// part of (bound). It says complete only when each change it names has
// its version. It says partial when a change it names has none, or when
// it leaves out a change, which may be the one whose version was not
// added.
func checkHappened(r *change.RestackReport, layers map[string]ObservedLayer, written map[string]*rebase.PendingVersion, news map[string]string) error {
	all, named := true, map[string]bool{}
	for _, l := range r.Layers {
		named[l.ChangeID] = true
		v, p := written[l.ChangeID], l.Published
		switch {
		case layers[l.ChangeID].ID == "":
			return fmt.Errorf("has a layer for %s, which is none of the family's changes", l.ChangeID)
		case p == nil || p.Head != change.HeadPrefix+news[layers[l.ChangeID].Branch]:
			return fmt.Errorf("does not say %s was published at the head prepared for it", l.ChangeID)
		case p.Version == nil && p.Record == nil:
			all = false
		case v == nil || p.Version == nil || *p.Version != v.Version || p.Record == nil || *p.Record != change.HeadPrefix+v.Commit:
			return fmt.Errorf("says %s was published as a version the family does not add", l.ChangeID)
		}
	}
	switch {
	case r.Result == change.RestackComplete && !all:
		return errors.New("says complete, and names a change with no version")
	case r.Result == change.RestackPartial && all && len(named) == len(layers):
		return errors.New("says partial, and names every change of the family with its version")
	case r.Result != change.RestackComplete && r.Result != change.RestackPartial:
		return fmt.Errorf("says %s, where complete or partial is expected", r.Result)
	}
	return nil
}

// checkClaims refuses a report that says something was published other
// than what the family's versions add. Each of the report's layers is one
// of the family's changes. A layer says it was published exactly when the
// family adds a version of that change, and then names that version's
// head, number and record commit.
func checkClaims(r *change.RestackReport, layers map[string]ObservedLayer, written map[string]*rebase.PendingVersion) error {
	for _, l := range r.Layers {
		v, p := written[l.ChangeID], l.Published
		switch {
		case layers[l.ChangeID].ID == "":
			return fmt.Errorf("has a layer for %s, which is none of the family's changes", l.ChangeID)
		case v == nil && p != nil:
			return fmt.Errorf("says %s was published, and the family adds no version of it", l.ChangeID)
		case v != nil && p == nil:
			return fmt.Errorf("says nothing was published for %s, and the family adds v%d of it", l.ChangeID, v.Version)
		case v != nil && (p.Head != change.HeadPrefix+v.Head || p.Version == nil || *p.Version != v.Version ||
			p.Record == nil || *p.Record != change.HeadPrefix+v.Commit):
			return fmt.Errorf("says %s was published as other than v%d at %s, record %s, which the family adds", l.ChangeID, v.Version, short(v.Head), short(v.Commit))
		}
	}
	return nil
}

func hexOf(s string, n int) bool {
	return len(s) == n && strings.Trim(s, "0123456789abcdef") == ""
}

// loadJournal reads the journal in the state directory: nil when there is
// none. A journal that does not read, or that is another's, is an error,
// and nothing it names is decided.
func loadJournal(dir string, b binding) (*Journal, error) {
	path := filepath.Join(dir, journalFile)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading the journal %s: %w", path, err)
	}
	var j Journal
	if err := strictly(data, &j); err != nil {
		return nil, fmt.Errorf("the journal %s does not read, so nothing it names is decided: %w", path, err)
	}
	if err := j.check(b); err != nil {
		return nil, fmt.Errorf("the journal %s is refused, so nothing it names is decided: %w", path, err)
	}
	return &j, nil
}

// strictly reads data into v, and takes only what json.Marshal writes of
// v, as rebase's kept forms are read: space between tokens is all that may
// differ.
func strictly(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return err
	}
	again, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if !bytes.Equal(compact.Bytes(), again) {
		return errors.New("it is not in the form it is kept in: a field is missing or named twice, or a value is written another way")
	}
	return nil
}

// retained is every object the journal's writes would push, each by the
// ref the scratch repository keeps it under: each prepared head, each
// record commit, each report commit and each replacement's.
func (j *Journal) retained() map[string]string {
	refs := map[string]string{}
	for _, f := range j.Families {
		var oids []string
		if f.Branches != nil {
			for _, u := range f.Branches.Updates {
				oids = append(oids, u.New)
			}
		}
		for _, v := range f.Versions {
			oids = append(oids, v.Commit)
		}
		for _, r := range append(append([]JournalReport{}, f.Reports...), f.Replacements...) {
			oids = append(oids, r.Pending.Commit)
		}
		for _, oid := range oids {
			refs[retainPrefix+f.Operation+"/"+oid] = oid
		}
	}
	return refs
}

// keep makes the journal durable before any of its writes is pushed. It
// archives the observation of each family whose reports leave something
// out of it, and keeps every object the writes would push reachable in
// the scratch repository, under refs/valley-restack/<operation>/<object>.
// It then writes the journal to the state directory, synced, in place of
// any before it. Only then does it let go of each object the journal no
// longer names. A journal that would not read back as written is refused
// before anything is kept, since a later pass would refuse it in turn.
func keep(ctx context.Context, g rebase.Git, dir string, j *Journal) error {
	if err := j.check(binding{Repository: j.Repository, Target: j.Target, Principal: j.Principal}); err != nil {
		return fmt.Errorf("the journal cannot be kept: %w", err)
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := strictly(data, &Journal{}); err != nil {
		return fmt.Errorf("the journal cannot be kept, since it would not read back as written: %w", err)
	}
	for _, f := range j.Families {
		for _, r := range f.Reports {
			if len(r.Omitted) > 0 {
				if err := archive(dir, f.Observed); err != nil {
					return fmt.Errorf("the journal cannot be kept, since the observation its reports are cut from cannot be archived: %w", err)
				}
				break
			}
		}
	}
	refs := j.retained()
	var names []string
	for ref := range refs {
		names = append(names, ref)
	}
	sort.Strings(names)
	var stdin strings.Builder
	for _, ref := range names {
		fmt.Fprintf(&stdin, "update %s %s\n", ref, refs[ref])
	}
	if err := updateRefs(ctx, g, stdin.String()); err != nil {
		return fmt.Errorf("could not keep the objects of the journal: %w", err)
	}
	if err := writeDurably(dir, journalFile, append(data, '\n')); err != nil {
		return err
	}
	return letGo(ctx, g, refs)
}

// observationsDir is where the state directory keeps the whole
// observation a report was cut from, when the report leaves something out
// of it.
const observationsDir = "observations"

// archive keeps the whole observation in the state directory as
// observations/<digest>.json, where <digest> is the observation's digest.
// The file holds the observation as the digest is taken of it, so its
// SHA-256 begins with that digest. A report that leaves something out
// names the file. The file outlives the journal: nothing the restacker
// does removes it.
func archive(dir string, o Observation) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	at := filepath.Join(dir, observationsDir)
	if err := os.MkdirAll(at, 0o700); err != nil {
		return err
	}
	return writeDurably(at, o.Digest()+".json", data)
}

// letGo lets go of every object the scratch repository keeps for the
// journal, but those kept names: nil lets go of them all.
func letGo(ctx context.Context, g rebase.Git, kept map[string]string) error {
	out, err := gitOut(ctx, g, "for-each-ref", "--format=%(refname)", retainPrefix)
	if err != nil {
		return err
	}
	var stdin strings.Builder
	for _, ref := range strings.Fields(out) {
		if _, ok := kept[ref]; !ok {
			fmt.Fprintf(&stdin, "delete %s\n", ref)
		}
	}
	if err := updateRefs(ctx, g, stdin.String()); err != nil {
		return fmt.Errorf("could not let go of the objects the journal no longer names: %w", err)
	}
	return nil
}

// updateRefs runs git update-ref --stdin with the instructions given, all
// in one transaction.
func updateRefs(ctx context.Context, g rebase.Git, instructions string) error {
	if instructions == "" {
		return nil
	}
	cmd := g.Command(ctx, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(instructions)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// forget removes the journal from the state directory, durably, and then
// lets go of every object kept for it.
func forget(ctx context.Context, g rebase.Git, dir string) error {
	if err := os.Remove(filepath.Join(dir, journalFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return err
	}
	return letGo(ctx, g, nil)
}

// writeDurably writes a file in dir through a synced temporary file, and
// renames it into place, so a crash leaves the old file or the new one,
// whole. The directory is synced after, so the rename is durable too.
func writeDurably(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if serr := tmp.Sync(); err == nil {
		err = serr
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing %s in %s: %w", name, dir, err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

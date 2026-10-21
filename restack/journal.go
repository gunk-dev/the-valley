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
	"strings"

	"the-valley/change"
	"the-valley/rebase"
)

// The journal is every write one operation intends, kept in the state
// directory before the first of them is pushed, so a pass that ends part
// way can tell afterwards what it did. It is the restacker's own, local
// file, journal.json, in the format valley-restack-journal/v1:
//
//	{
//	  "schema": "valley-restack-journal/v1",
//	  "repository": "<--repo>", "target": "main", "principal": "<--principal>",
//	  "operation": "20261020T120400Z-1a2b3c4d",
//	  "tip": "<main's tip as captured>",
//	  "evidence": "<the evidence fingerprint as captured>",
//	  "families": [
//	    {"observed": { the whole observation: Observation },
//	     "branches": { the branch push, as rebase.PendingBranches keeps it } or null,
//	     "versions": [ each version, as rebase.PendingVersion keeps it, lower layers first ],
//	     "reports": [ {"pending": { as rebase.PendingReport keeps it },
//	                   "omitted": [ what the report leaves out of the observation ]} ]}
//	  ]
//	}
//
// A family has a branch push and versions only when the operation would
// restack it. Each version is leased on the record commit the capture
// read, and each report on the report origin held. Every write names the
// object it would push by its full ID, and every one of those objects is
// kept reachable in the scratch repository, under
// refs/valley-restack/<operation>/, for as long as the journal names it
// (keep).
//
// The journal is read as strictly as it is written: a field missing,
// unknown or named twice, a form that does not agree with itself, or a
// journal another repository, target or principal wrote is refused, and
// nothing it names is decided (loadJournal). A family agrees with itself
// only when every write it holds is about one of its layers. A family
// that would restack adds exactly one version of each of its changes.
// Each report gives the family's result, and says published exactly what
// the family's versions add: nothing, for a family that adds none. A
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

// Journal is one operation's intended writes.
type Journal struct {
	Schema     string          `json:"schema"`
	Repository string          `json:"repository"`
	Target     string          `json:"target"`
	Principal  string          `json:"principal"`
	Operation  string          `json:"operation"`
	Tip        string          `json:"tip"`      // main's tip as captured
	Evidence   string          `json:"evidence"` // change.EvidenceFingerprint as captured
	Families   []JournalFamily `json:"families"`
}

// JournalFamily is one family's observation and the writes it would make.
type JournalFamily struct {
	Observed Observation              `json:"observed"`
	Branches *rebase.PendingBranches  `json:"branches"`
	Versions []*rebase.PendingVersion `json:"versions"`
	Reports  []JournalReport          `json:"reports"`
}

// JournalReport is one change's report, and what it leaves out of the
// family's observation: nothing for a report that holds it whole.
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
	case !operationName.MatchString(j.Operation) || len(j.Operation) > change.MaxOperation:
		return fmt.Errorf("its operation %q is not a report's operation name", j.Operation)
	case !rebase.IsObjectID(j.Tip):
		return fmt.Errorf("its tip %q is not a full object ID", j.Tip)
	case !hexOf(j.Evidence, 16):
		return fmt.Errorf("its evidence %q is not an evidence fingerprint", j.Evidence)
	}
	for i, f := range j.Families {
		if err := f.check(j); err != nil {
			return fmt.Errorf("its family %d: %w", i, err)
		}
	}
	return nil
}

func (f *JournalFamily) check(j *Journal) error {
	o := f.Observed
	switch {
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
		if b.Tip != j.Tip {
			return fmt.Errorf("its branch push was prepared on %s, not the captured tip %s", b.Tip, j.Tip)
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
		case p.Report.Operation != j.Operation || p.Report.Target != j.Target || p.Report.TargetTip != change.HeadPrefix+j.Tip:
			return fmt.Errorf("its report on %s is not this operation's, on %s at %s", p.ID, j.Target, j.Tip)
		case p.Report.Result != o.Result:
			return fmt.Errorf("its report on %s says %s, and the family's result is %s", p.ID, p.Report.Result, o.Result)
		}
		if err := checkClaims(p.Report, layers, written); err != nil {
			return fmt.Errorf("its report on %s %w", p.ID, err)
		}
		reported[p.ID] = true
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

// objects is every object the journal's writes would push: each prepared
// head, each record commit and each report commit.
func (j *Journal) objects() []string {
	var all []string
	for _, f := range j.Families {
		if f.Branches != nil {
			for _, u := range f.Branches.Updates {
				all = append(all, u.New)
			}
		}
		for _, v := range f.Versions {
			all = append(all, v.Commit)
		}
		for _, r := range f.Reports {
			all = append(all, r.Pending.Commit)
		}
	}
	return all
}

// keep makes the journal durable before any of its writes is pushed: it
// keeps every object the writes would push reachable in the scratch
// repository, under refs/valley-restack/<operation>/, and then writes the
// journal to the state directory, synced, in place of any before it. A
// journal that would not read back as written is refused before anything
// is kept, since a later pass would refuse it in turn.
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
	var stdin strings.Builder
	for i, oid := range j.objects() {
		fmt.Fprintf(&stdin, "update %s%s/%d %s\n", retainPrefix, j.Operation, i, oid)
	}
	cmd := g.Command(ctx, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(stdin.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not keep the objects of operation %s: %v: %s", j.Operation, err, strings.TrimSpace(string(out)))
	}
	return writeDurably(dir, journalFile, append(data, '\n'))
}

// release lets go of what an operation kept in the scratch repository.
func release(ctx context.Context, g rebase.Git, op string) error {
	out, err := gitOut(ctx, g, "for-each-ref", "--format=delete %(refname)", retainPrefix+op+"/")
	if err != nil {
		return err
	}
	cmd := g.Command(ctx, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(out)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not let go of the objects of operation %s: %v: %s", op, err, strings.TrimSpace(string(out)))
	}
	return nil
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

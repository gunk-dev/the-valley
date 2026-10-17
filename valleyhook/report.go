package main

import (
	"fmt"
	"strings"

	"the-valley/change"
)

// A restack report is a commit at refs/the-valley/restack/<target>/<change
// ID> whose tree holds one file, report.json (change/report.go): what the
// host restacker observed when it tried to rebase the change. Each report
// commit has the report it replaces as its one parent, and the first has
// none, so the ref keeps every report the change was given.
//
// The bounds on reading one, which the change module defines for every
// reader of a report.
var reportLayout = oneFile{
	subject: "a restack report", name: "report.json",
	commit: change.MaxReportCommit, tree: change.MaxReportTree, blob: change.MaxReportBlob,
}

const maxReportUpdates = change.MaxReportUpdates // restack report updates in one push

func (gitRepository) report(id string) (fileCommit, error) {
	return readOneFile(id, reportLayout)
}

// hasRecord asks for-each-ref whether the change's record ref exists. A
// pattern also matches the refs below it, so the ref is compared whole.
func (gitRepository) hasRecord(id string) (bool, error) {
	ref := change.Ref(recordTarget, id)
	out, err := run(nil, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == ref {
			return true, nil
		}
	}
	return false, nil
}

// checkReport decides a write to a restack report ref, and returns the
// verb and the reason that refuse it, or "" for the reason when the write
// is allowed. P is the new report commit's parents, and old the ref's
// value before the push.
//
//	create   restack holder   P = []
//	extend   restack holder   P = [old]
//
// The ref's target is main, and its change is one the repository holds a
// record of. The report.json is a valley-restack-report/v1 report about
// that change and that target, read within the format's bounds. No push
// deletes a report or replaces its history.
func (p policy) checkReport(principal string, u update) (verb, why string) {
	if principal == "" || !p.grants.holds(principal, grantRestack) {
		holder := principal + " holds none"
		if principal == "" {
			holder = "a key that names no principal holds no grant"
		}
		return "write", fmt.Sprintf("%s takes writes only from a principal holding the %s grant, and %s", change.RestackPrefix, grantRestack, holder)
	}
	target, id, ok := change.ParseRestackRef(u.ref)
	if !ok || target != recordTarget {
		return "write", fmt.Sprintf("a restack report's ref is %s%s/<change ID>, with the change ID as 32 letters from k to z", change.RestackPrefix, recordTarget)
	}
	verb = "update"
	switch {
	case isZero(u.new):
		return "delete", "a restack report keeps the change's history of reports, and no push deletes one"
	case isZero(u.old):
		verb = "create"
	}
	held, err := p.repo.hasRecord(id)
	switch {
	case err != nil:
		return verb, fmt.Sprintf("whether the repository holds a record of the change %s could not be read: %v", id, err)
	case !held:
		return verb, fmt.Sprintf("the repository holds no record of the change %s, and a restack report is about a change it records", id)
	}
	rc, err := p.repo.report(u.new)
	if err != nil {
		return verb, err.Error()
	}
	switch {
	case verb == "create" && len(rc.parents) != 0:
		return verb, fmt.Sprintf("a change's first restack report has no parent, and this one has %d", len(rc.parents))
	case verb == "update" && (len(rc.parents) != 1 || rc.parents[0] != u.old):
		return verb, fmt.Sprintf("a restack report has the report it replaces, %s, as its one parent, so a push only adds to the history of reports", u.old)
	}
	r, err := change.ParseRestackReport(rc.file)
	switch {
	case err != nil:
		return verb, "its report.json does not read: " + err.Error()
	case r.ChangeID != id:
		return verb, fmt.Sprintf("it is about the change %s, and its ref names %s", r.ChangeID, id)
	case r.Target != target:
		return verb, fmt.Sprintf("it names the target %s, and its ref names %s", r.Target, target)
	}
	return verb, ""
}

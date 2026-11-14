package main

import (
	"fmt"

	"the-valley/approval"
)

// An approval is one signed note stored as a blob at
// refs/the-valley/approvals/<target>/c-<change ID> (approval.Ref): an
// approver's statement that one recorded head of the change may land. A
// new approval replaces the one before it.
//
// The hook holds an approval to its form alone. Whether the key that
// signed it is an approval key, and whether the approval lets anything
// land, is for whatever reads it to decide against the keys it holds.

// approvalBudget bounds the one object an approval ref points at.
var approvalBudget = budget{subject: "an approval", fileSize: approval.MaxNote, bytes: approval.MaxNote}

// approval reads the blob id names, held to approval.MaxNote bytes before
// its content is asked for.
func (gitRepository) approval(id string) ([]byte, error) {
	objects, err := openObjectsWithin(approvalBudget)
	if err != nil {
		return nil, err
	}
	defer objects.close()
	return objects.read(id, "blob", approval.MaxNote)
}

// checkApproval decides a write to an approval ref, and returns the verb
// and the reason that refuse it, or "" for the reason when the write is
// allowed. A principal holding the approve grant creates, replaces and
// deletes one; nobody else writes one, whatever change they own. The ref's
// target is main, as a change record's is, and what it points at is a blob
// of at most approval.MaxNote bytes holding an approval note
// (approval.ParseNote) about the change and the target its ref names.
func (p policy) checkApproval(principal string, u update) (verb, why string) {
	target, id, ok := approval.ParseRef(u.ref)
	if !ok || target != recordTarget {
		return "write", fmt.Sprintf("an approval's ref is %s%s/c-<change ID>, with the change ID as 32 letters from k to z", approval.RefPrefix, recordTarget)
	}
	verb = "update"
	switch {
	case isZero(u.new):
		verb = "delete"
	case isZero(u.old):
		verb = "create"
	}
	if principal == "" || !p.grants.holds(principal, grantApprove) {
		holder := principal + " holds none"
		if principal == "" {
			holder = "a key that names no principal holds no grant"
		}
		return verb, fmt.Sprintf("%s takes writes only from a principal holding the %s, and %s", approval.RefPrefix, approveGrant, holder)
	}
	if verb == "delete" {
		return verb, ""
	}
	body, err := p.repo.approval(u.new)
	if err != nil {
		return verb, "it " + err.Error()
	}
	s, err := approval.ParseNote(body)
	switch {
	case err != nil:
		return verb, "it is not an approval note: " + err.Error()
	case s.Change != id:
		return verb, fmt.Sprintf("it approves the change %s, and its ref names %s", s.Change, id)
	case s.Target != target:
		return verb, fmt.Sprintf("it approves landing on %s, and its ref names %s", s.Target, target)
	}
	return verb, ""
}

package main

import (
	"fmt"
	"strings"

	"the-valley/approval"
	"the-valley/change"
)

// An approval is one signed note stored as a blob at
// refs/the-valley/approvals/<target>/c-<change ID> (approval.Ref): an
// approver's statement that one recorded head of the change may land. A
// new approval replaces the one before it.
//
// The hook holds an approval to its form, and to the integration request
// it lands beside when it is written (besideRequest). Whether the key that
// signed it is an approval key, whether the request beside it still
// matches it, and whether the approval lets anything land, are for
// whatever reads it to decide.

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
// (approval.ParseNote) about the change and the target its ref names, and
// about the head the change's request files (besideRequest).
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
	return verb, p.besideRequest(target, id, s)
}

// besideRequest compares an approval with the integration request of its
// change that it lands beside: the request this push writes, or else the
// one the repository holds. The approval must name the head that request
// files, and a version the change's record gives that head: the record
// this push writes, or else the one the repository holds. An approval
// with no request beside it is not compared.
//
// It returns the reason that refuses the approval, or "". It reads no
// signature and decides nothing about landing.
//
// It compares the pair only as this push writes it, against refs read
// before git installs any of the push, so a matched pair is not kept
// matched. Two pushes that each write a request and its approval
// atomically conflict at the approval's ref, whose old value git checks,
// and one fails whole. Two that write them without --atomic can each win
// one ref, leaving one's approval beside the other's request. A later
// push that writes the request alone moves it away from an approval
// already admitted. valley approve pushes the pair atomically. A
// mismatched pair is not an approval: what compares the signed statement
// with the request at landing is the landing gate (not built).
func (p policy) besideRequest(target, id string, s approval.Statement) string {
	request := requestPrefix + target + "/" + change.RequestName(id)
	head, ok, err := p.current(request)
	switch {
	case err != nil:
		return "the integration request it lands beside cannot be read: " + err.Error()
	case !ok:
		return ""
	case s.Head != change.HeadPrefix+head:
		return fmt.Sprintf("it approves the head %s, and the integration request beside it files %s",
			strings.TrimPrefix(s.Head, change.HeadPrefix), head)
	}
	commit, ok, err := p.current(change.Ref(target, id))
	switch {
	case err != nil:
		return "the change's record cannot be read: " + err.Error()
	case !ok:
		return fmt.Sprintf("it approves v%d, and the repository holds no record of the change", s.Version)
	}
	rc, err := p.repo.record(commit)
	if err != nil {
		return "the change's record " + err.Error()
	}
	r, err := change.Parse(rc.file)
	if err != nil {
		return "the change's record does not read: " + err.Error()
	}
	for _, v := range r.Versions {
		if v.N == s.Version && v.Head == s.Head {
			return ""
		}
	}
	return fmt.Sprintf("it approves v%d at %s, and the change's record gives v%d no such head", s.Version, head, s.Version)
}

// current is the value a ref holds once this push is in: what the push
// writes to it, or else what the repository holds. ok is false when the
// push deletes the ref, or neither writes nor holds one.
func (p policy) current(ref string) (string, bool, error) {
	if v, pushed := p.pushed[ref]; pushed {
		return v, !isZero(v), nil
	}
	return p.repo.ref(ref)
}

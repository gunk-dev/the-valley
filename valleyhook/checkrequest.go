package main

import (
	"bytes"
	"fmt"
	"strings"

	"the-valley/change"
)

// maxCheckRequests is how many check requests one principal may hold in a
// repository. The bound is soft, as the bound on change records is
// (record.go): pushes running at once each count the requests there before
// either installs a ref, so together they can pass it by the requests they
// create. Once a principal is over it, its every later create is refused.
const maxCheckRequests = 32

// checkRequests counts the check requests principal holds now, past the
// bound by one at most: for-each-ref stops printing there.
func (gitRepository) checkRequests(principal string) (int, error) {
	out, err := run(nil, "for-each-ref", fmt.Sprintf("--count=%d", maxCheckRequests+1), "--format=x", change.CheckRequestPrefix+principal+"/")
	if err != nil {
		return 0, err
	}
	return bytes.Count(out, []byte("\n")), nil
}

// requestCreates counts the check requests the push creates under
// principal's own name, the only ones it may create.
func requestCreates(principal string, updates []update) int {
	n := 0
	for _, u := range updates {
		if principal != "" && strings.HasPrefix(u.ref, change.CheckRequestPrefix+principal+"/") && isZero(u.old) && !isZero(u.new) {
			n++
		}
	}
	return n
}

// checkCheckRequest decides a write to a check request's ref, and returns
// the verb and the reason that refuse it, or "" for the reason when the
// write is allowed. A named principal creates, replaces and deletes the
// requests under its own name, and a holder of the approve grant deletes
// any. A request points at a commit, which the worker builds the required
// checks of; nothing else reads it, so it decides nothing that lands.
func (p policy) checkCheckRequest(principal string, u update) (verb, why string) {
	owner, _, _, ok := change.ParseCheckRequestRef(u.ref)
	if !ok {
		return "write", fmt.Sprintf("a check request's ref is %s<principal>/<worktree>/<attempt>, with the worktree and the attempt as %d lowercase hexadecimal digits each", change.CheckRequestPrefix, change.CheckRequestToken)
	}
	if principal == "" {
		return "write", "a check request takes writes only from a named principal, and a key that names no principal is none"
	}
	if isZero(u.new) {
		if principal != owner && !p.grants.holds(principal, grantApprove) {
			return "delete", fmt.Sprintf("a check request is deleted by the principal it is filed under or by a holder of the %s, and %s is neither", approveGrant, principal)
		}
		return "delete", ""
	}
	verb = "update"
	if isZero(u.old) {
		verb = "create"
	}
	if principal != owner {
		return verb, fmt.Sprintf("a principal files check requests under its own name, %s%s/, and this one is under %s", change.CheckRequestPrefix, principal, owner)
	}
	if verb == "create" && p.existingRequests+p.requestCreates > maxCheckRequests {
		return verb, fmt.Sprintf("%s holds %d check requests in %s, and this push would create %d more, past the %d one principal may hold", principal, p.existingRequests, p.project, p.requestCreates, maxCheckRequests)
	}
	kind, err := p.repo.kind(u.new)
	switch {
	case err != nil:
		return verb, fmt.Sprintf("%s %v", u.new, err)
	case kind != "commit":
		return verb, fmt.Sprintf("%s is a %s, and a check request points at a commit", u.new, kind)
	}
	return verb, ""
}

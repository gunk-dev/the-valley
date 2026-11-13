package change

import "strings"

// A check request asks a host's check worker to build the checks a commit
// requires, outside any change. It is a ref pointing straight at the
// commit:
//
//	refs/the-valley/check-requests/<principal>/<worktree>/<attempt>
//
// <principal> is the principal that filed it. <worktree> and <attempt> are
// tokens the requesting client makes, each CheckRequestToken lowercase
// hexadecimal digits: a client names the worktree it asks from with one,
// and each request it files from there with the other, so a rerun of the
// same commit is a new ref. The push hook admits the ref
// (valleyhook/checkrequest.go), and the worker reads it (status/worker.go).

// CheckRequestPrefix opens every check request's ref.
const CheckRequestPrefix = "refs/the-valley/check-requests/"

// CheckRequestToken is how many lowercase hexadecimal digits a check
// request's worktree and attempt tokens each hold.
const CheckRequestToken = 16

// ParseCheckRequestRef splits a check request's ref into its principal,
// worktree and attempt. It reports false for a ref that is not
// refs/the-valley/check-requests/<principal>/<worktree>/<attempt> with a
// principal's name as the principal, and CheckRequestToken lowercase
// hexadecimal digits as each token.
func ParseCheckRequestRef(ref string) (principal, worktree, attempt string, ok bool) {
	rest, found := strings.CutPrefix(ref, CheckRequestPrefix)
	if !found {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || !ValidPrincipal(parts[0]) || !requestToken(parts[1]) || !requestToken(parts[2]) {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// requestToken reports whether s is a check request's worktree or attempt
// token.
func requestToken(s string) bool {
	if len(s) != CheckRequestToken {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

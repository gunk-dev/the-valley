package approval

import (
	"strings"

	"the-valley/change"
)

// RefPrefix opens every stored approval's ref. An approval is stored as
// its note, one blob, at RefPrefix<target>/<request name>, where the
// request name is the one the change's integration request is filed
// under (change.RequestName): refs/the-valley/approvals/main/c-<change ID>.
// A change has one approval ref per target, and a new approval replaces
// the one before it.
const RefPrefix = "refs/the-valley/approvals/"

// Ref is the ref a change's approval for target is stored at.
func Ref(target, id string) string {
	return RefPrefix + target + "/" + change.RequestName(id)
}

// ParseRef splits an approval's ref into its target and change ID. It
// reports false for a ref that is not Ref of a branch name and a change
// ID.
func ParseRef(ref string) (target, id string, ok bool) {
	rest, found := strings.CutPrefix(ref, RefPrefix)
	if !found {
		return "", "", false
	}
	slash := strings.LastIndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	target, name := rest[:slash], rest[slash+1:]
	id, ok = change.RequestID(name)
	if !ok || !change.ValidBranch(target) {
		return "", "", false
	}
	return target, id, true
}

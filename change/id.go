package change

import (
	"crypto/rand"
	"strings"
)

// IDLength is the length of a change ID.
const IDLength = 32

// MinHandle is the shortest a handle may be.
const MinHandle = 4

// RefPrefix opens every change record's ref.
const RefPrefix = "refs/the-valley/changes/"

// Ref is the ref a change's record lives at.
func Ref(target, id string) string {
	return RefPrefix + target + "/" + id
}

// ParseRef splits a record's ref into its target and change ID. It
// reports false for a ref that is not refs/the-valley/changes/<target>/<ID>
// with a branch name as the target and a change ID as the ID.
func ParseRef(ref string) (target, id string, ok bool) {
	rest, found := strings.CutPrefix(ref, RefPrefix)
	if !found {
		return "", "", false
	}
	slash := strings.LastIndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	target, id = rest[:slash], rest[slash+1:]
	if !ValidBranch(target) || !ValidID(id) {
		return "", "", false
	}
	return target, id, true
}

// ValidID reports whether s is a change ID in jj's form: 32 letters from
// k to z.
func ValidID(s string) bool {
	return len(s) == IDLength && isIDText(s)
}

func isIDText(s string) bool {
	for _, c := range []byte(s) {
		if c < 'k' || c > 'z' {
			return false
		}
	}
	return true
}

// NewID makes a change ID the way jj does: 16 random bytes, each half of
// each byte written as one letter, z for 0 down to k for 15.
func NewID() (string, error) {
	var raw [IDLength / 2]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := make([]byte, 0, IDLength)
	for _, b := range raw {
		id = append(id, 'z'-b>>4, 'z'-b&0xf)
	}
	return string(id), nil
}

// Handle is the shortest prefix of id, MinHandle letters at least, that
// no other ID among shares.
func Handle(id string, among []string) string {
	n := MinHandle
	for _, other := range among {
		if other == id {
			continue
		}
		common := 0
		for common < len(id) && common < len(other) && id[common] == other[common] {
			common++
		}
		if common+1 > n {
			n = common + 1
		}
	}
	if n > len(id) {
		n = len(id)
	}
	return id[:n]
}

// Matching lists the IDs among that name could stand for: the ID itself
// when name is a whole change ID, and otherwise every ID it is a prefix
// of, when it is at least MinHandle letters from k to z. A name that
// cannot be a handle matches nothing.
func Matching(name string, among []string) []string {
	if len(name) < MinHandle || len(name) > IDLength || !isIDText(name) {
		return nil
	}
	var found []string
	for _, id := range among {
		if strings.HasPrefix(id, name) {
			found = append(found, id)
		}
	}
	return found
}

// LooksLikeHandle reports whether name has the shape of a handle or a
// change ID, so that it could name a change rather than a branch.
func LooksLikeHandle(name string) bool {
	return len(name) >= MinHandle && len(name) <= IDLength && isIDText(name)
}

// ValidBranch reports whether refs/heads/<name> is a ref name git would
// accept: what `git check-ref-format refs/heads/<name>` accepts. No part
// between slashes is empty, opens with a dot or ends in .lock. No two dots
// sit together, and nothing holds "@{", a control character, a space, or
// any of ~ ^ : ? * [ \. The whole does not end in a dot.
func ValidBranch(name string) bool {
	if name == "" || strings.HasSuffix(name, ".") || strings.Contains(name, "..") || strings.Contains(name, "@{") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part[0] == '.' || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	for _, c := range []byte(name) {
		if c < 0x20 || c == 0x7f || strings.IndexByte(" ~^:?*[\\", c) >= 0 {
			return false
		}
	}
	return true
}

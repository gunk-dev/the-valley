package change

import (
	"errors"
	"fmt"
	"strings"
)

// A landing record says that one head landed: the commit it landed as, and
// the request name it landed under. The integrator writes one for every
// head it lands, at refs/the-valley/integration-landings/<target>/<head>,
// in the same ref transaction that moves the target (integrator/refs.go).
// Nobody else writes one: the push hook refuses every push to the
// namespace.
//
// The ref points at a commit with no parent. Its tree holds one file,
// landing, of three lines:
//
//	landed <the commit the head landed as>
//	request <the request name it landed under>
//	head <the head>
//
// An outcome record is kept per request name, so the next version of a
// change to land under the same name replaces it. A landing record is kept
// per head, so a later landing leaves it in place.

// LandingPrefix opens every landing record's ref.
const LandingPrefix = "refs/the-valley/integration-landings/"

// LandingFile is the one file a landing record's tree holds.
const LandingFile = "landing"

// LandingRef is the ref of the landing record for head on target, a branch
// name such as main.
func LandingRef(target, head string) string {
	return LandingPrefix + target + "/" + head
}

// Landing is what a landing record says.
type Landing struct {
	Landed  string // the commit the head landed as
	Request string // the request name it landed under
	Head    string // the head that landed
}

// Format is the landing file that records l.
func (l Landing) Format() []byte {
	return []byte(fmt.Sprintf("landed %s\nrequest %s\nhead %s\n", l.Landed, l.Request, l.Head))
}

// ParseLanding reads a landing file. It reads the three lines Format
// writes, in that order and nothing else: each commit a full lowercase
// object ID, and the request name with no space or control character in
// it.
func ParseLanding(data []byte) (Landing, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) != 4 || lines[3] != "" {
		return Landing{}, errors.New("a landing record is three lines, each ending in a newline")
	}
	var values [3]string
	for i, key := range []string{"landed", "request", "head"} {
		value, ok := strings.CutPrefix(lines[i], key+" ")
		if !ok || value == "" || strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
			return Landing{}, fmt.Errorf("line %d of a landing record is not %s and a value with no space in it", i+1, key)
		}
		values[i] = value
	}
	l := Landing{Landed: values[0], Request: values[1], Head: values[2]}
	if !isObjectID(l.Landed) || !isObjectID(l.Head) {
		return Landing{}, errors.New("a landing record names its commits as full lowercase object IDs")
	}
	return l, nil
}

// isObjectID reports whether s is a full SHA-1 or SHA-256 object ID in
// lowercase hex.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

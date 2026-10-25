package export

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Digest is the export's canonical content digest: SHA-256, in hex, over
// the export's JSON with every display-time field cleared. Those fields
// are Collected and each claim's AgeSeconds and Stale, which change with
// the clock and not with the valley. Signing time is the manifest's, and
// is outside the export altogether. So two exports read from the same
// valley state have one digest, and the publisher uploads only when the
// digest changes or the receiver says it lacks something.
func (e *Export) Digest() (string, error) {
	c := *e
	c.Collected = ""
	c.Projects = make([]Project, len(e.Projects))
	for i, p := range e.Projects {
		p.Knots = make([]Knot, len(e.Projects[i].Knots))
		for j, k := range e.Projects[i].Knots {
			if k.Claim != nil {
				claim := *k.Claim
				claim.AgeSeconds, claim.Stale = 0, false
				k.Claim = &claim
			}
			p.Knots[j] = k
		}
		c.Projects[i] = p
	}
	// Every list in an export is a slice in a fixed order and it holds no
	// map, so its JSON is one byte string.
	b, err := json.Marshal(&c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

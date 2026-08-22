package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// The algorithm identifier the note format prefixes a public key with,
	// and the only algorithm this program renders (attest/sign.go).
	algEd25519 = 1
	sshEd25519 = "ssh-ed25519"

	// The environment variable carrying the pushing key's principal name.
	// One name, shared with nix/valley-host.nix — change it in both.
	principalEnv = "VALLEY_PRINCIPAL"

	dateLayout = "2006-01-02"
)

const knownSignersHeader = `# Compiled from the identity registry — do not edit (dcr-b87f6e8).
# Verifier keys whose attestations count, one per line: the name a key
# signs under, the note format's key hash over that name and key, and
# base64 of the algorithm byte and the raw ed25519 public key.
`

const authorizedKeysHeader = `# Compiled from the identity registry — do not edit (dcr-b87f6e8).
# Keys that may push, each tagged with the principal it acts as. sshd puts
# the tag in the environment of the receive-pack the pre-receive hook runs
# under, which is what tells one pusher from another over a shared user.
`

// artifacts is a whole compilation: both files, what was left out, and the
// counts the journal line reports. Nothing is written until every part of
// this exists, so a failure anywhere leaves the last good pair intact.
type artifacts struct {
	knownSigners   []byte
	authorizedKeys []byte
	notes          []string
	signers        int
	authorized     int
}

// render turns a registry into both artifacts as of one day.
func render(r registry, day time.Time) (artifacts, error) {
	var a artifacts
	var signers, authorized []string
	governed := false

	names := make([]string, 0, len(r.Principals))
	for name := range r.Principals {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		p := r.Principals[name]

		expired, err := hasExpired(p.Expires, day)
		if err != nil {
			return a, fmt.Errorf("%s: %w", name, err)
		}
		if expired {
			a.notes = append(a.notes, fmt.Sprintf(
				"%s expired %s: %d key(s) omitted from both artifacts", name, p.Expires, len(p.Keys)))
			continue
		}

		pushes, governs, err := heldGrants(r, p)
		if err != nil {
			return a, fmt.Errorf("%s: %w", name, err)
		}
		governed = governed || governs

		for i, k := range p.Keys {
			pub, err := sshEd25519Public(k.Public)
			if err != nil {
				return a, fmt.Errorf("%s: key %d: %w", name, i, err)
			}
			if k.Signs != "" {
				signers = append(signers, verifierKey(k.Signs, pub))
			}
			if pushes {
				authorized = append(authorized, fmt.Sprintf("environment=%q %s", principalEnv+"="+name, k.Public))
			}
		}
	}

	// The state the whole render is refused for: after the expiry cut,
	// nobody governs the registry's own stream (dcr-2f03be3).
	if !governed {
		return a, fmt.Errorf("governanceOrphaned: on %s no unexpired principal holds a grant at a boundary of kind %q",
			day.Format(dateLayout), boundaryRegistry)
	}

	a.knownSigners, a.signers = artifact(knownSignersHeader, signers)
	a.authorizedKeys, a.authorized = artifact(authorizedKeysHeader, authorized)
	return a, nil
}

// hasExpired reads an entry's expiry. The declared day is the first day the
// entry no longer counts, so a compilation on it already omits the entry.
func hasExpired(expires string, day time.Time) (bool, error) {
	if expires == "" {
		return false, nil
	}
	end, err := time.Parse(dateLayout, expires)
	if err != nil {
		return false, fmt.Errorf("expires %q is not a calendar day", expires)
	}
	return !day.Before(end), nil
}

// heldGrants answers which boundary kinds the entry holds a grant at. A
// grant naming a boundary the registry does not declare fails the render:
// the schema already refuses one, and a compiler that silently dropped it
// would turn a typo into a quiet loss of access.
func heldGrants(r registry, p principal) (pushes, governs bool, err error) {
	for _, name := range sortedGrants(p.Grants) {
		b, ok := r.Boundaries[p.Grants[name].Boundary]
		if !ok {
			return false, false, fmt.Errorf("grant %s names boundary %q, which the registry does not declare",
				name, p.Grants[name].Boundary)
		}
		switch b.Kind {
		case boundaryGitPush:
			pushes = true
		case boundaryRegistry:
			governs = true
		}
	}
	return pushes, governs, nil
}

func sortedGrants(g map[string]grant) []string {
	names := make([]string, 0, len(g))
	for name := range g {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// artifact writes one file's bytes: the header, then the lines sorted and
// deduplicated. Sorting is what makes a compilation reproducible, so a
// registry that did not change re-renders byte for byte and the atomic
// replace below has nothing to do.
func artifact(header string, lines []string) ([]byte, int) {
	sort.Strings(lines)
	out := bytes.NewBufferString(header)
	previous := ""
	count := 0
	for i, line := range lines {
		if i > 0 && line == previous {
			continue
		}
		previous = line
		count++
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), count
}

// keyHash is the note format's identifier for a name-and-key pair, byte for
// byte what attest computes (attest/sign.go): sha256 over the name, a
// newline, the algorithm byte and the public key, truncated to four bytes.
// The name is inside the hash, which is why a key's signing name is
// recorded per key and getting it wrong compiles a line no verifier
// matches.
func keyHash(name string, pub ed25519.PublicKey) uint32 {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte("\n"))
	h.Write([]byte{algEd25519})
	h.Write(pub)
	return binary.BigEndian.Uint32(h.Sum(nil))
}

// verifierKey is one line of the known-signers file.
func verifierKey(name string, pub ed25519.PublicKey) string {
	return fmt.Sprintf("%s+%08x+%s", name, keyHash(name, pub),
		base64.StdEncoding.EncodeToString(append([]byte{algEd25519}, pub...)))
}

// sshEd25519Public takes the raw 32-byte public key out of an
// authorized_keys line: base64 of the SSH wire format, which is the
// algorithm name and the key, each length-prefixed.
func sshEd25519Public(line string) (ed25519.PublicKey, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != sshEd25519 {
		return nil, fmt.Errorf("%q is not an %s public key", line, sshEd25519)
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return nil, fmt.Errorf("%s key: %w", sshEd25519, err)
	}
	algorithm, rest, err := sshString(blob)
	if err != nil {
		return nil, err
	}
	pub, rest, err := sshString(rest)
	if err != nil {
		return nil, err
	}
	switch {
	case string(algorithm) != sshEd25519:
		return nil, fmt.Errorf("key names algorithm %q, not %s", algorithm, sshEd25519)
	case len(pub) != ed25519.PublicKeySize:
		return nil, fmt.Errorf("key holds %d bytes, not the %d an ed25519 key has", len(pub), ed25519.PublicKeySize)
	case len(rest) != 0:
		return nil, fmt.Errorf("key carries %d trailing bytes", len(rest))
	}
	return ed25519.PublicKey(pub), nil
}

// sshString reads one length-prefixed field of the SSH wire format.
func sshString(b []byte) (value, rest []byte, err error) {
	if len(b) < 4 {
		return nil, nil, fmt.Errorf("truncated key blob")
	}
	n := binary.BigEndian.Uint32(b[:4])
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, fmt.Errorf("key blob claims a %d-byte field it does not hold", n)
	}
	return b[4 : 4+n], b[4+n:], nil
}

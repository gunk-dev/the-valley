package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"the-valley/web/export"
)

const goodLink = `{"id": "lk-0123456789abcdef", "principal": "alice", "note": "laptop", "verifier": "` +
	"3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7" + `", "issued": "2026-10-29T12:00:00Z", "changed": 3`

// The credential file is read strictly: a missing or empty file holds no
// links, and any key, ID or field the format does not allow is refused,
// never skipped.
func TestTheCredentialFileIsReadStrictly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "links")
	if f, err := readLinks(path); err != nil || len(f.Links) != 0 {
		t.Fatalf("a missing file read as %+v, %v", f, err)
	}
	read := func(body string) (linksFile, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return readLinks(path)
	}
	if f, err := read(""); err != nil || len(f.Links) != 0 {
		t.Fatalf("an empty file read as %+v, %v", f, err)
	}
	if f, err := read(`{"revision": 3, "links": [` + goodLink + `}]}`); err != nil || f.Revision != 3 || len(f.active()) != 1 {
		t.Fatalf("a valid file read as %+v, %v", f, err)
	}
	for _, bad := range []string{
		`{"revision": 3, "links": [` + goodLink + `, "secret": "x"}]}`,
		`{"revision": 3, "links": [` + strings.Replace(goodLink, `"principal"`, `"Principal"`, 1) + `}]}`,
		`{"revision": 3, "links": [` + goodLink + `}, ` + goodLink + `, "revoked": "2026-10-29T13:00:00Z"}]}`,
		`{"revision": 3, "links": [` + strings.Replace(goodLink, "lk-0123456789abcdef", "lk-0123456789ABCDEF", 1) + `}]}`,
		`{"revision": 3, "links": [` + strings.Replace(goodLink, `"alice"`, `"anyone"`, 1) + `}]}`,
		`{"revision": 3, "links": [` + strings.Replace(goodLink, "3a6e", "zz6e", 1) + `}]}`,
		`{"revision": 3, "links": [` + strings.Replace(goodLink, "12:00:00Z", "12:00:00+01:00", 1) + `}]}`,
		`{"revision": 3, "links": [` + strings.Replace(goodLink, "laptop", `lap\ttop`, 1) + `}]}`,
		`{"revision": -1, "links": []}`,
		`{"revision": 2, "links": [` + goodLink + `}]}`,
		`{"revision": 3, "links": [` + strings.Replace(goodLink, `"changed": 3`, `"changed": 0`, 1) + `}]}`,
		`{"revision": 3, "revision": 4, "links": []}`,
	} {
		if f, err := read(bad); err == nil {
			t.Errorf("%s read as %+v", bad, f)
		}
	}
}

// link runs the link command over the test valley, with the flags the
// verb reads after the arguments given.
func (v *testValley) link(links string, args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	args = append(args, "--links", links, "--state", v.state)
	if args[0] == "issue" {
		args = append(args, "--instance-repo", filepath.Join(v.srv, "valley.git"), "--destination", destination, "--host", v.host,
			"--read-schema", filepath.Join(v.schemas, "read.cue"),
			"--identity-schema", filepath.Join(v.schemas, "identity.cue"))
	}
	code := runLink(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

// issue issues a link for the principal and returns its ID. A failure
// reports the link's shape, never the link.
func (v *testValley) issue(links, principal string) string {
	v.t.Helper()
	stdout, stderr, code := v.link(links, "issue", "--principal", principal)
	m := linkPattern.FindStringSubmatch(stdout)
	if code != 0 || m == nil {
		v.t.Fatalf("issue exited %d and printed %d bytes, not one link: %s", code, len(stdout), stderr)
	}
	return m[1]
}

var linkPattern = regexp.MustCompile(`^https://` + regexp.QuoteMeta(destination) + `/-/redeem#(lk-[0-9a-f]{16})\.([A-Za-z0-9_-]{43})\n$`)

// A link is issued only to a principal who may read a project at the
// destination: one the registry does not name is refused, and so is one
// with a grant no project's policy/read consents to. An issued link is
// printed once; the file keeps its verifier, never its secret, and every
// link has an ID of its own.
func TestIssueNeedsEffectiveReadAccess(t *testing.T) {
	v := newValley(t)
	links := filepath.Join(v.root, "links", "links")
	if _, stderr, code := v.link(links, "issue", "--principal", "bob"); code != 1 || !strings.Contains(stderr, "grants bob no edge read") {
		t.Errorf("issuing to bob exited %d: %s", code, stderr)
	}
	seen := map[string]bool{}
	for i := range 3 {
		stdout, stderr, code := v.link(links, "issue", "--principal", "operator", "--note", "laptop")
		m := linkPattern.FindStringSubmatch(stdout)
		if code != 0 || m == nil {
			t.Fatalf("issue %d exited %d and printed %d bytes, not one link: %s", i, code, len(stdout), stderr)
		}
		secret, err := base64.RawURLEncoding.DecodeString(m[2])
		if err != nil || len(secret) != 32 {
			t.Fatalf("the secret of %s decodes to %d bytes, not 32: %v", m[1], len(secret), err)
		}
		f, err := readLinks(links)
		if err != nil || f.Revision != int64(i+1) || len(f.Links) != i+1 {
			t.Fatalf("after issue %d the file reads as %+v, %v", i, f, err)
		}
		l := f.Links[i]
		sum := sha256.Sum256(secret)
		if l.ID != m[1] || l.Principal != "operator" || l.Note != "laptop" || l.Changed != int64(i+1) {
			t.Errorf("issue %d recorded %+v", i, l)
		}
		if l.Verifier != hex.EncodeToString(sum[:]) {
			t.Errorf("issue %d recorded a verifier for %s that is not the SHA-256 of its secret", i, l.ID)
		}
		data, _ := os.ReadFile(links)
		if bytes.Contains(data, []byte(m[2])) {
			t.Errorf("the credential file holds the secret of %s", l.ID)
		}
		seen[l.ID] = true
	}
	if len(seen) != 3 {
		t.Errorf("three issues made %d IDs", len(seen))
	}
	if info, err := os.Stat(links); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("the credential file's mode is %v, %v", info.Mode(), err)
	}

	v.declare("app", policy("app", "anyone"))
	v.declare("site", policy("site", "anyone"))
	if _, stderr, code := v.link(links, "issue", "--principal", "operator"); code != 1 || !strings.Contains(stderr, "operator may read no project") {
		t.Errorf("issuing to operator, whom no policy names, exited %d: %s", code, stderr)
	}
	if f, _ := readLinks(links); len(f.Links) != 3 {
		t.Errorf("a refused issue changed the file: %+v", f)
	}
}

// Each pass carries the active links to the receiver, and a revoked link
// is absent from the next. A credential file that cannot be read fails
// the pass closed: the receiver stores an authorization with no binding,
// never one from an older file.
func TestBindingsFollowTheCredentialFile(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	links := filepath.Join(v.root, "links", "links")
	ids := []string{v.issue(links, "operator"), v.issue(links, "operator")}
	v.publish(append(rv.flags(), "--links", links, "--policy-only")...)
	if first := rv.stored(); len(first.Bindings) != 2 {
		t.Fatalf("the first authorization carries %d bindings, not 2", len(first.Bindings))
	}
	if stdout, _, code := v.link(links, "revoke", ids[0]); code != 0 || !strings.Contains(stdout, "revoked "+ids[0]) {
		t.Fatalf("revoke exited %d: %s", code, stdout)
	}
	if stdout, stderr, code := v.link(links, "list", "--check"); code != 1 || !strings.Contains(stderr, ids[0]+": revoked here; the edge may accept it") ||
		!regexp.MustCompile(ids[1]+` +operator +active +enforced`).MatchString(stdout) {
		t.Errorf("list before delivery exited %d:\n%s%s", code, stdout, stderr)
	}
	v.publish(append(rv.flags(), "--links", links, "--policy-only")...)
	second := rv.stored()
	if len(second.Bindings) != 1 || second.Bindings[0].ID != ids[1] {
		t.Errorf("after the revocation the authorization carries %+v", second.Bindings)
	}
	if stdout, stderr, code := v.link(links, "list", "--check", ids[0]); code != 0 || !regexp.MustCompile(ids[0]+` +operator +revoked +enforced`).MatchString(stdout) {
		t.Errorf("list after delivery exited %d:\n%s%s", code, stdout, stderr)
	}

	if err := os.WriteFile(links, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.tryPublish(nil, append(rv.flags(), "--links", links)...); err == nil {
		t.Error("a pass with a corrupt credential file succeeded")
	}
	if third := rv.stored(); third.Sequence <= second.Sequence || len(third.Bindings) != 0 {
		t.Errorf("a corrupt credential file sent authorization %d with %+v", third.Sequence, third.Bindings)
	}
}

// stored is the authorization the receiver stored.
func (rv *testReceiver) stored() *export.Authorization {
	rv.t.Helper()
	a, _, err := rv.store.Authorization()
	if err != nil {
		rv.t.Fatal(err)
	}
	return a
}

// The revision and enforcement rules. A link is enforced only once the
// receiver acknowledged an authorization carrying the links of the
// revision that set its state: a revocation made after an acknowledgement
// was lost is pending, though the record shows no authorization carrying
// the link. A credential file restored from an older copy is signed as it
// is, the links it holds active again, and the next change to it writes
// past every revision a pass signed from.
func TestRevisionsOnlyMoveForward(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	links := filepath.Join(v.root, "links", "links")
	pass := func() error {
		t.Helper()
		_, _, err := v.tryPublish(nil, append(rv.flags(), "--links", links, "--policy-only")...)
		return err
	}
	if err := pass(); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(v.state, linkDeliveryFile)
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lost := v.issue(links, "operator")
	if err := pass(); err != nil {
		t.Fatal(err)
	}
	if len(rv.stored().Bindings) != 1 {
		t.Fatalf("the receiver holds %d bindings, not 1", len(rv.stored().Bindings))
	}
	// The receiver stored it, and the acknowledgement was lost.
	if err := os.WriteFile(record, before, 0o600); err != nil {
		t.Fatal(err)
	}
	v.link(links, "revoke", lost)
	if stdout, _, code := v.link(links, "list", "--check", lost); code != 1 || !regexp.MustCompile(lost+` +operator +revoked +pending`).MatchString(stdout) {
		t.Errorf("a revocation after a lost acknowledgement is not pending (exit %d):\n%s", code, stdout)
	}
	if err := pass(); err != nil {
		t.Fatal(err)
	}
	if stdout, _, code := v.link(links, "list", "--check", lost); code != 0 || !regexp.MustCompile(lost+` +operator +revoked +enforced`).MatchString(stdout) {
		t.Errorf("a delivered revocation is not enforced (exit %d):\n%s", code, stdout)
	}

	restored := v.issue(links, "operator")
	older, err := os.ReadFile(links)
	if err != nil {
		t.Fatal(err)
	}
	v.link(links, "revoke", restored)
	if err := pass(); err != nil {
		t.Fatal(err)
	}
	f, _ := readLinks(links)
	published := f.Revision
	if err := os.WriteFile(links, older, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := pass(); err != nil {
		t.Fatalf("a pass from an older credential file failed: %v", err)
	}
	if b := rv.stored().Bindings; len(b) != 1 || b[0].ID != restored {
		t.Errorf("an older credential file sent %+v, not the one link it holds active", b)
	}
	if stdout, _, code := v.link(links, "list", "--check", restored); code != 0 || !regexp.MustCompile(restored+` +operator +active +enforced`).MatchString(stdout) {
		t.Errorf("a link active in the restored file is not enforced (exit %d):\n%s", code, stdout)
	}
	if _, stderr, code := v.link(links, "revoke", "--all"); code != 0 {
		t.Fatalf("revoke --all on an older credential file exited %d: %s", code, stderr)
	}
	if f, _ := readLinks(links); f.Revision != published+1 {
		t.Errorf("revoke --all on an older credential file wrote revision %d, not %d", f.Revision, published+1)
	}
	if stdout, _, code := v.link(links, "list", "--check", restored); code != 1 || !regexp.MustCompile(restored+` +operator +revoked +pending`).MatchString(stdout) {
		t.Errorf("a revocation past the restored file's revision is not pending (exit %d):\n%s", code, stdout)
	}
	if err := pass(); err != nil {
		t.Fatalf("a pass after revoke --all failed: %v", err)
	}
	if n := len(rv.stored().Bindings); n != 0 {
		t.Errorf("after revoke --all the authorization carries %d bindings", n)
	}
}

// sizedLinks is a credential file at revision 1 that writes to exactly
// size bytes: active links, then revoked ones, their notes padded to fit.
func sizedLinks(t *testing.T, size, active int) linksFile {
	t.Helper()
	written := func(f linksFile) int {
		data, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return len(data) + 1
	}
	record := func(i int, revoked bool) linkRecord {
		l := linkRecord{ID: fmt.Sprintf("lk-%016x", i), Principal: "operator",
			Verifier: strings.Repeat("3a", 32), Issued: "2026-10-29T12:00:00Z", Changed: 1}
		if revoked {
			l.Revoked = "2026-10-29T13:00:00Z"
		}
		return l
	}
	f := linksFile{Revision: 1, Links: []linkRecord{}}
	for i := range active {
		f.Links = append(f.Links, record(i, false))
	}
	f.Links = append(f.Links, record(active, true))
	one := written(f)
	f.Links = append(f.Links, record(active+1, true))
	each := written(f) - one
	for i := active + 2; i <= active+(size-one)/each; i++ {
		f.Links = append(f.Links, record(i, true))
	}
	for i := len(f.Links) - 1; written(f) < size && i >= 0; i-- {
		f.Links[i].Note = strings.Repeat("x", min(maxNote, size-written(f)))
	}
	if got := written(f); got != size {
		t.Fatalf("made a credential file of %d bytes, not %d", got, size)
	}
	return f
}

// writeRaw writes f as writeLinks would, with no limit.
func writeRaw(t *testing.T, path string, f linksFile) {
	t.Helper()
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
}

// Reads and writes hold the credential file to one limit, so a file the
// link command writes is one a pass can read.
func TestTheCredentialFileHasOneLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links")
	at, over := sizedLinks(t, maxLinksFile, 0), sizedLinks(t, maxLinksFile+1, 0)
	writeRaw(t, path, at)
	if _, err := readLinks(path); err != nil {
		t.Errorf("a file of %d bytes did not read: %v", maxLinksFile, err)
	}
	if err := writeLinks(path, at); err != nil {
		t.Errorf("a file of %d bytes was not written: %v", maxLinksFile, err)
	}
	writeRaw(t, path, over)
	if _, err := readLinks(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a file of %d bytes read with %v", maxLinksFile+1, err)
	}
	if err := writeLinks(path, over); err == nil || !strings.Contains(err.Error(), "valley-web-link prune") {
		t.Errorf("a file of %d bytes was written with %v", maxLinksFile+1, err)
	}
}

// Issue keeps 64 KiB of the file's limit for revocations: it writes a
// file up to the headroom and refuses one byte past it, saying to prune.
// A revocation of every link is accepted at the headroom, with the most
// active links an authorization carries.
func TestIssueKeepsHeadroomForRevocations(t *testing.T) {
	v := newValley(t)
	links := filepath.Join(v.root, "links", "links")
	if err := os.MkdirAll(filepath.Dir(links), 0o750); err != nil {
		t.Fatal(err)
	}
	headroom := maxLinksFile - linksHeadroom
	// What an issue adds to a file of revoked links at revision 1.
	base := sizedLinks(t, 100_000, 0)
	issued := base
	issued.Revision = 2
	issued.Links = append(slices.Clone(base.Links), linkRecord{ID: "lk-ffffffffffffffff", Principal: "operator",
		Verifier: strings.Repeat("3a", 32), Issued: "2026-10-29T12:00:00Z", Changed: 2})
	a, _ := json.MarshalIndent(base, "", "  ")
	b, _ := json.MarshalIndent(issued, "", "  ")
	adds := len(b) - len(a)

	writeRaw(t, links, sizedLinks(t, headroom-adds+1, 0))
	before, _ := os.ReadFile(links)
	if _, stderr, code := v.link(links, "issue", "--principal", "operator"); code != 1 || !strings.Contains(stderr, "run valley-web-link prune") {
		t.Errorf("an issue past the headroom exited %d: %s", code, stderr)
	}
	if after, _ := os.ReadFile(links); !bytes.Equal(after, before) {
		t.Error("a refused issue changed the credential file")
	}
	writeRaw(t, links, sizedLinks(t, headroom-adds, 0))
	v.issue(links, "operator")
	if info, err := os.Stat(links); err != nil || info.Size() != int64(headroom) {
		t.Errorf("an issue to the headroom wrote %v bytes, not %d: %v", info.Size(), headroom, err)
	}

	writeRaw(t, links, sizedLinks(t, headroom, export.MaxBindings))
	if stdout, stderr, code := v.link(links, "revoke", "--all"); code != 0 || strings.Count(stdout, "revoked lk-") != export.MaxBindings {
		t.Fatalf("revoke --all at the headroom exited %d: %s", code, stderr)
	}
	f, err := readLinks(links)
	if err != nil || len(f.active()) != 0 {
		t.Errorf("after revoke --all at the headroom the file reads as %d active links, %v", len(f.active()), err)
	}
}

// Prune removes the revocations a pass delivered and keeps those still
// pending.
func TestPruneRemovesOnlyEnforcedRevocations(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	links := filepath.Join(v.root, "links", "links")
	pass := func() error {
		t.Helper()
		_, _, err := v.tryPublish(nil, append(rv.flags(), "--links", links, "--policy-only")...)
		return err
	}
	ids := []string{v.issue(links, "operator"), v.issue(links, "operator"), v.issue(links, "operator")}
	if err := pass(); err != nil {
		t.Fatal(err)
	}
	v.link(links, "revoke", ids[0])
	if err := pass(); err != nil {
		t.Fatal(err)
	}
	v.link(links, "revoke", ids[1])
	stdout, stderr, code := v.link(links, "prune")
	if code != 0 || !strings.Contains(stdout, "pruned "+ids[0]) || strings.Contains(stdout, ids[1]) || !strings.Contains(stdout, "kept 1 revoked links") {
		t.Fatalf("prune exited %d:\n%s%s", code, stdout, stderr)
	}
	f, err := readLinks(links)
	if err != nil || len(f.Links) != 2 || f.Links[0].ID != ids[1] || f.Links[1].ID != ids[2] {
		t.Fatalf("after prune the file holds %+v, %v", f.Links, err)
	}
	if stdout, _, code := v.link(links, "list", "--check"); code != 1 || !regexp.MustCompile(ids[1]+` +operator +revoked +pending`).MatchString(stdout) ||
		!regexp.MustCompile(ids[2]+` +operator +active +enforced`).MatchString(stdout) {
		t.Errorf("list after prune exited %d:\n%s", code, stdout)
	}
	if err := pass(); err != nil {
		t.Fatal(err)
	}
	if b := rv.stored().Bindings; len(b) != 1 || b[0].ID != ids[2] {
		t.Errorf("after prune the receiver binds %+v", b)
	}
	if stdout, _, code := v.link(links, "prune"); code != 0 || !strings.Contains(stdout, "pruned "+ids[1]) {
		t.Errorf("a second prune exited %d: %s", code, stdout)
	}
	if stdout, _, code := v.link(links, "prune"); code != 0 || !strings.Contains(stdout, "no enforced revocation to prune") {
		t.Errorf("a prune with nothing to prune exited %d: %s", code, stdout)
	}
}

// Every change to the credential file holds the links lock, and so does a
// pass from its read of the file until it has signed. A change takes no
// lock in the publisher's state: one goes through while a pass holds the
// authorization lock. A change that cannot take the links lock says it is
// waiting, and after linksWait fails having changed nothing. A pass that
// cannot take it says so too, then signs no binding and fails. Once the
// lock is free, a change goes through.
func TestTheLinksLockSerializesChangesAndPasses(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	links := filepath.Join(v.root, "links", "links")
	id := v.issue(links, "operator")
	v.publish(append(rv.flags(), "--links", links, "--policy-only")...)

	unlockAuthorization, err := lockFile(filepath.Join(v.state, authorizationLockFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := v.link(links, "issue", "--principal", "operator"); code != 0 || strings.Contains(stderr, "waiting") {
		t.Errorf("issue while a pass holds the authorization lock exited %d: %s", code, stderr)
	}
	unlockAuthorization()

	before, err := os.ReadFile(links)
	if err != nil {
		t.Fatal(err)
	}
	was := linksWait
	linksWait = 300 * time.Millisecond
	t.Cleanup(func() { linksWait = was })
	unlock, err := lockLinks(links, func(string) { t.Error("the test waited for the links lock") })
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"issue", "--principal", "operator"}, {"revoke", id}, {"revoke", "--all"}, {"prune"}} {
		stdout, stderr, code := v.link(links, args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "waiting up to 300ms for the policy pass or the change that holds the links lock") ||
			!strings.Contains(stderr, "the credential file is unchanged") {
			t.Errorf("%s with the links lock held exited %d:\n%s%s", args[0], code, stdout, stderr)
		}
	}
	if after, err := os.ReadFile(links); err != nil || !bytes.Equal(after, before) {
		t.Errorf("a change that could not take the links lock wrote the credential file: %v", err)
	}
	_, stderr, err := v.tryPublish(nil, append(rv.flags(), "--links", links, "--policy-only")...)
	if err == nil || !strings.Contains(stderr, "waiting up to 300ms for the change to the credential file") ||
		!strings.Contains(stderr, "was held for longer than 300ms; the authorization carries no read links") {
		t.Errorf("a pass with the links lock held ended with %v:\n%s", err, stderr)
	}
	if b := rv.stored().Bindings; len(b) != 0 {
		t.Errorf("a pass that could not take the links lock sent %+v", b)
	}
	unlock()
	if stdout, stderr, code := v.link(links, "revoke", id); code != 0 || !strings.Contains(stdout, "revoked "+id) || strings.Contains(stderr, "waiting") {
		t.Errorf("revoke with the links lock free exited %d:\n%s%s", code, stdout, stderr)
	}
}

// The links lock is opened without following a symbolic link or waiting
// on a FIFO. A lock replaced by either is refused at once: by a change,
// which writes nothing and makes nothing where the link points, and by a
// pass, which signs no binding and fails. The delivery record, in the
// publisher's own state, which a change reads as root, is refused the same
// way.
func TestAHostileLinksLockIsRefused(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	dir := filepath.Join(v.root, "links")
	links := filepath.Join(dir, "links")
	id := v.issue(links, "operator")
	v.publish(append(rv.flags(), "--links", links, "--policy-only")...)
	before, err := os.ReadFile(links)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func(what string) {
		t.Helper()
		if after, err := os.ReadFile(links); err != nil || !bytes.Equal(after, before) {
			t.Errorf("%s: the credential file changed: %v", what, err)
		}
	}
	target := filepath.Join(v.root, "elsewhere")
	hostile := []struct {
		name, refused string
		make          func(path string) error
	}{
		{"a symbolic link", "is a symbolic link", func(path string) error { return os.Symlink(target, path) }},
		{"a FIFO", "is a FIFO, not", func(path string) error { return syscall.Mkfifo(path, 0o640) }},
	}
	lock := filepath.Join(dir, linksLockFile)
	for _, h := range hostile {
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
		if err := h.make(lock); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr string
		var code int
		promptly(t, "a revoke with a lock that is "+h.name, func() { stdout, stderr, code = v.link(links, "revoke", id) })
		if code != 1 || stdout != "" || !strings.Contains(stderr, "the links lock "+lock+" "+h.refused) {
			t.Errorf("a revoke with a lock that is %s exited %d:\n%s%s", h.name, code, stdout, stderr)
		}
		unchanged("a revoke with a lock that is " + h.name)
		var err error
		promptly(t, "a pass with a lock that is "+h.name, func() {
			_, stderr, err = v.tryPublish(nil, append(rv.flags(), "--links", links, "--policy-only")...)
		})
		if err == nil || !strings.Contains(stderr, "the links lock "+lock+" "+h.refused) {
			t.Errorf("a pass with a lock that is %s ended with %v:\n%s", h.name, err, stderr)
		}
		if b := rv.stored().Bindings; len(b) != 0 {
			t.Errorf("a pass with a lock that is %s sent %+v", h.name, b)
		}
		if _, err := os.Lstat(target); err == nil {
			t.Errorf("a lock that is %s made %s", h.name, target)
		}
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}

	delivery := filepath.Join(v.state, linkDeliveryFile)
	for _, h := range hostile {
		if err := os.Remove(delivery); err != nil {
			t.Fatal(err)
		}
		if err := h.make(delivery); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr string
		var code int
		promptly(t, "a revoke with a delivery record that is "+h.name, func() { stdout, stderr, code = v.link(links, "revoke", id) })
		if code != 1 || stdout != "" || !strings.Contains(stderr, delivery+" "+h.refused) {
			t.Errorf("a revoke with a delivery record that is %s exited %d:\n%s%s", h.name, code, stdout, stderr)
		}
		unchanged("a revoke with a delivery record that is " + h.name)
	}
}

// A change on a fresh host, before the publisher has a state directory,
// takes the links lock all the same, so it serializes with a pass that
// starts beside it. Here a revoke pauses holding the lock, as it reads a
// credential file that is a FIFO. A pass started meanwhile makes the state
// directory, checks the policy, and waits for the lock. Once the revoke
// has written the file, the pass signs it: the revoked link has no
// binding.
func TestAChangeOnAFreshHostSerializesWithAPass(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	dir := filepath.Join(v.root, "links")
	links := filepath.Join(dir, "links")
	revoked, kept := v.issue(links, "operator"), v.issue(links, "operator")
	if _, err := os.Stat(v.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the state directory exists before the first pass: %v", err)
	}
	data, err := os.ReadFile(links)
	if err != nil {
		t.Fatal(err)
	}
	// The credential file becomes a FIFO, whose reader waits for a writer.
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(links); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(fifo, links); err != nil {
		t.Fatal(err)
	}
	type result struct {
		stdout, stderr string
		code           int
	}
	revoking := make(chan result, 1)
	go func() {
		stdout, stderr, code := v.link(links, "revoke", revoked)
		revoking <- result{stdout, stderr, code}
	}()
	eventually(t, "the revoke taking the links lock", func() bool { return linksLocked(t, dir) })

	var passed lockedBuffer
	passing := make(chan error, 1)
	go func() {
		_, _, err := v.tryPublish(func(p *publisher) { p.stderr = &passed }, append(rv.flags(), "--links", links, "--policy-only")...)
		passing <- err
	}()
	eventually(t, "the pass waiting for the links lock", func() bool {
		return strings.Contains(passed.String(), "waiting up to 1m0s for the change to the credential file")
	})
	if _, err := os.Stat(v.state); err != nil {
		t.Errorf("the waiting pass has no state directory: %v", err)
	}

	// Once the revoke has opened the FIFO, a regular file takes its place
	// for the revoke's write, and the revoke reads the file through the
	// FIFO.
	w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "regular"), data, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "regular"), links); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if r := <-revoking; r.code != 0 || !strings.Contains(r.stdout, "revoked "+revoked) {
		t.Fatalf("the revoke exited %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	if err := <-passing; err != nil {
		t.Fatalf("the pass failed: %v\n%s", err, passed.String())
	}
	if b := rv.stored().Bindings; len(b) != 1 || b[0].ID != kept {
		t.Errorf("the pass that waited for the revoke sent %+v, not %s alone", b, kept)
	}
	if stdout, stderr, code := v.link(links, "list", "--check"); code != 0 {
		t.Errorf("list after the pass exited %d:\n%s%s", code, stdout, stderr)
	}
}

// promptly runs f, and fails the test unless f returns within thirty
// seconds.
func promptly(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s blocked", what)
	}
}

// eventually waits up to a minute for cond, and fails the test if it
// never holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within a minute", what)
		}
	}
}

// linksLocked is whether something holds the links lock in dir.
func linksLocked(t *testing.T, dir string) bool {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, linksLockFile))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil && !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal(err)
	}
	return err != nil
}

// lockedBuffer is a buffer that a pass running beside the test writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

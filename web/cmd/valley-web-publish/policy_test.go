package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"the-valley/note"
	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// The authorization counter reads no clock and survives the process: each
// publisher over one state directory allocates a sequence greater than
// every one allocated before, a floor from the receiver raises it, and a
// counter that cannot be read is an error, never a fresh start at one.
func TestTheAuthorizationSequenceIsDurable(t *testing.T) {
	state := t.TempDir()
	next := func(floor int64) (int64, error) {
		p := &publisher{cfg: config{state: state}, stdout: io.Discard, stderr: io.Discard}
		return p.nextSequence(floor)
	}
	for i, c := range []struct{ floor, want int64 }{{0, 1}, {0, 2}, {10, 11}, {3, 12}, {0, 13}} {
		got, err := next(c.floor)
		if err != nil || got != c.want {
			t.Fatalf("allocation %d with floor %d gave %d, %v; want %d", i+1, c.floor, got, err, c.want)
		}
	}
	data, err := os.ReadFile(filepath.Join(state, authorizationSequenceFile))
	if err != nil || string(data) != "13\n" {
		t.Errorf("the counter holds %q, %v; want 13", data, err)
	}
	for _, bad := range []string{"", "x\n", "-4\n"} {
		if err := os.WriteFile(filepath.Join(state, authorizationSequenceFile), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := next(0); err == nil {
			t.Errorf("a counter holding %q gave sequence %d", bad, got)
		}
	}
}

// An incomplete check keeps a project's access only from the authorization
// the receiver last said it stored, and only while the receiver still
// holds it. With app's policy unreadable, a pass keeps app, naming that
// authorization as its basis. Then app's own policy withdraws anyone, and
// the receiver stores the withdrawal. The publisher's state is then
// restored from a copy taken before the withdrawal, and app's policy is
// unreadable again. The restored record still lets anyone read app, but
// the receiver refuses the authorization that keeps it, as replayed, or as
// stale-basis when the restored counter is ahead, and the retry keeps
// nothing it could not check.
func TestARestoredStateRestoresNoWithdrawnAccess(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	app := filepath.Join(v.srv, "app.git")
	stored := func() *export.Authorization {
		t.Helper()
		a, _, err := rv.store.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	unreadable := func(f func()) (string, error) {
		t.Helper()
		if err := os.Rename(app, app+".moved"); err != nil {
			t.Fatal(err)
		}
		defer os.Rename(app+".moved", app)
		_, stderr, err := v.tryPublish(nil, rv.flags()...)
		if f != nil {
			f()
		}
		return stderr, err
	}

	v.publish(rv.flags()...)
	first := stored()
	if !first.Allows(export.Anyone, "app", identities["app"]) {
		t.Fatalf("the first authorization names %+v, want app", first.Projects)
	}
	backup := filepath.Join(t.TempDir(), "state")
	v.run("", "cp", "-a", v.state, backup)

	if _, err := unreadable(nil); !errors.Is(err, errIncomplete) {
		t.Fatalf("a pass with app unreadable gave %v, want an incomplete check", err)
	}
	if kept := stored(); !kept.Allows(export.Anyone, "app", identities["app"]) || kept.Basis != first.Sequence {
		t.Errorf("with app unreadable the receiver stored %+v with basis %d; want app kept on basis %d", kept.Projects, kept.Basis, first.Sequence)
	}

	v.declare("app", policy("app", "operator"))
	v.publish(rv.flags()...)
	if withdrawn := stored(); withdrawn.Allows(export.Anyone, "app", identities["app"]) {
		t.Fatalf("the withdrawal stored %+v, which names app", withdrawn.Projects)
	}

	for _, c := range []struct {
		name    string
		counter string // the restored counter, or "" to leave the copy's
		refused export.Code
	}{
		{"behind", "", export.CodeReplayed},
		{"ahead", "1000\n", export.CodeStaleBasis},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := os.RemoveAll(v.state); err != nil {
				t.Fatal(err)
			}
			v.run("", "cp", "-a", backup, v.state)
			if c.counter != "" {
				v.write(filepath.Join(v.state, authorizationSequenceFile), c.counter)
			}
			before := stored().Sequence
			stderr, err := unreadable(nil)
			if !errors.Is(err, errIncomplete) {
				t.Fatalf("the restored pass gave %v, want an incomplete check", err)
			}
			mustSay(t, "the restored pass", stderr, "refused")
			mustSay(t, "the restored pass", stderr, "as "+string(c.refused))
			got := stored()
			if got.Sequence <= before || got.Allows(export.Anyone, "app", identities["app"]) || got.Basis != 0 {
				t.Errorf("after the restored pass the receiver holds %d naming %+v on basis %d; want a newer one that keeps nothing",
					got.Sequence, got.Projects, got.Basis)
			}
		})
	}
}

// A failure while the counter is written, at the file's sync, its rename
// or the directory's sync, returns no sequence, and leaves no temporary
// file behind. No later allocation hands out a sequence at or below one
// handed out before: a failure at the directory's sync may have written
// the counter, and the next allocation counts from it.
func TestTheAuthorizationCounterSurvivesFailedWrites(t *testing.T) {
	state := t.TempDir()
	next := func() (int64, error) {
		p := &publisher{cfg: config{state: state}, stdout: io.Discard, stderr: io.Discard}
		return p.nextSequence(0)
	}
	handed := int64(0)
	allocate := func() {
		t.Helper()
		seq, err := next()
		if err != nil {
			t.Fatal(err)
		}
		if seq <= handed {
			t.Fatalf("allocated %d after %d", seq, handed)
		}
		handed = seq
	}
	allocate()
	failed := errors.New("injected")
	realSync, realRename := syncFile, renameFile
	defer func() { syncFile, renameFile = realSync, realRename }()
	dirSync := func(f *os.File) error {
		if info, err := f.Stat(); err == nil && info.IsDir() {
			return failed
		}
		return realSync(f)
	}
	for _, c := range []struct {
		name   string
		sync   func(*os.File) error
		rename func(string, string) error
	}{
		{"the file's sync", func(*os.File) error { return failed }, realRename},
		{"the rename", realSync, func(string, string) error { return failed }},
		{"the directory's sync", dirSync, realRename},
	} {
		syncFile, renameFile = c.sync, c.rename
		if seq, err := next(); !errors.Is(err, failed) {
			t.Errorf("a failure at %s gave sequence %d, %v", c.name, seq, err)
		}
		syncFile, renameFile = realSync, realRename
		entries, err := os.ReadDir(state)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != authorizationSequenceFile {
			t.Errorf("after a failure at %s the state directory holds %v", c.name, entries)
		}
		allocate()
	}
}

// A receiver that lost its state, as when its volume is replaced, holds no
// authorization. With app's policy unreadable, the pass keeps app's access
// on the basis of the authorization the receiver acknowledged before, and
// the receiver refuses it as stale-basis. The retry keeps nothing it could
// not check, and publishes the grant it could read: anyone may read site.
func TestAReceiverThatLostItsStateGetsTheReadableGrants(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	v.declare("site", policy("site", "anyone"))
	v.publish(rv.flags()...)
	before, _, err := rv.store.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if !before.Allows(export.Anyone, "app", identities["app"]) || !before.Allows(export.Anyone, "site", identities["site"]) {
		t.Fatalf("the first authorization names %+v, want app and site", before.Projects)
	}

	rv.replaceStore()
	app := filepath.Join(v.srv, "app.git")
	if err := os.Rename(app, app+".moved"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(app+".moved", app)
	_, stderr, err := v.tryPublish(nil, rv.flags()...)
	if !errors.Is(err, errIncomplete) {
		t.Fatalf("the pass gave %v, want an incomplete check", err)
	}
	mustSay(t, "the pass to a receiver that lost its state", stderr, "as "+string(export.CodeStaleBasis))
	got, _, err := rv.store.Authorization()
	if err != nil {
		t.Fatalf("the receiver that lost its state stored no authorization: %v", err)
	}
	if !got.Allows(export.Anyone, "site", identities["site"]) || got.Allows(export.Anyone, "app", identities["app"]) || got.Basis != 0 {
		t.Errorf("the receiver that lost its state holds %+v on basis %d; want site alone, on no basis", got.Projects, got.Basis)
	}
}

// A bounded fallback is measured, never assumed to fit. The receiver holds
// an authorization it admitted before a pass left room under the limit:
// exactly 262,144 bytes signed, at sequence 99,999,999, on no basis. The
// next check withdraws bob from app, which saves the 15 bytes of "reader
// app bob", and keeps notes, whose policy it could not read, on the basis
// of that authorization, which costs the 15 of "basis 99999999". Its
// sequence, 100,000,000, is a digit longer, so its authorization and the
// fallback narrowed from the stored one are a byte over the limit. The
// fallback drops notes, kept only on the basis, and the basis with it, so
// it fits, and the receiver admits the withdrawal of bob.
func TestAFallbackOverTheLimitStillDeliversAWithdrawal(t *testing.T) {
	p := &publisher{signer: exporttest.SignerName, limit: export.MaxAuthorization}
	notes := strings.Repeat("4", 32)
	prev := &export.Authorization{Instance: exporttest.Instance, Destination: exporttest.Destination, Sequence: 99_999_999,
		Checked: exporttest.Collected,
		Projects: []export.AuthorizedProject{
			{Name: "app", Identity: identities["app"], Readers: []string{"anyone", "bob", "operator"}},
			{Name: "notes", Identity: notes, Readers: []string{"alice"}},
		}}
	// Each further reader of app is "reader app " and a 9-character name,
	// 21 bytes, and the last is as long as fills the limit exactly.
	app := &prev.Projects[0]
	size := prev.SignedSize(p.signer)
	for i := 0; export.MaxAuthorization-size >= 21+25; i++ {
		app.Readers = append(app.Readers, fmt.Sprintf("r%08d", i))
		size += 21
	}
	app.Readers = append(app.Readers, "z"+strings.Repeat("0", export.MaxAuthorization-size-13))
	if size := prev.SignedSize(p.signer); size != export.MaxAuthorization {
		t.Fatalf("the stored authorization is %d bytes signed, want %d", size, export.MaxAuthorization)
	}

	full := &export.Authorization{Instance: prev.Instance, Destination: prev.Destination, Sequence: 100_000_000,
		Checked: exporttest.SignedAt, Basis: prev.Sequence,
		Projects: []export.AuthorizedProject{
			{Name: "app", Identity: app.Identity, Readers: slices.DeleteFunc(slices.Clone(app.Readers), func(r string) bool { return r == "bob" })},
			prev.Projects[1],
		}}
	size = full.SignedSize(p.signer)
	if size != export.MaxAuthorization+1 {
		t.Fatalf("the check's authorization is %d bytes signed, want a byte over %d", size, export.MaxAuthorization)
	}
	c := policyCheck{authorization: full, kept: []string{"notes"}}
	p.fallBack(&c, prev, size)

	signed, err := c.authorization.Sign(p.signer, exporttest.Key())
	if err != nil {
		t.Fatalf("the fallback is not signed: %v", err)
	}
	got, err := export.VerifyAuthorization(bytes.NewReader(signed), export.AuthorizationAdmission{
		Verifiers: []note.VerifierKey{exporttest.Verifier()}, Instance: prev.Instance, Destination: prev.Destination,
		HighWater: prev.Sequence})
	if err != nil {
		t.Fatalf("the receiver refuses the fallback: %v", err)
	}
	if got.Sequence != full.Sequence || got.Basis != 0 || got.Names("notes", notes) {
		t.Errorf("the receiver admitted authorization %d on basis %d naming notes %v; want %d, on no basis, without notes",
			got.Sequence, got.Basis, got.Names("notes", notes), full.Sequence)
	}
	if got.Allows("bob", "app", app.Identity) {
		t.Errorf("the fallback still lets bob read app")
	}
	for _, r := range full.Projects[0].Readers {
		if !got.Allows(r, "app", app.Identity) {
			t.Errorf("the fallback dropped %s from app, which it had no need to drop", r)
			break
		}
	}
	if !errors.Is(c.overflow, errOverflow) || c.pending != "no access" || c.dropped != "notes to alice" {
		t.Errorf("the check's overflow is %v, pending %q and dropped %q; want pending no access, and notes to alice dropped",
			c.overflow, c.pending, c.dropped)
	}
	if !strings.Contains(c.overflow.Error(), "so to fit it also drops notes to alice; pending until an authorization fits: no access") {
		t.Errorf("the overflow does not say what the fallback dropped and what is pending: %v", c.overflow)
	}
}

// The last resort of a fallback over the limit is an authorization that
// grants nothing, which always fits. With the limit lowered to exactly
// that authorization's size, a pass that adds anyone to site's readers
// sends it: the receiver stores an authorization that names no project,
// and the pass fails, saying what is pending and what the fallback
// dropped.
func TestTheLastResortGrantsNothing(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	v.publish(rv.flags()...)
	before, _, err := rv.store.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if !before.Allows(export.Anyone, "app", identities["app"]) {
		t.Fatalf("the first authorization names %+v, want app", before.Projects)
	}

	v.declare("site", policy("site", "anyone"))
	nothing := &export.Authorization{Instance: instanceName, Destination: destination, Sequence: before.Sequence + 1, Checked: before.Checked}
	stdout, stderr, failed := v.tryPublish(func(p *publisher) { p.limit = nothing.SignedSize(p.signer) }, rv.flags()...)
	if !errors.Is(failed, errOverflow) {
		t.Fatalf("the pass gave %v, want an overflow\nstdout:\n%s\nstderr:\n%s", failed, stdout, stderr)
	}
	got, _, err := rv.store.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if got.Sequence != nothing.Sequence || len(got.Projects) != 0 || len(got.Bindings) != 0 || got.Basis != 0 {
		t.Errorf("the receiver holds authorization %d naming %+v with %d bindings on basis %d; want %d, granting nothing",
			got.Sequence, got.Projects, len(got.Bindings), got.Basis, nothing.Sequence)
	}
	mustSay(t, "the pass", stdout, fmt.Sprintf("the receiver stored authorization %d: no project may be read\n", nothing.Sequence))
	mustSay(t, "the pass", stdout, "valley-web-publish: pending: site to anyone\n")
	mustSay(t, "the pass", stdout, "valley-web-publish: dropped: app to anyone, operator")
	mustSay(t, "the pass", failed.Error(), "so to fit it also drops app to anyone, operator")
}

// A fallback the receiver refuses is still reported when the retry fails.
// With the limit lowered to the size of an authorization that grants
// nothing, a pass that adds anyone to site's readers signs a fallback that
// drops app to fit. The receiver refuses it as replayed, and the instance
// repository is then unreadable, so the retry's policy check fails before
// it signs. The pass prints what is pending and what the fallback dropped,
// and its error names both beside the failure to read the instance.
func TestARefusedFallbackIsReportedWhenTheRetryFails(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	v.publish(rv.flags()...)
	before, _, err := rv.store.Authorization()
	if err != nil {
		t.Fatal(err)
	}

	v.declare("site", policy("site", "anyone"))
	instance := filepath.Join(v.srv, "valley.git")
	defer os.Rename(instance+".moved", instance)
	rv.mu.Lock()
	rv.refuse = func(kind string, _ int64) (*export.Error, int) {
		if kind != "authorization" {
			return nil, 0
		}
		if err := os.Rename(instance, instance+".moved"); err != nil {
			t.Error(err)
		}
		rv.refuse = nil
		return &export.Error{Code: export.CodeReplayed, Message: "refused by the test", HighWater: before.Sequence + 10}, 0
	}
	rv.mu.Unlock()
	nothing := &export.Authorization{Instance: instanceName, Destination: destination, Sequence: before.Sequence + 1, Checked: before.Checked}
	stdout, stderr, failed := v.tryPublish(func(p *publisher) { p.limit = nothing.SignedSize(p.signer) }, rv.flags()...)
	if !errors.Is(failed, errOverflow) {
		t.Fatalf("the pass gave %v, want an overflow\nstdout:\n%s\nstderr:\n%s", failed, stdout, stderr)
	}
	mustSay(t, "the pass", stderr, "as "+string(export.CodeReplayed))
	mustSay(t, "the pass", stdout, "valley-web-publish: pending: site to anyone\n")
	mustSay(t, "the pass", stdout, "valley-web-publish: dropped: app to anyone, operator\n")
	mustSay(t, "the pass", failed.Error(), "in the instance repository")
	mustSay(t, "the pass", failed.Error(), fmt.Sprintf("the receiver refused authorization %d as %s", nothing.Sequence, export.CodeReplayed))
	mustSay(t, "the pass", failed.Error(), "so to fit it also drops app to anyone, operator; pending until an authorization fits: site to anyone")
	if got, _, err := rv.store.Authorization(); err != nil || got.Sequence != before.Sequence {
		t.Errorf("the receiver holds authorization %v, %v; want %d, as it stored nothing since", got, err, before.Sequence)
	}
}

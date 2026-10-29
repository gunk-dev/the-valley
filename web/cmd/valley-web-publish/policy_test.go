package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"the-valley/web/export"
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

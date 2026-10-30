package export_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/note"
	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

func authorizationAdmission(hw int64) export.AuthorizationAdmission {
	return export.AuthorizationAdmission{Verifiers: []note.VerifierKey{exporttest.Verifier()},
		Instance: exporttest.Instance, Destination: exporttest.Destination, HighWater: hw}
}

// VerifyAuthorization takes its steps in order, each with its own refusal,
// and has no step for age: an authorization checked long ago, or dated
// ahead of every clock, is admitted when its sequence is above the stored
// one's.
func TestVerifyAuthorizationSteps(t *testing.T) {
	f := exporttest.New(t)
	sign := func(a *export.Authorization) []byte { return exporttest.SignAuthorization(t, a) }
	at := func(seq int64, checked time.Time) *export.Authorization {
		a := exporttest.Authorization(seq, "app")
		a.Checked = checked
		return a
	}
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	foreign, err := at(5, exporttest.Collected).Sign(exporttest.SignerName, other)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := at(5, exporttest.Collected)
	elsewhere.Destination = "elsewhere.example.org"
	otherInstance := at(5, exporttest.Collected)
	otherInstance.Instance = "other-valley"
	for _, c := range []struct {
		name   string
		signed []byte
		want   export.Code
	}{
		{"too large", bytes.Repeat([]byte("a"), export.MaxAuthorization+1), export.CodeTooLarge},
		{"another key", foreign, export.CodeBadSignature},
		{"a content manifest", f.Signed, export.CodeBadManifest},
		{"another instance", sign(otherInstance), export.CodeWrongInstance},
		{"another destination", sign(elsewhere), export.CodeWrongDestination},
		{"the stored sequence", sign(at(4, exporttest.Collected)), export.CodeReplayed},
		{"an older sequence", sign(at(3, exporttest.Collected)), export.CodeReplayed},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := export.VerifyAuthorization(bytes.NewReader(c.signed), authorizationAdmission(4))
			wantCode(t, err, c.want)
			if c.want == export.CodeReplayed {
				var e *export.Error
				if errors.As(err, &e) && e.HighWater != 4 {
					t.Errorf("the replayed refusal gives high water %d, want 4", e.HighWater)
				}
			}
		})
	}
	for _, checked := range []time.Time{time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), time.Now().Add(1000 * time.Hour).Truncate(time.Second)} {
		a, err := export.VerifyAuthorization(bytes.NewReader(sign(at(5, checked))), authorizationAdmission(4))
		if err != nil {
			t.Errorf("an authorization checked at %s was refused: %v", checked, err)
			continue
		}
		if !a.Allows(export.Anyone, "app", exporttest.Identities["app"]) || a.Allows(export.Anyone, "app", strings.Repeat("0", 32)) {
			t.Errorf("the authorization checked at %s allows %+v", checked, a.Projects)
		}
	}
}

// The authorization's text holds each record exactly as Text writes it.
func TestParseAuthorizationRefuses(t *testing.T) {
	text := string(exporttest.Authorization(1, "app").Text())
	if _, err := export.ParseAuthorization([]byte(text)); err != nil {
		t.Fatalf("the authorization's own text is refused: %v", err)
	}
	id := exporttest.Identities["app"]
	for name, bad := range map[string]string{
		"a content type line":  strings.Replace(text, export.AuthorizationType, export.ManifestType, 1),
		"no checked record":    strings.Replace(text, "checked 2026-10-25T12:00:00Z\n", "", 1),
		"a local checked time": strings.Replace(text, "2026-10-25T12:00:00Z", "2026-10-25T14:00:00+02:00", 1),
		"a project no reader":  strings.Replace(text, "reader app anyone\n", "", 1),
		"a reader no project":  text + "reader site anyone\n",
		"a reader twice":       text + "reader app anyone\n",
		"a short identity":     strings.Replace(text, id, id[:31], 1),
		"a sequence of zero":   strings.Replace(text, "sequence 1\n", "sequence 0\n", 1),
		"an unknown record":    text + "credential x\n",
		"a principal with a /": strings.Replace(text, "reader app anyone", "reader app any/one", 1),
		"no closing newline":   strings.TrimSuffix(text, "\n"),
		"a second destination": text + "destination example.org\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := export.ParseAuthorization([]byte(bad))
			wantCode(t, err, export.CodeBadManifest)
		})
	}
}

// Sign signs nothing a receiver would refuse for its size: SignedSize is
// what it writes, and one byte over MaxAuthorization is refused as too
// large, before any signature.
func TestSignRefusesAnAuthorizationOverTheLimit(t *testing.T) {
	a := exporttest.Authorization(1, "app")
	signed, err := a.Sign(exporttest.SignerName, exporttest.Key())
	if err != nil || len(signed) != a.SignedSize(exporttest.SignerName) {
		t.Fatalf("signed %d bytes, %v; SignedSize says %d", len(signed), err, a.SignedSize(exporttest.SignerName))
	}
	// Each reader record of app is "reader app " and a 9-character name:
	// 21 bytes.
	p := &a.Projects[0]
	for i := 0; a.SignedSize(exporttest.SignerName) < export.MaxAuthorization; i++ {
		p.Readers = append(p.Readers, fmt.Sprintf("r%08d", i))
	}
	for a.SignedSize(exporttest.SignerName) > export.MaxAuthorization {
		p.Readers = p.Readers[:len(p.Readers)-1]
	}
	pad := export.MaxAuthorization - a.SignedSize(exporttest.SignerName)
	a.Instance += strings.Repeat("x", pad)
	if signed, err := a.Sign(exporttest.SignerName, exporttest.Key()); err != nil || len(signed) != export.MaxAuthorization {
		t.Fatalf("an authorization of exactly %d bytes signed as %d bytes, %v", export.MaxAuthorization, len(signed), err)
	}
	a.Instance += "x"
	_, err = a.Sign(exporttest.SignerName, exporttest.Key())
	wantCode(t, err, export.CodeTooLarge)
}

// The bounded fallback is the last acknowledged authorization less every
// access the current check no longer gives, and never more. Here the
// check withdraws anyone and the operator from app and adds bob, gives
// site a new publication identity, withdraws docs, adds notes, reissues
// the operator's link under its ID with a new verifier, and adds a link.
// The fallback keeps app for alice alone, and alice's link, under the
// check's sequence, checked time and basis.
func TestNarrowKeepsOnlyWhatBothAllow(t *testing.T) {
	hex := func(c string) string { return strings.Repeat(c, 64) }
	prev := &export.Authorization{Instance: exporttest.Instance, Destination: exporttest.Destination, Sequence: 5,
		Checked: exporttest.Collected,
		Projects: []export.AuthorizedProject{
			{Name: "app", Identity: strings.Repeat("1", 32), Readers: []string{"alice", "anyone", "operator"}},
			{Name: "docs", Identity: strings.Repeat("3", 32), Readers: []string{"alice"}},
			{Name: "site", Identity: strings.Repeat("2", 32), Readers: []string{"anyone"}},
		},
		Bindings: []export.Binding{
			{ID: "lk-0000000000000001", Principal: "alice", Verifier: hex("a")},
			{ID: "lk-0000000000000002", Principal: "operator", Verifier: hex("b")},
		}}
	cur := &export.Authorization{Instance: exporttest.Instance, Destination: exporttest.Destination, Sequence: 9,
		Checked: exporttest.SignedAt, Basis: 5,
		Projects: []export.AuthorizedProject{
			{Name: "app", Identity: strings.Repeat("1", 32), Readers: []string{"alice", "bob"}},
			{Name: "notes", Identity: strings.Repeat("4", 32), Readers: []string{"carol"}},
			{Name: "site", Identity: strings.Repeat("5", 32), Readers: []string{"anyone"}},
		},
		Bindings: []export.Binding{
			{ID: "lk-0000000000000001", Principal: "alice", Verifier: hex("a")},
			{ID: "lk-0000000000000002", Principal: "operator", Verifier: hex("c")},
			{ID: "lk-0000000000000003", Principal: "bob", Verifier: hex("d")},
		}}
	n := export.Narrow(prev, cur)
	want := &export.Authorization{Instance: exporttest.Instance, Destination: exporttest.Destination, Sequence: 9,
		Checked: exporttest.SignedAt, Basis: 5,
		Projects: []export.AuthorizedProject{{Name: "app", Identity: strings.Repeat("1", 32), Readers: []string{"alice"}}},
		Bindings: []export.Binding{{ID: "lk-0000000000000001", Principal: "alice", Verifier: hex("a")}}}
	if !bytes.Equal(n.Text(), want.Text()) {
		t.Fatalf("the fallback is\n%s\nwant\n%s", n.Text(), want.Text())
	}
	for _, p := range append(append([]export.AuthorizedProject{}, prev.Projects...), cur.Projects...) {
		for _, r := range append([]string{"anyone", "alice", "bob", "carol", "operator"}, p.Readers...) {
			if n.Allows(r, p.Name, p.Identity) && !(prev.Allows(r, p.Name, p.Identity) && cur.Allows(r, p.Name, p.Identity)) {
				t.Errorf("the fallback lets %s read %s under %s, which prev and cur do not both allow", r, p.Name, p.Identity)
			}
		}
	}
	if empty := export.Narrow(nil, cur); len(empty.Projects) != 0 || len(empty.Bindings) != 0 || empty.Sequence != 9 {
		t.Errorf("the fallback from no acknowledged authorization is\n%s", empty.Text())
	}

	// However far its sequence and basis grow, the fallback of an
	// authorization the check still allows in full is within FallbackRoom
	// of it.
	cur = &export.Authorization{Instance: prev.Instance, Destination: prev.Destination, Sequence: math.MaxInt64,
		Checked: prev.Checked, Basis: math.MaxInt64 - 1, Projects: prev.Projects, Bindings: prev.Bindings}
	grown := export.Narrow(prev, cur).SignedSize(exporttest.SignerName) - prev.SignedSize(exporttest.SignerName)
	if grown > export.FallbackRoom {
		t.Errorf("the fallback is %d bytes larger than the authorization it narrows, more than FallbackRoom, %d", grown, export.FallbackRoom)
	}
}

// Fit restricts an authorization over the limit one access at a time, in
// its order, and stops at the first step that fits. docs, kept on the
// basis, goes first, whole, and the basis with it. Then a reader at a
// time goes, the last by sort of the project with the most readers left,
// app before site when they have as many, so each project goes with its
// last reader. Then the bindings go in ID order, and at last nothing is
// granted. An authorization that fits is returned as it is.
func TestFitDropsAccessInItsOrder(t *testing.T) {
	hex := func(c string) string { return strings.Repeat(c, 64) }
	ids := map[string]string{"app": strings.Repeat("1", 32), "site": strings.Repeat("2", 32), "docs": strings.Repeat("3", 32)}
	// state is the authorization with the basis, the last links of the
	// bindings in ID order, and the projects given, each name:readers.
	state := func(basis int64, links int, projects ...string) *export.Authorization {
		a := &export.Authorization{Instance: exporttest.Instance, Destination: exporttest.Destination, Sequence: 9,
			Checked: exporttest.SignedAt, Basis: basis}
		for _, p := range projects {
			name, readers, _ := strings.Cut(p, ":")
			a.Projects = append(a.Projects, export.AuthorizedProject{Name: name, Identity: ids[name], Readers: strings.Split(readers, ",")})
		}
		bindings := []export.Binding{
			{ID: "lk-0000000000000001", Principal: "bob", Verifier: hex("b")},
			{ID: "lk-0000000000000002", Principal: "alice", Verifier: hex("a")},
		}
		a.Bindings = bindings[len(bindings)-links:]
		return a
	}
	a := state(4, 2, "app:alice,anyone,bob", "docs:carol", "site:carol,dave")
	slices.Reverse(a.Bindings)
	steps := []*export.Authorization{
		state(0, 2, "app:alice,anyone,bob", "site:carol,dave"),
		state(0, 2, "app:alice,anyone", "site:carol,dave"),
		state(0, 2, "app:alice", "site:carol,dave"),
		state(0, 2, "app:alice", "site:carol"),
		state(0, 2, "site:carol"),
		state(0, 2),
		state(0, 1),
		state(0, 0),
	}
	onBasis := []string{"docs"}
	if got := export.Fit(a, onBasis, exporttest.SignerName, a.SignedSize(exporttest.SignerName)); got != a {
		t.Errorf("an authorization that fits became\n%s", got.Text())
	}
	for i, want := range steps {
		got := export.Fit(a, onBasis, exporttest.SignerName, want.SignedSize(exporttest.SignerName))
		if !bytes.Equal(got.Text(), want.Text()) {
			t.Errorf("step %d of the restriction is\n%s\nwant\n%s", i+1, got.Text(), want.Text())
		}
	}
	if got := export.Fit(a, onBasis, exporttest.SignerName, 1); !bytes.Equal(got.Text(), steps[len(steps)-1].Text()) {
		t.Errorf("under a limit nothing fits, the restriction is\n%s\nwant the authorization that grants nothing", got.Text())
	}
}

// Authorize takes the authorization lock alone: while an activation, or a
// process of its own, holds the activation lock, an authorization is
// stored at once, and Stage waits. A stored sequence is durable across a
// reopening, and nothing at or below it is stored again.
func TestStoreAuthorizesWhileTheActivationLockIsHeld(t *testing.T) {
	f := exporttest.New(t)
	dir := payload(t)
	s := openStore(t, dir)
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a := exporttest.Authorization(2)
	if err := s.Authorize(ctx, a, exporttest.SignAuthorization(t, a)); err != nil {
		t.Fatalf("an authorization was not stored while the activation lock was held: %v", err)
	}
	short, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.Stage(short, fixtureUpload(t, f).m); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stage gave %v while the activation lock was held, want it to wait", err)
	}
	lock.Close()

	for _, seq := range []int64{2, 1} {
		a := exporttest.Authorization(seq, "app")
		wantCode(t, s.Authorize(context.Background(), a, exporttest.SignAuthorization(t, a)), export.CodeReplayed)
	}
	again := openStore(t, dir)
	if hw, err := again.AuthorizationHighWater(); hw != 2 || err != nil {
		t.Errorf("after reopening the stored authorization is %d, %v; want 2", hw, err)
	}
	if got, _, err := again.Authorization(); err != nil || len(got.Projects) != 0 {
		t.Errorf("after reopening the stored authorization names %+v, %v; want nothing", got, err)
	}
}

// The serving lock is never held under a context that is done: a server
// whose deadline passed takes it, gives it back and fails, though it is
// free, so it writes no response's headers past its deadline.
func TestTheServingLockIsNotHeldPastADeadline(t *testing.T) {
	s := openStore(t, payload(t))
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := s.HoldServing(done); !errors.Is(err, context.Canceled) {
		if err == nil {
			release()
		}
		t.Errorf("HoldServing under a done context gave %v", err)
	}
	release, err := s.HoldServing(context.Background())
	if err != nil {
		t.Fatalf("the serving lock was not given back: %v", err)
	}
	release()
}

// A store with no authorization builds no repository and needs no bundle,
// and a stored authorization that cannot be read is no high-water mark of
// zero: nothing is stored over it.
func TestStoreBuildsOnlyWhatTheAuthorizationNames(t *testing.T) {
	f := exporttest.New(t)
	dir := payload(t)
	s, err := export.OpenStore(descriptorOf(dir), func(err error) { t.Errorf("the store reported: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	a := exporttest.Authorization(1)
	if err := s.Authorize(context.Background(), a, exporttest.SignAuthorization(t, a)); err != nil {
		t.Fatal(err)
	}
	u := fixtureUpload(t, f)
	st := stage(t, f, s, u)
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Needs) != 1 || inv.Needs[0].Name != export.ExportFile {
		t.Errorf("with app not named the inventory needs %+v, want export.json alone", inv.Needs)
	}
	putNeed(t, st, u, inv.Needs[0])
	g, err := s.Activate(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if got := entries(t, filepath.Join(g.Dir, "repos")); len(got) != 0 {
		t.Errorf("the generation holds repositories %v of projects no authorization names", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "authorization"), []byte("damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizationHighWater(); err == nil {
		t.Error("a damaged stored authorization gave a high-water mark")
	}
	next := exporttest.Authorization(9, "app")
	if err := s.Authorize(context.Background(), next, exporttest.SignAuthorization(t, next)); err == nil {
		t.Error("an authorization was stored over a damaged one")
	}
}

// An authorization that withdraws a project hides it at once, and Prune
// deletes its repository once no reader holds the generation, leaving
// export.json as it was signed. Named again, the project is missing, and
// the inventory needs its bundle.
func TestStorePrunesAWithdrawnRepository(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, payload(t))
	g := f.Upload(t, s)
	repo, _ := g.Repo("app")
	before, err := os.ReadFile(g.ExportPath())
	if err != nil {
		t.Fatal(err)
	}
	exporttest.Authorize(t, s)
	_, release, err := s.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(context.Background()); !errors.Is(err, export.ErrHeld) {
		t.Errorf("Prune gave %v while a reader held the generation, want ErrHeld", err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Errorf("Prune deleted the repository a reader held: %v", err)
	}
	release()
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Prune left the withdrawn repository: %v", err)
	}
	if after, err := os.ReadFile(g.ExportPath()); err != nil || !bytes.Equal(before, after) {
		t.Errorf("Prune changed export.json: %v", err)
	}

	exporttest.Authorize(t, s, "app")
	if missing, err := s.Missing(); err != nil || len(missing) != 1 || missing[0] != "app" {
		t.Errorf("named again, the missing projects are %v, %v; want app", missing, err)
	}
	u := later(t, f, time.Minute)
	st := stage(t, f, s, u)
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Holds) != 0 || len(inv.Needs) != 2 {
		t.Errorf("named again, the inventory holds %+v and needs %+v; want nothing held and both bodies", inv.Holds, inv.Needs)
	}
}

// The store's entry point is a descriptor file. A directory, as the first
// layout's state was, is refused by both openers, and nothing is created.
// A new store beside the first layout's state deletes none of it, then or
// on a later start: the receiver writes nothing outside its descriptor and
// v2/, and the operator deletes the first layout by hand.
func TestStoreDescriptorLeavesTheFirstLayout(t *testing.T) {
	dir := t.TempDir()
	if _, err := export.OpenStore(dir, nil); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("OpenStore of a directory gave %v", err)
	}
	if _, err := export.OpenReadOnly(dir); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("OpenReadOnly of a directory gave %v", err)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("a refused directory holds %v", got)
	}
	firstLayout(t, dir)
	want := "active exports high-water lock staging store trash v2"
	for start := 1; start <= 2; start++ {
		var r reports
		if _, err := export.OpenStore(filepath.Join(dir, "store"), r.report); err != nil {
			t.Fatal(err)
		}
		if got := entries(t, dir); strings.Join(got, " ") != want {
			t.Errorf("after start %d the directory holds %v, want %s", start, got, want)
		}
		if data, err := os.ReadFile(filepath.Join(dir, "exports", "1", "export.json")); err != nil || string(data) != "{}" {
			t.Errorf("after start %d the first layout's export.json holds %q, %v", start, data, err)
		}
		if target, err := os.Readlink(filepath.Join(dir, "active")); err != nil || target != "exports/1" {
			t.Errorf("after start %d the first layout's active points at %q, %v", start, target, err)
		}
		if errs := r.all(); len(errs) != 0 {
			t.Errorf("start %d reported %v", start, errs)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte("the-valley/web-store/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := export.OpenReadOnly(filepath.Join(dir, "other")); err == nil {
		t.Error("OpenReadOnly opened a file that is no store's descriptor")
	}
	if _, err := export.OpenReadOnly(filepath.Join(dir, "store")); err != nil {
		t.Errorf("OpenReadOnly of the descriptor gave %v", err)
	}
}

// An authorization with a basis is admitted only while the stored
// authorization is the one it names, by VerifyAuthorization and again by
// Store.Authorize under its lock, and its basis is less than its sequence.
func TestAnAuthorizationWithABasisNeedsItStored(t *testing.T) {
	based := func(seq, basis int64) *export.Authorization {
		a := exporttest.Authorization(seq, "app")
		a.Basis = basis
		return a
	}
	if _, err := export.VerifyAuthorization(bytes.NewReader(exporttest.SignAuthorization(t, based(9, 4))), authorizationAdmission(4)); err != nil {
		t.Errorf("an authorization on the stored basis was refused: %v", err)
	}
	_, err := export.VerifyAuthorization(bytes.NewReader(exporttest.SignAuthorization(t, based(9, 3))), authorizationAdmission(4))
	wantCode(t, err, export.CodeStaleBasis)
	var e *export.Error
	if errors.As(err, &e) && e.HighWater != 4 {
		t.Errorf("the stale-basis refusal gives high water %d, want 4", e.HighWater)
	}
	_, err = export.ParseAuthorization(based(4, 4).Text())
	wantCode(t, err, export.CodeBadManifest)

	s := openStore(t, payload(t))
	stored, _, err := s.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	stale := based(stored.Sequence+5, stored.Sequence+1)
	wantCode(t, s.Authorize(context.Background(), stale, exporttest.SignAuthorization(t, stale)), export.CodeStaleBasis)
	current := based(stored.Sequence+5, stored.Sequence)
	if err := s.Authorize(context.Background(), current, exporttest.SignAuthorization(t, current)); err != nil {
		t.Errorf("Authorize refused an authorization on the stored basis: %v", err)
	}
}

// A prune that selected a withdrawn repository, and is paused before it
// deletes it, deletes nothing once an authorization names the project
// again: it reads the authorization again under the authorization lock.
// The reply to that authorization says the receiver serves the export
// whole, and the repository survives the prune.
func TestAPruneKeepsWhatAnAuthorizationNamesAgain(t *testing.T) {
	f := exporttest.New(t)
	s := openStore(t, payload(t))
	g := f.Upload(t, s)
	repo, _ := g.Repo("app")
	digest, err := f.Export.Digest()
	if err != nil {
		t.Fatal(err)
	}
	exporttest.Authorize(t, s)
	paused, resume := make(chan struct{}), make(chan struct{})
	*export.PruneSelected = func() {
		close(paused)
		<-resume
	}
	defer func() { *export.PruneSelected = nil }()
	pruned := make(chan error, 1)
	go func() { pruned <- s.Prune(context.Background()) }()
	<-paused

	exporttest.Authorize(t, s, "app")
	reply := func() *export.AuthorizationReply {
		active, _, err := s.Holdings()
		if err != nil {
			t.Fatal(err)
		}
		missing, err := s.Missing()
		if err != nil {
			t.Fatal(err)
		}
		return &export.AuthorizationReply{Active: active, Missing: missing}
	}
	if r := reply(); !r.Serves(digest) {
		t.Errorf("named again while a prune was paused, the reply is %+v; want the export served whole", r)
	}
	close(resume)
	if err := <-pruned; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Errorf("the prune deleted the repository an authorization named again: %v", err)
	}
	if r := reply(); !r.Serves(digest) {
		t.Errorf("after the prune the reply is %+v; want the export served whole", r)
	}
}

// firstLayout makes, in dir, the state of the first store layout: active
// pointing at a generation, high-water, exports/, staging/, trash/ and
// lock.
func firstLayout(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{"exports/1/repos", "staging/2/bodies", "trash/old"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"high-water": "1\n", "lock": "", "exports/1/export.json": "{}"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("exports/1", filepath.Join(dir, "active")); err != nil {
		t.Fatal(err)
	}
}

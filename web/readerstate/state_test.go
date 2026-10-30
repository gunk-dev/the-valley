package readerstate

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"the-valley/web/export"
)

// link is a read link the tests issue: its credential, and the binding an
// authorization gives it.
type link struct {
	credential string
	binding    export.Binding
}

func newLink(n int, principal string) link {
	secret := make([]byte, 32)
	secret[0] = byte(n)
	sum := sha256.Sum256(secret)
	id := fmt.Sprintf("lk-%016x", n)
	return link{credential: id + "." + base64.RawURLEncoding.EncodeToString(secret),
		binding: export.Binding{ID: id, Principal: principal, Verifier: hex.EncodeToString(sum[:])}}
}

// authority is a stored authorization a test changes, and the Hold that
// reads it.
type authority struct {
	mu sync.Mutex
	a  *export.Authorization
}

func (au *authority) set(links ...link) {
	au.mu.Lock()
	defer au.mu.Unlock()
	au.a = &export.Authorization{Instance: "valley-host", Destination: "example.org", Sequence: 1}
	for _, l := range links {
		au.a.Bindings = append(au.a.Bindings, l.binding)
	}
}

func (au *authority) hold(context.Context) (*export.Authorization, func(), error) {
	au.mu.Lock()
	defer au.mu.Unlock()
	return au.a, func() {}, nil
}

func (au *authority) current() *export.Authorization {
	a, _, _ := au.hold(context.Background())
	return a
}

// browserDir is a new browser-state directory, mode 0700.
func browserDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "browser")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func open(t *testing.T, dir string, o Options) *State {
	t.Helper()
	s, err := Open(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// created is a state Initialize created under an authorization with no
// binding, and that authorization.
func created(t *testing.T, o Options) (*State, *authority, string) {
	t.Helper()
	dir := browserDir(t)
	s := open(t, dir, o)
	au := &authority{}
	au.set()
	if ok, err := s.Initialize(context.Background(), au.hold); !ok || err != nil {
		t.Fatalf("Initialize under no binding: %v, %v", ok, err)
	}
	return s, au, dir
}

// pending is Pending, with no deadline.
func (s *State) pending(t *testing.T) bool {
	t.Helper()
	p, err := s.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (s *State) sessionOf(t *testing.T, token string, a *export.Authorization) (string, error) {
	t.Helper()
	r, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Done()
	return r.Session(token, a)
}

func TestOpenRefusesWhatItDoesNotOwn(t *testing.T) {
	dir := browserDir(t)
	open(t, dir, Options{})
	if _, err := Open(dir, Options{}); err == nil || !strings.Contains(err.Error(), "another process owns") {
		t.Errorf("a second Open of one directory: %v", err)
	}
	wide := filepath.Join(t.TempDir(), "wide")
	if err := os.Mkdir(wide, 0o750); err != nil {
		t.Fatal(err)
	}
	os.Chmod(wide, 0o750)
	if _, err := Open(wide, Options{}); err == nil || !strings.Contains(err.Error(), "mode 0750") {
		t.Errorf("Open of a directory its group may read: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(browserDir(t), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link, Options{}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("Open of a symbolic link to a directory: %v", err)
	}
}

func TestInitializeOnlyUnderNoBinding(t *testing.T) {
	ctx := context.Background()
	s := open(t, browserDir(t), Options{})
	au := &authority{}
	if ok, err := s.Initialize(ctx, au.hold); ok || err != nil || !s.pending(t) {
		t.Errorf("Initialize with no authorization: %v, %v, pending %v", ok, err, s.pending(t))
	}
	au.set(newLink(1, "alice"))
	if ok, err := s.Initialize(ctx, au.hold); ok || err != nil || !s.pending(t) {
		t.Errorf("Initialize under a binding: %v, %v, pending %v", ok, err, s.pending(t))
	}
	if _, err := s.Redeem(ctx, au.hold, newLink(1, "alice").credential, ""); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Redeem before the state exists: %v", err)
	}
	if _, err := s.sessionOf(t, "", au.current()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a session before the state exists: %v", err)
	}
	au.set()
	if ok, err := s.Initialize(ctx, au.hold); !ok || err != nil || s.pending(t) {
		t.Errorf("Initialize under no binding: %v, %v, pending %v", ok, err, s.pending(t))
	}
	if ok, err := s.Initialize(ctx, au.hold); ok || err != nil {
		t.Errorf("a second Initialize: %v, %v", ok, err)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, stateFile))
	if err != nil || string(data) != Type+"\n" {
		t.Errorf("the created state holds %q, %v", data, err)
	}
	for _, name := range []string{stateFile, lockFile, ownerFile} {
		if info, err := os.Lstat(filepath.Join(s.dir, name)); err != nil || info.Mode() != 0o600 {
			t.Errorf("%s: %v, %v", name, info.Mode(), err)
		}
	}
}

func TestARedeemedLinkHoldsOneSession(t *testing.T) {
	ctx := context.Background()
	s, au, dir := created(t, Options{})
	alice, bob := newLink(1, "alice"), newLink(2, "bob")
	au.set(alice, bob)
	token, err := s.Redeem(ctx, au.hold, alice.credential, "")
	if prefixed := strings.HasPrefix(token, alice.binding.ID+"."); err != nil || !prefixed {
		t.Fatalf("Redeem: %v, and the token starts with the link's ID: %v", err, prefixed)
	}
	if p, err := s.sessionOf(t, token, au.current()); p != "alice" || err != nil {
		t.Errorf("the session is %q, %v", p, err)
	}
	for name, cred := range map[string]string{
		"redeemed":     alice.credential,
		"malformed":    "lk-0000000000000002",
		"unknown":      newLink(3, "bob").credential,
		"wrong":        bob.binding.ID + "." + strings.Split(alice.credential, ".")[1],
		"padded":       bob.credential + "=",
		"uppercase":    strings.ToUpper(bob.binding.ID[:3]) + bob.credential[3:],
		"noncanonical": bob.credential[:len(bob.credential)-1] + "B",
	} {
		if _, err := s.Redeem(ctx, au.hold, cred, ""); !errors.Is(err, ErrRefused) {
			t.Errorf("Redeem of a %s credential: %v", name, err)
		}
	}
	if _, err := s.Redeem(ctx, au.hold, bob.credential, token); !errors.Is(err, ErrSignedIn) {
		t.Errorf("Redeem with a valid session: %v", err)
	}
	for name, tok := range map[string]string{"unknown": newLink(9, "alice").credential, "malformed": "x", "empty": ""} {
		if p, err := s.sessionOf(t, tok, au.current()); p != "" || err != nil {
			t.Errorf("a session of an %s token: %q, %v", name, p, err)
		}
	}

	// The state survives a restart, and logout keeps the link consumed.
	s.Close()
	s = open(t, dir, Options{})
	if p, _ := s.sessionOf(t, token, au.current()); p != "alice" {
		t.Errorf("after a restart the session is %q", p)
	}
	if err := s.Logout(ctx, au.hold, token); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx, au.hold, token); err != nil {
		t.Errorf("a second logout: %v", err)
	}
	if p, _ := s.sessionOf(t, token, au.current()); p != "" {
		t.Errorf("after logout the session is %q", p)
	}
	if _, err := s.Redeem(ctx, au.hold, alice.credential, ""); !errors.Is(err, ErrRefused) {
		t.Errorf("Redeem after logout: %v", err)
	}
	if _, err := s.Redeem(ctx, au.hold, bob.credential, token); err != nil {
		t.Errorf("Redeem with a signed-out token: %v", err)
	}
}

// A session holds only while the authorization binds its link exactly as
// it was redeemed, and no authorization makes a consumed link redeemable.
func TestASessionNeedsItsExactBinding(t *testing.T) {
	ctx := context.Background()
	s, au, _ := created(t, Options{})
	alice := newLink(1, "alice")
	au.set(alice)
	token, err := s.Redeem(ctx, au.hold, alice.credential, "")
	if err != nil {
		t.Fatal(err)
	}
	mallory := alice
	mallory.binding.Principal = "mallory"
	other := alice
	other.binding.Verifier = newLink(5, "alice").binding.Verifier
	for name, l := range map[string]link{"another principal": mallory, "another verifier": other} {
		au.set(l)
		if p, err := s.sessionOf(t, token, au.current()); p != "" || err != nil {
			t.Errorf("under %s the session is %q, %v", name, p, err)
		}
		if _, err := s.Redeem(ctx, au.hold, alice.credential, ""); !errors.Is(err, ErrRefused) {
			t.Errorf("under %s the consumed link redeems: %v", name, err)
		}
	}
	au.set()
	if p, _ := s.sessionOf(t, token, au.current()); p != "" {
		t.Errorf("revoked, the session is %q", p)
	}
	au.set(alice)
	if p, _ := s.sessionOf(t, token, au.current()); p != "alice" {
		t.Errorf("bound again as it was, the session is %q", p)
	}
}

func TestConcurrentRedemptionsHaveOneWinner(t *testing.T) {
	s, au, _ := created(t, Options{})
	alice := newLink(1, "alice")
	au.set(alice)
	var wg sync.WaitGroup
	var won sync.Map
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if token, err := s.Redeem(context.Background(), au.hold, alice.credential, ""); err == nil {
				won.Store(i, token)
			}
		}()
	}
	wg.Wait()
	n := 0
	won.Range(func(any, any) bool { n++; return true })
	if n != 1 {
		t.Errorf("%d redemptions of one link succeeded", n)
	}
}

// A transaction whose context is done once it holds a lock changes
// nothing: not with every lock free, and not when the context ends while
// it waits for the serving lock. A wait for the snapshot's lock, to read
// or to write, ends at its deadline.
func TestADoneContextChangesNothing(t *testing.T) {
	reached, slow := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, au, _ := created(t, Options{Fault: func(op Op, step Step) error {
		if op == Redeeming && step == Created {
			once.Do(func() { close(reached); <-slow })
		}
		return nil
	}})
	alice, bob := newLink(1, "alice"), newLink(2, "bob")
	au.set(alice, bob)
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Redeem(done, au.hold, alice.credential, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("Redeem under a done context: %v", err)
	}
	ending, end := context.WithCancel(context.Background())
	endsInHold := func(ctx context.Context) (*export.Authorization, func(), error) {
		end()
		return au.hold(ctx)
	}
	if _, err := s.Redeem(ending, endsInHold, alice.credential, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("Redeem whose context ended in its hold: %v", err)
	}
	if strings.Contains(string(s.snap.encode()), alice.binding.ID) {
		t.Fatal("a redemption under a done context consumed its link")
	}

	redeemed := make(chan error, 1)
	go func() {
		_, err := s.Redeem(context.Background(), au.hold, alice.credential, "")
		redeemed <- err
	}()
	<-reached
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if _, err := s.Redeem(short, au.hold, bob.credential, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Redeem behind a slow transaction: %v", err)
	}
	if _, err := s.Read(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Read behind a slow transaction: %v", err)
	}
	close(slow)
	if err := <-redeemed; err != nil {
		t.Fatalf("the slow redemption: %v", err)
	}
	token, err := s.Redeem(context.Background(), au.hold, bob.credential, "")
	if err != nil {
		t.Fatalf("bob's link, after its redemption ran out of time: %v", err)
	}
	if err := s.Logout(done, au.hold, token); !errors.Is(err, context.Canceled) {
		t.Errorf("Logout under a done context: %v", err)
	}
	if p, err := s.sessionOf(t, token, au.current()); p != "bob" || err != nil {
		t.Errorf("a logout under a done context ended the session: %q, %v", p, err)
	}
}

// A transaction whose deadline passes after it holds every lock, while it
// encodes the next document, stops before it makes state.new: a
// redemption consumes nothing and a logout ends no session, and the state
// stays usable.
func TestADeadlineWhileEncodingChangesNothing(t *testing.T) {
	// late is the context of the transaction the fault hook holds at its
	// encoding until the context's deadline passes.
	var late context.Context
	s, au, dir := created(t, Options{Fault: func(op Op, step Step) error {
		if step == Encoded && late != nil {
			<-late.Done()
		}
		return nil
	}})
	alice := newLink(1, "alice")
	au.set(alice)
	before, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatal(err)
	}
	short := func() context.Context {
		ctx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
		t.Cleanup(stop)
		return ctx
	}
	late = short()
	if _, err := s.Redeem(late, au.hold, alice.credential, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a redemption whose deadline passed while it encoded: %v", err)
	}
	late = nil
	if after, err := os.ReadFile(filepath.Join(dir, stateFile)); err != nil || string(after) != string(before) {
		t.Errorf("a redemption whose deadline passed while it encoded changed the state to %q, %v", after, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, nextFile)); err == nil {
		t.Error("a redemption whose deadline passed while it encoded made state.new")
	}
	token, err := s.Redeem(context.Background(), au.hold, alice.credential, "")
	if err != nil {
		t.Fatalf("the link, after its redemption ran out of time: %v", err)
	}
	late = short()
	if err := s.Logout(late, au.hold, token); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a logout whose deadline passed while it encoded: %v", err)
	}
	late = nil
	if p, err := s.sessionOf(t, token, au.current()); p != "alice" || err != nil {
		t.Errorf("a logout whose deadline passed while it encoded ended the session: %q, %v", p, err)
	}
}

func TestAFullStateRefusesWithoutConsuming(t *testing.T) {
	ctx := context.Background()
	one := len(Type+"\n") + len("link lk-0000000000000001 alice ") + 64 + 1 + 64 + 1
	s, au, _ := created(t, Options{Max: one + 10})
	alice, bob := newLink(1, "alice"), newLink(2, "bob")
	au.set(alice, bob)
	token, err := s.Redeem(ctx, au.hold, alice.credential, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(ctx, au.hold, bob.credential, ""); !errors.Is(err, ErrFull) {
		t.Errorf("Redeem past the bound: %v", err)
	}
	if err := s.Logout(ctx, au.hold, token); err != nil {
		t.Errorf("logout of a full state: %v", err)
	}
	if strings.Contains(string(s.snap.encode()), bob.binding.ID) {
		t.Error("the refused redemption consumed its link")
	}
}

// A transaction that fails at any step leaves the state unavailable until
// a restart. After it, the link is consumed when the rename happened, and
// redeems once when it did not.
func TestAFailedTransactionLeavesTheStateUnavailable(t *testing.T) {
	ctx := context.Background()
	for _, step := range Steps {
		t.Run(step.String(), func(t *testing.T) {
			fail := true
			s, au, dir := created(t, Options{Fault: func(op Op, st Step) error {
				if fail && op == Redeeming && st == step {
					return errors.New("injected")
				}
				return nil
			}})
			alice, bob := newLink(1, "alice"), newLink(2, "bob")
			au.set(alice, bob)
			if _, err := s.Redeem(ctx, au.hold, alice.credential, ""); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("a redemption that failed at %s: %v", step, err)
			}
			fail = false
			if _, err := s.Redeem(ctx, au.hold, bob.credential, ""); !errors.Is(err, ErrUnavailable) {
				t.Errorf("a redemption after the failure: %v", err)
			}
			if _, err := s.sessionOf(t, "", au.current()); !errors.Is(err, ErrUnavailable) {
				t.Errorf("a session after the failure: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, nextFile)); step < Renamed && err == nil {
				t.Errorf("state.new is left after a failure at %s", step)
			}
			s.Close()
			s = open(t, dir, Options{})
			_, err := s.Redeem(ctx, au.hold, alice.credential, "")
			if step < Renamed && err != nil || step >= Renamed && !errors.Is(err, ErrRefused) {
				t.Errorf("after a restart, a redemption of the link that failed at %s: %v", step, err)
			}
			if _, err := s.Redeem(ctx, au.hold, bob.credential, ""); err != nil {
				t.Errorf("after a restart, another link: %v", err)
			}
		})
	}
}

func TestAStateChangedUnderTheProcessIsUnavailable(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(path string){
		"removed": func(path string) { os.Remove(path) },
		"replaced": func(path string) {
			data, _ := os.ReadFile(path)
			os.WriteFile(path+".x", data, 0o600)
			os.Rename(path+".x", path)
		},
		"appended": func(path string) {
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString("x\n")
			f.Close()
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, au, dir := created(t, Options{})
			alice := newLink(1, "alice")
			au.set(alice)
			token, err := s.Redeem(ctx, au.hold, alice.credential, "")
			if err != nil {
				t.Fatal(err)
			}
			change(filepath.Join(dir, stateFile))
			if _, err := s.sessionOf(t, token, au.current()); !errors.Is(err, ErrUnavailable) {
				t.Errorf("a session: %v", err)
			}
			if err := s.Logout(ctx, au.hold, token); !errors.Is(err, ErrUnavailable) {
				t.Errorf("a logout: %v", err)
			}
		})
	}
}

func TestOpenKeepsAStateItCannotRead(t *testing.T) {
	for name, make := range map[string]func(path string){
		"garbage":  func(path string) { os.WriteFile(path, []byte("garbage\n"), 0o600) },
		"unsorted": func(path string) { os.WriteFile(path, []byte(Type+"\n"+recordLine(2)+recordLine(1)), 0o600) },
		"open":     func(path string) { os.WriteFile(path, []byte(Type+"\n"), 0o644); os.Chmod(path, 0o644) },
		"symlink": func(path string) {
			os.WriteFile(path+".real", []byte(Type+"\n"), 0o600)
			os.Symlink(path+".real", path)
		},
		"fifo": func(path string) { syscall.Mkfifo(path, 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := browserDir(t)
			make(filepath.Join(dir, stateFile))
			s := open(t, dir, Options{})
			if s.Err() == nil || s.pending(t) {
				t.Errorf("Open of a %s state: err %v, pending %v", name, s.Err(), s.pending(t))
			}
			au := &authority{}
			au.set()
			if ok, err := s.Initialize(context.Background(), au.hold); ok || !errors.Is(err, ErrUnavailable) {
				t.Errorf("Initialize over a %s state: %v, %v", name, ok, err)
			}
		})
	}
}

func TestAnAbandonedNextStateIsNeverPromoted(t *testing.T) {
	dir := browserDir(t)
	next := Type + "\n" + recordLine(1)
	if err := os.WriteFile(filepath.Join(dir, nextFile), []byte(next), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, dir, Options{})
	if !s.pending(t) {
		t.Error("an abandoned state.new made a state")
	}
	if _, err := os.Lstat(filepath.Join(dir, nextFile)); err == nil {
		t.Error("Open kept the abandoned state.new")
	}
}

func recordLine(n int) string {
	l := newLink(n, "alice")
	return fmt.Sprintf("link %s alice %s -\n", l.binding.ID, l.binding.Verifier)
}

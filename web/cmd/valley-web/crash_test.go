package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
	"the-valley/web/readerstate"
)

// These tests are the web-reader-state-crash check: valley-web run as a
// process of its own, killed or failed at each step of a browser-state
// transaction, and run again over what it left. web-server-unit skips
// them.

// faultOf is the fault hook for VALLEY_WEB_TEST_FAULT, "<op>:<step>:<how>":
// at that step of every transaction of that kind, the process kills
// itself with SIGKILL when how is kill, and the step fails when it is
// fail. "" is no hook.
func faultOf(spec string) func(readerstate.Op, readerstate.Step) error {
	if spec == "" {
		return nil
	}
	parts := strings.Split(spec, ":")
	return func(op readerstate.Op, step readerstate.Step) error {
		if op.String() != parts[0] || step.String() != parts[1] {
			return nil
		}
		if parts[2] == "kill" {
			syscall.Kill(os.Getpid(), syscall.SIGKILL)
			select {}
		}
		return errors.New("a fault the test injected")
	}
}

const testOrigin = "https://example.org"

// readerEdge is a store the fixture's export was uploaded to, with an
// empty browser-state directory beside its descriptor, and the links its
// authorization may bind.
type readerEdge struct {
	t     *testing.T
	state string
	store *export.Store
	links []testLink
}

type testLink struct {
	credential string
	binding    export.Binding
}

func newReaderEdge(t *testing.T) *readerEdge {
	t.Helper()
	_, state := uploaded(t)
	if err := os.Mkdir(filepath.Join(filepath.Dir(state), "browser"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := export.OpenStore(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := &readerEdge{t: t, state: state, store: store}
	for n := 1; n <= 3; n++ {
		secret := bytes.Repeat([]byte{byte(n), 0x5a}, 16)
		sum := sha256.Sum256(secret)
		id := fmt.Sprintf("lk-%016x", n)
		e.links = append(e.links, testLink{id + "." + base64.RawURLEncoding.EncodeToString(secret),
			export.Binding{ID: id, Principal: "alice", Verifier: hex.EncodeToString(sum[:])}})
	}
	return e
}

// bind stores, as the receiver does, an authorization one past the
// stored one that binds the edge's links when bound is set, and none when
// it is not.
func (e *readerEdge) bind(bound bool) {
	e.t.Helper()
	a, _, err := e.store.Authorization()
	if err != nil {
		e.t.Fatal(err)
	}
	next := *a
	next.Sequence, next.Bindings = a.Sequence+1, nil
	if bound {
		for _, l := range e.links {
			next.Bindings = append(next.Bindings, l.binding)
		}
	}
	if err := e.store.Authorize(context.Background(), &next, exporttest.SignAuthorization(e.t, &next)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *readerEdge) browser(name string) string {
	return filepath.Join(filepath.Dir(e.state), "browser", name)
}

// served is one valley-web process, with what it logged.
type served struct {
	cmd  *exec.Cmd
	addr string
	mu   sync.Mutex
	log  bytes.Buffer
	done chan struct{}
}

func (s *served) logged() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.String()
}

// serve starts valley-web over the edge with --origin, meeting fault, and
// waits for it to listen.
func (e *readerEdge) serve(fault string) *served {
	e.t.Helper()
	cmd := valleyWeb("--state", e.state, "--listen", "127.0.0.1:0", "--origin", testOrigin)
	cmd.Env = append(cmd.Env, "VALLEY_WEB_TEST_FAULT="+fault)
	return e.start(cmd)
}

// start starts cmd, a valley-web over the edge, keeps what it logs, and
// waits for it to listen.
func (e *readerEdge) start(cmd *exec.Cmd) *served {
	e.t.Helper()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := exporttest.Start(e.t, cmd); err != nil {
		e.t.Fatal(err)
	}
	s := &served{cmd: cmd, done: make(chan struct{})}
	lines := make(chan string, 1)
	go func() {
		r := bufio.NewReader(stderr)
		for {
			line, err := r.ReadString('\n')
			s.mu.Lock()
			s.log.WriteString(line)
			s.mu.Unlock()
			if addr, ok := strings.CutPrefix(strings.TrimSpace(line), "valley-web: listening on "); ok {
				lines <- addr
			}
			if err != nil {
				close(lines)
				break
			}
		}
		cmd.Wait()
		close(s.done)
	}()
	e.t.Cleanup(func() { s.kill() })
	select {
	case addr, ok := <-lines:
		if !ok {
			e.t.Fatalf("valley-web exited before it listened:\n%s", redacted(s.logged()))
		}
		s.addr = addr
	case <-time.After(30 * time.Second):
		e.t.Fatal("valley-web did not say where it listens")
	}
	return s
}

// secretPart is a link's credential or a session token: a credential ID,
// then 32 bytes of unpadded base64url, the secret or the token's random
// part.
var secretPart = regexp.MustCompile(`\b(lk-[0-9a-f]{16})\.[A-Za-z0-9_-]{43}`)

// redacted is s, for a failure's message, with each credential and
// session token in it cut to its credential ID.
func redacted(s string) string { return secretPart.ReplaceAllString(s, "$1.<redacted>") }

func (s *served) kill() {
	s.cmd.Process.Kill()
	<-s.done
}

// exited waits for the process to end, as a fault that kills it ends it.
func (s *served) exited(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
		t.Fatal("valley-web did not exit")
	}
}

var client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// redeem posts a redemption of credential as a reader page's script does,
// and returns its status and the token it sets. A request the process
// never answered is status 0.
func (s *served) redeem(t *testing.T, credential string) (int, string) {
	t.Helper()
	return s.post(t, "/-/redeem", `{"credential":"`+credential+`"}`, "")
}

// logout posts a logout as a reader page's script does, carrying token's
// session cookie, and returns its status, 0 when the process never
// answered.
func (s *served) logout(t *testing.T, token string) int {
	t.Helper()
	status, _ := s.post(t, "/-/logout", `{}`, token)
	return status
}

// post posts body to a reader page with the headers its script sends,
// and token's session cookie when token is not "". It returns the status,
// 0 when the process never answered, and the session token the response
// sets.
func (s *served) post(t *testing.T, path, body, token string) (int, string) {
	t.Helper()
	r, _ := http.NewRequest("POST", "http://"+s.addr+path, strings.NewReader(body))
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Valley-Reader", "1")
	if token != "" {
		r.Header.Set("Cookie", "__Host-valley-reader="+token)
	}
	res, err := client.Do(r)
	if err != nil {
		return 0, ""
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	set := ""
	for _, c := range res.Cookies() {
		if c.Name == "__Host-valley-reader" {
			set = c.Value
		}
	}
	return res.StatusCode, set
}

// get is the status of a GET of target carrying token's session cookie,
// when token is not "", and whether the response renewed it.
func (s *served) get(t *testing.T, target, token string) (int, bool) {
	t.Helper()
	r, _ := http.NewRequest("GET", "http://"+s.addr+target, nil)
	if token != "" {
		r.Header.Set("Cookie", "__Host-valley-reader="+token)
	}
	res, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	renewed := token != "" && strings.HasPrefix(res.Header.Get("Set-Cookie"), "__Host-valley-reader="+token+";")
	return res.StatusCode, renewed
}

// created waits for the edge's browser state to be made.
func (e *readerEdge) created() {
	e.t.Helper()
	for range 300 {
		if data, err := os.ReadFile(e.browser("state")); err == nil && string(data) == readerstate.Type+"\n" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("valley-web did not make the browser state")
}

// A redemption killed at each step of its transaction is retried once the
// process restarts: before the rename the link redeems once, and from the
// rename on it is consumed. A redemption failed at each step leaves the
// state unavailable until the restart, with anyone served meanwhile. In
// no case does a link redeem twice.
func TestCrashARedemption(t *testing.T) {
	for _, how := range []string{"kill", "fail"} {
		for _, step := range readerstate.Steps {
			t.Run(how+"-"+step.String(), func(t *testing.T) {
				e := newReaderEdge(t)
				first := e.serve("")
				e.created()
				first.kill()
				e.bind(true)
				s := e.serve("redeem:" + step.String() + ":" + how)
				status, _ := s.redeem(t, e.links[0].credential)
				switch how {
				case "kill":
					if status != 0 {
						t.Fatalf("a redemption killed at %s answered %d", step, status)
					}
					s.exited(t)
				case "fail":
					if status != http.StatusServiceUnavailable {
						t.Fatalf("a redemption failed at %s answered %d", step, status)
					}
					if status, _ := s.redeem(t, e.links[1].credential); status != http.StatusServiceUnavailable {
						t.Errorf("another redemption after the failure answered %d", status)
					}
					if status, _ := s.get(t, "/-/knots", e.links[1].credential); status != http.StatusServiceUnavailable {
						t.Errorf("a request with a cookie after the failure answered %d", status)
					}
					if status, _ := s.get(t, "/-/knots", ""); status != http.StatusOK {
						t.Errorf("a request with no cookie after the failure answered %d", status)
					}
					s.kill()
				}
				if _, err := os.Lstat(e.browser("state.new")); step < readerstate.Renamed && how == "fail" && err == nil {
					t.Error("a failed redemption left state.new")
				}
				again := e.serve("")
				status, token := again.redeem(t, e.links[0].credential)
				want := http.StatusNoContent
				if step >= readerstate.Renamed {
					want = http.StatusForbidden
				}
				if status != want {
					t.Errorf("after a restart, the link answered %d, want %d", status, want)
				}
				if status, _ := again.redeem(t, e.links[0].credential); status != http.StatusForbidden {
					t.Errorf("the link redeemed twice: %d", status)
				}
				if token != "" {
					if _, renewed := again.get(t, "/-/knots", token); !renewed {
						t.Error("the retried redemption's session is not valid")
					}
				}
				if status, _ := again.redeem(t, e.links[1].credential); status != http.StatusNoContent {
					t.Errorf("another link after the restart answered %d", status)
				}
			})
		}
	}
}

// A redemption whose answer reached the reader survives a crash right
// after it, and its session with it. Neither the link nor the token is
// ever logged.
func TestCrashAfterAnAcknowledgedRedemption(t *testing.T) {
	e := newReaderEdge(t)
	first := e.serve("")
	e.created()
	first.kill()
	e.bind(true)
	s := e.serve("")
	status, token := s.redeem(t, e.links[0].credential)
	if status != http.StatusNoContent || token == "" {
		t.Fatalf("the redemption answered %d", status)
	}
	s.cmd.Process.Signal(syscall.SIGKILL)
	s.exited(t)
	again := e.serve("")
	if status, renewed := again.get(t, "/-/knots", token); status != http.StatusOK || !renewed {
		t.Errorf("after the crash the session answered %d, renewed %v", status, renewed)
	}
	if status, _ := again.redeem(t, e.links[0].credential); status != http.StatusForbidden {
		t.Errorf("after the crash the link answered %d", status)
	}
	secret := strings.SplitN(e.links[0].credential, ".", 2)[1]
	random := strings.SplitN(token, ".", 2)[1]
	for _, p := range []*served{first, s, again} {
		if log := p.logged(); strings.Contains(log, secret) || strings.Contains(log, random) {
			t.Errorf("valley-web logged a secret or a token:\n%s", strings.NewReplacer(secret, "<secret>", random, "<token>").Replace(log))
		}
	}
	data, _ := os.ReadFile(e.browser("state"))
	if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(random)) {
		t.Error("the browser state holds the link's secret or the session's token")
	}
}

// A logout killed or failed at each step of its transaction is never
// acknowledged. Before the rename, the session holds after the restart,
// and the reader, told nothing, signs out again; from the rename on, the
// token works no more. A failed logout leaves the state unavailable until
// the restart, with anyone served meanwhile.
func TestCrashALogout(t *testing.T) {
	for _, how := range []string{"kill", "fail"} {
		for _, step := range readerstate.Steps {
			t.Run(how+"-"+step.String(), func(t *testing.T) {
				e := newReaderEdge(t)
				first := e.serve("")
				e.created()
				first.kill()
				e.bind(true)
				s := e.serve("logout:" + step.String() + ":" + how)
				status, token := s.redeem(t, e.links[0].credential)
				if status != http.StatusNoContent {
					t.Fatalf("the redemption answered %d", status)
				}
				status = s.logout(t, token)
				switch how {
				case "kill":
					if status != 0 {
						t.Fatalf("a logout killed at %s answered %d", step, status)
					}
					s.exited(t)
				case "fail":
					if status != http.StatusServiceUnavailable {
						t.Fatalf("a logout failed at %s answered %d", step, status)
					}
					if status, renewed := s.get(t, "/-/knots", token); status != http.StatusServiceUnavailable || renewed {
						t.Errorf("the session after the failure answered %d, renewed %v", status, renewed)
					}
					if status, _ := s.get(t, "/-/knots", ""); status != http.StatusOK {
						t.Errorf("a request with no cookie after the failure answered %d", status)
					}
					s.kill()
				}
				again := e.serve("")
				if step < readerstate.Renamed {
					if status, renewed := again.get(t, "/-/knots", token); status != http.StatusOK || !renewed {
						t.Errorf("after a restart, the session the logout did not end answered %d, renewed %v", status, renewed)
					}
					if status := again.logout(t, token); status != http.StatusNoContent {
						t.Errorf("signing out again answered %d", status)
					}
				}
				if status, renewed := again.get(t, "/-/knots", token); status != http.StatusOK || renewed {
					t.Errorf("after the logout, the token answered %d, renewed %v", status, renewed)
				}
				if status, _ := again.redeem(t, e.links[0].credential); status != http.StatusForbidden {
					t.Errorf("the signed-out link redeemed again: %d", status)
				}
			})
		}
	}
}

// A logout whose 204 reached the reader holds through a crash right after
// it: a copy of the token it ended never works again.
func TestCrashAfterAnAcknowledgedLogout(t *testing.T) {
	e := newReaderEdge(t)
	first := e.serve("")
	e.created()
	first.kill()
	e.bind(true)
	s := e.serve("")
	status, token := s.redeem(t, e.links[0].credential)
	if status != http.StatusNoContent {
		t.Fatalf("the redemption answered %d", status)
	}
	if status := s.logout(t, token); status != http.StatusNoContent {
		t.Fatalf("the logout answered %d", status)
	}
	s.cmd.Process.Signal(syscall.SIGKILL)
	s.exited(t)
	again := e.serve("")
	if status, renewed := again.get(t, "/-/knots", token); status != http.StatusOK || renewed {
		t.Errorf("after the crash, the signed-out token answered %d, renewed %v", status, renewed)
	}
}

// crashMaking makes valley-web make the edge's state, killed or failed at
// step, and leaves no valley-web running. A failed one answers a cookie
// 503 before it stops.
func (e *readerEdge) crashMaking(step readerstate.Step, how string) {
	t := e.t
	t.Helper()
	fault := "initialize:" + step.String() + ":" + how
	if how == "kill" {
		cmd := valleyWeb("--state", e.state, "--listen", "127.0.0.1:0", "--origin", testOrigin)
		cmd.Env = append(cmd.Env, "VALLEY_WEB_TEST_FAULT="+fault)
		if _, err := output(t, cmd); err == nil {
			t.Fatal("valley-web killed while it made the state exited 0")
		}
		return
	}
	s := e.serve(fault)
	if status, _ := s.get(t, "/-/knots", e.links[0].credential); status != http.StatusServiceUnavailable {
		t.Errorf("a request with a cookie after the failure answered %d", status)
	}
	s.kill()
}

// Making the state, killed or failed at each step, leaves no state or an
// empty one, and the next process makes it or uses it, under the same
// authorization with no binding.
func TestCrashMakingTheState(t *testing.T) {
	for _, how := range []string{"kill", "fail"} {
		for _, step := range readerstate.Steps {
			t.Run(how+"-"+step.String(), func(t *testing.T) {
				e := newReaderEdge(t)
				e.crashMaking(step, how)
				data, err := os.ReadFile(e.browser("state"))
				switch {
				case step < readerstate.Renamed && err == nil:
					t.Errorf("a state was left by a crash before its rename: %q", data)
				case step >= readerstate.Renamed && string(data) != readerstate.Type+"\n":
					t.Errorf("the state left after its rename holds %q, %v", data, err)
				}
				s := e.serve("")
				e.created()
				e.bind(true)
				if status, _ := s.redeem(t, e.links[0].credential); status != http.StatusNoContent {
					t.Errorf("a link after the restart answered %d", status)
				}
			})
		}
	}
}

// Making the state, killed or failed before its rename, leaves none. An
// authorization that binds links, stored before the restart, keeps the
// restart from making one: redemption stays closed, a cookie answers 503,
// and anyone is served. From the rename on, the empty state was made
// before the binding was stored, and the link redeems.
func TestCrashMakingTheStateThenABinding(t *testing.T) {
	for _, how := range []string{"kill", "fail"} {
		for _, step := range readerstate.Steps {
			t.Run(how+"-"+step.String(), func(t *testing.T) {
				e := newReaderEdge(t)
				e.crashMaking(step, how)
				e.bind(true)
				s := e.serve("")
				status, _ := s.redeem(t, e.links[0].credential)
				if step >= readerstate.Renamed {
					if status != http.StatusNoContent {
						t.Errorf("a link bound after the state was made answered %d", status)
					}
					return
				}
				if status != http.StatusServiceUnavailable {
					t.Errorf("a link bound while no state was made answered %d", status)
				}
				if status, _ := s.get(t, "/-/knots", e.links[1].credential); status != http.StatusServiceUnavailable {
					t.Errorf("a request with a cookie answered %d", status)
				}
				if status, _ := s.get(t, "/-/knots", ""); status != http.StatusOK {
					t.Errorf("a request with no cookie answered %d", status)
				}
				if data, err := os.ReadFile(e.browser("state")); err == nil {
					t.Errorf("the restart made a state under an authorization that binds links: %q", data)
				}
			})
		}
	}
}

// One process owns a browser state: a second valley-web over it refuses
// to start, and so does one whose browser directory is missing or open to
// other users, or whose origin is not an HTTPS origin.
func TestCrashOneOwner(t *testing.T) {
	e := newReaderEdge(t)
	e.serve("")
	out, err := output(t, valleyWeb("--state", e.state, "--listen", "127.0.0.1:0", "--origin", testOrigin))
	if err == nil || !strings.Contains(string(out), "another process owns the browser state") {
		t.Errorf("a second valley-web gave %v:\n%s", err, out)
	}
	_, other := uploaded(t)
	out, err = output(t, valleyWeb("--state", other, "--listen", "127.0.0.1:0", "--origin", testOrigin))
	if err == nil || !strings.Contains(string(out), "opening the browser state") {
		t.Errorf("valley-web with no browser directory gave %v:\n%s", err, out)
	}
	os.Mkdir(filepath.Join(filepath.Dir(other), "browser"), 0o755)
	os.Chmod(filepath.Join(filepath.Dir(other), "browser"), 0o755)
	out, err = output(t, valleyWeb("--state", other, "--listen", "127.0.0.1:0", "--origin", testOrigin))
	if err == nil || !strings.Contains(string(out), "mode 0755") {
		t.Errorf("valley-web with an open browser directory gave %v:\n%s", err, out)
	}
	for _, o := range []string{"http://example.org", "https://example.org/", "https://Example.org", "https://example.org:443", "https://example.org?x", "https://u@example.org", "example.org"} {
		out, err := output(t, valleyWeb("--state", e.state, "--listen", "127.0.0.1:0", "--origin", o))
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "is not an HTTPS origin") {
			t.Errorf("--origin %s gave %v:\n%s", o, err, out)
		}
	}
}

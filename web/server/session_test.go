package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
	"the-valley/web/readerstate"
)

// origin is the public origin the session tests' server takes posts from.
const origin = "https://example.org"

// readerLink is a read link the host issued: the credential its fragment
// carries, and the binding an authorization gives it.
type readerLink struct {
	credential string
	binding    export.Binding
}

func issue(n int, principal string) readerLink {
	secret := bytes.Repeat([]byte{byte(n)}, 32)
	sum := sha256.Sum256(secret)
	id := fmt.Sprintf("lk-%016x", n)
	return readerLink{id + "." + base64.RawURLEncoding.EncodeToString(secret),
		export.Binding{ID: id, Principal: principal, Verifier: hex.EncodeToString(sum[:])}}
}

// withReaders gives the harness a server with browser state, in a
// directory of its own, which takes posts from origin. Every request is
// read through its session. The server's log is kept, and logged reads it.
func (h *harness) withReaders(o readerstate.Options) *readerstate.State {
	h.t.Helper()
	dir := filepath.Join(h.t.TempDir(), "browser")
	if err := os.Mkdir(dir, 0o700); err != nil {
		h.t.Fatal(err)
	}
	st, err := readerstate.Open(dir, o)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { st.Close() })
	h.browser = dir
	h.srv = New(Config{Store: h.store, Now: func() time.Time { return h.now }, Log: h.keep, Readers: st, Origin: origin})
	return st
}

func (h *harness) keep(err error) {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	h.kept = append(h.kept, err.Error())
}

func (h *harness) logged() string {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	return strings.Join(h.kept, "\n")
}

// bind stores an authorization one sequence past the stored one's, with
// its projects, that binds the links given and no others.
func (h *harness) bind(links ...readerLink) {
	h.t.Helper()
	a, _, err := h.store.Authorization()
	if err != nil {
		h.t.Fatal(err)
	}
	next := *a
	next.Sequence, next.Bindings = a.Sequence+1, nil
	for _, l := range links {
		next.Bindings = append(next.Bindings, l.binding)
	}
	h.authorize(&next)
}

// signedIn is a server with browser state over an edge that lets anyone
// read app and alice read site, and a session alice redeemed from her
// link, which the stored authorization binds with bob's.
func signedIn(t *testing.T) (h *harness, alice, bob readerLink, token string) {
	h = newHarness(t)
	h.withReaders(readerstate.Options{})
	h.srv.Prepare(context.Background())
	h.edge(exporttest.SignedAt.Add(time.Minute), map[string][]string{"app": {export.Anyone}, "site": {"alice"}})
	alice, bob = issue(1, "alice"), issue(2, "bob")
	h.bind(alice, bob)
	w := h.redeem(alice.credential)
	if w.Code != http.StatusNoContent {
		t.Fatalf("redeeming alice's link answered %d: %s", w.Code, w.Body)
	}
	token = cookieOf(t, w)
	return h, alice, bob, token
}

// post answers a reader page's POST of body, with the headers a reader
// page's script sends, changed by change when it is not nil.
func (h *harness) post(path, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Valley-Reader", "1")
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w
}

func (h *harness) redeem(credential string, cookies ...string) *httptest.ResponseRecorder {
	return h.post("/-/redeem", `{"credential":"`+credential+`"}`, func(r *http.Request) {
		for _, c := range cookies {
			r.Header.Add("Cookie", cookieName+"="+c)
		}
	})
}

func (h *harness) logout(token string) *httptest.ResponseRecorder {
	return h.post("/-/logout", `{}`, func(r *http.Request) { r.Header.Set("Cookie", cookieName+"="+token) })
}

// with answers method target carrying the Cookie headers given.
func (h *harness) with(method, target string, cookies ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for _, c := range cookies {
		r.Header.Add("Cookie", c)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w
}

func cookie(token string) string { return cookieName + "=" + token }

// cookieOf is the session token a response sets, as sessionCookie sets it.
func cookieOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	set := w.Header().Values("Set-Cookie")
	if len(set) != 1 {
		t.Fatalf("the response sets %d cookies: %q", len(set), setting(w))
	}
	token, _, _ := strings.Cut(strings.TrimPrefix(set[0], cookieName+"="), ";")
	if set[0] != sessionCookie(token) || !strings.HasPrefix(set[0], cookieName+"=lk-") {
		t.Fatalf("the response sets %q", setting(w))
	}
	return token
}

// setting is what a response's Set-Cookie headers set, for a failure's
// message: each cookie's value is cut to the credential ID it starts
// with, so no message carries a session token.
func setting(w *httptest.ResponseRecorder) []string {
	var shown []string
	for _, set := range w.Header().Values("Set-Cookie") {
		name, rest, _ := strings.Cut(set, "=")
		value, attributes, _ := strings.Cut(rest, ";")
		if id, _, _ := strings.Cut(value, "."); export.ValidLinkID(id) {
			value = id + ".<redacted>"
		} else if value != "" {
			value = "<redacted>"
		}
		shown = append(shown, name+"="+value+";"+attributes)
	}
	return shown
}

// pending is st.Pending, with no deadline.
func pending(t *testing.T, st *readerstate.State) bool {
	t.Helper()
	p, err := st.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func renews(w *httptest.ResponseRecorder, token string) bool {
	set := w.Header().Values("Set-Cookie")
	return len(set) == 1 && set[0] == sessionCookie(token)
}

func TestARedeemedLinkSignsInItsReader(t *testing.T) {
	h, alice, bob, token := signedIn(t)
	if got := h.logged(); got != "" {
		t.Errorf("the server logged: %s", got)
	}
	if w := h.with("GET", "/tools/site/"); w.Code != http.StatusNotFound {
		t.Errorf("site answered anyone %d", w.Code)
	}
	w := h.with("GET", "/tools/site/", cookie(token))
	if w.Code != http.StatusFound || !renews(w, token) {
		t.Errorf("site answered alice %d, setting %q", w.Code, setting(w))
	}
	page := h.with("GET", w.Header().Get("Location"), cookie(token))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<a href="/-/logout">Sign out</a>`) || !renews(page, token) {
		t.Errorf("site's tree answered alice %d, with no Sign out link or renewal", page.Code)
	}
	if strings.Contains(h.page("/-/knots"), "Sign out") {
		t.Error("anyone's knots page links to Sign out")
	}
	missing, hidden := h.with("GET", "/tools/nosuch/", cookie(token)), h.with("GET", "/web/docs/", cookie(token))
	for _, w := range []*httptest.ResponseRecorder{missing, hidden} {
		if w.Code != http.StatusNotFound || !bytes.Equal(w.Body.Bytes(), notFoundPage) || !renews(w, token) {
			t.Errorf("a missing or hidden project answered alice %d, setting %q", w.Code, setting(w))
		}
	}
	head := h.with("HEAD", "/tools/site/", cookie(token))
	if head.Code != http.StatusFound || !renews(head, token) || head.Body.Len() != 0 {
		t.Errorf("HEAD answered %d with %d bytes, setting %q", head.Code, head.Body.Len(), setting(head))
	}

	// A redeemed link redeems no more. A valid session refuses another
	// link before it is consumed; after logout the link redeems.
	if w := h.redeem(alice.credential); w.Code != http.StatusForbidden || w.Body.String() != refusedLink {
		t.Errorf("a second redemption answered %d: %s", w.Code, w.Body)
	}
	if w := h.redeem(bob.credential, token); w.Code != http.StatusConflict || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Errorf("a redemption beside a valid session answered %d", w.Code)
	}
	w = h.logout(token)
	if w.Code != http.StatusNoContent || w.Header().Get("Set-Cookie") != sessionCookie("") {
		t.Errorf("logout answered %d, setting %q", w.Code, setting(w))
	}
	if w := h.logout(token); w.Code != http.StatusNoContent {
		t.Errorf("a second logout answered %d", w.Code)
	}
	if w := h.with("GET", "/tools/site/", cookie(token)); w.Code != http.StatusNotFound || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Errorf("a signed-out session answered %d, setting %q", w.Code, setting(w))
	}
	if w := h.redeem(bob.credential, token); w.Code != http.StatusNoContent {
		t.Errorf("bob's link, beside a signed-out session, answered %d", w.Code)
	}
	if w := h.redeem(alice.credential); w.Code != http.StatusForbidden {
		t.Errorf("alice's link, after her logout, answered %d", w.Code)
	}
}

func TestReaderPostsKeepTheirContract(t *testing.T) {
	h := newHarness(t)
	h.withReaders(readerstate.Options{})
	h.srv.Prepare(context.Background())
	alice := issue(1, "alice")
	h.bind(alice)
	good := `{"credential":"` + alice.credential + `"}`
	set := func(k, v string) func(*http.Request) { return func(r *http.Request) { r.Header.Set(k, v) } }
	add := func(k, v string) func(*http.Request) { return func(r *http.Request) { r.Header.Add(k, v) } }
	for name, c := range map[string]struct {
		body   string
		change func(*http.Request)
	}{
		"no origin":         {good, func(r *http.Request) { r.Header.Del("Origin") }},
		"another origin":    {good, set("Origin", "https://example.org:8443")},
		"an http origin":    {good, set("Origin", "http://example.org")},
		"two origins":       {good, add("Origin", origin)},
		"no reader header":  {good, func(r *http.Request) { r.Header.Del("X-Valley-Reader") }},
		"reader header 2":   {good, set("X-Valley-Reader", "2")},
		"two reader header": {good, add("X-Valley-Reader", "1")},
		"a form":            {good, set("Content-Type", "application/x-www-form-urlencoded")},
		"text/plain":        {good, set("Content-Type", "text/plain")},
		"latin-1":           {good, set("Content-Type", "application/json; charset=iso-8859-1")},
		"a parameter":       {good, set("Content-Type", "application/json; v=1")},
		"two types":         {good, add("Content-Type", "application/json")},
		"an encoding":       {good, set("Content-Encoding", "gzip")},
		"a query":           {good, func(r *http.Request) { r.URL.RawQuery = "x=1" }},
		"a bare query":      {good, func(r *http.Request) { r.URL.ForceQuery = true }},
		"too long":          {`{"credential":"` + alice.credential + strings.Repeat(" ", maxPost) + `"}`, nil},
		"twice":             {`{"credential":"x","credential":"` + alice.credential + `"}`, nil},
		"unknown":           {`{"credential":"` + alice.credential + `","next":"/"}`, nil},
		"trailing":          {good + `{}`, nil},
		"an array":          {`["` + alice.credential + `"]`, nil},
		"null":              {`null`, nil},
		"cased":             {`{"Credential":"` + alice.credential + `"}`, nil},
		"no credential":     {`{}`, nil},
		"a null credential": {`{"credential":null}`, nil},
		"a number":          {`{"credential":1}`, nil},
	} {
		if w := h.post("/-/redeem", c.body, c.change); w.Code != http.StatusForbidden || w.Body.String() != refusedPost {
			t.Errorf("a post with %s answered %d: %s", name, w.Code, w.Body)
		}
	}
	if w := h.post("/-/logout", `{"token":"x"}`, nil); w.Code != http.StatusForbidden {
		t.Errorf("a logout with a key answered %d", w.Code)
	}
	if w := h.post("/-/redeem", good, set("Content-Type", "application/json; charset=UTF-8")); w.Code != http.StatusNoContent {
		t.Errorf("the link, after every refused post, answered %d: %s", w.Code, w.Body)
	}
	for _, method := range []string{"OPTIONS", "PUT", "DELETE"} {
		w := h.with(method, "/-/redeem")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD, POST" {
			t.Errorf("%s /-/redeem answered %d, Allow %q", method, w.Code, w.Header().Get("Allow"))
		}
		for k := range w.Header() {
			if strings.HasPrefix(k, "Access-Control-") {
				t.Errorf("%s /-/redeem carries %s", method, k)
			}
		}
	}
	if w := h.post("/tools/app/", good, nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("a post to a content page answered %d", w.Code)
	}
	plain := newHarness(t)
	if w := plain.post("/-/redeem", good, nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("with no browser state a redemption answered %d", w.Code)
	}
	if w := plain.get("/-/redeem"); w.Code != http.StatusNotFound {
		t.Errorf("with no browser state /-/redeem answered %d", w.Code)
	}
}

func TestEveryRefusedLinkAnswersAlike(t *testing.T) {
	h, alice, bob, _ := signedIn(t)
	revoked := issue(3, "carol")
	h.bind(alice, bob)
	secretOf := func(l readerLink) string { return strings.SplitN(l.credential, ".", 2)[1] }
	for name, credential := range map[string]string{
		"malformed": "lk-0000000000000002." + secretOf(bob)[1:],
		"unknown":   issue(4, "bob").credential,
		"wrong":     bob.binding.ID + "." + secretOf(alice),
		"revoked":   revoked.credential,
		"consumed":  alice.credential,
		"empty":     "",
	} {
		w := h.redeem(credential)
		if w.Code != http.StatusForbidden || w.Body.String() != refusedLink || len(w.Header().Values("Set-Cookie")) != 0 {
			t.Errorf("a %s credential answered %d: %s", name, w.Code, w.Body)
		}
	}
	if w := h.redeem(bob.credential); w.Code != http.StatusNoContent {
		t.Errorf("bob's link answered %d", w.Code)
	}
}

func TestSessionCookiesAreCountedAndChecked(t *testing.T) {
	h, alice, bob, token := signedIn(t)
	for name, cookies := range map[string][]string{
		"twice in one header": {cookie(token) + "; " + cookie(token)},
		"in two headers":      {cookie(token), cookie(token)},
		"beside another":      {"a=b; " + cookie(token) + "; " + cookie("x")},
		"malformed":           {cookie(token + "x")},
		"unknown":             {cookie(issue(9, "alice").credential)},
		"quoted":              {cookieName + `="` + token + `"`},
	} {
		w := h.with("GET", "/tools/site/", cookies...)
		if w.Code != http.StatusNotFound || len(w.Header().Values("Set-Cookie")) != 0 {
			t.Errorf("a session cookie %s answered %d, setting %q", name, w.Code, setting(w))
		}
	}
	if w := h.with("GET", "/tools/site/", "other=1; "+cookie(token)); w.Code != http.StatusFound || !renews(w, token) {
		t.Errorf("the session beside another cookie answered %d", w.Code)
	}
	// The session holds only while its link is bound as it was redeemed.
	h.bind(bob)
	if w := h.with("GET", "/tools/site/", cookie(token)); w.Code != http.StatusNotFound || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Errorf("a revoked session answered %d", w.Code)
	}
	mallory := alice
	mallory.binding.Principal = "mallory"
	h.bind(mallory)
	if w := h.with("GET", "/tools/site/", cookie(token)); w.Code != http.StatusNotFound {
		t.Errorf("a session whose link is bound to another principal answered %d", w.Code)
	}
	h.bind(alice)
	if w := h.with("GET", "/tools/site/", cookie(token)); w.Code != http.StatusFound || !renews(w, token) {
		t.Errorf("a session whose link is bound again as it was answered %d", w.Code)
	}
	// Losing every grant leaves the session, with what anyone may see.
	e, _ := h.buildExport(true, nil)
	withDocs(e, map[string]string{})
	h.authorizeReaders(e, map[string][]string{"app": {export.Anyone}})
	h.bind(alice)
	if w := h.with("GET", "/tools/site/", cookie(token)); w.Code != http.StatusNotFound || !renews(w, token) {
		t.Errorf("a session with no grant answered %d, setting %q", w.Code, setting(w))
	}
}

func TestReaderPagesRenderWithTheirOwnPolicy(t *testing.T) {
	f := exporttest.New(t)
	store, err := export.OpenStore(exporttest.StorePath(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, f: f, store: store, now: exporttest.Collected}
	h.withReaders(readerstate.Options{})
	for _, path := range []string{"/-/redeem", "/-/logout"} {
		w := h.with("GET", path)
		if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != readerPolicy {
			t.Errorf("%s with nothing published answered %d, policy %q", path, w.Code, w.Header().Get("Content-Security-Policy"))
		}
		if !strings.Contains(w.Body.String(), "<script>"+readerJS+"</script>") || strings.Contains(w.Body.String(), "Sign out</a>") {
			t.Errorf("%s does not carry the reader script, or links to Sign out", path)
		}
	}
	if !strings.Contains(readerPolicy, "script-src "+inlineHash(readerJS)+"; ") || !strings.Contains(readerPolicy, "connect-src 'self'") {
		t.Errorf("the reader policy is %q", readerPolicy)
	}
	for _, p := range []string{knotsPolicy, contentPolicy} {
		if strings.Contains(p, inlineHash(readerJS)) || !strings.Contains(p, "connect-src 'none'") {
			t.Errorf("a content policy admits the reader script or a connection: %q", p)
		}
	}
	for _, path := range []string{"/-/redeem/", "/-/redeem/x", "/-/Redeem"} {
		if w := h.with("GET", path); w.Code == http.StatusOK {
			t.Errorf("%s answered %d", path, w.Code)
		}
	}
	if w := h.with("GET", "/"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("/ with nothing published answered %d", w.Code)
	}

	s, _, _, token := signedIn(t)
	w := s.with("GET", "/-/redeem", cookie(token))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-session="1"`) || !strings.Contains(w.Body.String(), "Sign out</a>") || !renews(w, token) {
		t.Errorf("/-/redeem for a signed-in reader answered %d without its session", w.Code)
	}
	if w := s.with("GET", "/-/redeem"); !strings.Contains(w.Body.String(), `data-session="0"`) {
		t.Error("/-/redeem for anyone shows a session")
	}
}

func TestAnUnavailableStateServesAnyoneAlone(t *testing.T) {
	h, _, bob, token := signedIn(t)
	dir := h.browser
	data, err := os.ReadFile(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copy"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "copy"), filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/tools/site/", "/tools/app/", "/-/redeem"} {
		w := h.with("GET", target, cookie(token))
		if w.Code != http.StatusServiceUnavailable || !bytes.Equal(w.Body.Bytes(), unavailableHTML) || len(w.Header().Values("Set-Cookie")) != 0 {
			t.Errorf("%s with a session answered %d, setting %q", target, w.Code, setting(w))
		}
	}
	if w := h.with("GET", "/tools/app/"); w.Code != http.StatusFound {
		t.Errorf("app answered anyone %d", w.Code)
	}
	if w := h.redeem(bob.credential); w.Code != http.StatusServiceUnavailable || w.Body.String() != unavailable {
		t.Errorf("a redemption answered %d: %s", w.Code, w.Body)
	}
	if w := h.logout(token); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a logout answered %d", w.Code)
	}
	if !strings.Contains(h.logged(), "replaced or changed") {
		t.Errorf("the server did not log why: %s", h.logged())
	}
}

func TestNoStateIsMadeUnderABinding(t *testing.T) {
	h := newHarness(t)
	alice, bob := issue(1, "alice"), issue(2, "bob")
	h.bind(alice)
	st := h.withReaders(readerstate.Options{})
	h.srv.Prepare(context.Background())
	if !pending(t, st) {
		t.Fatal("the state was made under a binding")
	}
	if w := h.redeem(alice.credential); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a redemption answered %d", w.Code)
	}
	if w := h.with("GET", "/tools/app/", cookie(alice.credential)); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a request with a cookie answered %d", w.Code)
	}
	if w := h.with("GET", "/tools/app/"); w.Code != http.StatusFound {
		t.Errorf("a request with no cookie answered %d", w.Code)
	}
	// An empty binding set read by any request makes the state, with no
	// restart, and later links redeem.
	h.bind()
	if w := h.with("GET", "/tools/app/"); w.Code != http.StatusFound || pending(t, st) {
		t.Errorf("the request after the bindings were withdrawn answered %d, pending %v", w.Code, pending(t, st))
	}
	h.bind(bob)
	if w := h.redeem(bob.credential); w.Code != http.StatusNoContent {
		t.Errorf("bob's link answered %d", w.Code)
	}
}

// A binding stored after a request read an authorization with none, and
// before the state is made under the serving lock, stops it. A binding the
// receiver stores while the state is being made waits for it.
func TestMakingTheStateRacesNoBinding(t *testing.T) {
	h := newHarness(t)
	st := h.withReaders(readerstate.Options{})
	stale := h.srv.readAuthorization()
	h.bind(issue(1, "alice"))
	h.srv.prepare(context.Background(), stale)
	if !pending(t, st) {
		t.Fatal("the state was made under an authorization that binds a link")
	}

	h = newHarness(t)
	var stored atomic.Bool
	waited := make(chan bool, 1)
	alice := issue(1, "alice")
	st = h.withReaders(readerstate.Options{Fault: func(op readerstate.Op, step readerstate.Step) error {
		if op == readerstate.Initializing && step == readerstate.Created {
			go func() { h.bind(alice); stored.Store(true) }()
			time.Sleep(200 * time.Millisecond)
			waited <- !stored.Load()
		}
		return nil
	}})
	h.srv.Prepare(context.Background())
	if pending(t, st) || !<-waited {
		t.Fatal("the receiver stored a binding while the state was being made")
	}
	eventually(t, "the binding is stored", stored.Load)
	if w := h.redeem(alice.credential); w.Code != http.StatusNoContent {
		t.Errorf("alice's link answered %d", w.Code)
	}
}

func TestAFullStateRefusesAndSignsOut(t *testing.T) {
	h := newHarness(t)
	one := len(readerstate.Type+"\n") + len("link lk-0000000000000001 alice ") + 64 + 1 + 64 + 1
	h.withReaders(readerstate.Options{Max: one})
	h.srv.Prepare(context.Background())
	alice, bob := issue(1, "alice"), issue(2, "bob")
	h.bind(alice, bob)
	w := h.redeem(alice.credential)
	if w.Code != http.StatusNoContent {
		t.Fatalf("alice's link answered %d", w.Code)
	}
	if w := h.redeem(bob.credential); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a redemption past the bound answered %d", w.Code)
	}
	if w := h.logout(cookieOf(t, w)); w.Code != http.StatusNoContent {
		t.Errorf("a logout of a full state answered %d", w.Code)
	}
}

func TestConcurrentRedemptionsSignInOnce(t *testing.T) {
	h, _, bob, _ := signedIn(t)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := h.redeem(bob.credential); w.Code == http.StatusNoContent {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("%d redemptions of bob's link succeeded", wins.Load())
	}
}

// A revocation, or a logout, that completes while a response is being
// rendered keeps that response from being written: it is rendered again,
// as anyone's, with no renewal.
func TestASessionResponseIsHeldToItsSession(t *testing.T) {
	for _, mutation := range []string{"revoke", "logout"} {
		for _, path := range []string{"/tools/site/source/blob/%s/README.md", "/tools/site/source/archive/%s.tar.gz",
			"/tools/site/knots/oc-b000001", "/site/", "/tools/site/source/tree/%s/"} {
			t.Run(mutation+path, func(t *testing.T) {
				h, _, bob, token := signedIn(t)
				target := strings.ReplaceAll(path, "%s", git(t, h.f.Repos["site"], nil, "rev-parse", export.MainRef))
				if w := h.with("GET", target, cookie(token)); w.Code != http.StatusOK && w.Code != http.StatusPermanentRedirect {
					t.Fatalf("GET %s as alice answered %d", target, w.Code)
				}
				renders := 0
				h.srv.checked = func() {
					if renders++; renders == 1 {
						if mutation == "revoke" {
							h.bind(bob)
						} else if w := h.logout(token); w.Code != http.StatusNoContent {
							t.Errorf("logout answered %d", w.Code)
						}
					}
				}
				w := h.with("GET", target, cookie(token))
				if w.Code != http.StatusNotFound || !bytes.Equal(w.Body.Bytes(), notFoundPage) || len(w.Header().Values("Set-Cookie")) != 0 {
					t.Errorf("GET %s after a %s while it rendered answered %d, setting %q", target, mutation, w.Code, setting(w))
				}
				if renders != 2 {
					t.Errorf("GET %s rendered %d times, want twice", target, renders)
				}
			})
		}
	}
}

// A redemption that waits behind a slow transaction stops waiting at its
// deadline, and consumes nothing: its link redeems once the slow one is
// done.
func TestARedemptionPastItsDeadlineConsumesNothing(t *testing.T) {
	h := newHarness(t)
	reached, slow := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.withReaders(readerstate.Options{Fault: func(op readerstate.Op, step readerstate.Step) error {
		if op == readerstate.Redeeming && step == readerstate.Created {
			once.Do(func() { close(reached); <-slow })
		}
		return nil
	}})
	h.srv.Prepare(context.Background())
	alice, bob := issue(1, "alice"), issue(2, "bob")
	h.bind(alice, bob)
	h.srv.timeout = 300 * time.Millisecond
	first := make(chan int, 1)
	go func() { first <- h.redeem(alice.credential).Code }()
	<-reached
	queued := make(chan *httptest.ResponseRecorder, 1)
	go func() { queued <- h.redeem(bob.credential) }()
	var w *httptest.ResponseRecorder
	select {
	case w = <-queued:
	case <-time.After(5 * time.Second):
		t.Error("a redemption queued behind a slow one waited past its deadline")
		close(slow)
		w = <-queued
	}
	if w.Code != http.StatusServiceUnavailable || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Errorf("the queued redemption answered %d, setting %q", w.Code, setting(w))
	}
	select {
	case <-slow:
	default:
		close(slow)
	}
	if code := <-first; code != http.StatusNoContent {
		t.Errorf("the slow redemption answered %d", code)
	}
	h.srv.timeout = requestTimeout
	if w := h.redeem(bob.credential); w.Code != http.StatusNoContent {
		t.Errorf("bob's link, after its redemption ran out of time, answered %d: %s", w.Code, w.Body)
	}
	if got := h.logged(); got != "" {
		t.Errorf("the server logged: %s", got)
	}
}

// A request that asks whether the state is still to be made waits for
// the read lock only until its deadline. While the state's first write,
// or a logout's, stalls in the filesystem under the write lock, a GET and
// a redemption each answer 503 at their deadline, and consume nothing.
func TestAStalledWriteHoldsNoRequestPastItsDeadline(t *testing.T) {
	for _, op := range []readerstate.Op{readerstate.Initializing, readerstate.SigningOut} {
		t.Run(op.String(), func(t *testing.T) {
			h := newHarness(t)
			reached, slow := make(chan struct{}), make(chan struct{})
			var once sync.Once
			st := h.withReaders(readerstate.Options{Fault: func(o readerstate.Op, step readerstate.Step) error {
				if o == op && step == readerstate.Created {
					once.Do(func() { close(reached); <-slow })
				}
				return nil
			}})
			alice, bob := issue(1, "alice"), issue(2, "bob")
			stalled := make(chan int, 1)
			if op == readerstate.Initializing {
				go func() { h.srv.Prepare(context.Background()); stalled <- http.StatusNoContent }()
			} else {
				h.srv.Prepare(context.Background())
				h.bind(alice)
				token := cookieOf(t, h.redeem(alice.credential))
				// With no link bound, every request asks whether the
				// state is still to be made.
				h.bind()
				go func() { stalled <- h.logout(token).Code }()
			}
			<-reached
			h.srv.timeout = 300 * time.Millisecond
			answered := make(chan *httptest.ResponseRecorder, 2)
			go func() { answered <- h.with("GET", "/tools/app/") }()
			go func() { answered <- h.redeem(bob.credential) }()
			for range 2 {
				select {
				case w := <-answered:
					if w.Code != http.StatusServiceUnavailable || len(w.Header().Values("Set-Cookie")) != 0 {
						t.Errorf("a request behind the stalled %s answered %d, setting %q", op, w.Code, setting(w))
					}
				case <-time.After(5 * time.Second):
					t.Errorf("a request behind the stalled %s waited past its deadline", op)
				}
			}
			close(slow)
			if code := <-stalled; code != http.StatusNoContent {
				t.Errorf("the stalled %s answered %d", op, code)
			}
			h.srv.timeout = requestTimeout
			if pending(t, st) {
				t.Error("the state was not made")
			}
			h.bind(bob)
			if w := h.redeem(bob.credential); w.Code != http.StatusNoContent {
				t.Errorf("bob's link, after its redemption ran out of time, answered %d: %s", w.Code, w.Body)
			}
		})
	}
}

// A response whose deadline passes after its last check found its
// session valid is not written: it answers 503, and renews no session.
func TestAResponsePastItsDeadlineIsNotRenewed(t *testing.T) {
	h, _, _, token := signedIn(t)
	h.srv.timeout = time.Second
	started := false
	h.srv.starting = func() {
		started = true
		time.Sleep(h.srv.timeout + 100*time.Millisecond)
	}
	w := h.with("GET", "/tools/site/", cookie(token))
	if !started {
		t.Fatal("the request ran out of time before its last check")
	}
	if w.Code != http.StatusServiceUnavailable || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Errorf("a response past its deadline answered %d, setting %q", w.Code, setting(w))
	}
	h.srv.starting = nil
	h.srv.timeout = requestTimeout
	if w := h.with("GET", "/tools/site/", cookie(token)); w.Code != http.StatusFound || !renews(w, token) {
		t.Errorf("the next response answered %d, setting %q", w.Code, setting(w))
	}
}

// /-/session tells the reader page's script which link the browser's
// session was redeemed from, and nothing for no valid session. It
// answers GET and HEAD alone, and renews a valid session as every
// response does.
func TestTheSessionStateNamesItsLink(t *testing.T) {
	h, alice, _, token := signedIn(t)
	w := h.with("GET", "/-/session", cookie(token))
	if w.Code != http.StatusOK || w.Body.String() != `{"link":"`+alice.binding.ID+`"}` || !renews(w, token) {
		t.Errorf("/-/session for alice answered %d %q, setting %q", w.Code, w.Body, setting(w))
	}
	if ct, p := w.Header().Get("Content-Type"), w.Header().Get("Content-Security-Policy"); ct != "application/json" || p != readerPolicy {
		t.Errorf("/-/session is %q under %q", ct, p)
	}
	for _, cookies := range [][]string{nil, {cookie(token + "x")}} {
		if w := h.with("GET", "/-/session", cookies...); w.Code != http.StatusOK || w.Body.String() != `{"link":""}` {
			t.Errorf("/-/session with %q answered %d %q", cookies, w.Code, w.Body)
		}
	}
	for _, method := range []string{"POST", "PUT"} {
		w := h.post("/-/session", `{}`, func(r *http.Request) { r.Method = method })
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s /-/session answered %d, Allow %q", method, w.Code, w.Header().Get("Allow"))
		}
	}
	if w := h.logout(token); w.Code != http.StatusNoContent {
		t.Fatalf("logout answered %d", w.Code)
	}
	if w := h.with("GET", "/-/session", cookie(token)); w.Body.String() != `{"link":""}` || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Errorf("/-/session after logout answered %q, setting %q", w.Body, setting(w))
	}
	if w := newHarness(t).get("/-/session"); w.Code != http.StatusNotFound {
		t.Errorf("with no browser state /-/session answered %d", w.Code)
	}
}

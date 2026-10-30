package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"the-valley/web/export"
)

// TestBrowser is the web-reader-browser-e2e check: the packaged
// valley-web, which VALLEY_WEB names, behind a TLS proxy, and headless
// Chromium, which VALLEY_WEB_CHROMIUM names, driven over its automation
// pipe. Chromium trusts the proxy's certificate through a test CA added
// to the temporary profile's certificate database with certutil; nothing
// turns certificate checks or browser security off. It holds the browser
// engine to the reader page alone: loading the page takes the link out of
// the address bar and sends nothing, the page's policy runs its script and
// lets it fetch while it blocks a script put in the page, and one click
// redeems the link once, stores the host-only cookie over HTTPS, and
// lands on /. Two answers the proxy spoils then leave the outcome
// unknown, and the page says so. A redemption, after a sign-out, answered
// 502 with the new session's cookie: the retry asks /-/session, finds
// this link's session, and lands on / with no second sign-out. A
// redemption whose answer never arrived: the retry finds no session,
// signs no one out, and is told the link cannot be used. web-server-unit
// skips it.
func TestBrowser(t *testing.T) {
	chromium, web := os.Getenv("VALLEY_WEB_CHROMIUM"), os.Getenv("VALLEY_WEB")
	if chromium == "" || web == "" {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatal("VALLEY_WEB_CHROMIUM or VALLEY_WEB names nothing, and VALLEY_TEST_TOOLS=required")
		}
		t.Skip("VALLEY_WEB_CHROMIUM and VALLEY_WEB name no browser and no valley-web; the web-reader-browser-e2e check provides both")
	}
	home := t.TempDir()
	certificate := trustedCertificate(t, home)

	// The proxy listens first, so valley-web is given its origin.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	origin := fmt.Sprintf("https://localhost:%d", ln.Addr().(*net.TCPAddr).Port)

	e := newReaderEdge(t)
	first := e.start(exec.Command(web, "--state", e.state, "--listen", "127.0.0.1:0", "--origin", origin))
	e.created()
	e.bind(true)

	target, _ := url.Parse("http://" + first.addr)
	seen := &requests{}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = seen.answer
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, errDropped) {
			if c, _, err := http.NewResponseController(w).Hijack(); err == nil {
				c.Close()
				return
			}
		}
		w.WriteHeader(http.StatusBadGateway)
	}
	// Each answer closes its connection, so no request the browser sends
	// again on a connection it reused hides a dropped answer.
	front := &http.Server{Handler: proxy}
	front.SetKeepAlivesEnabled(false)
	go front.Serve(ln)

	t.Cleanup(func() {
		if t.Failed() {
			seen.mu.Lock()
			defer seen.mu.Unlock()
			t.Logf("the proxy passed on %+v, and valley-web logged:\n%s", seen.seen, redacted(first.logged()))
		}
	})
	b := startBrowser(t, chromium, home)
	page := b.page(t)
	page.call(t, "Page.enable", nil)
	page.open(t, origin+"/-/redeem#"+e.links[0].credential)
	if href := page.eval(t, `location.href`); href != origin+"/-/redeem" {
		t.Errorf("the address bar holds %s once the page loaded", redacted(fmt.Sprint(href)))
	}
	if !page.visible(t, "r-redeem") {
		t.Error("the reader script did not run under the page's policy: its button is hidden")
	}
	// The page is read back and searched here, so no expression and no
	// failure carries the secret.
	if html, ok := page.eval(t, `document.documentElement.outerHTML`).(string); !ok || strings.Contains(html, strings.SplitN(e.links[0].credential, ".", 2)[1]) {
		t.Error("the page could not be read, or the link's secret is in it")
	}
	blocked := page.eval(t, `(() => {
		const s = document.createElement("script");
		s.textContent = "window.injected = true";
		document.head.appendChild(s);
		return window.injected === undefined;
	})()`)
	if blocked != true {
		t.Error("the page's policy ran a script put in the page")
	}
	time.Sleep(500 * time.Millisecond)
	if n := seen.count("POST", "/-/redeem"); n != 0 {
		t.Fatalf("loading the page sent %d redemptions", n)
	}

	page.click(t, "r-redeem")
	// A command sent while the page navigates may go unanswered, so the
	// navigation is followed through the proxy first.
	page.until(t, "the browser follows / with its session", func() bool { return seen.cookied("GET", "/") })
	page.until(t, "the click lands on a page of the source", func() bool { return b.landed(origin) })
	if n := seen.count("POST", "/-/redeem"); n != 1 || seen.status("POST", "/-/redeem") != http.StatusNoContent {
		t.Errorf("one click sent %d redemptions, the first answered %d", n, seen.status("POST", "/-/redeem"))
	}
	if !seen.cookied("GET", "/") {
		t.Error("the browser did not carry the session cookie to /")
	}
	cookies := b.cookies(t)
	if len(cookies) != 1 {
		t.Fatalf("the browser holds %d cookies", len(cookies))
	}
	c := cookies[0]
	if c.Name != "__Host-valley-reader" || c.Domain != "localhost" || c.Path != "/" || c.SameSite != "Lax" || !c.Secure || !c.HTTPOnly {
		t.Errorf("the browser holds the cookie %v", c)
	}

	// A switch to another link whose redemption answers 502, with the new
	// session's cookie: the outcome is unknown, and the page says so. The
	// retry finds this link's session, and lands with no second sign-out.
	page.open(t, origin+"/-/redeem#"+e.links[1].credential)
	if !page.visible(t, "r-switch") {
		t.Fatal("a signed-in browser is not offered to sign out first")
	}
	seen.spoil(func(res *http.Response) error {
		res.StatusCode, res.Status = http.StatusBadGateway, "502 Bad Gateway"
		return nil
	})
	page.click(t, "r-switch")
	page.unknown(t, "r-switch")
	page.click(t, "r-switch")
	page.until(t, "the retry lands on a page of the source", func() bool { return b.landed(origin) })
	if n := seen.count("POST", "/-/logout"); n != 1 {
		t.Errorf("the switch and its retry sent %d logouts, want 1", n)
	}
	if n, asked := seen.count("POST", "/-/redeem"), seen.count("GET", "/-/session"); n != 2 || asked != 1 {
		t.Errorf("the switch and its retry sent %d redemptions in all and asked for the session %d times, want 2 and 1", n, asked)
	}
	if cookies := b.cookies(t); len(cookies) != 1 || !strings.HasPrefix(cookies[0].Value, e.links[1].binding.ID+".") {
		t.Errorf("after the retry the browser holds %v, not the second link's session", cookies)
	}

	// A switch whose redemption's answer never arrives: the session it
	// started never reaches the browser. The retry finds no session, so
	// it signs no one out, and the link, used already, is refused.
	page.open(t, origin+"/-/redeem#"+e.links[2].credential)
	seen.spoil(func(*http.Response) error { return errDropped })
	page.click(t, "r-switch")
	page.unknown(t, "r-switch")
	page.click(t, "r-switch")
	page.until(t, "the retry is told the link cannot be used", func() bool { return page.visible(t, "r-refused") })
	if n := seen.count("POST", "/-/logout"); n != 2 {
		t.Errorf("the second switch and its retry sent %d logouts in all, want 2", n)
	}
	if n, asked := seen.count("POST", "/-/redeem"), seen.count("GET", "/-/session"); n != 4 || asked != 2 {
		t.Errorf("the second switch and its retry sent %d redemptions in all and asked for the session %d times, want 4 and 2", n, asked)
	}
	if cookies := b.cookies(t); len(cookies) != 0 {
		t.Errorf("after a lost answer the browser holds %v", cookies)
	}
}

// errDropped is what the proxy's spoiler returns to drop an answer: the
// proxy closes the connection, and writes none of it.
var errDropped = errors.New("the proxy drops this answer")

// open navigates the tab to a reader page, and waits for it to load. A
// command sent while the page navigates may go unanswered, so it waits
// for the navigation to the page first, and asks again when an answer
// does not come.
func (p *tab) open(t *testing.T, target string) {
	t.Helper()
	navigated := p.call(t, "Page.navigate", map[string]any{"url": target})
	page, _, _ := strings.Cut(target, "#")
	for range 200 {
		if p.b.navigated() == page {
			loaded, ok := p.evalWithin(t, `document.readyState === "complete" && !!document.getElementById("reader")`, 2*time.Second)
			if ok && loaded == true {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	p.b.mu.Lock()
	events := strings.Join(p.b.events, "\n")
	p.b.mu.Unlock()
	t.Fatalf("%s did not load within ten seconds: the last navigation went to %s, and Page.navigate answered %s; events:\n%s", page, redacted(p.b.navigated()), navigated, redacted(events))
}

// click clicks the middle of the element id, as a reader does.
func (p *tab) click(t *testing.T, id string) {
	t.Helper()
	box := p.eval(t, `JSON.stringify(document.getElementById(`+jsString(id)+`).getBoundingClientRect())`)
	s, _ := box.(string)
	var rect struct{ X, Y, Width, Height float64 }
	if err := json.Unmarshal([]byte(s), &rect); err != nil || rect.Width == 0 {
		t.Fatalf("the page has no %s to click: %v", id, box)
	}
	x, y := rect.X+rect.Width/2, rect.Y+rect.Height/2
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		p.call(t, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": 1})
	}
}

// visible reports whether the element id is shown.
func (p *tab) visible(t *testing.T, id string) bool {
	t.Helper()
	return p.eval(t, `(() => { const e = document.getElementById(`+jsString(id)+`); return !!e && !e.hidden; })()`) == true
}

// unknown waits for the page to say that an outcome is unknown, with the
// button retried enabled again.
func (p *tab) unknown(t *testing.T, button string) {
	t.Helper()
	p.until(t, "the page says the outcome is unknown", func() bool {
		return p.visible(t, "r-unknown") && p.eval(t, `document.getElementById(`+jsString(button)+`).disabled`) == false
	})
	text, _ := p.eval(t, `document.getElementById("r-unknown").textContent`).(string)
	if !strings.Contains(text, "cannot tell whether the server used your link") || !strings.Contains(text, "request another link") {
		t.Errorf("the page says %q of an unknown outcome", text)
	}
}

// landed reports whether the last navigation went to a page of this
// origin's source, out of /-/.
func (b *browser) landed(origin string) bool {
	u, err := url.Parse(b.navigated())
	return err == nil && u.Host == strings.TrimPrefix(origin, "https://") && !strings.HasPrefix(u.Path, "/-/")
}

// cookie is a cookie the browser holds, as Storage.getCookies tells it.
type cookie struct {
	Name, Value, Domain, Path, SameSite string
	Secure                              bool
	HTTPOnly                            bool `json:"httpOnly"`
}

// String is the cookie for a failure's message: its metadata, and the
// credential ID its value starts with, never the value.
func (c cookie) String() string {
	id, _, _ := strings.Cut(c.Value, ".")
	if !export.ValidLinkID(id) {
		id = "none"
	}
	return fmt.Sprintf("{%s for %s%s, SameSite=%s, Secure=%v, HttpOnly=%v, credential ID %s}", c.Name, c.Domain, c.Path, c.SameSite, c.Secure, c.HTTPOnly, id)
}

func (b *browser) cookies(t *testing.T) []cookie {
	t.Helper()
	var held struct{ Cookies []cookie }
	if err := json.Unmarshal(b.send(t, "", "Storage.getCookies", nil), &held); err != nil {
		t.Fatal(err)
	}
	return held.Cookies
}

// trustedCertificate makes a test CA, adds it to a certificate database
// under home, as Chromium reads trusted roots there, and returns a
// certificate for localhost it signed.
func trustedCertificate(t *testing.T, home string) tls.Certificate {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "valley test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ = x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pemFile := filepath.Join(home, "ca.pem")
	os.WriteFile(pemFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600)
	db := "sql:" + filepath.Join(home, ".pki", "nssdb")
	os.MkdirAll(filepath.Join(home, ".pki", "nssdb"), 0o700)
	for _, args := range [][]string{
		{"-N", "-d", db, "--empty-password"},
		{"-A", "-d", db, "-n", "valley test CA", "-t", "C,,", "-i", pemFile},
	} {
		if out, err := exec.Command("certutil", args...).CombinedOutput(); err != nil {
			t.Fatalf("certutil %v: %v\n%s", args, err, out)
		}
	}
	return tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: key}
}

// requests are the requests the proxy passed on, with their answers, and
// what the proxy does to the next answer to a redemption.
type requests struct {
	mu      sync.Mutex
	seen    []seenRequest
	spoiler func(*http.Response) error
}

// spoil has the proxy pass the next answer to a redemption through f.
func (r *requests) spoil(f func(*http.Response) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spoiler = f
}

type seenRequest struct {
	method, path string
	cookie       bool
	status       int
}

// answer records an answer valley-web gave, and passes the next answer
// to a redemption through the spoiler a test set, which then is unset.
func (r *requests) answer(res *http.Response) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := res.Request
	r.seen = append(r.seen, seenRequest{q.Method, q.URL.Path, strings.Contains(q.Header.Get("Cookie"), "__Host-valley-reader="), res.StatusCode})
	if f := r.spoiler; f != nil && q.Method == "POST" && q.URL.Path == "/-/redeem" {
		r.spoiler = nil
		return f(res)
	}
	return nil
}

func (r *requests) find(method, path string) []seenRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found []seenRequest
	for _, s := range r.seen {
		if s.method == method && s.path == path {
			found = append(found, s)
		}
	}
	return found
}

func (r *requests) count(method, path string) int { return len(r.find(method, path)) }

func (r *requests) status(method, path string) int {
	if f := r.find(method, path); len(f) > 0 {
		return f[0].status
	}
	return 0
}

func (r *requests) cookied(method, path string) bool {
	for _, s := range r.find(method, path) {
		if s.cookie {
			return true
		}
	}
	return false
}

// browser is a headless Chromium driven over its automation pipe: it
// reads messages from the file descriptor 3 it was given and writes them
// to 4, each a JSON object ended by a NUL byte.
type browser struct {
	mu      sync.Mutex
	in      *os.File
	next    int
	replies map[int]chan json.RawMessage
	// url is where the last navigation of a main frame went, and events
	// are the events the browser sent, each its method and its frame's URL
	// and parent, which a page that did not load is told with.
	url    string
	events []string
}

// navigated is where the last navigation of a main frame went, as the
// browser's Page.frameNavigated events tell it.
func (b *browser) navigated() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.url
}

func startBrowser(t *testing.T, chromium, home string) *browser {
	t.Helper()
	toBrowser, writer, _ := os.Pipe()
	reader, fromBrowser, _ := os.Pipe()
	cmd := exec.Command(chromium, "--headless=new", "--user-data-dir="+filepath.Join(home, "profile"), "--remote-debugging-pipe", "about:blank")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.ExtraFiles = []*os.File{toBrowser, fromBrowser}
	logFile, _ := os.Create(filepath.Join(home, "chromium.log"))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	toBrowser.Close()
	fromBrowser.Close()
	t.Cleanup(func() {
		writer.Close()
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			log, _ := os.ReadFile(logFile.Name())
			var kept []string
			for _, line := range strings.Split(string(log), "\n") {
				if !strings.Contains(line, "render_text_harfbuzz") {
					kept = append(kept, line)
				}
			}
			t.Logf("chromium logged:\n%s", redacted(tail(strings.Join(kept, "\n"), 40)))
		}
	})
	b := &browser{in: writer, replies: map[int]chan json.RawMessage{}}
	go func() {
		r := bufio.NewReader(reader)
		for {
			msg, err := r.ReadBytes(0)
			if err != nil {
				return
			}
			var reply struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
				Method string          `json:"method"`
				Params struct {
					Frame struct {
						ParentID string `json:"parentId"`
						URL      string `json:"url"`
					} `json:"frame"`
				} `json:"params"`
			}
			if json.Unmarshal(msg[:len(msg)-1], &reply) != nil {
				continue
			}
			if reply.Method != "" {
				b.mu.Lock()
				b.events = append(b.events, strings.TrimSpace(reply.Method+" "+reply.Params.Frame.URL+" "+reply.Params.Frame.ParentID))
				b.mu.Unlock()
			}
			if reply.Method == "Page.frameNavigated" && reply.Params.Frame.ParentID == "" {
				b.mu.Lock()
				b.url = reply.Params.Frame.URL
				b.mu.Unlock()
			}
			if reply.ID == 0 {
				continue
			}
			b.mu.Lock()
			c := b.replies[reply.ID]
			delete(b.replies, reply.ID)
			b.mu.Unlock()
			if c != nil {
				if reply.Error != nil {
					c <- append(json.RawMessage(`{"error":`), append(reply.Error, '}')...)
				} else {
					c <- reply.Result
				}
			}
		}
	}()
	return b
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// send sends one command, in session when it is not "", and waits for its
// result.
func (b *browser) send(t *testing.T, session, method string, params any) json.RawMessage {
	t.Helper()
	result, ok := b.sendWithin(t, session, method, params, 30*time.Second)
	if !ok {
		t.Fatalf("%s had no answer", method)
	}
	return result
}

// sendWithin is send, waiting at most d for the result, and false when
// none came.
func (b *browser) sendWithin(t *testing.T, session, method string, params any, d time.Duration) (json.RawMessage, bool) {
	t.Helper()
	b.mu.Lock()
	b.next++
	id := b.next
	c := make(chan json.RawMessage, 1)
	b.replies[id] = c
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if session != "" {
		msg["sessionId"] = session
	}
	data, _ := json.Marshal(msg)
	_, err := b.in.Write(append(data, 0))
	b.mu.Unlock()
	if err != nil {
		t.Fatalf("sending %s: %v", method, err)
	}
	select {
	case result := <-c:
		if strings.HasPrefix(string(result), `{"error":`) {
			t.Fatalf("%s failed: %s", method, redacted(string(result)))
		}
		return result, true
	case <-time.After(d):
		return nil, false
	}
}

// tab is one page of the browser, attached to its own session.
type tab struct {
	b       *browser
	session string
}

func (b *browser) page(t *testing.T) *tab {
	t.Helper()
	var target struct{ TargetID string }
	json.Unmarshal(b.send(t, "", "Target.createTarget", map[string]any{"url": "about:blank"}), &target)
	var attached struct{ SessionID string }
	json.Unmarshal(b.send(t, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}), &attached)
	return &tab{b, attached.SessionID}
}

func (p *tab) call(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	return p.b.send(t, p.session, method, params)
}

func (p *tab) decode(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

// eval is the value of a JavaScript expression in the page. It runs as
// the automation endpoint's, not as one of the page's scripts.
func (p *tab) eval(t *testing.T, expression string) any {
	t.Helper()
	v, ok := p.evalWithin(t, expression, 30*time.Second)
	if !ok {
		t.Fatalf("Runtime.evaluate of %s had no answer", expression)
	}
	return v
}

// evalWithin is eval, waiting at most d for the answer, and false when
// none came.
func (p *tab) evalWithin(t *testing.T, expression string, d time.Duration) (any, bool) {
	t.Helper()
	var r struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	result, ok := p.b.sendWithin(t, p.session, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, d)
	if !ok {
		return nil, false
	}
	p.decode(t, result, &r)
	if r.Exception != nil {
		return nil, true
	}
	return r.Result.Value, true
}

func (p *tab) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: not within ten seconds", what)
}

func jsString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

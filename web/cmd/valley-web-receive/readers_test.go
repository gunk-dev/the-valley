package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// A private export, signed as the publisher signs one, goes through the
// real receiver, and the real valley-web, with readers signing in at an
// origin, serves it. app is anyone's, site alice's alone and docs bob's
// alone. Alice and bob each redeem a read link the stored authorization
// binds. Each is served the knots, the source and the archives of what
// anyone and their principal may read, and every other project answers
// 404, as both private projects do to anyone. Withdrawing alice's grant
// takes site from her next request, while her session and bob's grant
// hold.
func TestNamedReadersSignInOverAPrivateExport(t *testing.T) {
	e := newEdge(t, exporttest.New(t).Private(t))
	grants := map[string][]string{"app": {export.Anyone}, "site": {"alice"}, "docs": {"bob"}}
	e.grant(2, grants)
	e.upload()
	browser := filepath.Join(filepath.Dir(e.store), "browser")
	if err := os.Mkdir(browser, 0o700); err != nil {
		t.Fatal(err)
	}
	web := e.valleyWeb("--origin", readerOrigin)
	waitFor(t, "valley-web makes its browser state", func(ctx context.Context) bool {
		data, err := os.ReadFile(filepath.Join(browser, "state"))
		return err == nil && string(data) == "the-valley/web-reader-state/v1\n"
	})
	alice, bob := readLink(1, "alice"), readLink(2, "bob")
	e.grant(3, grants, alice, bob)
	tokens := map[string]string{"anyone": "", "alice": redeem(t, web, alice), "bob": redeem(t, web, bob)}

	appTip, siteTip := e.f.Inputs.SourceTips["app"], e.f.Inputs.SourceTips["site"]
	pages := map[string][]string{
		"app":  {"/tools/app/knots/oc-a000002", "/tools/app/source/tree/" + appTip + "/", "/tools/app/source/archive/" + appTip + ".tar.gz"},
		"site": {"/tools/site/knots/oc-b000001", "/tools/site/source/tree/" + siteTip + "/", "/tools/site/source/archive/" + siteTip + ".tar.gz"},
		"docs": {"/pages/docs/knots/oc-b000001", "/pages/docs/source/tree/" + siteTip + "/", "/pages/docs/source/archive/" + siteTip + ".tar.gz"},
	}
	knots := map[string]string{"site": `href="/tools/site/knots/oc-b000001"`, "docs": `href="/pages/docs/knots/oc-b000001"`}
	served := func(grants map[string][]string) {
		t.Helper()
		for reader, token := range tokens {
			for project, targets := range pages {
				want := http.StatusNotFound
				for _, r := range grants[project] {
					if r == export.Anyone || r == reader {
						want = http.StatusOK
					}
				}
				for _, target := range targets {
					status, _, renewed := read(t, web, target, token)
					if status != want || renewed != (token != "") {
						t.Errorf("GET %s as %s answered %d, renewed %v; want %d", target, reader, status, renewed, want)
					}
				}
				_, page, _ := read(t, web, "/-/knots", token)
				if listed := knots[project] != "" && strings.Contains(page, knots[project]); knots[project] != "" && listed != (want == http.StatusOK) {
					t.Errorf("the knots page as %s lists %s's knot: %v", reader, project, listed)
				}
			}
		}
	}
	served(grants)
	withdrawn := map[string][]string{"app": {export.Anyone}, "docs": {"bob"}}
	e.grant(4, withdrawn, alice, bob)
	served(withdrawn)
}

// readerOrigin is the origin the test's valley-web takes reader posts from.
const readerOrigin = "https://example.org"

// readerLink is a read link: its credential, and the binding an
// authorization gives it.
type readerLink struct {
	credential string
	binding    export.Binding
}

func readLink(n int, principal string) readerLink {
	secret := bytes.Repeat([]byte{byte(n), 0xa5}, 16)
	sum := sha256.Sum256(secret)
	id := fmt.Sprintf("lk-%016x", n)
	return readerLink{id + "." + base64.RawURLEncoding.EncodeToString(secret),
		export.Binding{ID: id, Principal: principal, Verifier: hex.EncodeToString(sum[:])}}
}

// grant posts an authorization with sequence seq that lets the readers
// given read each project of the edge's export, under its identity, and
// binds the links given, and fails the test unless it is stored.
func (e *edge) grant(seq int64, readers map[string][]string, links ...readerLink) {
	e.t.Helper()
	a := &export.Authorization{Instance: exporttest.Instance, Destination: exporttest.Destination, Sequence: seq, Checked: exporttest.Collected}
	for _, p := range e.f.Export.Projects {
		if rs := readers[p.Name]; len(rs) > 0 {
			a.Projects = append(a.Projects, export.AuthorizedProject{Name: p.Name, Identity: p.Identity, Readers: rs})
		}
	}
	for _, l := range links {
		a.Bindings = append(a.Bindings, l.binding)
	}
	if status, reply, _ := e.request(http.MethodPost, export.AuthorizationPath, exporttest.SignAuthorization(e.t, a), nil); status != http.StatusOK {
		e.t.Fatalf("authorization %d was answered %d: %s", seq, status, reply)
	}
}

// readerClient is the client a reader's browser is: it follows no redirect.
var readerClient = &http.Client{Timeout: requestTimeout, Transport: client.Transport,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// redeem redeems l at the valley-web at addr, as a reader page's script
// does, and returns the session token it sets.
func redeem(t *testing.T, addr string, l readerLink) string {
	t.Helper()
	r, _ := http.NewRequest("POST", "http://"+addr+"/-/redeem", strings.NewReader(`{"credential":"`+l.credential+`"}`))
	r.Header.Set("Origin", readerOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Valley-Reader", "1")
	res, err := readerClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == "__Host-valley-reader" && res.StatusCode == http.StatusNoContent {
			return c.Value
		}
	}
	t.Fatalf("redeeming %s's link answered %d with no session", l.binding.Principal, res.StatusCode)
	return ""
}

// read is a GET of target at the valley-web at addr, carrying token's
// session cookie when token is not "": its status, its body, and whether
// it renewed the session.
func read(t *testing.T, addr, target, token string) (int, string, bool) {
	t.Helper()
	r, _ := http.NewRequest("GET", "http://"+addr+target, nil)
	if token != "" {
		r.Header.Set("Cookie", "__Host-valley-reader="+token)
	}
	res, err := readerClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	renewed := token != "" && strings.HasPrefix(res.Header.Get("Set-Cookie"), "__Host-valley-reader="+token+";")
	return res.StatusCode, string(body), renewed
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"the-valley/exactjson"
	"the-valley/web/export"
	"the-valley/web/readerstate"
)

// The reader's own pages: the page a read link opens, and the page that
// signs a reader out. Each answers GET and HEAD with a page, and POST
// with the transaction the page's script asks for. sessionPage answers
// GET and HEAD alone, with the session the request carries, which the
// reader page's script asks for before it tries again.
const (
	redeemPage  = "redeem"
	logoutPage  = "logout"
	sessionPage = "session"
	logoutURL   = "/" + own + "/" + logoutPage
)

// readerPageOf is the reader page a path's segments name, /-/redeem,
// /-/logout or /-/session, or "" for any other path. A server with no
// browser state has no reader pages.
func (s *Server) readerPageOf(parts []string, ok bool) string {
	if s.readers == nil || !ok || len(parts) != 2 || parts[0] != own {
		return ""
	}
	switch parts[1] {
	case redeemPage, logoutPage, sessionPage:
		return parts[1]
	}
	return ""
}

// posted reports whether a reader page answers POST.
func posted(page string) bool { return page == redeemPage || page == logoutPage }

// cookieName is the session cookie's name. The __Host- prefix makes a
// browser keep it only when it was set over HTTPS, with Path=/ and no
// Domain, so it is this host's alone.
const cookieName = "__Host-valley-reader"

// sessionAge is how long a browser keeps the session cookie after the
// last response that renewed it, in seconds: 30 days. It is the browser's
// retention alone: the server keeps no expiry.
const sessionAge = 2592000

// sessionCookie is the Set-Cookie value that gives a browser token, or,
// for "", that clears the cookie.
func sessionCookie(token string) string {
	age := sessionAge
	if token == "" {
		age = 0
	}
	return fmt.Sprintf("%s=%s; Secure; HttpOnly; Path=/; SameSite=Lax; Max-Age=%d", cookieName, token, age)
}

// carriedCookie reports whether the request carries the session cookie,
// and its value when it carries it exactly once. Every Cookie header and
// every pair in each is counted, so a request that carries the cookie
// twice carries no token.
func carriedCookie(r *http.Request) (carried bool, token string) {
	n := 0
	for _, header := range r.Header.Values("Cookie") {
		for _, pair := range strings.Split(header, ";") {
			name, value, _ := strings.Cut(strings.Trim(pair, " \t"), "=")
			if name == cookieName {
				n++
				token = value
			}
		}
	}
	if n != 1 {
		token = ""
	}
	return n > 0, token
}

// session is the session a request carries, as the browser state resolved
// it under one authorization.
type session struct {
	// token is the session cookie's value when the request carries it
	// once, and valid is set when it names a valid session, whose
	// principal is reader. reader is anyone for every other request.
	token  string
	reader string
	valid  bool
	// unavailable is set when the request carries the session cookie and
	// the browser state cannot be read.
	unavailable bool
}

// same reports whether two resolutions give a request the same reader.
func (st session) same(o session) bool {
	return st.reader == o.reader && st.valid == o.valid && st.unavailable == o.unavailable
}

// readState takes the browser state's read lock, for the session's
// resolution and, at the final check, for the headers written after it,
// waiting for it until ctx is done. done gives it back. A server with no
// browser state holds nothing.
func (s *Server) readState(ctx context.Context) (r readerstate.Reading, done func(), err error) {
	if s.readers == nil {
		return readerstate.Reading{}, func() {}, nil
	}
	if r, err = s.readers.Read(ctx); err != nil {
		return r, nil, err
	}
	return r, r.Done, nil
}

// sessionOf resolves the session cookie a request carries under the
// authorization a, with the browser state's read lock held as r.
func (s *Server) sessionOf(r readerstate.Reading, carried bool, token string, a *export.Authorization) session {
	if s.readers == nil || !carried {
		return session{reader: export.Anyone}
	}
	principal, err := r.Session(token, a)
	switch {
	case err != nil:
		return session{reader: export.Anyone, unavailable: true}
	case principal == "":
		return session{reader: export.Anyone}
	}
	return session{token: token, reader: principal, valid: true}
}

// resolve is sessionOf, taking and giving back the read lock itself. A
// request whose context is done before it takes the lock is answered as
// one whose state cannot be read; its last check, under the same context,
// writes none of it.
func (s *Server) resolve(ctx context.Context, carried bool, token string, a *export.Authorization) session {
	if s.readers == nil || !carried {
		return session{reader: export.Anyone}
	}
	r, done, err := s.readState(ctx)
	if err != nil {
		return session{reader: export.Anyone, unavailable: true}
	}
	defer done()
	return s.sessionOf(r, carried, token, a)
}

// hold takes the serving lock shared and reads the stored authorization
// under it, for a browser-state transaction (readerstate.Hold).
func (s *Server) hold(ctx context.Context) (*export.Authorization, func(), error) {
	release, err := s.store.HoldServing(ctx)
	if err != nil {
		return nil, nil, err
	}
	return s.readAuthorization().a, release, nil
}

// Prepare creates the empty browser state when there is none yet and the
// stored authorization was read, parsed, and carries no binding. Every
// request asks again while the state is still to be created, so the state
// is created once the host delivers an authorization with no binding,
// with no restart. A server with no browser state does nothing.
func (s *Server) Prepare(ctx context.Context) {
	s.prepare(ctx, s.readAuthorization())
}

// prepare waits for the browser state's locks only until ctx is done. A
// request whose wait ended so goes on, and is answered as one past its
// deadline.
func (s *Server) prepare(ctx context.Context, auth authState) {
	if s.readers == nil || auth.a == nil || len(auth.a.Bindings) > 0 {
		return
	}
	if pending, err := s.readers.Pending(ctx); err != nil || !pending {
		return
	}
	if _, err := s.readers.Initialize(ctx, s.hold); err != nil && ctx.Err() == nil {
		s.log(fmt.Errorf("creating the browser state: %w", err))
	}
}

// maxPost is the most bytes of a reader page's POST body.
const maxPost = 1 << 10

// The fixed answers of a reader page's POST. Every credential the
// browser state refuses answers refusedLink, whatever the reason, and
// every request that breaks the POST's contract answers refusedPost.
const (
	refusedLink   = "This link cannot be used. Request another link.\n"
	refusedPost   = "This request is refused.\n"
	anotherReader = "Another reader is signed in on this browser.\n"
	unavailable   = "Sign-in is unavailable on this server right now. Try again later.\n"
)

// post answers a POST to a reader page: a redemption, or a logout. It
// runs once, under the request's deadline, outside the rendering loop,
// and touches no export. Its headers are set already.
func (s *Server) post(ctx context.Context, w http.ResponseWriter, r *http.Request, page string) {
	body, ok := checkReaderPost(ctx, w, r, s.origin)
	if !ok {
		answer(w, http.StatusForbidden, refusedPost)
		return
	}
	_, token := carriedCookie(r)
	var err error
	switch page {
	case redeemPage:
		// The credential is a string, present and not null: {} and
		// {"credential":null} break the post's contract, as any other
		// shape does, before the browser state is reached.
		var in struct {
			Credential *string `json:"credential"`
		}
		if exactjson.Decode(body, &in) != nil || in.Credential == nil {
			answer(w, http.StatusForbidden, refusedPost)
			return
		}
		s.prepare(ctx, s.readAuthorization())
		var next string
		next, err = s.readers.Redeem(ctx, s.hold, *in.Credential, token)
		if err == nil {
			w.Header().Set("Set-Cookie", sessionCookie(next))
		}
	case logoutPage:
		var in struct{}
		if exactjson.Decode(body, &in) != nil {
			answer(w, http.StatusForbidden, refusedPost)
			return
		}
		if err = s.readers.Logout(ctx, s.hold, token); err == nil {
			w.Header().Set("Set-Cookie", sessionCookie(""))
		}
	}
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, readerstate.ErrRefused):
		answer(w, http.StatusForbidden, refusedLink)
	case errors.Is(err, readerstate.ErrSignedIn):
		answer(w, http.StatusConflict, anotherReader)
	case r.Context().Err() != nil:
	default:
		if ctx.Err() == nil {
			s.log(fmt.Errorf("%s: %w", page, err))
		}
		answer(w, http.StatusServiceUnavailable, unavailable)
	}
}

// answer writes a short plain-text response.
func answer(w http.ResponseWriter, status int, text string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", fmt.Sprint(len(text)))
	w.WriteHeader(status)
	io.WriteString(w, text)
}

// checkReaderPost holds a reader page's POST to its contract, and returns
// its body. The request carries exactly one Origin, the server's public
// origin; exactly one Content-Type, application/json with at most a UTF-8
// charset; exactly one X-Valley-Reader: 1; no Content-Encoding; and no
// query. Its body is at most maxPost bytes and is one JSON object. The
// body is read under the request's deadline. A request held to none of it
// is answered by no browser state.
func checkReaderPost(ctx context.Context, w http.ResponseWriter, r *http.Request, origin string) ([]byte, bool) {
	h := r.Header
	one := func(name string) (string, bool) {
		vs := h.Values(name)
		if len(vs) != 1 {
			return "", false
		}
		return vs[0], true
	}
	if v, ok := one("Origin"); !ok || origin == "" || v != origin {
		return nil, false
	}
	if v, ok := one("X-Valley-Reader"); !ok || v != "1" {
		return nil, false
	}
	if v, ok := one("Content-Type"); !ok || !jsonType(v) {
		return nil, false
	}
	if len(h.Values("Content-Encoding")) > 0 || r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength > maxPost {
		return nil, false
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = http.NewResponseController(w).SetReadDeadline(deadline)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPost+1))
	if err != nil || len(body) > maxPost {
		return nil, false
	}
	if trimmed := bytes.TrimLeft(body, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	return body, true
}

// jsonType reports whether a Content-Type is application/json, with no
// parameter but a UTF-8 charset.
func jsonType(v string) bool {
	media, params, err := mime.ParseMediaType(v)
	if err != nil || media != "application/json" {
		return false
	}
	for k, p := range params {
		if k != "charset" || !strings.EqualFold(p, "utf-8") {
			return false
		}
	}
	return true
}

// readerContent is what a reader page shows: whether the request carries
// a valid session.
type readerContent struct {
	Session bool
}

// readerPage answers GET and HEAD of a reader page. It needs no export
// and no authorization, so it renders when nothing is published.
func (q *request) readerPage() {
	if q.rpage == sessionPage {
		q.sessionState()
		return
	}
	title := "Continue as reader"
	if q.rpage == logoutPage {
		title = "Sign out"
	}
	q.page(http.StatusOK, q.rpage, title, readerContent{Session: q.sess.valid})
}

// sessionState answers /-/session: {"link":"<credential-id>"}, the ID of
// the link the request's valid session was redeemed from, or
// {"link":""} for a request with no valid session. The ID is not secret:
// it names the link, and redeems nothing.
func (q *request) sessionState() {
	id := ""
	if q.sess.valid {
		id, _, _ = strings.Cut(q.sess.token, ".")
	}
	data, _ := json.Marshal(struct {
		Link string `json:"link"`
	}{id})
	q.static(http.StatusOK, "application/json", data)
}

// unavailablePage answers a request that carries the session cookie while
// the browser state cannot be read.
func (q *request) unavailablePage() {
	q.static(http.StatusServiceUnavailable, htmlType, unavailableHTML)
}

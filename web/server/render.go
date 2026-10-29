package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The pages' templates, and the one stylesheet and one script they carry
// inline. Nothing else is served: no font, image or script is fetched.
var (
	//go:embed templates/*.tmpl
	templateFiles embed.FS
	//go:embed assets/style.css
	styleCSS string
	//go:embed assets/plan.js
	planJS string
)

// csp is every response's Content-Security-Policy. The inline stylesheet
// and script are admitted by their SHA-256 hashes alone, and every other
// fetch is held to the server's own origin.
var csp = "default-src 'self'; script-src " + inlineHash(planJS) + "; style-src " + inlineHash(styleCSS) +
	"; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func inlineHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

var pages = parsePages("plan", "knot", "tree", "blob", "log", "error", "notfound")

// parsePages parses each page's template over the shared layout.
func parsePages(names ...string) map[string]*template.Template {
	funcs := template.FuncMap{"glyph": glyphClass, "label": stateLabel, "inc": func(i int) int { return i + 1 }}
	base := template.Must(template.New("layout").Funcs(funcs).ParseFS(templateFiles, "templates/layout.tmpl"))
	m := map[string]*template.Template{}
	for _, name := range names {
		m[name] = template.Must(template.Must(base.Clone()).ParseFS(templateFiles, "templates/"+name+".tmpl"))
	}
	return m
}

// layout is what every page shares: its title, the inline stylesheet, the
// script on the plan page alone, the links to the plan and to each
// project's source, and the export's age.
type layout struct {
	Title   string
	Style   template.CSS
	Script  template.JS
	Nav     []link
	Fresh   *freshness
	Content any
}

// errorPage is a page that answers a request with no content of its own.
type errorPage struct {
	Heading string
	Text    string
}

// link is one link: its text and where it goes.
type link struct {
	Text string
	URL  string
}

// page renders the page called name with content as the response, to be
// written with status. A page is rendered whole before anything is
// written, so a failure to render answers 500 rather than half a page. A
// page that would pass the server's maxResponse stops rendering there,
// and answers with the short page that says it is too large. A page the
// response budget has no room for, or whose request runs out of time
// while it renders, answers 503. A request runs no git once it renders,
// so it gives back its git slot first.
func (q *request) page(status int, name, title string, content any) {
	q.giveSlot()
	data := layout{Title: title, Style: template.CSS(styleCSS), Content: content}
	if name == "plan" {
		data.Script = template.JS(planJS)
	}
	if q.e != nil {
		data.Nav = append(data.Nav, link{"Plan", "/"})
		for _, name := range q.idx.public {
			p := q.idx.projects[name]
			data.Nav = append(data.Nav, link{name + " source", treeURL(q.idx.bases[name], p.SourceTip, nil)})
		}
		data.Fresh = freshnessOf(q.e.Collected, q.auth.a.Checked, q.now)
	}
	q.drop()
	if q.s.rendering != nil {
		q.s.rendering(name)
	}
	b := &body{s: q.s, ctx: q.ctx}
	err := pages[name].ExecuteTemplate(b, "layout", data)
	if err == nil {
		q.status, q.ctype, q.body = status, htmlType, b
		return
	}
	b.free()
	switch {
	case errors.Is(err, errNoBudget):
		q.busy()
	case errors.Is(err, errTooLarge):
		q.tooLarge()
	case q.ctx.Err() != nil:
		q.fail(fmt.Errorf("rendering %s: %w", name, err))
	default:
		q.s.log(fmt.Errorf("rendering %s: %w", name, err))
		q.static(http.StatusInternalServerError, "text/plain; charset=utf-8", notRendered)
	}
}

const htmlType = "text/html; charset=utf-8"

// tooLarge logs a response that would pass the server's maxResponse, and
// answers with the short page that says the page is too large.
func (q *request) tooLarge() {
	q.s.log(fmt.Errorf("rendering %q: the page is larger than %d bytes", q.r.URL.EscapedPath(), q.s.maxResponse))
	q.static(http.StatusInternalServerError, htmlType, tooLargePage)
}

// static answers with a response made once, when the server starts. It
// shows no export, and takes nothing from the budget.
func (q *request) static(status int, ctype string, page []byte) {
	q.drop()
	q.status, q.ctype, q.body = status, ctype, &body{pieces: [][]byte{page}, n: len(page)}
}

// drop gives back the pieces of the response the request rendered, if it
// rendered one.
func (q *request) drop() {
	if q.body != nil {
		q.body.free()
		q.body = nil
	}
}

// The responses made once: three pages, and the text that answers a page
// that failed to render. The not-found page is the one every 404 answers
// with. It shows the box of text the operator chose, whose last line
// links to the plan, and it names no site, no project and nothing of the
// request.
var (
	busyPage = staticPage("error", "Busy", errorPage{
		Heading: "Busy", Text: "The server is serving too many pages at once. Try again in a few seconds.",
	})
	tooLargePage = staticPage("error", "Page too large", errorPage{
		Heading: "Page too large", Text: "This page is larger than the server sends.",
	})
	notFoundPage = staticPage("notfound", "Not found", nil)
	notRendered  = []byte("The server could not render this page.\n")
)

func staticPage(name, title string, content any) []byte {
	var b bytes.Buffer
	data := layout{Title: title, Style: template.CSS(styleCSS), Content: content}
	if err := pages[name].ExecuteTemplate(&b, "layout", data); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// The errors a body's Write fails with: the page is larger than the
// server sends, or the response budget has no piece left for it.
var (
	errTooLarge = errors.New("the page is larger than the server sends")
	errNoBudget = errors.New("the response budget is spent")
)

// body is a response's body: the pieces it is held in, and their length.
// A page is rendered into pieces of pieceSize, each taken from the
// server's budget and given back by free, until its request's ctx is
// done. Once a body grows past its first SmallPage bytes, all of its
// pieces are held against the large budget too. A static page is one
// piece of its own, and its body has no server.
type body struct {
	s      *Server
	ctx    context.Context
	pieces [][]byte
	n      int
}

// Write appends p to the body. It writes nothing of p that would take the
// body past the server's maxResponse, and fails with errTooLarge, and
// nothing once ctx is done. It fails with errNoBudget when the budget, or
// for a body past its first SmallPage bytes the large budget, has no room
// left for the rest of p.
func (b *body) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > b.s.maxResponse-b.n {
		return 0, errTooLarge
	}
	written := 0
	for written < len(p) {
		last := len(b.pieces) - 1
		if last < 0 || len(b.pieces[last]) == cap(b.pieces[last]) {
			if !b.s.take(len(b.pieces)) {
				return written, errNoBudget
			}
			b.pieces = append(b.pieces, make([]byte, 0, pieceSize))
			last++
		}
		piece := b.pieces[last]
		k := copy(piece[len(piece):cap(piece)], p[written:])
		b.pieces[last] = piece[:len(piece)+k]
		written += k
		b.n += k
	}
	return written, nil
}

// free gives the body's pieces back to the budget, once.
func (b *body) free() {
	if b.s != nil {
		b.s.give(len(b.pieces))
	}
	b.pieces, b.n = nil, 0
}

// The budget in pieces: all of it, the large budget, and how many of a
// response's pieces SmallPage is.
const (
	budgetPieces = MaxBuffered / pieceSize
	largePieces  = MaxLarge / pieceSize
	smallPieces  = SmallPage / pieceSize
)

// take takes a response's piece i from the budget. A response's first
// smallPieces pieces take from the budget alone. The piece that takes it
// past them takes from the large budget too, for itself and for every
// piece before it, and each piece after that takes one more. So responses
// larger than SmallPage hold at most MaxLarge in all, counting their
// first SmallPage bytes, and the rest of the budget is left to the
// others. take takes nothing and is false when either budget has too
// little left.
func (s *Server) take(i int) bool {
	large := 0
	switch {
	case i == smallPieces:
		large = smallPieces + 1
	case i > smallPieces:
		large = 1
	}
	s.pieces.Lock()
	defer s.pieces.Unlock()
	if s.held == budgetPieces || s.large+large > largePieces {
		return false
	}
	s.held++
	s.large += large
	return true
}

// give gives back the n pieces a response took.
func (s *Server) give(n int) {
	s.pieces.Lock()
	defer s.pieces.Unlock()
	s.held -= n
	if n > smallPieces {
		s.large -= n
	}
}

// freshness is when the export's plan was collected, and when the
// publisher last finished a check of the policy, with that check's age as
// of the request. The pages are stale once that check is older than
// StaleAfter: the publisher checks the policy on every pass, whether or
// not the content changed, so a page goes stale only once the publisher
// stops reaching the edge. The collection time is shown apart, and says
// how old the content is; a recent check does not make old content new.
type freshness struct {
	Collected string
	Checked   string
	Age       string
	Stale     bool
}

func freshnessOf(collected string, checked, now time.Time) *freshness {
	f := &freshness{Collected: stampOf(collected), Checked: stamp(checked)}
	age := now.Sub(checked)
	f.Age, f.Stale = ago(age), age > StaleAfter
	return f
}

// stamp is a time as the pages show it, to the minute, in UTC.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// stampOf is stamp of an RFC 3339 time, or the text as it is when it is
// not one.
func stampOf(s string) string {
	t, err := parseTime(s)
	if err != nil {
		return s
	}
	return stamp(t)
}

// ago is an age in words. A negative age, from a clock behind the
// publisher's, reads as the youngest.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute ago"
	case d < time.Hour:
		return count(int(d/time.Minute), "minute") + " ago"
	case d < 48*time.Hour:
		return count(int(d/time.Hour), "hour") + " ago"
	}
	return count(int(d/(24*time.Hour)), "day") + " ago"
}

// trimEnd is text without the line ends that close it, which a
// preformatted block would show as blank lines.
func trimEnd(s string) string { return strings.TrimRight(s, "\r\n") }

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// The derived states valley-plan/v1 gives a knot, and the words the pages
// show for each. ready is shown as free to start.
var stateWords = map[string]string{
	"ready":       "free to start",
	"in-progress": "in progress",
	"blocked":     "blocked",
	"active":      "question or idea",
	"done":        "done",
	"abandoned":   "abandoned",
}

// glyphClass is the class of a state's glyph. A state the pages do not
// know is drawn as active.
func glyphClass(state string) string {
	if _, ok := stateWords[state]; ok {
		return state
	}
	return "active"
}

func stateLabel(state string) string {
	if w, ok := stateWords[state]; ok {
		return w
	}
	return state
}

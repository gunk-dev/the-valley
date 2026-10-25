package server

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
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

var pages = parsePages("plan", "knot", "tree", "blob", "log", "error")

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

// page renders the page called name with content, and writes it with
// status. A page is rendered whole before anything is written, so a
// failure to render answers 500 rather than half a page.
func (q *request) page(status int, name, title string, content any) {
	data := layout{Title: title, Style: template.CSS(styleCSS), Content: content}
	if name == "plan" {
		data.Script = template.JS(planJS)
	}
	if q.e != nil {
		data.Nav = append(data.Nav, link{"Plan", "/"})
		for _, name := range q.idx.public {
			p := q.idx.projects[name]
			data.Nav = append(data.Nav, link{name + " source", treeURL(name, p.SourceTip, nil)})
		}
		data.Fresh = freshnessOf(q.e.Collected, q.now)
	}
	var b bytes.Buffer
	if err := pages[name].ExecuteTemplate(&b, "layout", data); err != nil {
		q.s.log(fmt.Errorf("rendering %s: %w", name, err))
		q.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		q.w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(q.w, "The server could not render this page.")
		return
	}
	h := q.w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(b.Len()))
	q.w.WriteHeader(status)
	q.w.Write(b.Bytes())
}

// freshness is the export's collection time and its age, as of the
// request. An export is stale once it is older than StaleAfter.
type freshness struct {
	Collected string
	Age       string
	Stale     bool
}

func freshnessOf(collected string, now time.Time) *freshness {
	at, err := parseTime(collected)
	if err != nil {
		return &freshness{Collected: collected, Age: "of unknown age", Stale: true}
	}
	age := now.Sub(at)
	return &freshness{Collected: stamp(at), Age: ago(age), Stale: age > StaleAfter}
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

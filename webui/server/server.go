// Package server is the web UI's read side. It serves the active export
// of a receiver's state directory to anonymous readers: the plan page,
// each knot, and a source browser over each public project's main.
//
// # Snapshots
//
// Every request opens the store's active generation once, with
// export.Store.Open, and serves its whole response from that generation.
// It releases the generation when the response is written. An activation
// during the request changes nothing the request reads.
//
// # Routes
//
// The server answers GET and HEAD alone:
//
//	/                                       the plan page
//	/knots/<project>/<id>                   one knot, with its body and comments
//	/source/<project>/                      a redirect to the tree at main's tip
//	/source/<project>/tree/<commit>/<path>  a directory and its README
//	/source/<project>/blob/<commit>/<path>  a file
//	/source/<project>/log/<commit>          the history that ends at a commit
//
// Every source link names its project, a full commit ID and a path. A
// commit is served only when export.Reachable finds it reachable from the
// project's published source tip, and a path is resolved one tree entry
// at a time from that commit's tree, so no other object can be named.
//
// # Access
//
// Every route checks that the export holds its project as public. A
// project it does not hold answers exactly as a project that does not
// exist: every 404 is the same page.
//
// # Safety
//
// Every page is rendered on the server with html/template, so everything
// read from the export or a repository is escaped. Each response carries
// a strict Content-Security-Policy: default-src 'self', and one inline
// stylesheet and one inline script admitted by their SHA-256 hashes. No
// repository file is served as itself: a file is shown as escaped text,
// and a binary file or one larger than MaxFile is not shown at all.
package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"the-valley/webui/export"
)

// StaleAfter is how old an export may be before its pages mark it stale.
const StaleAfter = 5 * time.Minute

// The bounds on what one request reads from a repository.
const (
	// MaxFile is the largest file a page shows, README or otherwise.
	MaxFile = 1 << 20
	// MaxLines is the most lines of a file a page shows.
	MaxLines = 10_000
	// MaxShown is the most bytes a page spends on a file's text once it
	// is escaped, counting each line's markup on a file page. A page
	// stops at the line that would take it past MaxShown.
	MaxShown = 2 << 20
	// MaxEntries is the most entries a directory page lists.
	MaxEntries = 1000
	// MaxRead is the most bytes of output a page reads from one git
	// listing: a directory's entries, or a page of history. git is
	// stopped once it prints more.
	MaxRead = 4 << 20
	// MaxDepth is the most path segments a source route takes.
	MaxDepth = 64
	// LogPage is the number of commits on one page of a log.
	LogPage = 50
	// MaxGit is the most source requests that run git at once. Others
	// wait for one to finish.
	MaxGit = 8
	// gitTimeout bounds the git commands of one request.
	gitTimeout = 15 * time.Second
)

// Config is what a Server serves from.
type Config struct {
	// Store is the receiver's state directory.
	Store *export.Store
	// Now is the clock an export's age is read against. Nil is time.Now.
	Now func() time.Time
	// Log is told of each failure a reader sees only as an error page.
	// Nil logs with the standard logger.
	Log func(error)
}

// Server is the read side's HTTP handler.
type Server struct {
	store *export.Store
	now   func() time.Time
	log   func(error)
	git   chan struct{}

	mu     sync.Mutex
	cached *snapshot

	// opened, when set, is called with each generation a request opens,
	// before the request reads anything from it.
	opened func(export.Generation)
}

// New is a Server over c's store.
func New(c Config) *Server {
	s := &Server{store: c.Store, now: c.Now, log: c.Log, git: make(chan struct{}, MaxGit)}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = func(err error) { log.Print(err) }
	}
	return s
}

// snapshot is one generation's export, decoded, and its index. A
// generation is immutable once active, and its directory names its
// sequence, so a snapshot is kept for as long as its generation is the
// one requests open.
type snapshot struct {
	dir string
	e   *export.Export
	idx *index
}

// request is one request, and the generation it serves from.
type request struct {
	s   *Server
	w   http.ResponseWriter
	r   *http.Request
	g   export.Generation
	e   *export.Export
	idx *index
	now time.Time
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setHeaders(w.Header())
	q := &request{s: s, w: w, r: r, now: s.now()}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		q.page(http.StatusMethodNotAllowed, "error", "Method not allowed", errorPage{
			Heading: "Method not allowed", Text: "This server answers GET and HEAD alone.",
		})
		return
	}
	g, release, err := s.store.Open()
	defer release()
	if errors.Is(err, export.ErrNoExport) {
		q.page(http.StatusServiceUnavailable, "error", "Nothing published", errorPage{
			Heading: "Nothing published yet", Text: "No export has been activated on this server.",
		})
		return
	}
	if err != nil {
		q.fail(err)
		return
	}
	if s.opened != nil {
		s.opened(g)
	}
	snap, err := s.snapshot(g)
	if err != nil {
		q.fail(err)
		return
	}
	q.g, q.e, q.idx = g, snap.e, snap.idx
	q.route()
}

// snapshot reads the generation's export, or keeps the one read last when
// it is the same generation's.
func (s *Server) snapshot(g export.Generation) (*snapshot, error) {
	s.mu.Lock()
	cached := s.cached
	s.mu.Unlock()
	if cached != nil && cached.dir == g.Dir {
		return cached, nil
	}
	e, err := g.Export()
	if err != nil {
		return nil, err
	}
	snap := &snapshot{dir: g.Dir, e: e, idx: newIndex(e)}
	s.mu.Lock()
	s.cached = snap
	s.mu.Unlock()
	return snap, nil
}

// route dispatches on the path's segments, each unescaped on its own, so
// an escaped slash stays inside its segment.
func (q *request) route() {
	parts, ok := segments(q.r.URL.EscapedPath())
	switch {
	case !ok:
		q.notFound()
	case len(parts) == 1 && parts[0] == "":
		q.plan()
	case len(parts) == 3 && parts[0] == "knots":
		q.knot(parts[1], parts[2])
	case len(parts) >= 2 && parts[0] == "source":
		q.source(parts[1], parts[2:])
	default:
		q.notFound()
	}
}

func segments(escaped string) ([]string, bool) {
	rest, ok := strings.CutPrefix(escaped, "/")
	if !ok {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	for i, p := range parts {
		u, err := url.PathUnescape(p)
		if err != nil {
			return nil, false
		}
		parts[i] = u
	}
	return parts, true
}

// project is the export's project called name, when the export holds it
// as public. Every route that names a project asks here first, and a
// project it refuses answers as one that does not exist.
func (q *request) project(name string) (*export.Project, bool) {
	p, ok := q.idx.projects[name]
	if !ok || p.Visibility != export.Public {
		return nil, false
	}
	return p, true
}

// notFound is the one 404 page, whatever was not found. It names nothing
// of the request, so a hidden project, a missing one, an unreachable
// commit and a missing path all answer alike.
func (q *request) notFound() {
	q.page(http.StatusNotFound, "error", "Not found", errorPage{
		Heading: "Not found", Text: "Nothing is published at this address.",
	})
}

// fail logs err and answers with the one 500 page, which says nothing of
// the failure. A reader that went away is answered with nothing: git
// killed for it fails, and that is no failure of the server's.
func (q *request) fail(err error) {
	if q.r.Context().Err() != nil {
		return
	}
	q.s.log(err)
	q.page(http.StatusInternalServerError, "error", "Error", errorPage{
		Heading: "Something went wrong", Text: "The server could not read this page.",
	})
}

// gitContext is the context the request's git commands run under. It
// waits for one of the MaxGit slots, and done gives the slot back.
func (q *request) gitContext() (ctx context.Context, done func(), err error) {
	select {
	case q.s.git <- struct{}{}:
	case <-q.r.Context().Done():
		return nil, func() {}, q.r.Context().Err()
	}
	ctx, cancel := context.WithTimeout(q.r.Context(), gitTimeout)
	return ctx, func() { cancel(); <-q.s.git }, nil
}

// setHeaders sets the headers every response carries.
func setHeaders(h http.Header) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "no-cache")
}

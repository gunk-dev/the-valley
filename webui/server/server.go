// Package server is the web UI's read side. It serves the active export
// of a receiver's state directory to anonymous readers: the plan page,
// each knot, and a source browser over each public project's main.
//
// # Snapshots
//
// Every request opens the store's active generation once, with
// export.Store.Open, and renders its whole response from that
// generation. An activation during the request changes nothing the
// request reads. Each generation's export is read once, however many
// requests open it at once, and is kept while the generation is the
// newest one read. At most MaxLoads exports are read at once. A read
// starts only once it has one of those slots, and it runs in the
// goroutine of the request that took the slot, which holds the
// generation open while it reads. The requests that wait for a read
// stop waiting when their reader goes away or their time runs out.
//
// # Admission
//
// Every request's time is bounded from when it arrives: its waits, its
// git commands and its rendering. A source request runs git, and at most
// MaxGit run at once. A source request takes one of those slots before
// it opens a generation, and waits for one only while fewer than
// MaxWaiting wait. A request the server cannot admit, or that runs out
// of time, answers 503.
//
// # Responses
//
// A response is rendered whole before it is written. A request gives back
// its generation and its git slot before it writes, so a reader that
// reads slowly holds neither. It keeps its response until the response
// is written.
//
// A response's body is at most MaxResponse bytes. A page that would pass
// it stops rendering there, and the request answers with a short page
// that says the page is too large. Rendered responses take their memory
// from a budget of MaxBuffered bytes, which they give back once they are
// written. A request that finds the budget spent answers 503.
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
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
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
	// MaxPlanDepth is the most levels of knots a milestone's tree
	// shows, and the most parents a knot's trail names.
	MaxPlanDepth = 32
	// LogPage is the number of commits on one page of a log.
	LogPage = 50
	// MaxGit is the most source requests that run git at once.
	MaxGit = 8
	// MaxWaiting is the most source requests that wait for one of the
	// MaxGit to finish. A source request that finds every slot taken
	// and MaxWaiting waiting answers 503 at once.
	MaxWaiting = 16
	// requestTimeout bounds a request from when it arrives: its wait for
	// a git slot or for its generation's export to be read, its git
	// commands and its rendering.
	requestTimeout = 15 * time.Second
	// MaxLoads is the most generations whose export is read at once.
	MaxLoads = 2
	// MaxResponse is the most bytes a response's body takes. A page
	// that would take more stops rendering, and the request answers with
	// a short page that says the page is too large.
	MaxResponse = 8 << 20
	// MaxBuffered is the most bytes of rendered responses the server
	// holds at once, from when each starts rendering until it is
	// written. A response takes them in pieces of pieceSize as it
	// renders.
	MaxBuffered = 64 << 20
	// pieceSize is the size of the pieces a response is rendered into.
	pieceSize = 64 << 10
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
	// timeout is requestTimeout, and maxResponse is MaxResponse. A test
	// shortens either.
	timeout     time.Duration
	maxResponse int
	// slots holds a token for each source request running git, and
	// waiting one for each source request waiting for a slot.
	slots   chan struct{}
	waiting chan struct{}
	// loaders holds a token for each export being read.
	loaders chan struct{}
	// budget holds a token for each piece of a response that is being
	// rendered or written: MaxBuffered bytes, in pieces of pieceSize.
	budget chan struct{}

	mu sync.Mutex
	// cached is the newest generation's snapshot read so far.
	cached *snapshot
	// loads are the reads of generations' exports under way, by sequence.
	loads map[int64]*load

	// The test hooks. opened, when set, is called with each generation a
	// request opens, before the request reads anything from it. admitted
	// is called when a source request takes a git slot, before it opens
	// a generation. loading is called with each generation whose export
	// is read, before it is read.
	opened   func(export.Generation)
	admitted func()
	loading  func(export.Generation)
}

// New is a Server over c's store.
func New(c Config) *Server {
	s := &Server{store: c.Store, now: c.Now, log: c.Log, timeout: requestTimeout, maxResponse: MaxResponse,
		slots: make(chan struct{}, MaxGit), waiting: make(chan struct{}, MaxWaiting),
		loaders: make(chan struct{}, MaxLoads), budget: make(chan struct{}, MaxBuffered/pieceSize),
		loads: map[int64]*load{}}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = func(err error) { log.Print(err) }
	}
	return s
}

// snapshot is one generation's export, decoded, and its index. A
// generation is immutable once active, and its sequence names it, so a
// snapshot serves every request that opens its generation.
type snapshot struct {
	seq int64
	e   *export.Export
	idx *index
}

// load is one read of a generation's export, which every request that
// opens the generation while it is under way waits for. The first of
// those requests to take a loader slot reads the export.
type load struct {
	// waiting counts the requests that wait for the read, and reading
	// is set once one of them reads the export. A load every request gave
	// up on before it started is dropped. Both are guarded by the
	// server's mu.
	waiting int
	reading bool
	// started is closed once a request reads the export, and done once
	// it has read it.
	started chan struct{}
	done    chan struct{}
	snap    *snapshot
	err     error
}

// request is one request, the generation it serves from, and the
// response it renders.
type request struct {
	s *Server
	w http.ResponseWriter
	r *http.Request
	// ctx is the context the request waits and runs git under. It ends
	// at the request's deadline, or when its reader goes away.
	ctx context.Context
	g   export.Generation
	e   *export.Export
	idx *index
	now time.Time

	// The response: a status and a page, or a redirect. It is written
	// once the request has given back its generation and git slot, and
	// the page's pieces are given back to the budget once it is written.
	status   int
	ctype    string
	body     *body
	location string
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setHeaders(w.Header())
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	q := &request{s: s, w: w, r: r, ctx: ctx, now: s.now()}
	defer q.drop()
	q.serve()
	q.write()
}

// serve renders the request's response. A source request is admitted
// first, and holds its git slot until serve returns. Every request holds
// its generation until serve returns.
func (q *request) serve() {
	if q.r.Method != http.MethodGet && q.r.Method != http.MethodHead {
		q.w.Header().Set("Allow", "GET, HEAD")
		q.page(http.StatusMethodNotAllowed, "error", "Method not allowed", errorPage{
			Heading: "Method not allowed", Text: "This server answers GET and HEAD alone.",
		})
		return
	}
	parts, ok := segments(q.r.URL.EscapedPath())
	if ok && runsGit(parts) {
		done, admitted := q.s.admit(q.ctx)
		if !admitted {
			q.busy()
			return
		}
		defer done()
		if q.s.admitted != nil {
			q.s.admitted()
		}
	}
	g, release, err := q.s.store.Open()
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
	if q.s.opened != nil {
		q.s.opened(g)
	}
	snap, err := q.s.snapshot(q.ctx, g)
	if err != nil {
		q.fail(err)
		return
	}
	q.g, q.e, q.idx = g, snap.e, snap.idx
	q.route(parts, ok)
}

// runsGit reports whether a path is a source route that reads a
// repository: a tree, a file or a log.
func runsGit(parts []string) bool {
	return len(parts) >= 3 && parts[0] == "source" && (parts[2] == "tree" || parts[2] == "blob" || parts[2] == "log")
}

// admit takes a git slot for a source request. When every slot is taken,
// it waits for one until ctx is done, unless MaxWaiting requests wait
// already. done gives the slot back.
func (s *Server) admit(ctx context.Context) (done func(), ok bool) {
	give := func() { <-s.slots }
	select {
	case s.slots <- struct{}{}:
		return give, true
	default:
	}
	select {
	case s.waiting <- struct{}{}:
	default:
		return nil, false
	}
	defer func() { <-s.waiting }()
	select {
	case s.slots <- struct{}{}:
		return give, true
	case <-ctx.Done():
		return nil, false
	}
}

// snapshot is the generation's snapshot: the cached one when it is the
// generation's, and otherwise the one a single read of the generation's
// export gives every request that waits for it. A read is kept only when
// its generation is newer than the cached one, so a request still serving
// an older generation never displaces a newer.
//
// At most MaxLoads exports are read at once. The requests waiting for a
// read that has not started each try to take a loader slot, and the
// first to take one reads the export itself, so no read is queued that
// no request waits for. A request stops waiting when ctx is done, and a
// read that every waiting request gave up on before it started is
// dropped.
func (s *Server) snapshot(ctx context.Context, g export.Generation) (*snapshot, error) {
	s.mu.Lock()
	if c := s.cached; c != nil && c.seq == g.Sequence {
		s.mu.Unlock()
		return c, nil
	}
	l, ok := s.loads[g.Sequence]
	if !ok {
		l = &load{started: make(chan struct{}), done: make(chan struct{})}
		s.loads[g.Sequence] = l
	}
	l.waiting++
	s.mu.Unlock()
	select {
	case <-l.started:
	case s.loaders <- struct{}{}:
		if s.start(l) {
			s.load(g, l)
			return l.snap, l.err
		}
		<-s.loaders
	case <-ctx.Done():
		s.leave(g.Sequence, l)
		return nil, fmt.Errorf("waiting for generation %d's export to be read: %w", g.Sequence, ctx.Err())
	}
	select {
	case <-l.done:
		return l.snap, l.err
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for generation %d's export to be read: %w", g.Sequence, ctx.Err())
	}
}

// start makes l's read the read of the request that took a loader slot
// for it. It is false when another request reads it already.
func (s *Server) start(l *load) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.reading {
		return false
	}
	l.reading = true
	close(l.started)
	return true
}

// leave is a request giving up on l. The last request to give up on a
// read that has not started drops it.
func (s *Server) leave(seq int64, l *load) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.waiting--; l.waiting == 0 && !l.reading && s.loads[seq] == l {
		delete(s.loads, seq)
	}
}

// load reads g's export for every request waiting on l, and gives back
// the loader slot the reading request took. That request holds g open
// while it reads.
func (s *Server) load(g export.Generation, l *load) {
	defer func() { <-s.loaders }()
	if s.loading != nil {
		s.loading(g)
	}
	e, err := g.Export()
	if err == nil {
		l.snap = &snapshot{seq: g.Sequence, e: e, idx: newIndex(e)}
	}
	l.err = err
	s.mu.Lock()
	delete(s.loads, g.Sequence)
	if l.snap != nil && (s.cached == nil || s.cached.seq < g.Sequence) {
		s.cached = l.snap
	}
	s.mu.Unlock()
	close(l.done)
}

// route dispatches on the path's segments, each unescaped on its own, so
// an escaped slash stays inside its segment. ok is false for a path whose
// segments do not unescape.
func (q *request) route(parts []string, ok bool) {
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
func (q *request) project(name string) (*export.Project, bool) { return q.idx.project(name) }

func (idx *index) project(name string) (*export.Project, bool) {
	p, ok := idx.projects[name]
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
// killed for it fails, and that is no failure of the server's. A request
// that ran out of time answers 503.
func (q *request) fail(err error) {
	if q.r.Context().Err() != nil {
		return
	}
	q.s.log(err)
	if q.ctx.Err() != nil {
		q.busy()
		return
	}
	q.page(http.StatusInternalServerError, "error", "Error", errorPage{
		Heading: "Something went wrong", Text: "The server could not read this page.",
	})
}

// busy answers a request the server has no room for: a source request it
// could not admit, a request that ran out of time, or a page the response
// budget has no room for. A reader that went away is answered with
// nothing.
func (q *request) busy() {
	if q.r.Context().Err() != nil {
		return
	}
	q.w.Header().Set("Retry-After", "5")
	q.static(http.StatusServiceUnavailable, htmlType, busyPage)
}

// redirect answers with a redirect to url.
func (q *request) redirect(url string) { q.location = url }

// write writes the rendered response, or nothing for a reader that went
// away. It returns once the response is written, or the connection
// failed or timed out.
func (q *request) write() {
	switch {
	case q.location != "":
		http.Redirect(q.w, q.r, q.location, http.StatusFound)
	case q.status != 0:
		h := q.w.Header()
		h.Set("Content-Type", q.ctype)
		h.Set("Content-Length", strconv.Itoa(q.body.n))
		q.w.WriteHeader(q.status)
		for _, p := range q.body.pieces {
			if _, err := q.w.Write(p); err != nil {
				return
			}
		}
	}
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

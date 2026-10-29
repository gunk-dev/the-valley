// Package server is the web UI's read side. It serves the active export
// of a receiver's store to anonymous readers, as the store's authorization
// allows: the plan page, each knot, and a source browser over the main of
// each project the authorization lets anyone read.
//
// # Snapshots
//
// Every request reads the store's authorization when it arrives, opens
// the store's active generation once, with export.Store.Open, and renders
// its whole response from that generation under that authorization. An
// activation during the request changes nothing the request reads. The
// server keeps a snapshot of the newest generation it has read under one
// authorization: the export, decoded, and its index for anyone. A request
// opens its generation only once the generation has a snapshot under the
// authorization it read, and every such request serves from the one
// snapshot. A new authorization indexes the cached export again, and
// reads no export.json.
//
// A request whose generation has no snapshot yet waits for a load. A load
// runs in a worker of its own, which opens the active generation, holds
// it while it reads and indexes the export, and then gives it back. At
// most MaxLoads workers run at once. The requests that wait for a
// generation share one load, so each export is read once. A waiting
// request holds no generation, and stops waiting when its reader goes away
// or its time runs out. A load stops once no request waits for it, and
// the server keeps nothing of what it read.
//
// # Admission
//
// Every request's time is bounded from when it arrives: its waits, its
// git commands and its rendering. A source request runs git, and at most
// MaxGit run at once. A source request takes one of those slots before
// it opens a generation, and waits for one only while fewer than
// MaxWaiting wait. A request waits for a load only while fewer than
// MaxLoadWaiting wait, and it is admitted among them before it opens
// anything. A request the server cannot admit, or that runs out of time,
// answers 503.
//
// A source request holds its git slot for gitTimeout at most. That time
// runs from when it takes the slot, and covers its wait for its
// generation's export to be read and its git commands. It gives the slot
// back before it renders its page. A source request that runs out of
// that time answers 503.
//
// # Responses
//
// A response is rendered whole before it is written. A request gives back
// its git slot before it renders, and its generation before it writes, so
// a reader that reads slowly holds neither. It keeps its response until
// the response is written.
//
// A response's body is at most MaxResponse bytes. A page that would pass
// it stops rendering there, and the request answers with a short page
// that says the page is too large. Rendered responses take their memory
// from a budget of MaxBuffered bytes, which they give back once they are
// written. A request that finds the budget spent answers 503.
//
// Responses larger than SmallPage hold at most MaxLarge of the budget
// between them, counting their first SmallPage bytes. So readers that
// never read large pages hold at most MaxLarge, however many there are,
// and the rest of the budget is always there for small pages: the plan,
// most knots, and most directories and files.
//
// # Routes
//
// The server answers GET and HEAD alone. A project's pages are under its
// group's name and its own web name, which need not be its identifier:
//
//	/                                                 the plan page
//	/<group>/<project>/                               a redirect to the tree at main's tip
//	/<group>/<project>/knots/<id>                     one knot, with its body and comments
//	/<group>/<project>/source/                        a redirect to the tree at main's tip
//	/<group>/<project>/source/tree/<commit>/<path>    a directory and its README
//	/<group>/<project>/source/blob/<commit>/<path>    a file
//	/<group>/<project>/source/log/<commit>            the history that ends at a commit
//	/<group>/<project>/source/log/<commit>?from=…     an older page of that history
//	/<group>/<former name>/…                          a permanent redirect
//	/<alias>/…                                        a permanent redirect
//	/-/...                                            the server's own paths
//
// A group's alias redirects to the same path under the group's name, a
// project's former name to the same path under its web name, and a
// project's short alias to the same path under /<group>/<project>. The
// redirect keeps the rest of the path and the query as they were sent.
//
// Every path but / and those under /-/ starts with a group's name or an
// alias. No name starts with "-", so no group's path is ever one of the
// server's own, whatever paths the server adds there. It serves no page
// under /-/, and answers each path there with the one 404. Package export
// refuses an export whose group names and aliases collide, or whose
// projects' web names and former names collide within a group.
//
// Every source link names its group and project, a full commit ID and a
// path. A commit is served only when export.Reachable finds it reachable
// from the project's published source tip, and a path is resolved one
// tree entry at a time from that commit's tree, so no other object can be
// named.
//
// # Access
//
// The store's authorization decides what is served. A request reads it
// from the store when it arrives, and reads it again once its response is
// rendered, before the first byte is written. A response rendered under an
// authorization that has since been replaced is not written: the request
// renders again under the new one. So the next request after the receiver
// stores an authorization is held to it, with no restart and no cache in
// between. A store with no authorization, or one that cannot be read,
// serves no project: every page says nothing is published.
//
// The index a request serves from holds only the projects the
// authorization lets anyone read, under the identity the export gives
// them (visible). Every route, every reference between projects, every
// alias and every redirect is built from that index, so a project the
// authorization does not name answers exactly as a project that does not
// exist: every 404 is the same page, made once when the server starts.
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"the-valley/web/export"
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
	// MaxFrontier is the most commits a link to an older page of a log
	// names: the commits that page starts from.
	MaxFrontier = 64
	// MaxLeftToShow is the most commits a page of a log leaves to show.
	// Its Older link starts from MaxFrontier of them, and each of the
	// rest has a link of its own. A page ends before a merge that would
	// leave more.
	MaxLeftToShow = 192
	// MaxParents is the most parents of one commit that a page of a log
	// follows: the first MaxParents, in the commit's order. A page marks
	// a merge that has more, and leaves none of its others to show.
	MaxParents = 128
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
	// gitTimeout bounds how long a source request holds its git slot,
	// from when it takes it: its wait for its generation's export to be
	// read, and its git commands, all of them together. A source request
	// still holding its slot past it answers 503.
	gitTimeout = 5 * time.Second
	// MaxLoads is the most loads that run at once, each in a worker of
	// its own that reads one generation's export.
	MaxLoads = 2
	// MaxLoadWaiting is the most requests that wait for loads. A request
	// that would wait while MaxLoadWaiting do answers 503 at once.
	MaxLoadWaiting = 64
	// MaxResponse is the most bytes a response's body takes. A page
	// that would take more stops rendering, and the request answers with
	// a short page that says the page is too large.
	MaxResponse = 8 << 20
	// MaxBuffered is the most bytes of rendered responses the server
	// holds at once, from when each starts rendering until it is
	// written. A response takes them in pieces of pieceSize as it
	// renders.
	MaxBuffered = 64 << 20
	// SmallPage is the largest response that takes from MaxBuffered
	// alone.
	SmallPage = 256 << 10
	// MaxLarge is the most of MaxBuffered that responses larger than
	// SmallPage hold between them, all of each counted. The rest,
	// MaxBuffered less MaxLarge, is left to smaller responses.
	MaxLarge = 48 << 20
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
	// timeout is requestTimeout, gitTime is gitTimeout, and maxResponse
	// is MaxResponse. A test shortens any of them.
	timeout     time.Duration
	gitTime     time.Duration
	maxResponse int
	// slots holds a token for each source request running git, and
	// waiting one for each source request waiting for a slot.
	slots   chan struct{}
	waiting chan struct{}
	// loaders holds a token for each load's worker, and loadWaiting one
	// for each request waiting for a load.
	loaders     chan struct{}
	loadWaiting chan struct{}
	// pieces guards the response budget. held counts the pieces of
	// responses being rendered or written, of MaxBuffered bytes in pieces
	// of pieceSize. large counts the pieces of those responses that are
	// larger than SmallPage, every piece of each, of MaxLarge bytes.
	pieces sync.Mutex
	held   int
	large  int

	mu sync.Mutex
	// cached is the newest generation's snapshot read so far, under the
	// authorization a load read last.
	cached *snapshot
	// auth is the last valid authorization readAuthorization read.
	auth authState
	// loads are the loads a request that waits may still join: those that
	// have not ended, and that some request waits for.
	loads map[*load]struct{}

	// The test hooks. opened, when set, is called with each generation a
	// request opens, before the request reads anything from it. admitted
	// is called when a source request takes a git slot, before it opens
	// a generation. loading is called by a load's worker with the
	// generation it opened, before it reads the generation's export.
	// rendering is called with a page's name before the page renders.
	opened    func(export.Generation)
	admitted  func()
	loading   func(export.Generation)
	rendering func(name string)
	// checked is called once a request has rendered its response, before
	// it reads the authorization again.
	checked func()
}

// New is a Server over c's store.
func New(c Config) *Server {
	s := &Server{store: c.Store, now: c.Now, log: c.Log,
		timeout: requestTimeout, gitTime: gitTimeout, maxResponse: MaxResponse,
		slots: make(chan struct{}, MaxGit), waiting: make(chan struct{}, MaxWaiting),
		loaders: make(chan struct{}, MaxLoads), loadWaiting: make(chan struct{}, MaxLoadWaiting),
		loads: map[*load]struct{}{}}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = func(err error) { log.Print(err) }
	}
	return s
}

// snapshot is one generation's export, decoded, and its index under one
// authorization, for anyone. A generation's export.json never changes once
// it is active, and its sequence names it, and auth is the stored
// authorization's signed note the index was built under, so a snapshot
// serves every request that opens its generation under that authorization.
type snapshot struct {
	seq  int64
	auth authState
	e    *export.Export
	idx  *index
}

// authState is the stored authorization as one read found it: the signed
// note, and the authorization parsed from it, or the error that left none.
// Two reads are of one revision when their notes are the same bytes.
type authState struct {
	signed []byte
	a      *export.Authorization
	err    error
}

// same reports whether two reads found one revision of the authorization.
func (st authState) same(o authState) bool {
	return bytes.Equal(st.signed, o.signed) && (st.a == nil) == (o.a == nil)
}

// readAuthorization reads the stored authorization. It reads the file on
// every call, and parses it again only when its bytes changed since the
// last valid one it read.
func (s *Server) readAuthorization() authState {
	signed, err := s.store.ReadAuthorization()
	if err != nil {
		return authState{err: err}
	}
	s.mu.Lock()
	last := s.auth
	s.mu.Unlock()
	if last.a != nil && bytes.Equal(last.signed, signed) {
		return last
	}
	a, err := export.ReadStoredAuthorization(signed)
	if err != nil {
		return authState{signed: signed, err: err}
	}
	st := authState{signed: signed, a: a}
	s.mu.Lock()
	s.auth = st
	s.mu.Unlock()
	return st
}

// load reads the active generation's export for every request that
// waits for it. It starts once one of those requests takes a loader slot
// for it, and runs in a worker of its own, so a request can stop waiting
// while the load goes on for the others.
type load struct {
	// wants counts the requests that wait for the load. cancel is set once
	// the load has started, and stops its worker. The worker sets opened
	// and seq once it has opened a generation. All are guarded by the
	// server's mu.
	wants  int
	cancel context.CancelFunc
	opened bool
	seq    int64
	auth   authState
	// started is closed once the load starts, and done once its worker
	// ends. err is the worker's error, set before done is closed.
	started chan struct{}
	done    chan struct{}
	err     error
}

// request is one request, the generation it serves from, and the
// response it renders.
type request struct {
	s *Server
	w http.ResponseWriter
	r *http.Request
	// ctx is the context the request waits and renders under. It ends
	// at the request's deadline, or when its reader goes away. git is
	// the context a source request holds its git slot under: it waits
	// for its generation's export and runs git under it. git ends
	// gitTime after the request takes its slot, or with ctx. slot gives
	// the slot back.
	ctx  context.Context
	git  context.Context
	slot func()
	g    export.Generation
	e    *export.Export
	idx  *index
	now  time.Time
	// auth is the stored authorization as the request read it when it
	// arrived. The request serves what it allows, and its response is
	// written only if the authorization is the same when it is.
	auth authState

	// The response: a status and a page, or a redirect. It is written
	// once the request has given back its generation and git slot, and
	// the page's pieces are given back to the budget once it is written.
	status    int
	ctype     string
	body      *body
	location  string
	permanent bool
}

// ServeHTTP reads the stored authorization, renders the response under
// it, and reads it again before the first byte is written. A response the
// authorization changed under is not written: the request is rendered
// again under the new one, at most maxRenders times in all, and then
// answers 503. So every response, a redirect and an error page too, is
// one the authorization stored when it is written allows.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	for render := 1; ; render++ {
		h := w.Header()
		for k := range h {
			delete(h, k)
		}
		setHeaders(h)
		q := &request{s: s, w: w, r: r, ctx: ctx, now: s.now(), auth: s.readAuthorization()}
		q.serve()
		if s.checked != nil {
			s.checked()
		}
		if s.readAuthorization().same(q.auth) {
			q.write()
			q.drop()
			return
		}
		q.drop()
		if render == maxRenders {
			q.busy()
			q.write()
			return
		}
	}
}

// maxRenders is how many times a request is rendered while the stored
// authorization keeps changing under it.
const maxRenders = 3

// serve renders the request's response. A source request is admitted
// first, and holds its git slot until it renders, or until serve returns
// when it renders nothing. Every request holds its generation until serve
// returns.
func (q *request) serve() {
	if q.r.Method != http.MethodGet && q.r.Method != http.MethodHead {
		q.w.Header().Set("Allow", "GET, HEAD")
		q.page(http.StatusMethodNotAllowed, "error", "Method not allowed", errorPage{
			Heading: "Method not allowed", Text: "This server answers GET and HEAD alone.",
		})
		return
	}
	if q.auth.a == nil {
		if !errors.Is(q.auth.err, export.ErrNoAuthorization) {
			q.s.log(fmt.Errorf("the stored authorization cannot be read, so nothing is served: %w", q.auth.err))
		}
		q.page(http.StatusServiceUnavailable, "error", "Nothing published", errorPage{
			Heading: "Nothing published yet", Text: "This server holds no authorization to serve anything.",
		})
		return
	}
	parts, ok := segments(q.r.URL.EscapedPath())
	wait := q.ctx
	if ok && runsGit(parts) {
		done, admitted := q.s.admit(q.ctx)
		if !admitted {
			q.busy()
			return
		}
		q.slot = done
		defer q.giveSlot()
		var cancel context.CancelFunc
		q.git, cancel = context.WithTimeout(q.ctx, q.s.gitTime)
		defer cancel()
		wait = q.git
		if q.s.admitted != nil {
			q.s.admitted()
		}
	}
	g, snap, release, err := q.s.open(wait, q.auth)
	defer release()
	switch {
	case errors.Is(err, export.ErrNoExport):
		q.page(http.StatusServiceUnavailable, "error", "Nothing published", errorPage{
			Heading: "Nothing published yet", Text: "No export has been activated on this server.",
		})
		return
	case errors.Is(err, errLoadsFull), errors.Is(err, errAuthorizationChanged):
		q.busy()
		return
	case err != nil:
		q.fail(err)
		return
	}
	if q.s.opened != nil {
		q.s.opened(g)
	}
	q.g, q.e, q.idx = g, snap.e, snap.idx
	q.route(parts, ok)
}

// runsGit reports whether a path is a source route that reads a
// repository: a tree, a file or a log of something that may be a group's
// project. It reads the path's shape alone, before the export is read, so
// a path through a group's alias that has the shape takes a git slot too,
// and gives it back when it redirects.
func runsGit(parts []string) bool {
	return len(parts) >= 4 && export.ValidName(parts[0]) && export.ValidName(parts[1]) && parts[2] == "source" &&
		(parts[3] == "tree" || parts[3] == "blob" || parts[3] == "log")
}

// giveSlot gives back the request's git slot, when it holds one.
func (q *request) giveSlot() {
	if q.slot != nil {
		q.slot()
		q.slot = nil
	}
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

// errLoadsFull is open's error for a request that would wait for a load
// while MaxLoadWaiting requests wait already.
var errLoadsFull = errors.New("too many requests wait for exports to be read")

// errAuthorizationChanged is open's error for a request whose
// authorization was replaced while it waited. ServeHTTP renders it again
// under the new one.
var errAuthorizationChanged = errors.New("the stored authorization changed")

// open opens the active generation once its snapshot under auth is
// cached, and returns both. release gives the generation back.
//
// A request whose active generation has no snapshot waits for a load,
// and opens nothing until the load ends. It is admitted among the
// MaxLoadWaiting requests that may wait before it waits, and it keeps its
// place until open returns. Once the load ends it looks again, since
// another generation may have been activated meanwhile.
func (s *Server) open(ctx context.Context, auth authState) (g export.Generation, snap *snapshot, release func(), err error) {
	release = func() {}
	waiting := false
	defer func() {
		if waiting {
			<-s.loadWaiting
		}
	}()
	for {
		seq, err := s.store.Active()
		if err != nil {
			return g, nil, release, err
		}
		s.mu.Lock()
		snap = s.cached
		s.mu.Unlock()
		if snap != nil && snap.seq == seq && snap.auth.same(auth) {
			g, release, err = s.store.Open()
			if err != nil || g.Sequence == seq {
				return g, snap, release, err
			}
			release() // active moved on since it was read
			release = func() {}
			continue
		}
		if !waiting {
			select {
			case s.loadWaiting <- struct{}{}:
				waiting = true
			default:
				return g, nil, release, errLoadsFull
			}
		}
		if err := s.await(ctx, seq, auth); err != nil {
			return g, nil, release, err
		}
		if !s.readAuthorization().same(auth) {
			return g, nil, release, errAuthorizationChanged
		}
	}
}

// await waits until a load of generation seq under auth, or of a newer
// generation, ends, or until ctx is done. It joins a load that will read
// one of those, since a worker reads whatever generation is active and
// whatever authorization is stored once it starts, and otherwise makes
// one.
//
// A load starts only once one of its requests takes a loader slot for
// it. While every slot is taken, each of its requests waits for one, and
// the first to take one starts the load, so no load is queued that no
// request waits for. A request that stops waiting leaves the load, and
// the last to leave stops it.
func (s *Server) await(ctx context.Context, seq int64, auth authState) error {
	s.mu.Lock()
	if c := s.cached; c != nil && (c.seq > seq || c.seq == seq && c.auth.same(auth)) {
		s.mu.Unlock()
		return nil
	}
	var l *load
	for m := range s.loads {
		if !m.opened || m.seq > seq || m.seq == seq && m.auth.same(auth) {
			l = m
			break
		}
	}
	if l == nil {
		l = &load{started: make(chan struct{}), done: make(chan struct{})}
		s.loads[l] = struct{}{}
	}
	l.wants++
	s.mu.Unlock()
	select {
	case <-l.started:
	case s.loaders <- struct{}{}:
		if !s.start(l) {
			<-s.loaders
		}
	case <-ctx.Done():
		s.leave(l)
		return fmt.Errorf("waiting for generation %d's export to be read: %w", seq, ctx.Err())
	}
	select {
	case <-l.done:
		return l.err
	case <-ctx.Done():
		s.leave(l)
		return fmt.Errorf("waiting for generation %d's export to be read: %w", seq, ctx.Err())
	}
}

// start starts l's worker, for the request that took a loader slot for
// it. It is false when another request started it already.
func (s *Server) start(l *load) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.cancel != nil {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	close(l.started)
	go s.run(ctx, l)
	return true
}

// leave is a request that stopped waiting for l. Once no request waits for
// it, no request may join it, and a load that started is stopped.
func (s *Server) leave(l *load) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.wants--; l.wants > 0 {
		return
	}
	delete(s.loads, l)
	if l.cancel != nil {
		l.cancel()
	}
}

// run is l's worker. It caches the snapshot it reads when the snapshot's
// generation is newer than the cached one's, or the same generation under
// another authorization, so a load of an older generation never displaces
// a newer. A load that was stopped caches nothing: leave stops it under
// mu, and run looks under mu. The worker gives back its generation before
// it ends, and then its loader slot.
func (s *Server) run(ctx context.Context, l *load) {
	snap, err := s.read(ctx, l)
	s.mu.Lock()
	delete(s.loads, l)
	if snap != nil && ctx.Err() == nil && (s.cached == nil || s.cached.seq < snap.seq ||
		s.cached.seq == snap.seq && !s.cached.auth.same(snap.auth)) {
		s.cached = snap
	}
	l.err = err
	s.mu.Unlock()
	l.cancel()
	close(l.done)
	<-s.loaders
}

// read opens the active generation for l, reads the stored
// authorization, and holds the generation while it reads and indexes the
// generation's export under that authorization. It reads nothing when the
// generation is cached already under that authorization or another load
// reads the same, and its waiting requests look again. A generation cached
// under another authorization is not read again: its export is indexed
// anew. It stops reading once ctx is done.
func (s *Server) read(ctx context.Context, l *load) (snap *snapshot, err error) {
	// Cancellation can close the file during any operation. Report the
	// context error instead of the resulting descriptor error.
	defer func() {
		if stopped := ctx.Err(); stopped != nil {
			snap, err = nil, stopped
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, release, err := s.store.Open()
	if err != nil {
		return nil, err
	}
	defer release()
	auth := s.readAuthorization()
	if auth.a == nil {
		return nil, nil
	}
	s.mu.Lock()
	cached := s.cached
	read := cached == nil || cached.seq < g.Sequence || cached.seq == g.Sequence && !cached.auth.same(auth)
	for m := range s.loads {
		if m != l && m.opened && m.seq == g.Sequence && m.auth.same(auth) {
			read = false
		}
	}
	l.opened, l.seq, l.auth = true, g.Sequence, auth
	s.mu.Unlock()
	if !read {
		return nil, nil
	}
	if cached != nil && cached.seq == g.Sequence {
		idx, err := newIndex(ctx, cached.e, visible(cached.e, auth.a, export.Anyone))
		if err != nil {
			return nil, err
		}
		return &snapshot{seq: g.Sequence, auth: auth, e: cached.e, idx: idx}, nil
	}
	if s.loading != nil {
		s.loading(g)
	}
	f, err := os.OpenFile(g.ExportPath(), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Nonblocking open avoids waiting for a FIFO writer. Go's file poller
	// waits for reads, and Close wakes a read when the last waiter leaves.
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(&contextReader{ctx: ctx, r: f, waitForData: info.Mode()&os.ModeNamedPipe != 0})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("reading generation %d's export: %w", g.Sequence, err)
	}
	e, err := export.DecodeContext(ctx, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idx, err := newIndex(ctx, e, visible(e, auth.a, export.Anyone))
	if err != nil {
		return nil, err
	}
	return &snapshot{seq: g.Sequence, auth: auth, e: e, idx: idx}, nil
}

// contextReader reads from r until ctx is done.
type contextReader struct {
	ctx         context.Context
	r           io.Reader
	waitForData bool
}

func (c *contextReader) Read(p []byte) (int, error) {
	for {
		if err := c.ctx.Err(); err != nil {
			return 0, err
		}
		n, err := c.r.Read(p)
		if n > 0 {
			c.waitForData = false
		}
		if !c.waitForData || err != io.EOF {
			return n, err
		}
		// A nonblocking FIFO reports EOF before its first writer arrives.
		// Wait for that writer without holding a blocking open syscall.
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return 0, c.ctx.Err()
		case <-timer.C:
		}
	}
}

// route dispatches on the path's segments, each unescaped on its own, so
// an escaped slash stays inside its segment. ok is false for a path whose
// segments do not unescape. The first segment is a group's name or an
// alias, and under a group's name the second is a project's name.
func (q *request) route(parts []string, ok bool) {
	switch {
	case !ok:
		q.notFound()
	case len(parts) == 1 && parts[0] == "":
		q.plan()
	case parts[0] == own:
		q.notFound()
	default:
		if to, ok := q.idx.aliases[parts[0]]; ok {
			q.moved(to, 1)
			return
		}
		if len(parts) >= 2 {
			if p := q.idx.groups[parts[0]][parts[1]]; p != nil {
				q.projectRoute(p.Name, parts[2:])
				return
			}
			if to, ok := q.idx.formers[parts[0]][parts[1]]; ok {
				q.moved(to, 2)
				return
			}
		}
		q.notFound()
	}
}

// projectRoute dispatches on the segments of a path after its group's and
// its project's names.
func (q *request) projectRoute(project string, rest []string) {
	switch {
	case len(rest) == 0, len(rest) == 1 && rest[0] == "":
		q.home(project)
	case len(rest) == 2 && rest[0] == "knots":
		q.knot(project, rest[1])
	case len(rest) >= 1 && rest[0] == "source":
		q.source(project, rest[1:])
	default:
		q.notFound()
	}
}

// moved answers a path under an alias or a former name with a permanent
// redirect to the same path under to, the escaped URL the path's first n
// segments stand for. The rest of the path after them, and the query,
// are kept as they were sent, even a query that is a bare "?".
func (q *request) moved(to string, n int) {
	esc := strings.Split(strings.TrimPrefix(q.r.URL.EscapedPath(), "/"), "/")
	rest := ""
	if len(esc) > n {
		rest = "/" + strings.Join(esc[n:], "/")
	}
	if q.r.URL.RawQuery != "" || q.r.URL.ForceQuery {
		rest += "?" + q.r.URL.RawQuery
	}
	q.location, q.permanent = to+rest, true
}

// own is the first segment of the server's own paths, /-/. No group's
// name, and no alias, can be it, or start with it.
const own = "-"

// home answers /<group>/<project>/, and /<group>/<project>, with a
// redirect to the project's page: the tree at its published tip, with its
// README.
func (q *request) home(project string) {
	p, ok := q.project(project)
	if !ok {
		q.notFound()
		return
	}
	q.redirect(treeURL(q.idx.bases[p.Name], p.SourceTip, nil))
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
// as public, in a group. Every route that names a project asks here
// first, and a project it refuses answers as one that does not exist.
func (q *request) project(name string) (*export.Project, bool) { return q.idx.project(name) }

func (idx *index) project(name string) (*export.Project, bool) {
	p, ok := idx.projects[name]
	if _, placed := idx.bases[name]; !ok || !placed {
		return nil, false
	}
	return p, true
}

// notFound is the one 404 page, whatever was not found. It is made once,
// when the server starts, and names nothing of the request or the export,
// so a hidden project, a missing one, an unreachable commit and a missing
// path all answer alike, byte for byte.
func (q *request) notFound() {
	q.static(http.StatusNotFound, htmlType, notFoundPage)
}

// fail logs err and answers with the one 500 page, which says nothing of
// the failure. A reader that went away is answered with nothing: git
// killed for it fails, and that is no failure of the server's. A request
// that ran out of time, or held its git slot past gitTime, answers 503.
func (q *request) fail(err error) {
	if q.r.Context().Err() != nil {
		return
	}
	if q.ctx.Err() == nil && q.git != nil && q.git.Err() != nil {
		err = fmt.Errorf("the request held its git slot past %v: %w", q.s.gitTime, err)
	}
	q.s.log(err)
	if q.ctx.Err() != nil || q.git != nil && q.git.Err() != nil {
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
// failed or timed out. A permanent redirect names its location as it was
// made, with no cleaning, so the path it redirects to is the one the
// reader sent under another name.
func (q *request) write() {
	switch {
	case q.location != "" && q.permanent:
		q.w.Header().Set("Location", q.location)
		q.w.WriteHeader(http.StatusPermanentRedirect)
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

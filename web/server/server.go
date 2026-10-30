// Package server is the web UI's read side. It serves the active export
// of a receiver's store to each reader as the store's authorization
// allows: the knots page, each knot, and a source browser over the main of
// each project the reader may read. The source browser also serves a
// gzipped tar archive of each reachable commit.
//
// # Readers and views
//
// Every request is read as one principal, its reader. Started with
// --origin, valley-web reads a request that carries a valid session as
// the session's principal, and every other request as anyone. A request
// that carries a session cookie while the browser state cannot be read
// is answered as unavailable. A request's view is built from the projects
// its reader may see: those the authorization lets anyone read, and those
// it lets the reader read (visible). Each of those projects is copied for
// the reader, with what it says of other projects' knots taken out
// (projectForReader), and the view's index is built from the copies. So
// every route, every reference between projects, every alias and every
// redirect a reader is served is built from that reader's projects alone.
//
// # Requests, loads and indexes
//
// Every request reads the store's authorization when it arrives, and
// renders its whole response under it. A request takes, in this order:
//
//  1. its reader and its authorization;
//  2. the active generation's export, decoded, and the index of its view;
//  3. its route, resolved through that index;
//  4. a missing page, or a redirect, answered from the index alone;
//  5. for a source route that reads a repository, a git slot, and then
//     the generation its view was built from, opened;
//  6. its whole response, rendered;
//  7. the generation and the index given back;
//  8. the authorization read again, and the response's headers written.
//
// It holds no generation while it waits for an index or a git slot. When
// the active generation is another by the time it opens one, it gives
// back its slot, its generation and its index, and takes its view again,
// within its time. An activation during the request changes nothing the
// request reads.
//
// The server keeps the newest generation's export it has read, decoded.
// A request whose generation has no decoded export yet waits for a load.
// A load runs in a worker of its own, which opens the active generation,
// holds it while it reads and decodes the export, and then gives it back.
// At most MaxLoads loads run at once. One of their workers runs a load
// only while an anonymous request waits for it, so loads that only named
// readers wait for take one worker at most. When the last anonymous
// request leaves a load on that worker, the load moves to the other
// worker if it is free. Otherwise the load is stopped and gives the worker
// back at once, and its named readers' requests wait for a worker again.
// The requests that wait for a generation share one read. A waiting
// request holds no generation, and stops waiting when its reader goes
// away or its time runs out. A load stops once no request waits for it,
// and the server keeps nothing of what it read, so a later request may
// read that generation again, as it may after a load stopped as above.
//
// Indexes are kept apart from decoded exports. An index is named by its
// generation, the authorization's revision, and the sorted names and
// identities of its view's projects, so readers who may see the same
// projects share one index, and who they are never reaches it. An index
// is built once its export is decoded, after the load has given back its
// worker, in a worker of its own that stops once no request waits for it.
//
// Indexes take places of two pools. Anonymous requests have anonymousViews
// places of their own, for the index of what anyone may see: the current
// generation's, and the one that replaces or is replaced by it. Named
// readers' requests share MaxViews places. A named reader who may see no
// more than anyone serves from the same index as anonymous requests, but
// takes a named place for it. So an index takes a place in the pool of
// each kind of request that serves from it or waits for it to be built,
// and gives back a pool's place once only the other pool's requests pin
// it. An index no request pins keeps the place of the last request that
// let it go. An index being built takes its places too. No named
// reader's request ever keeps an anonymous place: when the named places
// are full, named readers' requests wait for one, and an anonymous request
// for the current generation never waits on a named one for its index.
//
// An index whose build was stopped keeps its place until its worker ends,
// and no request joins it, so the workers that build indexes are never
// more than the places. An index no request serves from may be dropped to
// make room in its pool, and no other. A request that finds no room in
// its pool waits for some, and answers 503 at its deadline. No worker
// waits for an index while it holds a load worker, and no load waits for
// an index.
//
// # Admission
//
// Every request's time is bounded from when it arrives: its waits, its
// git commands and its rendering. A source request that reads a
// repository runs git, and at most MaxGit run at once. A request takes one
// of those slots once its route is resolved through its index, before it
// opens a generation, and waits for one only while fewer than MaxWaiting
// wait. So a project the reader may not see, a project that does not
// exist, a redirect and a missing page take no slot. A request waits for
// its view, a load or an index, only while fewer than MaxLoadWaiting wait.
// A named reader's request waits only while fewer than MaxNamedWaiting
// named readers' requests wait too. So named readers never take the last
// MaxLoadWaiting less MaxNamedWaiting places: those are left to anonymous
// requests, which wait there for a loader and for the anonymous places of
// indexes, which named readers never take either. A request the server
// cannot admit, or that runs out of time, answers 503.
//
// A source request holds its git slot for gitTimeout at most. That time
// runs from when it takes the slot, and covers its git commands. It gives
// the slot back before it renders its page. A source request that runs
// out of that time answers 503.
//
// A source request that shows Markdown renders it after its git commands,
// and at most MaxRendering render at once. A request waits for a
// rendering slot while it still holds its git slot, within its gitTimeout,
// and gives the git slot back once it has one. So the requests that hold
// a file's text while they wait to render are among the MaxGit. The
// memory rendering takes grows with the file and not with the page, and
// one render allocates less than 128 MiB (package markdown), so the
// renders at once allocate at most MaxRendering times that.
//
// Every bound but the indexes' places is shared by every reader: MaxGit,
// MaxRendering, MaxBuffered, MaxLarge and MaxResponse, and the bounds on
// an archive. More readers do not make any of them larger.
//
// # Responses
//
// A response is rendered whole before it is written. A request gives back
// its git slot before it renders, and its generation and its index before
// it writes, so a reader that reads slowly holds none of them. It keeps
// its response until the response is written.
//
// A response's body is at most MaxResponse bytes. A page or archive that
// exceeds MaxResponse stops there, and the request answers with a short
// page saying the page is too large. The server builds an archive in full
// into the response body while holding the request's git slot. It writes
// the tar itself from the commit's tree, applying no git attribute, and
// streams each file through tar and gzip into the body, so it never holds
// a whole file. An archive whose files add up to more than
// MaxArchiveInput, or whose tree holds more than MaxArchiveEntries
// entries, is refused with the same short page before any file is read.
// Rendered
// responses take their memory from a budget of MaxBuffered bytes, which
// they give back once they are written. A request that finds the budget
// spent answers 503.
//
// Responses larger than SmallPage hold at most MaxLarge of the budget
// between them, counting their first SmallPage bytes. So readers that
// never read large pages hold at most MaxLarge, however many there are,
// and the rest of the budget is always there for small pages: the knots
// page, most knots, and most directories and files.
//
// # Routes
//
// The server answers GET and HEAD alone. A project's pages are under its
// group's name and its own web name, which need not be its identifier:
//
//	/                                                   a redirect to the first project's tree at main's tip
//	/-/knots                                            the knots page
//	/<group>/<project>/                                 a redirect to the tree at main's tip
//	/<group>/<project>/knots/<id>                       one knot, with its body and comments
//	/<group>/<project>/source/                          a redirect to the tree at main's tip
//	/<group>/<project>/source/tree/<commit>/<path>      a directory and its README
//	/<group>/<project>/source/blob/<commit>/<path>      a file
//	/<group>/<project>/source/log/<commit>              the history that ends at a commit
//	/<group>/<project>/source/log/<commit>?from=…       an older page of that history
//	/<group>/<project>/source/archive/<commit>.tar.gz   the commit's tree as a gzipped tar archive
//	/<group>/<project>/source/archive/main.tar.gz       a redirect to the archive of main's tip
//	/<group>/<former name>/…                            a permanent redirect
//	/<alias>/…                                          a permanent redirect
//	/-/...                                              the server's own paths
//
// A group's alias redirects to the same path under the group's name, a
// project's former name to the same path under its web name, and a
// project's short alias to the same path under /<group>/<project>. The
// redirect keeps the rest of the path and the query as they were sent.
//
// Every path but / and those under /-/ starts with a group's name or an
// alias. No name starts with "-", so no group's path is ever one of the
// server's own, whatever paths the server adds there. Under /-/ it serves
// the knots page alone, and answers every other path there with the one
// 404. / redirects to the tree at main's published tip of the first
// project the reader may see, in the export's order, and to the knots
// page when the reader may see none. Package export
// refuses an export whose group names and aliases collide, or whose
// projects' web names and former names collide within a group.
//
// Every source link names its group and project, a full commit ID, and a
// path, or a full commit ID alone for an archive. A commit is served only
// when export.Reachable finds it reachable from the project's published
// source tip, and a path is resolved one tree entry at a time from that
// commit's tree, so no other object can be named.
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
// A project the reader may not see answers exactly as a project that does
// not exist: every 404 is the same page, made once when the server
// starts, and a group none of whose projects the reader may see has no
// alias.
//
// # Safety
//
// Every page is rendered on the server with html/template, so everything
// read from the export or a repository is escaped. Each response carries
// its route's strict Content-Security-Policy: default-src 'self', the one
// inline stylesheet admitted by its SHA-256 hash, no connection from a
// script, and, on the knots page alone, its one inline script admitted by
// its hash. Every response, a redirect and an error too, is private and
// never stored by a cache: Cache-Control: private, no-store, and Vary:
// Cookie. No response carries an ETag, and none answers 304. No page
// serves a repository file as raw content: a file is shown as escaped
// text, and a binary file or one larger than MaxFile is not shown at all.
// Raw repository files are served only inside a gzipped tar archive sent
// as an attachment.
//
// A Markdown file, and a directory's Markdown README, is rendered by
// package markdown, and ?plain=1 shows the file as escaped text. The
// renderer writes only its own fixed set of elements and attributes, and
// escapes every byte it takes from the file, raw HTML included. It
// writes a link only for an http, https or mailto destination, or for a
// relative one resolved to a page of the source browser at the same
// commit, and no img, script, form or style attribute. So rendered
// Markdown is no active content, and the policy above admits it as it
// is. What a project's own files and knots say is published as written:
// the filter takes out what a page says of other projects, never what
// their authors wrote.
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
	"the-valley/web/readerstate"
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
	// MaxArchiveInput is the most bytes of files an archive holds before
	// it is gzipped: the sizes of the files and symbolic links in its
	// commit's tree, added up. An archive of a larger tree is refused
	// before any file is read.
	MaxArchiveInput = 32 << 20
	// MaxArchiveEntries is the most entries an archive holds: its
	// files, symbolic links, directories and submodules together. An
	// archive of a tree that holds more is refused as one that passes
	// MaxArchiveInput is.
	MaxArchiveEntries = 10_000
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
	// MaxRendering is the most source requests that render Markdown at
	// once.
	MaxRendering = 2
	// requestTimeout bounds a request from when it arrives: its wait for
	// its view, for a git slot or for a rendering slot, its git commands
	// and its rendering.
	requestTimeout = 15 * time.Second
	// gitTimeout bounds how long a source request holds its git slot,
	// from when it takes it: its git commands, all of them together, and
	// its wait for a rendering slot. A source request still holding its
	// slot past it answers 503.
	gitTimeout = 5 * time.Second
	// MaxLoads is the most loads that run at once, each in a worker of
	// its own that reads one generation's export. One of them runs only
	// while an anonymous request waits for it.
	MaxLoads = 2
	// MaxLoadWaiting is the most requests that wait for their views: for
	// a load, or for an index to be built or to have room. A request that
	// would wait while MaxLoadWaiting do answers 503 at once.
	MaxLoadWaiting = 64
	// MaxNamedWaiting is the most of the MaxLoadWaiting places that
	// requests read as a named reader take. The rest, MaxLoadWaiting less
	// MaxNamedWaiting, are left to anonymous requests. A named reader's
	// request that would wait while MaxNamedWaiting named readers'
	// requests do answers 503 at once.
	MaxNamedWaiting = 48
	// MaxViews is how many places the server keeps for the indexes named
	// readers' requests serve from or wait for: those of views of more
	// than anyone may see, and that of what anyone may see while a named
	// reader who may see no more pins it. Those it keeps, those being
	// built, and those requests serve from count together.
	MaxViews = 8
	// anonymousViews is how many places the server keeps for the indexes
	// anonymous requests serve from or wait for: the current index of
	// what anyone may see, and the one that replaces or is replaced by it.
	// No named reader's request takes one.
	anonymousViews = 2
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
	// Readers is the browser state, which holds the read links redeemed
	// here and their sessions, and Origin the public HTTPS origin, such
	// as https://example.org, that a reader page's POST must come from.
	// With no Readers, every request is read as anyone, and the server
	// has no reader pages.
	Readers *readerstate.State
	Origin  string
}

// Server is the read side's HTTP handler.
type Server struct {
	store   *export.Store
	now     func() time.Time
	log     func(error)
	readers *readerstate.State
	origin  string
	// timeout is requestTimeout, gitTime is gitTimeout, and maxResponse
	// is MaxResponse. A test shortens any of them.
	timeout     time.Duration
	gitTime     time.Duration
	maxResponse int
	// gitMu guards the git slots. running counts the source requests
	// that hold a slot, and queue holds a channel for each that waits for
	// one, the longest waiting first. A slot given back while requests
	// wait passes to the first of them: its channel is closed, and it
	// leaves queue then, so queue holds only requests that have no slot.
	gitMu   sync.Mutex
	running int
	queue   []chan struct{}
	// renders holds a token for each source request rendering
	// Markdown.
	renders chan struct{}
	// anonLoader holds a token for the load an anonymous request started
	// on it, and loaders one for each other load. A request that is not
	// anonymous starts a load only with a token of loaders, and a load
	// that only named readers' requests still wait for gives anonLoader
	// back (leave), so anonymous requests always have a worker. loadWaiting
	// holds one for each request waiting for its view, and namedWaiting
	// one more for each of those read as a named reader.
	anonLoader   chan struct{}
	loaders      chan struct{}
	loadWaiting  chan struct{}
	namedWaiting chan struct{}
	// pieces guards the response budget. held counts the pieces of
	// responses being rendered or written, of MaxBuffered bytes in pieces
	// of pieceSize. large counts the pieces of those responses that are
	// larger than SmallPage, every piece of each, of MaxLarge bytes.
	pieces sync.Mutex
	held   int
	large  int

	mu sync.Mutex
	// exported is the newest generation's export decoded so far.
	exported *decoded
	// auth is the last valid authorization readAuthorization read.
	auth authState
	// loads are the loads a request that waits may still join: those that
	// have not ended, that some request waits for, and that leave has not
	// dropped.
	loads map[*load]struct{}
	// views are the indexes the server holds, by name: built, being
	// built, or pinned by the requests that serve from them. stopping are
	// the views whose builds were stopped and whose workers have not
	// ended: no request takes them, and each keeps its place until its
	// worker ends. freed is closed, and replaced, whenever a view may have
	// left a place free. used counts the views requests have taken, and
	// orders the views by when each was last taken.
	views    map[viewKey]*view
	stopping map[*view]struct{}
	freed    chan struct{}
	used     uint64

	// reader, when set, names the principal each request is read as, in
	// place of its session. Tests name readers with it.
	reader func(*http.Request) string

	// The test hooks. viewed is called once a request has its view,
	// before it resolves its route. admitted is called when a source
	// request takes a git slot, before it opens a generation. opened,
	// when set, is called with each generation a request opens, before
	// the request reads anything from it. loading is called by a load's
	// worker with the generation it opened, before it reads the
	// generation's export. indexing is called by a view's worker before
	// it builds the view's index, with the worker's context and the
	// view's name. rendering is called with a page's name before the page
	// renders.
	viewed    func()
	admitted  func()
	opened    func(export.Generation)
	loading   func(export.Generation)
	indexing  func(ctx context.Context, key viewKey)
	rendering func(name string)
	// checked is called once a request has rendered its response, before
	// it reads the authorization again. starting is called once that read
	// found the authorization unchanged, with the serving lock held, before
	// the response's headers are written.
	checked  func()
	starting func()
}

// New is a Server over c's store.
func New(c Config) *Server {
	s := &Server{store: c.Store, now: c.Now, log: c.Log, readers: c.Readers, origin: c.Origin,
		timeout: requestTimeout, gitTime: gitTimeout, maxResponse: MaxResponse,
		renders:    make(chan struct{}, MaxRendering),
		anonLoader: make(chan struct{}, 1), loaders: make(chan struct{}, MaxLoads-1),
		loadWaiting: make(chan struct{}, MaxLoadWaiting), namedWaiting: make(chan struct{}, MaxNamedWaiting),
		loads: map[*load]struct{}{}, views: map[viewKey]*view{}, stopping: map[*view]struct{}{},
		freed: make(chan struct{})}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = func(err error) { log.Print(err) }
	}
	return s
}

// decoded is one generation's export, decoded. A generation's export.json
// never changes once it is active, and its sequence names it, so one
// decoded export serves every request that opens its generation, under
// any authorization and for any reader.
type decoded struct {
	seq int64
	e   *export.Export
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
	// wants counts the requests that wait for the load, and anon those of
	// them that are anonymous. cancel is set once the load has started,
	// and stops its worker. token is the slot the load holds, anonLoader
	// or loaders, from when it starts until its worker ends or leave gives
	// the slot back. The worker sets opened and seq once it has opened a
	// generation. All are guarded by the server's mu.
	wants  int
	anon   int
	cancel context.CancelFunc
	token  chan struct{}
	opened bool
	seq    int64
	// started is closed once the load starts, and done once its worker
	// ends. err is the worker's error, set before done is closed. dropped
	// is closed when leave stops the load to give back anonLoader while
	// named readers' requests still wait for it: they wait for another.
	started chan struct{}
	done    chan struct{}
	dropped chan struct{}
	err     error
}

// viewKey names one index: the generation whose export it is built from,
// the authorization's revision it is built under, as the signed note's
// bytes, and its view's projects, each name and identity on a line of its
// own, in the export's order, which is the names' order. Who the reader is
// is no part of it.
type viewKey struct {
	seq      int64
	auth     string
	projects string
}

// pool is the places views take for one kind of request: anonymous
// requests', or named readers'.
type pool int

const (
	anonymousPool pool = iota
	namedPool
	pools
)

// poolOf is the pool whose places the requests read as reader take.
func poolOf(reader string) pool {
	if reader == export.Anyone {
		return anonymousPool
	}
	return namedPool
}

// size is how many places p holds: anonymousViews for anonymous requests,
// and MaxViews for named readers'.
func (p pool) size() int {
	if p == anonymousPool {
		return anonymousViews
	}
	return MaxViews
}

// view is one index the server holds, the requests that hold it, and the
// places it takes.
type view struct {
	key viewKey
	// e is the decoded export the index is built from, and shown its
	// view's projects.
	e     *export.Export
	shown map[string]bool
	// pins counts the requests that serve from the view or wait for it to
	// be built, by the pool of each, and holds is set for each pool whose
	// place the view takes. A view that requests pin takes a place in the
	// pool of each kind of request that pins it, and in no other. A view
	// no request pins keeps the one place of the last request that let it
	// go. used is when a request last took it, and cancel stops the worker
	// that builds it. All are guarded by the server's mu.
	pins   [pools]int
	holds  [pools]bool
	used   uint64
	cancel context.CancelFunc
	// done is closed once the worker ends, and idx is the index then, or
	// err why there is none.
	done chan struct{}
	idx  *index
	err  error
}

// pinned reports whether a request pins v. It is called with the
// server's mu held.
func (v *view) pinned() bool { return v.pins[anonymousPool]+v.pins[namedPool] > 0 }

// request is one request, the view it serves from, and the response it
// renders.
type request struct {
	s *Server
	w http.ResponseWriter
	r *http.Request
	// parts are the request path's segments, each unescaped, and ok is
	// false for a path whose segments do not unescape.
	parts []string
	ok    bool
	// reader is the principal the request is read as, and sess the
	// session it carries, which names it. rpage is the reader page the
	// path names, or "".
	reader string
	sess   session
	rpage  string
	// ctx is the context the request waits and renders under. It ends
	// at the request's deadline, or when its reader goes away. git is
	// the context a source request holds its git slot under: it runs git
	// under it. git ends gitTime after the request takes its slot, or
	// with ctx. slot gives the slot back.
	ctx  context.Context
	git  context.Context
	slot func()
	// view is the view the request serves from, pinned until it renders,
	// and e and idx are its export and index.
	view *view
	e    *export.Export
	idx  *index
	now  time.Time
	// auth is the stored authorization as the request read it when it
	// arrived. The request serves what it allows, and its response is
	// written only if the authorization is the same when it is.
	auth authState

	// The response: a status and a page, or a redirect. It is written
	// once the request has given back its generation, its view and its
	// git slot, and the page's pieces are given back to the budget once
	// it is written. disposition is the Content-Disposition of an
	// archive, and "" for any other response.
	status      int
	ctype       string
	disposition string
	body        *body
	location    string
	permanent   bool
}

// ServeHTTP answers a POST to a reader page once, with no rendering
// (post). Every other request it reads under the stored authorization and
// the session the request carries, renders its response, and reads both
// again before the first byte is written. A response the authorization or
// the session changed under is not written: the request is rendered again
// under the new ones, at most maxRenders times in all, and then answers
// 503. So every response, a redirect and an error page too, is one the
// authorization stored when it is written allows its reader. A response
// whose reader has a valid session renews the session's cookie, only once
// that last check found it valid.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	parts, ok := segments(r.URL.EscapedPath())
	rpage := s.readerPageOf(parts, ok)
	policy := policyFor(parts, ok)
	if rpage != "" {
		policy = readerPolicy
		if r.Method == http.MethodPost && posted(rpage) {
			setHeaders(w.Header(), policy)
			s.post(ctx, w, r, rpage)
			return
		}
	}
	carried, token := carriedCookie(r)
	for render := 1; ; render++ {
		h := w.Header()
		for k := range h {
			delete(h, k)
		}
		setHeaders(h, policy)
		q := &request{s: s, w: w, r: r, parts: parts, ok: ok, rpage: rpage, ctx: ctx, now: s.now(),
			auth: s.readAuthorization()}
		s.prepare(ctx, q.auth)
		q.sess = s.resolve(ctx, carried, token, q.auth.a)
		q.reader = q.sess.reader
		if s.reader != nil {
			q.reader = s.reader(r)
		}
		q.serve()
		if s.checked != nil {
			s.checked()
		}
		// The last read and the response's headers happen under the
		// browser state's read lock and the serving lock, shared, in that
		// order. The receiver renames a new authorization into place under
		// the serving lock, exclusively, and a logout replaces the browser
		// state under its write lock. So a response whose headers are
		// written after the receiver replies, or after a logout answers,
		// was checked against what they stored. The wait for either lock
		// ends with the request's deadline, or when its reader goes away,
		// and then nothing rendered is written: busy answers 503, or
		// nothing to a reader that went away, and no session is renewed.
		state, done, err := s.readState(ctx)
		var release func()
		if err == nil {
			if release, err = s.store.HoldServing(ctx); err != nil {
				done()
			}
		}
		if err != nil {
			q.discard()
			if ctx.Err() == nil {
				q.s.log(fmt.Errorf("taking the serving lock: %w", err))
			}
			q.busy()
			q.write(func() {})
			return
		}
		auth := s.readAuthorization()
		if auth.same(q.auth) && s.sessionOf(state, carried, token, auth.a).same(q.sess) {
			if s.starting != nil {
				s.starting()
			}
			// A response whose deadline passed by its last check is not
			// written, and renews no session.
			if ctx.Err() != nil {
				release()
				done()
				q.discard()
				q.busy()
				q.write(func() {})
				return
			}
			if q.sess.valid {
				h.Set("Set-Cookie", sessionCookie(q.sess.token))
			}
			q.write(sync.OnceFunc(func() { release(); done() }))
			q.drop()
			return
		}
		release()
		done()
		q.discard()
		if render == maxRenders {
			q.busy()
			q.write(func() {})
			return
		}
	}
}

// maxRenders is how many times a request is rendered while the stored
// authorization keeps changing under it.
const maxRenders = 3

// serve renders the request's response, in the order the package comment
// gives. It takes the request's view, resolves the route through it, and
// answers from the index alone where it can. A route that reads a
// repository is admitted to a git slot, holds it until it renders, and
// holds its generation until it has rendered. The request gives back its
// view once it has rendered, before serve returns.
func (q *request) serve() {
	if q.r.Method != http.MethodGet && q.r.Method != http.MethodHead {
		allow, text := "GET, HEAD", "This server answers GET and HEAD alone."
		if posted(q.rpage) {
			allow, text = "GET, HEAD, POST", "This page answers GET, HEAD and POST alone."
		}
		q.w.Header().Set("Allow", allow)
		q.page(http.StatusMethodNotAllowed, "error", "Method not allowed", errorPage{
			Heading: "Method not allowed", Text: text,
		})
		return
	}
	if q.sess.unavailable {
		q.unavailablePage()
		return
	}
	if q.rpage != "" {
		q.readerPage()
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
	defer q.release()
	for {
		v, err := q.s.viewFor(q.ctx, q.auth, q.reader)
		switch {
		case errors.Is(err, export.ErrNoExport):
			q.nothingPublished()
			return
		case errors.Is(err, errLoadsFull):
			q.busy()
			return
		case err != nil:
			q.fail(err)
			return
		}
		q.view, q.e, q.idx = v, v.e, v.idx
		if q.s.viewed != nil {
			q.s.viewed()
		}
		op := q.route()
		if op == nil || !q.runGit(op) {
			return
		}
		q.release()
	}
}

// nothingPublished answers a request when the store has no active
// generation.
func (q *request) nothingPublished() {
	q.page(http.StatusServiceUnavailable, "error", "Nothing published", errorPage{
		Heading: "Nothing published yet", Text: "No export has been activated on this server.",
	})
}

// release gives back the request's view, when it holds one.
func (q *request) release() {
	if q.view != nil {
		q.s.unpin(q.view, poolOf(q.reader))
		q.view, q.e, q.idx = nil, nil, nil
	}
}

// gitOp is the work of a source route in a repository: the project whose
// repository it reads, and what it does there, under the request's git
// context, once the request holds a git slot and its generation.
type gitOp struct {
	project *export.Project
	run     func(ctx context.Context, repo string) error
}

// runGit admits the request to a git slot, opens the active generation,
// and runs op in the generation's repository of op's project. again is set
// when the active generation is no longer the one the request's view was
// built from: the request has then given back its slot and the generation,
// and rendered nothing, and takes its view again.
func (q *request) runGit(op *gitOp) (again bool) {
	done, admitted := q.s.admit(q.ctx)
	if !admitted {
		q.busy()
		return false
	}
	q.slot = done
	defer q.giveSlot()
	var cancel context.CancelFunc
	q.git, cancel = context.WithTimeout(q.ctx, q.s.gitTime)
	defer cancel()
	defer func() {
		if again {
			q.git = nil
		}
	}()
	if q.s.admitted != nil {
		q.s.admitted()
	}
	g, release, err := q.s.store.Open()
	switch {
	case errors.Is(err, export.ErrNoExport):
		q.nothingPublished()
		return false
	case err != nil:
		q.fail(err)
		return false
	}
	defer release()
	if g.Sequence != q.view.key.seq {
		return true
	}
	if q.s.opened != nil {
		q.s.opened(g)
	}
	repo, ok := g.Repo(op.project.Name)
	if !ok {
		q.notFound()
		return false
	}
	if err := op.run(q.git, repo); err != nil {
		q.fail(err)
	}
	return false
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
// already. A request stops counting as waiting when it is given its slot,
// not when it next runs, so a request that has its slot never takes a
// waiting place from another. done gives the slot back.
func (s *Server) admit(ctx context.Context) (done func(), ok bool) {
	s.gitMu.Lock()
	if s.running < MaxGit {
		s.running++
		s.gitMu.Unlock()
		return s.giveGit, true
	}
	if len(s.queue) >= MaxWaiting {
		s.gitMu.Unlock()
		return nil, false
	}
	given := make(chan struct{})
	s.queue = append(s.queue, given)
	s.gitMu.Unlock()
	select {
	case <-given:
		return s.giveGit, true
	case <-ctx.Done():
	}
	s.gitMu.Lock()
	for i, c := range s.queue {
		if c == given {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			s.gitMu.Unlock()
			return nil, false
		}
	}
	s.gitMu.Unlock()
	// The slot was given as ctx ended: pass it on.
	s.giveGit()
	return nil, false
}

// giveGit gives back a git slot: to the request that has waited longest
// for one, or to no one when no request waits.
func (s *Server) giveGit() {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if len(s.queue) == 0 {
		s.running--
		return
	}
	close(s.queue[0])
	s.queue = s.queue[1:]
}

// errLoadsFull is viewFor's error for a request that would wait for its
// view while MaxLoadWaiting requests wait already, or, for a named
// reader's request, while MaxNamedWaiting named readers' requests do.
var errLoadsFull = errors.New("too many requests wait for their views")

// viewFor is the view of the active generation that a request under auth,
// read as reader, serves from, pinned for the request in reader's pool:
// unpin gives it back. The view holds the generation's export, decoded,
// and the index of the projects the reader may see (visible). A named
// reader who may see no more than anyone has the view of what anyone may
// see, and pins it in the named readers' pool.
//
// A request whose active generation has no decoded export waits for a
// load, and one whose view has no index waits for it to be built, or for
// room for it. It holds no generation while it waits. It is admitted among
// the MaxLoadWaiting requests that may wait before it first waits, and
// keeps its place until viewFor returns. A request read as a named reader
// is admitted only while fewer than MaxNamedWaiting such requests wait, so
// anonymous requests always have places of their own. Once a load ends it
// looks again, since another generation may have been activated meanwhile.
func (s *Server) viewFor(ctx context.Context, auth authState, reader string) (*view, error) {
	anyone := reader == export.Anyone
	p := poolOf(reader)
	waiting := false
	defer func() {
		if waiting {
			<-s.loadWaiting
			if !anyone {
				<-s.namedWaiting
			}
		}
	}()
	wait := func() error {
		if waiting {
			return nil
		}
		if !anyone {
			select {
			case s.namedWaiting <- struct{}{}:
			default:
				return errLoadsFull
			}
		}
		select {
		case s.loadWaiting <- struct{}{}:
			waiting = true
			return nil
		default:
			if !anyone {
				<-s.namedWaiting
			}
			return errLoadsFull
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seq, err := s.store.Active()
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		d := s.exported
		s.mu.Unlock()
		if d == nil || d.seq < seq {
			if err := wait(); err != nil {
				return nil, err
			}
			if err := s.await(ctx, seq, anyone); err != nil {
				return nil, err
			}
			continue
		}
		if d.seq > seq {
			continue // the active generation moved on since it was read
		}
		shown := visible(d.e, auth.a, reader)
		key := viewKey{seq: seq, auth: string(auth.signed), projects: projectsKey(d.e, shown)}
		v, freed := s.pin(key, p, d.e, shown)
		if v == nil {
			if err := wait(); err != nil {
				return nil, err
			}
			select {
			case <-freed:
				continue
			case <-ctx.Done():
				return nil, fmt.Errorf("waiting for room to index generation %d: %w", seq, ctx.Err())
			}
		}
		select {
		case <-v.done:
		default:
			if err := wait(); err != nil {
				s.unpin(v, p)
				return nil, err
			}
			select {
			case <-v.done:
			case <-ctx.Done():
				s.unpin(v, p)
				return nil, fmt.Errorf("waiting for generation %d's index: %w", seq, ctx.Err())
			}
		}
		if v.err != nil {
			s.unpin(v, p)
			return nil, v.err
		}
		return v, nil
	}
}

// projectsKey is the part of a view's name that names its projects: the
// name and identity of each project of e that shown names, in e's order.
func projectsKey(e *export.Export, shown map[string]bool) string {
	var b strings.Builder
	for _, p := range e.Projects {
		if shown[p.Name] {
			b.WriteString(p.Name + " " + p.Identity + "\n")
		}
	}
	return b.String()
}

// pin is the view called key, pinned for one request of pool p: the view
// the server holds, built or being built, or a new one, which a worker of
// its own starts to build. The view takes a place of p for the request,
// unless it holds one already. A view no request pinned gives back the
// place it kept for another pool. A view whose build was stopped keeps its
// place until its worker ends, and is never pinned again. When every
// place of p is taken, room drops the view that keeps one with no request
// pinning it and was taken longest ago. When every place of p is pinned or
// stopping, pin is nil, and freed is closed once a place may be free.
func (s *Server) pin(key viewKey, p pool, e *export.Export, shown map[string]bool) (v *view, freed chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.used++
	v = s.views[key]
	if (v == nil || !v.holds[p]) && !s.room(p) {
		return nil, s.freed
	}
	if v == nil {
		ctx, cancel := context.WithCancel(context.Background())
		v = &view{key: key, e: e, shown: shown, cancel: cancel, done: make(chan struct{})}
		s.views[key] = v
		go s.build(ctx, v)
	} else if !v.pinned() && !v.holds[p] {
		v.holds = [pools]bool{}
		s.wake()
	}
	v.holds[p] = true
	v.pins[p]++
	v.used = s.used
	return v, nil
}

// room is whether a view may take one more place of p. When every place of
// p is taken, it drops the view no request pins that keeps one and was
// taken longest ago, and is false when there is none: every place of p is
// held by a view requests of p pin, or by one whose build was stopped. It
// is called with mu held.
func (s *Server) room(p pool) bool {
	taken := 0
	for o := range s.stopping {
		if o.holds[p] {
			taken++
		}
	}
	var oldest *view
	for _, o := range s.views {
		if !o.holds[p] {
			continue
		}
		taken++
		if !o.pinned() && (oldest == nil || o.used < oldest.used) {
			oldest = o
		}
	}
	if taken < p.size() {
		return true
	}
	if oldest == nil {
		return false
	}
	delete(s.views, oldest.key)
	return true
}

// unpin is a request of pool p that no longer holds v: it has rendered,
// or stopped waiting. A view that requests of p no longer pin while
// requests of the other pool do gives back its place of p. A view no
// request pins keeps its place of p, and is stopped when it is still being
// built: no request takes it again, and it keeps its place until its
// worker ends (build). A built view no request pins is dropped when its
// generation is older than the newest decoded export or the authorization
// it was built under is no longer the newest read. Its place may then be
// taken.
func (s *Server) unpin(v *view, p pool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.pins[p]--; v.pins[p] > 0 {
		return
	}
	if v.pinned() {
		v.holds[p] = false
		s.wake()
		return
	}
	building := v.idx == nil && v.err == nil
	if building || s.stale(v) {
		if s.views[v.key] == v {
			delete(s.views, v.key)
		}
		if building {
			s.stopping[v] = struct{}{}
		}
		v.cancel()
	}
	s.wake()
}

// stale reports whether a view serves no request to come: its generation
// is older than the newest decoded export, or the authorization it was
// built under is not the newest read. It is called with mu held.
func (s *Server) stale(v *view) bool {
	return s.exported != nil && v.key.seq < s.exported.seq || v.key.auth != string(s.auth.signed)
}

// wake tells the requests that wait for a place that one may be free. It
// is called with mu held.
func (s *Server) wake() {
	close(s.freed)
	s.freed = make(chan struct{})
}

// build is v's worker. It builds v's index from v's decoded export, which
// a load has read already: it holds no loader slot, and waits for none.
// A view whose build fails, or is stopped, is dropped. A view whose build
// unpin stopped gives back its place only here, as its worker ends.
func (s *Server) build(ctx context.Context, v *view) {
	if s.indexing != nil {
		s.indexing(ctx, v.key)
	}
	idx, err := newIndex(ctx, v.e, v.shown)
	if err == nil {
		err = ctx.Err()
	}
	s.mu.Lock()
	if err != nil {
		v.err = err
		if s.views[v.key] == v {
			delete(s.views, v.key)
		}
		s.wake()
	} else {
		v.idx = idx
	}
	if _, stopped := s.stopping[v]; stopped {
		delete(s.stopping, v)
		s.wake()
	}
	s.mu.Unlock()
	v.cancel()
	close(v.done)
}

// await waits until a load of generation seq, or of a newer generation,
// ends, or until ctx is done. It joins a load that will read one of those,
// since a worker reads whatever generation is active once it starts, and
// otherwise makes one.
//
// A load starts only once one of its requests takes a loader slot for it.
// While every slot it may take is taken, each of its requests waits for
// one, and the first to take one starts the load, so no load is queued
// that no request waits for. An anonymous request may take anonLoader or
// a slot of loaders, and any other request a slot of loaders alone. A
// request that stops waiting leaves the load, and the last to leave stops
// it. When leave drops a load to give back anonLoader, the requests still
// waiting for it wait for another load.
func (s *Server) await(ctx context.Context, seq int64, anonymous bool) error {
	for {
		s.mu.Lock()
		if d := s.exported; d != nil && d.seq >= seq {
			s.mu.Unlock()
			return nil
		}
		var l *load
		for m := range s.loads {
			if !m.opened || m.seq >= seq {
				l = m
				break
			}
		}
		if l == nil {
			l = &load{started: make(chan struct{}), done: make(chan struct{}), dropped: make(chan struct{})}
			s.loads[l] = struct{}{}
		}
		l.wants++
		if anonymous {
			l.anon++
		}
		s.mu.Unlock()
		var anon chan struct{}
		if anonymous {
			anon = s.anonLoader
			select {
			case <-l.started:
			case anon <- struct{}{}:
				if !s.start(l, anon) {
					<-anon
				}
			default:
			}
		}
		select {
		case <-l.started:
		case anon <- struct{}{}:
			if !s.start(l, anon) {
				<-anon
			}
		case s.loaders <- struct{}{}:
			if !s.start(l, s.loaders) {
				<-s.loaders
			}
		case <-ctx.Done():
			s.leave(l, anonymous)
			return fmt.Errorf("waiting for generation %d's export to be read: %w", seq, ctx.Err())
		}
		select {
		case <-l.done:
			select {
			case <-l.dropped:
				continue
			default:
				return l.err
			}
		case <-l.dropped:
			continue
		case <-ctx.Done():
			s.leave(l, anonymous)
			return fmt.Errorf("waiting for generation %d's export to be read: %w", seq, ctx.Err())
		}
	}
}

// start starts l's worker, for the request that took token's slot for it.
// It is false when another request started it already.
func (s *Server) start(l *load, token chan struct{}) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.cancel != nil {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.token = cancel, token
	close(l.started)
	go s.run(ctx, l)
	return true
}

// leave is a request that stopped waiting for l. Once no request waits for
// it, no request may join it, and a load that started is stopped.
//
// anonLoader runs a load only while an anonymous request waits for it.
// When the last anonymous request leaves a load that runs on anonLoader
// while others wait for it, the load takes a free slot of loaders and
// runs on, so the requests that wait lose nothing. With no slot of
// loaders free, it is dropped: it is stopped, no request may join it, and
// those that wait for it wait for another load, within their own time.
// Either way it gives back anonLoader at once.
func (s *Server) leave(l *load, anonymous bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l.wants--
	if anonymous {
		l.anon--
	}
	if l.wants == 0 {
		delete(s.loads, l)
		if l.cancel != nil {
			l.cancel()
		}
		return
	}
	if l.anon > 0 || l.token != s.anonLoader {
		return
	}
	select {
	case s.loaders <- struct{}{}:
		l.token = s.loaders
	default:
		delete(s.loads, l)
		l.cancel()
		l.token = nil
		close(l.dropped)
	}
	<-s.anonLoader
}

// run is l's worker. It keeps the export it decodes when its generation is
// newer than the kept one's, so a load of an older generation never
// displaces a newer, and drops the views of older generations that no
// request pins, waking the requests that wait for room. A load that was
// stopped keeps nothing: leave stops it under mu, and run looks under mu.
// The worker gives back its generation before it ends, and then the
// loader slot its load holds, if leave has not given it back already. It
// builds no index.
func (s *Server) run(ctx context.Context, l *load) {
	d, err := s.read(ctx, l)
	s.mu.Lock()
	delete(s.loads, l)
	if d != nil && ctx.Err() == nil && (s.exported == nil || s.exported.seq < d.seq) {
		s.exported = d
		for key, v := range s.views {
			if !v.pinned() && s.stale(v) {
				delete(s.views, key)
			}
		}
		s.wake()
	}
	l.err = err
	token := l.token
	l.token = nil
	s.mu.Unlock()
	l.cancel()
	close(l.done)
	if token != nil {
		<-token
	}
}

// read opens the active generation for l, and holds the generation while
// it reads and decodes the generation's export. It reads nothing when the
// generation's export is decoded already or another load reads the same,
// and its waiting requests look again. It stops reading once ctx is done.
func (s *Server) read(ctx context.Context, l *load) (d *decoded, err error) {
	// Cancellation can close the file during any operation. Report the
	// context error instead of the resulting descriptor error.
	defer func() {
		if stopped := ctx.Err(); stopped != nil {
			d, err = nil, stopped
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
	s.mu.Lock()
	read := s.exported == nil || s.exported.seq < g.Sequence
	for m := range s.loads {
		if m != l && m.opened && m.seq == g.Sequence {
			read = false
		}
	}
	l.opened, l.seq = true, g.Sequence
	s.mu.Unlock()
	if !read {
		return nil, nil
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
	return &decoded{seq: g.Sequence, e: e}, nil
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

// route dispatches on the request path's segments, each unescaped on its
// own, so an escaped slash stays inside its segment, through the request's
// index. The first segment is a group's name or an alias, and under a
// group's name the second is a project's name. It answers every route
// from the index alone, but a source route that reads a repository, whose
// work it returns for runGit to run.
func (q *request) route() *gitOp {
	parts := q.parts
	switch {
	case !q.ok:
		q.notFound()
	case len(parts) == 1 && parts[0] == "":
		q.landing()
	case isKnotsPage(parts):
		q.plan()
	case parts[0] == own:
		q.notFound()
	default:
		if to, ok := q.idx.aliases[parts[0]]; ok {
			q.moved(to, 1)
			return nil
		}
		if len(parts) >= 2 {
			if p := q.idx.groups[parts[0]][parts[1]]; p != nil {
				return q.projectRoute(p.Name, parts[2:])
			}
			if to, ok := q.idx.formers[parts[0]][parts[1]]; ok {
				q.moved(to, 2)
				return nil
			}
		}
		q.notFound()
	}
	return nil
}

// isKnotsPage reports whether a path's segments name the knots page,
// /-/knots.
func isKnotsPage(parts []string) bool {
	return len(parts) == 2 && parts[0] == own && parts[1] == "knots"
}

// projectRoute dispatches on the segments of a path after its group's and
// its project's names.
func (q *request) projectRoute(project string, rest []string) *gitOp {
	switch {
	case len(rest) == 0, len(rest) == 1 && rest[0] == "":
		q.home(project)
	case len(rest) == 2 && rest[0] == "knots":
		q.knot(project, rest[1])
	case len(rest) >= 1 && rest[0] == "source":
		return q.source(project, rest[1:])
	default:
		q.notFound()
	}
	return nil
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

// knotsURL is the knots page's path, the one page the server serves
// under /-/.
const knotsURL = "/" + own + "/knots"

// landing answers / with a redirect to the page of the first project the
// reader may see, in the index's order: the tree at its published tip.
// With no project to see, it redirects to the knots page.
func (q *request) landing() {
	if len(q.idx.public) == 0 {
		q.redirect(knotsURL)
		return
	}
	q.home(q.idx.public[0])
}

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

// project is the project called name in the request's index: one the
// reader may see, in a group. Every route that names a project asks here
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

// discard drops the rendered response, a page or a redirect, so that none
// of it is written.
func (q *request) discard() {
	q.drop()
	q.status, q.disposition, q.body, q.location, q.permanent = 0, "", nil, "", false
}

// redirect answers with a redirect to url.
func (q *request) redirect(url string) { q.location = url }

// write writes the rendered response, or nothing for a reader that went
// away. It returns once the response is written, or the connection
// failed or timed out. A permanent redirect names its location as it was
// made, with no cleaning, so the path it redirects to is the one the
// reader sent under another name.
//
// released is called once the headers are written, and before the body is,
// or at once when there is nothing to write.
func (q *request) write(released func()) {
	defer released()
	switch {
	case q.location != "" && q.permanent:
		q.w.Header().Set("Location", q.location)
		q.w.WriteHeader(http.StatusPermanentRedirect)
	case q.location != "":
		http.Redirect(releasing{q.w, released}, q.r, q.location, http.StatusFound)
	case q.status != 0:
		h := q.w.Header()
		h.Set("Content-Type", q.ctype)
		if q.disposition != "" {
			h.Set("Content-Disposition", q.disposition)
		}
		h.Set("Content-Length", strconv.Itoa(q.body.n))
		q.w.WriteHeader(q.status)
		released()
		for _, p := range q.body.pieces {
			if _, err := q.w.Write(p); err != nil {
				return
			}
		}
	}
}

// releasing is a response writer that calls released once the headers are
// written, before any of the body is, so http.Redirect writes its short
// body after the serving lock is let go, as every other response does.
type releasing struct {
	http.ResponseWriter
	released func()
}

func (w releasing) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	w.released()
}

// setHeaders sets the headers every response carries, with policy, its
// route's Content-Security-Policy. Every response is the reader's own, so
// no cache stores it, and none is told it may be reused: no response
// carries an ETag or a Last-Modified.
func setHeaders(h http.Header, policy string) {
	h.Set("Content-Security-Policy", policy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Vary", "Cookie")
}

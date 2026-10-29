package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"the-valley/note"
	"the-valley/web/export"
)

// receiver is the upload's three steps over the state directory, as
// package export defines them.
type receiver struct {
	store       *export.Store
	verifiers   []note.VerifierKey
	instance    string
	destination string
	token       string
	maxBody     int64
	maxUpload   int64
	log         *log.Logger

	// slots holds one token for each authenticated upload request
	// running, and authSlots one for each authorization request, so a
	// saturated upload never holds up an authorization.
	slots     chan struct{}
	authSlots chan struct{}
	// pruning is set while a prune runs after an authorization, so one
	// prune at a time waits for the readers of the active generation.
	pruning atomic.Bool
	// pruneDelay is the first pause before prune tries again while a
	// reader holds the generation. A test shortens it.
	pruneDelay time.Duration

	mu sync.Mutex
	// staged is the newest upload a manifest staged, or nil before the
	// first since the receiver started. Store.Stage discards every older
	// upload, so no other is worth a body or a commit.
	staged *export.Staging
	// receiving holds the path each body a request is staging goes to,
	// which names the body's upload and its SHA-256. A second request for
	// the same body of the same upload is answered 503 while the first
	// runs, so staging holds one partial file of each body, as the
	// store's storage cap counts it. A newer upload's request for the
	// same body is not held up: staging the newer manifest stops the
	// older upload's requests first.
	receiving map[string]bool
}

// errBusy is body's error when another request is staging the same body
// of the same upload.
var errBusy = errors.New("another request is staging this body")

// The kinds of request the publisher makes.
const (
	stepAuthorization = "authorization"
	stepManifest      = "manifest"
	stepBody          = "body"
	stepCommit        = "commit"
	// stepLegacy is a path of the first version of the upload, which is
	// refused as unsupported-version.
	stepLegacy = "legacy"
)

// ServeHTTP authenticates the request, then routes it, then takes a slot
// for it, in that order. A request without the token is refused with no
// slot taken and no byte of its body read.
func (rv *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(shortTimeout))
	if !export.CarriesToken(r, rv.token) {
		// Closing the connection after the reply reads none of the body.
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		w.Header().Set("WWW-Authenticate", `Bearer realm="valley-web-receive"`)
		rv.fail(w, r, &export.Error{Code: export.CodeUnauthenticated,
			Message: "Authorization header must carry the receiver's bearer token"})
		return
	}
	step, seq, sum, allow := route(r.URL.Path)
	switch {
	case step == "":
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		reply(w, http.StatusNotFound, nil)
		return
	case step == stepLegacy:
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		rv.fail(w, r, &export.Error{Code: export.CodeUnsupportedVersion,
			Message: "this receiver takes signed authorizations and the-valley/web-export/v2 manifests under " + export.PathPrefix + "; upgrade valley-web-publish on the valley host"})
		return
	case r.Method != allow:
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		w.Header().Set("Allow", allow)
		reply(w, http.StatusMethodNotAllowed, nil)
		return
	}
	slots := rv.slots
	if step == stepAuthorization {
		slots = rv.authSlots
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		busy(w, rc)
		return
	}

	// The deadline bounds the connection's reads and writes, and ctx the
	// store's work: waiting for its lock, and the git a commit runs. ctx
	// is done, too, once the client goes away.
	timeout := map[string]time.Duration{stepAuthorization: authorizationTimeout, stepManifest: manifestTimeout, stepBody: bodyTimeout, stepCommit: commitTimeout}[step]
	deadline := time.Now().Add(timeout)
	rc.SetReadDeadline(deadline)
	rc.SetWriteDeadline(deadline)
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	switch step {
	case stepAuthorization:
		got, err := rv.authorization(ctx, r)
		if err != nil {
			rv.fail(w, r, err)
			return
		}
		rv.log.Printf("stored authorization %d", got.Sequence)
		reply(w, http.StatusOK, got)
	case stepManifest:
		inv, err := rv.manifest(ctx, r)
		if err != nil {
			rv.fail(w, r, err)
			return
		}
		reply(w, http.StatusOK, inv)
	case stepBody:
		err := rv.body(ctx, rc, r, seq, sum)
		if errors.Is(err, errBusy) {
			busy(w, rc)
			return
		}
		if err != nil {
			rv.fail(w, r, err)
			return
		}
		reply(w, http.StatusNoContent, nil)
	case stepCommit:
		g, err := rv.commit(ctx, seq)
		if err != nil {
			rv.fail(w, r, err)
			return
		}
		rv.log.Printf("activated sequence %d", g.Sequence)
		reply(w, http.StatusOK, export.Committed{Schema: export.CommittedSchema, Sequence: g.Sequence})
	}
}

// route reads the step a path names, and the method that step takes. A
// body's path names its upload's sequence and its SHA-256, and a commit's
// the sequence. step is "" for a path the upload does not use.
func route(path string) (step string, seq int64, sum, method string) {
	switch {
	case path == export.AuthorizationPath:
		return stepAuthorization, 0, "", http.MethodPost
	case path == export.ManifestPath:
		return stepManifest, 0, "", http.MethodPost
	case strings.HasPrefix(path, export.LegacyPrefix):
		return stepLegacy, 0, "", ""
	}
	rest, ok := strings.CutPrefix(path, export.PathPrefix)
	if !ok {
		return "", 0, "", ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", 0, "", ""
	}
	seq, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || seq <= 0 || strconv.FormatInt(seq, 10) != parts[0] {
		return "", 0, "", ""
	}
	switch {
	case parts[1] == "commit":
		return stepCommit, seq, "", http.MethodPost
	case isSHA256(parts[1]):
		return stepBody, seq, parts[1], http.MethodPut
	}
	return "", 0, "", ""
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !('0' <= s[i] && s[i] <= '9' || 'a' <= s[i] && s[i] <= 'f') {
			return false
		}
	}
	return true
}

// authorization admits a signed authorization by its signature, its type,
// its instance and destination, and its sequence, and stores it durably,
// without the store's activation lock. The reply is sent once it is
// stored. It then deletes, in the background, the repositories the
// authorization withdrew, once no reader holds them; neither the reply nor
// the next authorization waits for that.
func (rv *receiver) authorization(ctx context.Context, r *http.Request) (export.AuthorizationReply, error) {
	hw, err := rv.store.AuthorizationHighWater()
	if err != nil {
		return export.AuthorizationReply{}, err
	}
	signed, err := io.ReadAll(io.LimitReader(r.Body, export.MaxAuthorization+1))
	if err != nil {
		return export.AuthorizationReply{}, &export.Error{Code: export.CodeUnreadable, Message: fmt.Sprintf("the authorization could not be read: %v", err)}
	}
	a, err := export.VerifyAuthorization(bytes.NewReader(signed), export.AuthorizationAdmission{Verifiers: rv.verifiers,
		Instance: rv.instance, Destination: rv.destination, HighWater: hw})
	if err != nil {
		return export.AuthorizationReply{}, err
	}
	if err := rv.store.Authorize(ctx, a, signed); err != nil {
		return export.AuthorizationReply{}, err
	}
	rv.prune()
	got := export.AuthorizationReply{Schema: export.AuthorizedSchema, Sequence: a.Sequence}
	// What the receiver serves only advises the publisher, so a failure
	// to read it does not undo the reply: the publisher then uploads.
	active, _, err := rv.store.Holdings()
	if err == nil {
		got.Missing, err = rv.store.Missing()
	}
	if err != nil {
		rv.log.Printf("reading what the store serves: %v", err)
		return got, nil
	}
	got.Active = active
	return got, nil
}

// prune deletes the repositories the stored authorization withdrew, in a
// goroutine of its own, unless one is running already: that one reads the
// authorization again each time it tries. While a reader holds the active
// generation it tries again, after a pause that doubles up to five
// seconds, for at most pruneTimeout. A prune that does not finish is
// logged, and the next authorization, activation or start of the receiver
// tries again.
func (rv *receiver) prune() {
	if !rv.pruning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer rv.pruning.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), pruneTimeout)
		defer cancel()
		pause := rv.pruneDelay
		for {
			err := rv.store.Prune(ctx)
			if err == nil {
				return
			}
			if !errors.Is(err, export.ErrHeld) {
				rv.log.Printf("deleting withdrawn repositories: %v", err)
				return
			}
			select {
			case <-time.After(pause):
				pause = min(2*pause, 5*time.Second)
			case <-ctx.Done():
				rv.log.Printf("deleting withdrawn repositories: readers held the active generation for %v; will retry on the next authorization or activation", pruneTimeout)
				return
			}
		}
	}()
}

// manifest is step 1: admit the manifest, hold its bodies to the caps,
// stage it, and reply with the inventory. Staging waits for the store's
// lock, which a commit holds throughout, until ctx is done.
func (rv *receiver) manifest(ctx context.Context, r *http.Request) (export.Inventory, error) {
	hw, err := rv.store.HighWater()
	if err != nil {
		return export.Inventory{}, err
	}
	m, err := export.VerifyManifest(r.Body, export.Admission{Verifiers: rv.verifiers,
		Instance: rv.instance, Destination: rv.destination, HighWater: hw, Now: time.Now()})
	if err != nil {
		return export.Inventory{}, err
	}
	if err := m.CheckSizes(rv.maxBody, rv.maxUpload); err != nil {
		return export.Inventory{}, err
	}
	st, err := rv.store.Stage(ctx, m)
	if err != nil {
		return export.Inventory{}, err
	}
	rv.mu.Lock()
	if rv.staged == nil || rv.staged.Manifest.Sequence <= m.Sequence {
		rv.staged = &st
	}
	rv.mu.Unlock()
	return rv.store.Inventory(st)
}

// upload is the staged upload with sequence: no-manifest when nothing
// newer than it is staged, and superseded when something newer is.
func (rv *receiver) upload(seq int64) (export.Staging, error) {
	rv.mu.Lock()
	defer rv.mu.Unlock()
	switch {
	case rv.staged == nil || rv.staged.Manifest.Sequence < seq:
		return export.Staging{}, &export.Error{Code: export.CodeNoManifest,
			Message: fmt.Sprintf("no manifest is staged for sequence %d", seq)}
	case rv.staged.Manifest.Sequence > seq:
		return export.Staging{}, &export.Error{Code: export.CodeSuperseded,
			Message: fmt.Sprintf("staged upload %d supersedes sequence %d", rv.staged.Manifest.Sequence, seq)}
	}
	return *rv.staged, nil
}

// body is step 2 for the body with SHA-256 sum. It stages the body until
// ctx is done, or until the store discards the upload's bodies (see
// export.Store.Writer), and then ends the read that waits on the client.
func (rv *receiver) body(ctx context.Context, rc *http.ResponseController, r *http.Request, seq int64, sum string) error {
	st, err := rv.upload(seq)
	if err != nil {
		return err
	}
	var want *export.Body
	for _, b := range st.Manifest.Bodies() {
		if b.SHA256 == sum {
			want = &b
			break
		}
	}
	if want == nil {
		return &export.Error{Code: export.CodeNotDeclared,
			Message: fmt.Sprintf("manifest for sequence %d declares no body with SHA-256 %s", seq, sum)}
	}
	path := st.Body(*want)
	rv.mu.Lock()
	if rv.receiving[path] {
		rv.mu.Unlock()
		return errBusy
	}
	if rv.receiving == nil {
		rv.receiving = map[string]bool{}
	}
	rv.receiving[path] = true
	rv.mu.Unlock()
	defer func() {
		rv.mu.Lock()
		delete(rv.receiving, path)
		rv.mu.Unlock()
	}()
	wctx, done, err := rv.store.Writer(ctx, st)
	if err != nil {
		return err
	}
	defer done()
	// A read deadline set once the handler has returned would end the
	// connection's next request, so the handler waits for one being set.
	interrupted := make(chan struct{})
	stop := context.AfterFunc(wctx, func() {
		rc.SetReadDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	_, err = export.StageBody(st.Bodies(), *want, rv.maxBody, r.WithContext(wctx))
	if errors.Is(err, fs.ErrNotExist) {
		// The upload's staging directory is gone: a newer manifest
		// discarded it, or a commit activated it.
		if _, err := rv.upload(seq); err != nil {
			return err
		}
		return &export.Error{Code: export.CodeNoManifest,
			Message: fmt.Sprintf("staging for sequence %d was cleared before the body finished", seq)}
	}
	return err
}

// commit is step 3: activate the upload with sequence, until ctx is done.
func (rv *receiver) commit(ctx context.Context, seq int64) (export.Generation, error) {
	st, err := rv.upload(seq)
	if err != nil {
		return export.Generation{}, err
	}
	g, err := rv.store.Activate(ctx, st)
	if err == nil {
		// Activate prunes what it can; a reader may have held the old
		// generation, which the new one replaced, or the new one.
		rv.prune()
	}
	return g, err
}

// fail replies with the refusal err is, or with 500 and no refusal for any
// other error, which the publisher retries. Either is logged.
func (rv *receiver) fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *export.Error
	if errors.As(err, &e) {
		rv.log.Printf("%s %s from %s: refused: %v", r.Method, r.URL.Path, r.RemoteAddr, e)
		reply(w, e.Code.Status(), e.Reply())
		return
	}
	rv.log.Printf("%s %s from %s: %v", r.Method, r.URL.Path, r.RemoteAddr, err)
	reply(w, http.StatusInternalServerError, nil)
}

// drain takes every request slot, so it waits until each request that
// holds one has finished, with the git it runs, or until ctx is done. It
// reports whether it took them all. A request that comes after takes no
// slot, and is answered 503.
func (rv *receiver) drain(ctx context.Context) bool {
	for _, slots := range []chan struct{}{rv.slots, rv.authSlots} {
		for range cap(slots) {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return false
			}
		}
	}
	return true
}

// busy answers 503 with Retry-After and no refusal, which the publisher
// retries after a pause, and closes the connection without reading the
// body.
func busy(w http.ResponseWriter, rc *http.ResponseController) {
	rc.SetReadDeadline(time.Now())
	w.Header().Set("Connection", "close")
	w.Header().Set("Retry-After", "5")
	reply(w, http.StatusServiceUnavailable, nil)
}

// reply writes the status, and doc as JSON when it is not nil.
func reply(w http.ResponseWriter, status int, doc any) {
	w.Header().Set("Cache-Control", "no-store")
	if doc == nil {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(doc)
}

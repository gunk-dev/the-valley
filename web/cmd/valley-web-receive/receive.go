package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

	// slots holds one token for each authenticated request running.
	slots chan struct{}

	mu sync.Mutex
	// staged is the newest upload a manifest staged, or nil before the
	// first since the receiver started. Store.Stage discards every older
	// upload, so no other is worth a body or a commit.
	staged *export.Staging
}

// The kinds of request the upload makes.
const (
	stepManifest = "manifest"
	stepBody     = "body"
	stepCommit   = "commit"
)

// ServeHTTP authenticates the request, then routes it, then takes a slot
// for it, in that order. A request without the token is refused with no
// slot taken and no byte of its body read.
func (rv *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(shortTimeout))
	if !export.Authorized(r, rv.token) {
		// Closing the connection after the reply reads none of the body.
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		w.Header().Set("WWW-Authenticate", `Bearer realm="valley-web-receive"`)
		rv.fail(w, r, &export.Error{Code: export.CodeUnauthenticated,
			Message: "the request does not carry the receiver's token in its Authorization header"})
		return
	}
	step, seq, sum, allow := route(r.URL.Path)
	switch {
	case step == "":
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		reply(w, http.StatusNotFound, nil)
		return
	case r.Method != allow:
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		w.Header().Set("Allow", allow)
		reply(w, http.StatusMethodNotAllowed, nil)
		return
	}
	select {
	case rv.slots <- struct{}{}:
		defer func() { <-rv.slots }()
	default:
		rc.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		w.Header().Set("Retry-After", "5")
		reply(w, http.StatusServiceUnavailable, nil)
		return
	}

	// The deadline bounds the connection's reads and writes, and ctx the
	// store's work: waiting for its lock, and the git a commit runs. ctx
	// is done, too, once the client goes away.
	timeout := map[string]time.Duration{stepManifest: manifestTimeout, stepBody: bodyTimeout, stepCommit: commitTimeout}[step]
	deadline := time.Now().Add(timeout)
	rc.SetReadDeadline(deadline)
	rc.SetWriteDeadline(deadline)
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	switch step {
	case stepManifest:
		inv, err := rv.manifest(ctx, r)
		if err != nil {
			rv.fail(w, r, err)
			return
		}
		reply(w, http.StatusOK, inv)
	case stepBody:
		if err := rv.body(r, seq, sum); err != nil {
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
	if path == export.ManifestPath {
		return stepManifest, 0, "", http.MethodPost
	}
	rest, ok := strings.CutPrefix(path, "/web-export/v1/")
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
			Message: fmt.Sprintf("nothing is staged for sequence %d", seq)}
	case rv.staged.Manifest.Sequence > seq:
		return export.Staging{}, &export.Error{Code: export.CodeSuperseded,
			Message: fmt.Sprintf("the upload with sequence %d supersedes this one, %d", rv.staged.Manifest.Sequence, seq)}
	}
	return *rv.staged, nil
}

// body is step 2 for the body with SHA-256 sum.
func (rv *receiver) body(r *http.Request, seq int64, sum string) error {
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
			Message: fmt.Sprintf("the manifest with sequence %d declares no body with SHA-256 %s", seq, sum)}
	}
	_, err = export.StageBody(st.Bodies(), *want, rv.maxBody, r)
	if errors.Is(err, fs.ErrNotExist) {
		// The upload's staging directory is gone: a newer manifest
		// discarded it, or a commit activated it.
		if _, err := rv.upload(seq); err != nil {
			return err
		}
		return &export.Error{Code: export.CodeNoManifest,
			Message: fmt.Sprintf("nothing is staged for sequence %d any more", seq)}
	}
	return err
}

// commit is step 3: activate the upload with sequence, until ctx is done.
func (rv *receiver) commit(ctx context.Context, seq int64) (export.Generation, error) {
	st, err := rv.upload(seq)
	if err != nil {
		return export.Generation{}, err
	}
	return rv.store.Activate(ctx, st)
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

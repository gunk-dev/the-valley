package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"the-valley/web/export"
)

// How an upload is retried. A pass makes at most uploadAttempts attempts.
// It waits for its clock to pass a receiver's high-water mark only when
// that is at most maxClockWait away; a mark further ahead is a clock fault
// the operator has to see, and the next pass tries again.
const (
	uploadAttempts = 5
	maxClockWait   = 2 * export.MaxAhead
	firstBackoff   = 2 * time.Second
)

// How long each request may take. A commit waits for the receiver to
// fetch and check every bundle it was sent.
const (
	manifestTimeout = 2 * time.Minute
	bodyTimeout     = 30 * time.Minute
	commitTimeout   = 30 * time.Minute
	maxReply        = 1 << 20
)

// bundles are each exported project's bundle of main, by project: the
// file, and the body it makes. A bundle is made once for each source tip
// and kept in the cache, so the body a manifest declares for a tip is the
// same in every pass. The bundles the export does not declare are removed
// first. Each bundle still to be made is then written only into the room
// --max-bundles and --max-storage leave: its writing stops there, and the
// pass then removes every bundle it wrote and sends nothing.
func (p *publisher) bundles(ctx context.Context, r *reading) (map[string]string, map[string]export.Body, error) {
	root := filepath.Join(p.cache, "bundles")
	paths := map[string]string{}
	for _, proj := range r.export.Projects {
		paths[proj.Name] = filepath.Join(root, proj.Name, proj.SourceTip+".bundle")
	}
	pruneBundles(root, paths)
	used, err := p.storageUsed()
	if err != nil {
		return nil, nil, err
	}
	var held int64 // what the export's bundles hold together
	var missing []export.Project
	for _, proj := range r.export.Projects {
		info, err := os.Stat(paths[proj.Name])
		switch {
		case errors.Is(err, os.ErrNotExist):
			missing = append(missing, proj)
		case err != nil:
			return nil, nil, err
		default:
			held += info.Size()
		}
	}
	if held > int64(p.cfg.maxBundles) {
		return nil, nil, fmt.Errorf("the bundles hold %d bytes together, more than --max-bundles %d, so the pass sends nothing", held, p.cfg.maxBundles)
	}
	if used > int64(p.cfg.maxStorage) {
		return nil, nil, p.overStorage(used)
	}
	var made []string
	for _, proj := range missing {
		room, bound := int64(p.cfg.maxBundles)-held, fmt.Sprintf("--max-bundles %d", p.cfg.maxBundles)
		if left := int64(p.cfg.maxStorage) - used; left < room {
			room, bound = left, fmt.Sprintf("--max-storage %d", p.cfg.maxStorage)
		}
		size, err := makeBundle(ctx, r.mirrors[proj.Name], paths[proj.Name], proj.SourceTip, room)
		if errors.Is(err, errOverBudget) {
			err = fmt.Errorf("its bundle is larger than the %d bytes %s leaves for it, so the pass sends nothing and removes the bundles it wrote", room, bound)
		}
		if err != nil {
			// Each bundle's directory goes with it, once it is empty.
			for _, path := range append(made, paths[proj.Name]) {
				os.Remove(path)
				os.Remove(filepath.Dir(path))
			}
			return nil, nil, fmt.Errorf("%s: %w", proj.Name, err)
		}
		made = append(made, paths[proj.Name])
		held += size
		used += size
	}
	bodies := map[string]export.Body{}
	for name, path := range paths {
		if bodies[name], err = export.FileBody(name, path); err != nil {
			return nil, nil, err
		}
	}
	return paths, bodies, nil
}

// pruneBundles removes everything under root but the bundles keep names:
// the bundles of a project's earlier tips, of projects the export does not
// hold, and what a pass that was stopped left of one it was writing.
func pruneBundles(root string, keep map[string]string) {
	projects, _ := os.ReadDir(root)
	for _, d := range projects {
		dir := filepath.Join(root, d.Name())
		if _, ok := keep[d.Name()]; !ok || !d.IsDir() {
			os.RemoveAll(dir)
			continue
		}
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if path := filepath.Join(dir, f.Name()); path != keep[d.Name()] {
				os.RemoveAll(path)
			}
		}
	}
}

// checkStorage holds the state directory and the caches to --max-storage.
// A pass checks before it fetches anything, and again once it has read the
// valley, whether it then sends anything or not.
func (p *publisher) checkStorage() error {
	used, err := p.storageUsed()
	if err != nil {
		return err
	}
	if used > int64(p.cfg.maxStorage) {
		return p.overStorage(used)
	}
	return nil
}

func (p *publisher) overStorage(used int64) error {
	return fmt.Errorf("the state directory and the cache hold %d bytes, more than --max-storage %d, so the pass sends nothing", used, p.cfg.maxStorage)
}

// storageUsed is the size of every regular file the publisher keeps: in
// its state directory, its cache, and the valley CLI's cache beside it,
// which holds the CLI's clones of every project.
func (p *publisher) storageUsed() (int64, error) {
	var total int64
	for _, dir := range []string{p.cfg.state, p.cache, filepath.Join(filepath.Dir(p.cache), "valley")} {
		err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				var info fs.FileInfo
				if info, err = d.Info(); err == nil {
					total += info.Size()
				}
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// errOverBudget is makeBundle's error for a bundle larger than the room
// it is given.
var errOverBudget = errors.New("the bundle is larger than the room left for it")

// makeBundle writes a full bundle of the mirror's main to path, and returns
// its size. git writes the bundle to its stdout, and the publisher writes
// it to a temporary name, which is renamed into place once git bundle
// list-heads shows it carries main at tip. At most room bytes reach the
// file: a write past them fails, which ends git at its next write, and
// makeBundle removes the file and returns errOverBudget.
func makeBundle(ctx context.Context, mirror, path, tip string, room int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp)
	w := &budgeted{w: f, room: room}
	err = gitStream(ctx, mirror, nil, w, "bundle", "create", "--quiet", "-", export.MainRef)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if w.over {
		return 0, errOverBudget
	}
	if err != nil {
		return 0, err
	}
	heads, err := git(ctx, mirror, "bundle", "list-heads", tmp)
	if err != nil {
		return 0, err
	}
	if got, want := strings.TrimSpace(string(heads)), tip+" "+export.MainRef; got != want {
		return 0, fmt.Errorf("the bundle made of main carries %q, not %q", got, want)
	}
	return w.written, os.Rename(tmp, path)
}

// budgeted writes through to w until room bytes are written. A write that
// would pass room writes nothing, fails, and sets over. It is no
// io.ReaderFrom, so os/exec copies through Write.
type budgeted struct {
	w             io.Writer
	room, written int64
	over          bool
}

func (b *budgeted) Write(p []byte) (int, error) {
	if int64(len(p)) > b.room-b.written {
		b.over = true
		return 0, errOverBudget
	}
	n, err := b.w.Write(p)
	b.written += int64(n)
	return n, err
}

// writeOut writes the export to --out, as an upload would send it: the
// signed manifest, export.json, and bundles/<project>.bundle. The
// directory is written beside its place and renamed into it whole, so a
// reader never sees part of it.
func (p *publisher) writeOut(ctx context.Context, r *reading) error {
	dir := p.cfg.out
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("--out %s is not empty", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	paths, bodies, err := p.bundles(ctx, r)
	if err != nil {
		return err
	}
	m, signed, err := p.sign(r, bodies, p.now().UnixNano())
	if err != nil {
		return err
	}
	parent := filepath.Dir(filepath.Clean(dir))
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "manifest"), signed, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, export.ExportFile), r.json, 0o644); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(tmp, "bundles"), 0o755); err != nil {
		return err
	}
	for name, path := range paths {
		if err := copyFile(path, filepath.Join(tmp, "bundles", name+".bundle")); err != nil {
			return err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	p.say("wrote the export to %s, as sequence %d", dir, m.Sequence)
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// sign makes and signs the manifest for the export, with the sequence
// given.
func (p *publisher) sign(r *reading, bodies map[string]export.Body, sequence int64) (*export.Manifest, []byte, error) {
	m, err := export.NewManifest(p.cfg.instance, p.cfg.destination, sequence, r.export, r.json, bodies)
	if err != nil {
		return nil, nil, err
	}
	signed, err := m.Sign(p.signer, p.key)
	if err != nil {
		return nil, nil, err
	}
	return m, signed, nil
}

// upload sends the export to the receiver, by the protocol package export
// defines, and records the receiver's acknowledgement. It retries:
//
//   - a request that failed in transit, that the receiver answered with a
//     status and no refusal, or that it refused as unreadable, with the
//     same manifest, after a pause that doubles each time; the receiver
//     keeps the bodies already sent;
//   - incomplete, with the same manifest, whose inventory names what is
//     still needed;
//   - replayed, with a manifest signed once the clock has passed the
//     receiver's high-water mark, waiting for it when it is close;
//   - superseded and no-manifest, with a new manifest, after a pause.
//
// Every other refusal ends the pass: it says the token, the manifest or a
// body is wrong, or that the clocks disagree by more than the receiver's
// window, and the operator has to see it. Out-of-window is the last of
// those.
func (p *publisher) upload(ctx context.Context, r *reading, digest string, ack acknowledged) error {
	paths, bodies, err := p.bundles(ctx, r)
	if err != nil {
		return err
	}
	rc := receiver{base: strings.TrimSuffix(p.cfg.receiver, "/"), token: p.token, client: p.client}
	highWater := ack.Sequence
	var m *export.Manifest
	var signed []byte
	backoff := firstBackoff
	for attempt := 1; ; attempt++ {
		if m == nil {
			seq, err := p.sequenceAfter(ctx, highWater)
			if err != nil {
				return err
			}
			if m, signed, err = p.sign(r, bodies, seq); err != nil {
				return err
			}
		}
		active, err := p.send(ctx, rc, m, signed, r.json, paths, digest)
		if err == nil {
			return p.recordAcknowledged(digest, active)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == uploadAttempts {
			return fmt.Errorf("the upload failed %d times: %w", attempt, err)
		}
		pause := time.Duration(0)
		var refusal *export.Error
		switch {
		case !errors.As(err, &refusal) || refusal.Code == export.CodeUnreadable:
			pause, backoff = backoff, 2*backoff
		case refusal.Code == export.CodeIncomplete:
		case refusal.Code == export.CodeReplayed:
			highWater = max(highWater, refusal.HighWater)
			m = nil
		case refusal.Code == export.CodeSuperseded || refusal.Code == export.CodeNoManifest:
			pause, backoff = backoff, 2*backoff
			m = nil
		case refusal.Code == export.CodeOutOfWindow:
			return fmt.Errorf("the receiver refused the manifest signed at %s: %s; check this host's clock and the receiver's", m.SignedAt().Format(time.RFC3339), refusal.Message)
		default:
			return fmt.Errorf("the receiver refused the upload: %w", err)
		}
		p.warn("%v; trying again", err)
		if err := p.sleep(ctx, pause); err != nil {
			return err
		}
	}
}

// sequenceAfter is a sequence for a new manifest: the clock now, in Unix
// nanoseconds, once it is past the greatest sequence the receiver is known
// to have accepted. The clock is waited for when it is close behind, and
// is otherwise a fault: publishing out of order is never done.
func (p *publisher) sequenceAfter(ctx context.Context, highWater int64) (int64, error) {
	seq := p.now().UnixNano()
	if seq > highWater {
		return seq, nil
	}
	behind := time.Duration(highWater-seq) + time.Millisecond
	if behind > maxClockWait {
		return 0, fmt.Errorf("the receiver has accepted sequence %d (signed at %s), and this host's clock is %s behind it; that exceeds the 2m wait limit, so the pass fails", highWater, time.Unix(0, highWater).UTC().Format(time.RFC3339), behind.Round(time.Second))
	}
	if err := p.sleep(ctx, behind); err != nil {
		return 0, err
	}
	if seq = p.now().UnixNano(); seq <= highWater {
		return 0, fmt.Errorf("this host's clock did not pass the receiver's sequence %d", highWater)
	}
	return seq, nil
}

// send is one attempt at the three steps. It returns the sequence the
// receiver serves the export as: the active one when the inventory shows
// it already serves this digest and needs no bundle, and otherwise the
// manifest's, once committed.
func (p *publisher) send(ctx context.Context, rc receiver, m *export.Manifest, signed, exportJSON []byte,
	bundles map[string]string, digest string) (int64, error) {
	inv, err := rc.postManifest(ctx, signed)
	if err != nil {
		return 0, err
	}
	if inv.Sequence != m.Sequence {
		return 0, fmt.Errorf("the receiver's inventory is for sequence %d, not %d", inv.Sequence, m.Sequence)
	}
	needsBundle := false
	for _, n := range inv.Needs {
		needsBundle = needsBundle || n.Name != export.ExportFile
	}
	if inv.Active != nil && inv.Active.Digest == digest && !needsBundle {
		p.say("the receiver serves this export already, as sequence %d; nothing is sent", inv.Active.Sequence)
		return inv.Active.Sequence, nil
	}
	declared := map[string]export.Body{}
	for _, b := range m.Bodies() {
		declared[b.SHA256] = b
	}
	var sent []string
	for _, n := range inv.Needs {
		b, ok := declared[n.SHA256]
		if !ok || b.Size != n.Size {
			return 0, fmt.Errorf("the receiver needs a body, %s %s, that the manifest does not declare", n.Name, n.SHA256)
		}
		open := func() (io.ReadCloser, error) { return os.Open(bundles[b.Name]) }
		if b.Name == export.ExportFile {
			open = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(exportJSON)), nil }
		}
		if err := rc.putBody(ctx, m.Sequence, b, open); err != nil {
			return 0, err
		}
		sent = append(sent, b.Name)
	}
	c, err := rc.commit(ctx, m.Sequence)
	if err != nil {
		return 0, err
	}
	if c.Sequence != m.Sequence {
		return 0, fmt.Errorf("the receiver committed sequence %d, not %d", c.Sequence, m.Sequence)
	}
	if len(sent) == 0 {
		sent = []string{"nothing"}
	}
	p.say("sent %s; the receiver activated sequence %d, digest %s", strings.Join(sent, ", "), c.Sequence, short(digest))
	return c.Sequence, nil
}

// receiver is the receiver's upload endpoints under its base URL, and the
// token every request to them carries.
type receiver struct {
	base   string
	token  string
	client *http.Client
}

// checkReceiver holds --receiver to an http or https URL with a host, and
// no query or fragment the paths would be appended after.
func checkReceiver(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("--receiver %q: %w", raw, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("--receiver %q is not an http or https URL with a host, and no user, query or fragment", raw)
	}
	return nil
}

// newClient is the HTTP client uploads are made with. It follows no
// redirect and uses no proxy: it connects to the receiver configured, and
// neither a POST nor the token is ever sent elsewhere.
func newClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: commitTimeout,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}

// postManifest is step 1.
func (rc receiver) postManifest(ctx context.Context, signed []byte) (export.Inventory, error) {
	var inv export.Inventory
	err := rc.do(ctx, manifestTimeout, http.MethodPost, export.ManifestPath, "text/plain; charset=utf-8",
		func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(signed)), nil },
		int64(len(signed)), http.StatusOK, export.InventorySchema, &inv)
	return inv, err
}

// putBody is step 2, for one body: its bytes as they are, with their
// declared size as the Content-Length and no Content-Encoding.
func (rc receiver) putBody(ctx context.Context, sequence int64, b export.Body, open func() (io.ReadCloser, error)) error {
	return rc.do(ctx, bodyTimeout, http.MethodPut, export.BodyPath(sequence, b.SHA256), "application/octet-stream",
		open, b.Size, http.StatusNoContent, "", nil)
}

// commit is step 3.
func (rc receiver) commit(ctx context.Context, sequence int64) (export.Committed, error) {
	var c export.Committed
	err := rc.do(ctx, commitTimeout, http.MethodPost, export.CommitPath(sequence), "",
		nil, 0, http.StatusOK, export.CommittedSchema, &c)
	return c, err
}

// do makes one request and reads its reply. A refusal the receiver replies
// with is returned as an *export.Error; any other failure, in transit or
// a status with no refusal in it, is an error of its own, which upload
// retries.
func (rc receiver) do(ctx context.Context, timeout time.Duration, method, path, contentType string,
	open func() (io.ReadCloser, error), size int64, wantStatus int, schema string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.ReadCloser = http.NoBody
	if open != nil {
		var err error
		if body, err = open(); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, rc.base+path, body)
	if err != nil {
		body.Close()
		return err
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", export.Authorization(rc.token))
	resp, err := rc.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if err != nil {
		return fmt.Errorf("%s %s: reading the reply: %w", method, path, err)
	}
	if len(reply) > maxReply {
		return fmt.Errorf("%s %s: the reply is longer than %d bytes", method, path, maxReply)
	}
	if resp.StatusCode != wantStatus {
		var refusal export.ErrorReply
		if json.Unmarshal(reply, &refusal) == nil && refusal.Schema == export.ErrorSchema && refusal.Code != "" {
			return &export.Error{Code: refusal.Code, Message: refusal.Message, HighWater: refusal.HighWater, Needs: refusal.Needs}
		}
		return fmt.Errorf("%s %s: the receiver replied %s", method, path, resp.Status)
	}
	if into == nil {
		return nil
	}
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(reply, &head); err != nil || head.Schema != schema {
		return fmt.Errorf("%s %s: the reply is not %s", method, path, schema)
	}
	if err := json.Unmarshal(reply, into); err != nil {
		return fmt.Errorf("%s %s: the reply is not %s: %w", method, path, schema, err)
	}
	return nil
}

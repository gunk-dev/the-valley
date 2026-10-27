package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// The tests run the receiver as a process of its own: the test binary is
// valley-web-receive when VALLEY_WEB_RECEIVE_TEST_MAIN is set, and it runs
// the git VALLEY_WEB_RECEIVE_TEST_GIT names, when that is set. Uploads are
// the shared fixture's export, under manifests signed now with the
// fixture's key, and they are accepted with real git. valley-web is the
// real program, which VALLEY_WEB names.
func TestMain(m *testing.M) {
	if os.Getenv("VALLEY_WEB_RECEIVE_TEST_MAIN") == "1" {
		if git := os.Getenv("VALLEY_WEB_RECEIVE_TEST_GIT"); git != "" {
			export.GitProgram = git
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const token = "receiver-test-token-0123456789abcdef"

// How long a test waits for a request, a dial, or anything it polls for.
// Every wait here is bounded, so a receiver that stops answering fails the
// test rather than hanging it.
const (
	requestTimeout = 30 * time.Second
	dialTimeout    = 5 * time.Second
	waitTimeout    = 20 * time.Second
)

// client makes every request the tests make, each within requestTimeout,
// on a connection of its own that it closes after the reply. A connection
// kept for the next request would hold one of the receiver's, which the
// tests that fill them count on being free.
var client = &http.Client{
	Timeout: requestTimeout,
	Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext:       (&net.Dialer{Timeout: dialTimeout}).DialContext,
	},
}

// edge is a receiver running over a state directory, and the fixture it
// is sent.
type edge struct {
	t     *testing.T
	f     *exporttest.Fixture
	state string
	addr  string
	proc  *proc
}

// files writes the token file and the verifier file a receiver is given.
func files(t *testing.T) (tokenFile, verifiers string) {
	t.Helper()
	dir := t.TempDir()
	tokenFile, verifiers = filepath.Join(dir, "token"), filepath.Join(dir, "verifiers")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(verifiers, []byte(exporttest.Verifier().String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tokenFile, verifiers
}

// command is a program the tests run. start and output run it with
// exporttest.Start, so neither it nor any git it starts outlives the test,
// even when the test process dies first, as on a timeout or an external
// kill.
func command(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// output runs cmd, and returns what it wrote to stdout and stderr
// together. A cmd that has not exited within requestTimeout fails the
// test.
func output(t *testing.T, cmd *exec.Cmd) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := exporttest.Run(t, cmd, requestTimeout)
	return out.Bytes(), err
}

func receiveCmd(args ...string) *exec.Cmd {
	cmd := command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "VALLEY_WEB_RECEIVE_TEST_MAIN=1")
	return cmd
}

// newEdge starts a receiver over a new state directory, with the flags
// given after the ones every receiver here needs.
func newEdge(t *testing.T, f *exporttest.Fixture, more ...string) *edge {
	t.Helper()
	return newEdgeWithGit(t, f, "", more...)
}

// newEdgeWithGit starts a receiver that runs git, or the default git when
// git is "".
func newEdgeWithGit(t *testing.T, f *exporttest.Fixture, git string, more ...string) *edge {
	t.Helper()
	tokenFile, verifiers := files(t)
	state := filepath.Join(t.TempDir(), "state")
	cmd := receiveCmd(append([]string{"--state", state, "--listen", "127.0.0.1:0",
		"--instance", exporttest.Instance, "--destination", exporttest.Destination,
		"--verifiers", verifiers, "--token-file", tokenFile}, more...)...)
	if git != "" {
		cmd.Env = append(cmd.Env, "VALLEY_WEB_RECEIVE_TEST_GIT="+git)
	}
	p := start(t, cmd, "valley-web-receive")
	return &edge{t: t, f: f, state: state, proc: p, addr: p.addr}
}

// proc is a program start runs: where it listens, what it writes to
// stderr, and how it exits.
type proc struct {
	cmd  *exec.Cmd
	addr string
	log  *stderrLog
	// done is closed once the program has exited, and err is then how.
	done chan struct{}
	err  error
}

// stderrLog keeps what a program writes to stderr, and hands on its first
// line.
type stderrLog struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	first chan string
}

func (l *stderrLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	if line, _, ok := strings.Cut(l.buf.String(), "\n"); ok && l.first != nil {
		l.first <- line
		l.first = nil
	}
	return len(p), nil
}

func (l *stderrLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// start runs cmd with exporttest.Start and waits for it to say where it
// listens. The cleanup is registered as soon as cmd starts: it kills every
// process in cmd's session, even once cmd has exited, since a git cmd
// started may outlive it. It logs what cmd wrote when the test failed.
func start(t *testing.T, cmd *exec.Cmd, name string) *proc {
	t.Helper()
	p := &proc{cmd: cmd, log: &stderrLog{first: make(chan string, 1)}, done: make(chan struct{})}
	first := p.log.first
	cmd.Stderr = p.log
	kill, err := exporttest.Start(t, cmd)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		kill()
		<-p.done
		if t.Failed() {
			t.Logf("%s wrote:\n%s", name, p.log)
		}
	})
	select {
	case line := <-first:
		addr, ok := strings.CutPrefix(line, name+": listening on ")
		if !ok {
			t.Fatalf("%s printed %q", name, line)
		}
		p.addr = addr
		return p
	case <-p.done:
		t.Fatalf("%s exited with %v before it listened", name, p.err)
	case <-time.After(waitTimeout):
		t.Fatalf("%s did not say where it listens", name)
	}
	return nil
}

// logged reports whether the receiver has logged a line that holds every
// one of parts.
func (e *edge) logged(parts ...string) bool {
	for _, line := range strings.Split(e.proc.log.String(), "\n") {
		all := true
		for _, part := range parts {
			all = all && strings.Contains(line, part)
		}
		if all {
			return true
		}
	}
	return false
}

// manifest is the fixture's export under a manifest with sequence, signed
// with key, and the manifest itself.
func (e *edge) manifest(sequence int64, key ed25519.PrivateKey, destination string) ([]byte, *export.Manifest) {
	e.t.Helper()
	bundles := map[string]export.Body{}
	for _, p := range e.f.Manifest.Projects {
		bundles[p.Name] = p.Bundle
	}
	m, err := export.NewManifest(exporttest.Instance, destination, sequence, e.f.Export, e.f.ExportJSON, bundles)
	if err != nil {
		e.t.Fatal(err)
	}
	signed, err := m.Sign(exporttest.SignerName, key)
	if err != nil {
		e.t.Fatal(err)
	}
	return signed, m
}

// signedNow is the fixture's export under a manifest signed now.
func (e *edge) signedNow() ([]byte, *export.Manifest) {
	return e.manifest(time.Now().UnixNano(), exporttest.Key(), exporttest.Destination)
}

// do makes one request with the token within ctx, and returns the status,
// the reply and its header.
func (e *edge) do(ctx context.Context, method, path string, body []byte, header http.Header) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+e.addr+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", export.Authorization(token))
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	reply, err := io.ReadAll(res.Body)
	return res.StatusCode, reply, res.Header, err
}

// request makes one request with the token, and returns the status and the
// reply. A request not answered within requestTimeout fails the test.
func (e *edge) request(method, path string, body []byte, header http.Header) (int, []byte, http.Header) {
	e.t.Helper()
	status, reply, h, err := e.do(context.Background(), method, path, body, header)
	if err != nil {
		e.t.Fatal(err)
	}
	return status, reply, h
}

// dial opens a connection to the receiver, which the test closes when it
// ends if nothing has before.
func (e *edge) dial() net.Conn {
	e.t.Helper()
	c, err := net.DialTimeout("tcp", e.addr, dialTimeout)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { c.Close() })
	return c
}

// idle makes one request with the token on a connection of its own, a
// commit of an upload never staged, which the receiver refuses and keeps
// the connection open after. It returns the connection, which then holds
// one of the receiver's connections until it is closed or the receiver's
// idle timeout.
func (e *edge) idle() net.Conn {
	e.t.Helper()
	for range 100 {
		c := e.dial()
		fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\nContent-Length: 0\r\n\r\n",
			export.CommitPath(1), e.addr, export.Authorization(token))
		c.SetReadDeadline(time.Now().Add(requestTimeout))
		res, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			e.t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		c.SetReadDeadline(time.Time{})
		switch {
		case res.StatusCode == http.StatusServiceUnavailable:
			// A request just answered had not yet given its slot back.
			c.Close()
			time.Sleep(20 * time.Millisecond)
		case res.Close:
			e.t.Fatalf("the receiver closed the connection after a %d", res.StatusCode)
		default:
			return c
		}
	}
	e.t.Fatal("the receiver never had a request slot free")
	return nil
}

// stall sends the headers of a request with content, and one byte of it,
// on a connection of its own. The request then holds one of the
// receiver's request slots while the receiver waits for the rest. stall
// returns nil when the receiver answered at once: it had no slot free, or
// it refused the request.
func (e *edge) stall(method, path string, content []byte) net.Conn {
	e.t.Helper()
	c := e.dial()
	fmt.Fprintf(c, "%s %s HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\nContent-Length: %d\r\n\r\n",
		method, path, e.addr, export.Authorization(token), len(content))
	c.Write(content[:1])
	if answered(c, 200*time.Millisecond) {
		c.Close()
		return nil
	}
	return c
}

// stallAll holds every request slot with a stalled manifest, signed.
func (e *edge) stallAll(signed []byte) []net.Conn {
	e.t.Helper()
	var stalled []net.Conn
	for range 100 {
		if c := e.stall(http.MethodPost, export.ManifestPath, signed); c != nil {
			stalled = append(stalled, c)
		}
		if len(stalled) == maxRequests {
			return stalled
		}
	}
	e.t.Fatalf("only %d of %d stalled manifests held a request slot", len(stalled), maxRequests)
	return nil
}

// answered reports whether anything arrives on c within d, or c is closed.
// It reads nothing when nothing arrives.
func answered(c net.Conn, d time.Duration) bool {
	c.SetReadDeadline(time.Now().Add(d))
	defer c.SetReadDeadline(time.Time{})
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return !(errors.As(err, &ne) && ne.Timeout())
}

// refused checks that a reply is the refusal with code, and returns it.
func refused(t *testing.T, what string, status int, reply []byte, code export.Code) export.ErrorReply {
	t.Helper()
	var r export.ErrorReply
	if err := json.Unmarshal(reply, &r); err != nil || r.Schema != export.ErrorSchema || r.Code != code || status != code.Status() {
		t.Errorf("%s: the receiver replied %d %s, want %d and %s", what, status, reply, code.Status(), code)
	}
	return r
}

// post posts the signed manifest, and returns its inventory.
func (e *edge) post(signed []byte) export.Inventory {
	e.t.Helper()
	status, reply, _ := e.request(http.MethodPost, export.ManifestPath, signed, nil)
	var inv export.Inventory
	if err := json.Unmarshal(reply, &inv); status != http.StatusOK || err != nil || inv.Schema != export.InventorySchema {
		e.t.Fatalf("the manifest was answered %d %s", status, reply)
	}
	return inv
}

// put sends one body the manifest declares, and fails the test unless it
// is accepted. A 503 is retried, as the publisher retries it: a request
// that staged the body may not have given it up yet.
func (e *edge) put(m *export.Manifest, name string) {
	e.t.Helper()
	b := body(e.t, m, name)
	var status int
	var reply []byte
	for range 50 {
		status, reply, _ = e.request(http.MethodPut, export.BodyPath(m.Sequence, b.SHA256), e.f.BodyBytes(e.t, name), nil)
		if status != http.StatusServiceUnavailable {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != http.StatusNoContent {
		e.t.Fatalf("body %s was answered %d %s", name, status, reply)
	}
}

func body(t *testing.T, m *export.Manifest, name string) export.Body {
	t.Helper()
	for _, b := range m.Bodies() {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("the manifest declares no body %s", name)
	return export.Body{}
}

// commit commits the upload, and fails the test unless it is activated.
func (e *edge) commit(m *export.Manifest) {
	e.t.Helper()
	status, reply, _ := e.request(http.MethodPost, export.CommitPath(m.Sequence), nil, nil)
	var c export.Committed
	if err := json.Unmarshal(reply, &c); status != http.StatusOK || err != nil || c.Sequence != m.Sequence {
		e.t.Fatalf("the commit was answered %d %s", status, reply)
	}
}

// upload runs the three steps for a manifest signed now, and returns it.
func (e *edge) upload() *export.Manifest {
	e.t.Helper()
	signed, m := e.signedNow()
	for _, n := range e.post(signed).Needs {
		e.put(m, n.Name)
	}
	e.commit(m)
	return m
}

// highWater is the state directory's high-water mark, 0 for none.
func (e *edge) highWater() int64 {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.state, "high-water"))
	if os.IsNotExist(err) {
		return 0
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || perr != nil {
		e.t.Fatalf("high-water: %v %v", err, perr)
	}
	return n
}

// staged are the entries of the state directory's staging area.
func (e *edge) staged() []string {
	e.t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.state, "staging"))
	if err != nil {
		e.t.Fatal(err)
	}
	var names []string
	for _, d := range entries {
		names = append(names, d.Name())
	}
	return names
}

// valleyWeb starts the real valley-web over the edge's state directory,
// and returns where it listens.
func (e *edge) valleyWeb() string {
	e.t.Helper()
	bin := os.Getenv("VALLEY_WEB")
	if bin == "" {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			e.t.Fatal("VALLEY_WEB names no valley-web, and VALLEY_TEST_TOOLS=required")
		}
		e.t.Skip("VALLEY_WEB names no valley-web; the nix check provides one")
	}
	return start(e.t, command(bin, "--state", e.state, "--listen", "127.0.0.1:0"), "valley-web").addr
}

// serves checks that valley-web at addr answers every page of the
// fixture's export.
func (e *edge) serves(addr string) {
	e.t.Helper()
	tip := e.f.Export.Projects[0].SourceTip
	for target, status := range map[string]int{
		"/":                             http.StatusOK,
		"/app/knots/oc-a000002":         http.StatusOK,
		"/app/source/tree/" + tip + "/": http.StatusOK,
		"/app/source/log/" + tip:        http.StatusOK,
		"/site/source/":                 http.StatusNotFound,
	} {
		res, err := client.Get("http://" + addr + target)
		if err != nil {
			e.t.Fatal(err)
		}
		page, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != status {
			e.t.Errorf("valley-web answered GET %s with %d, want %d:\n%s", target, res.StatusCode, status, page)
		}
	}
}

// An upload of the three steps activates the export, and valley-web, as a
// process of its own over the same state directory, serves it. The
// manifest posted again, and the commit made again, are refused as
// replayed, with the high-water mark.
func TestAnUploadActivatesAndValleyWebServesIt(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	signed, m := e.signedNow()
	inv := e.post(signed)
	if inv.Active != nil || len(inv.Needs) != 2 || inv.Needs[0].Name != export.ExportFile || inv.Needs[1].Name != "app" {
		t.Fatalf("the first inventory is %+v, want no active export and both bodies needed, export.json first", inv)
	}
	again := e.post(signed)
	if len(again.Needs) != 2 {
		t.Errorf("the manifest posted again needs %+v, want both bodies still", again.Needs)
	}
	e.put(m, export.ExportFile)
	if needs := e.post(signed).Needs; len(needs) != 1 || needs[0].Name != "app" {
		t.Errorf("the manifest posted after export.json needs %+v, want app's bundle alone", needs)
	}
	e.put(m, "app")
	e.put(m, "app") // a body already staged is accepted again
	e.commit(m)
	if hw := e.highWater(); hw != m.Sequence {
		t.Errorf("high-water is %d after the commit, want %d", hw, m.Sequence)
	}
	e.serves(e.valleyWeb())

	status, reply, _ := e.request(http.MethodPost, export.ManifestPath, signed, nil)
	if r := refused(t, "the manifest posted after its commit", status, reply, export.CodeReplayed); r.HighWater != m.Sequence {
		t.Errorf("the replayed refusal gives high-water %d, want %d", r.HighWater, m.Sequence)
	}
	status, reply, _ = e.request(http.MethodPost, export.CommitPath(m.Sequence), nil, nil)
	refused(t, "the commit made again", status, reply, export.CodeReplayed)

	// The next upload carries app's repository over, and needs export.json
	// alone.
	signed, m = e.signedNow()
	if needs := e.post(signed).Needs; len(needs) != 1 || needs[0].Name != export.ExportFile {
		t.Errorf("the next upload needs %+v, want export.json alone", needs)
	}
	e.put(m, export.ExportFile)
	e.commit(m)
}

// A request without the token is refused as unauthenticated, whatever it
// asks, and stages nothing. So is one with another token, or with the
// token twice.
func TestAnUnauthenticatedRequestIsRefused(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	signed, m := e.signedNow()
	send := func(what, method, path string, body []byte, auth []string) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+e.addr+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header["Authorization"] = auth
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		reply, _ := io.ReadAll(res.Body)
		res.Body.Close()
		refused(t, what, res.StatusCode, reply, export.CodeUnauthenticated)
		if !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer ") {
			t.Errorf("%s: the refusal names no scheme to authenticate with", what)
		}
	}
	send("a manifest with no token", http.MethodPost, export.ManifestPath, signed, nil)
	send("a manifest with another token", http.MethodPost, export.ManifestPath, signed, []string{export.Authorization(token + "x")})
	send("a manifest with the token in another scheme", http.MethodPost, export.ManifestPath, signed, []string{"Basic " + token})
	send("a manifest with the token twice", http.MethodPost, export.ManifestPath, signed,
		[]string{export.Authorization(token), export.Authorization(token)})
	send("a body with no token", http.MethodPut, export.BodyPath(m.Sequence, m.File.SHA256), e.f.ExportJSON, nil)
	send("a commit with no token", http.MethodPost, export.CommitPath(m.Sequence), nil, nil)
	send("a path the upload does not use", http.MethodGet, "/", nil, nil)
	if staged := e.staged(); len(staged) != 0 || e.highWater() != 0 {
		t.Errorf("unauthenticated requests left %v staged and high-water %d", staged, e.highWater())
	}
}

// A manifest is refused when it is stale, signed ahead of the receiver's
// clock, signed by another key, or for another receiver; and an older
// manifest than the one staged is superseded. None of them is staged.
func TestAManifestIsAdmittedOnlyWhenFresh(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	now := time.Now()
	for _, c := range []struct {
		what        string
		at          time.Time
		key         ed25519.PrivateKey
		destination string
		code        export.Code
	}{
		{"a manifest signed four days ago", now.Add(-96 * time.Hour), exporttest.Key(), exporttest.Destination, export.CodeOutOfWindow},
		{"a manifest signed two minutes ahead", now.Add(2 * time.Minute), exporttest.Key(), exporttest.Destination, export.CodeOutOfWindow},
		{"a manifest signed by another key", now, other, exporttest.Destination, export.CodeBadSignature},
		{"a manifest for another receiver", now, exporttest.Key(), "elsewhere.example.org", export.CodeWrongDestination},
	} {
		signed, _ := e.manifest(c.at.UnixNano(), c.key, c.destination)
		status, reply, _ := e.request(http.MethodPost, export.ManifestPath, signed, nil)
		refused(t, c.what, status, reply, c.code)
	}
	if staged := e.staged(); len(staged) != 0 {
		t.Errorf("refused manifests left %v staged", staged)
	}

	older, om := e.manifest(now.Add(-time.Minute).UnixNano(), exporttest.Key(), exporttest.Destination)
	newer, _ := e.signedNow()
	e.post(newer)
	status, reply, _ := e.request(http.MethodPost, export.ManifestPath, older, nil)
	refused(t, "a manifest older than the one staged", status, reply, export.CodeSuperseded)
	status, reply, _ = e.request(http.MethodPut, export.BodyPath(om.Sequence, om.File.SHA256), e.f.ExportJSON, nil)
	refused(t, "a body of the older upload", status, reply, export.CodeSuperseded)
	status, reply, _ = e.request(http.MethodPost, export.CommitPath(om.Sequence+1e12), nil, nil)
	refused(t, "a commit of an upload never staged", status, reply, export.CodeNoManifest)
}

// A body is refused when its bytes are not what the manifest declares: a
// hash mismatch, a size mismatch whether or not the length is given, a
// Content-Encoding, or a hash the manifest does not declare. An upload
// cut off part way through a body is refused at the commit as incomplete,
// and the active export stays as it was until the body is sent whole.
func TestABodyIsKeptOnlyWhenItVerifies(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	first := e.upload()
	signed, m := e.signedNow()
	// A second upload of the same export needs export.json again, staged
	// for it, and no bundle: app's repository is carried over.
	if needs := e.post(signed).Needs; len(needs) != 1 {
		t.Fatalf("the second upload needs %+v, want export.json alone", needs)
	}
	file := body(t, m, export.ExportFile)
	path := export.BodyPath(m.Sequence, file.SHA256)
	wrong := append([]byte{}, e.f.ExportJSON...)
	wrong[0] ^= 1

	status, reply, _ := e.request(http.MethodPut, path, wrong, nil)
	refused(t, "a body with other bytes of the same size", status, reply, export.CodeHashMismatch)
	status, reply, _ = e.request(http.MethodPut, path, e.f.ExportJSON[1:], nil)
	refused(t, "a body one byte short", status, reply, export.CodeSizeMismatch)
	status, reply, _ = e.request(http.MethodPut, path, e.f.ExportJSON, http.Header{"Content-Encoding": {"gzip"}})
	refused(t, "a body with a Content-Encoding", status, reply, export.CodeEncoding)
	status, reply, _ = e.request(http.MethodPut, export.BodyPath(m.Sequence, strings.Repeat("0", 64)), e.f.ExportJSON, nil)
	refused(t, "a body the manifest does not declare", status, reply, export.CodeNotDeclared)

	// Sent with no length, one byte long.
	req, err := http.NewRequest(http.MethodPut, "http://"+e.addr+path, io.MultiReader(bytes.NewReader(e.f.ExportJSON), strings.NewReader("x")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", export.Authorization(token))
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = io.ReadAll(res.Body)
	res.Body.Close()
	refused(t, "a body one byte long, sent with no length", res.StatusCode, reply, export.CodeSizeMismatch)

	// Sent while another request is staging it: staging holds one partial
	// file of each body.
	stalled := e.stall(http.MethodPut, path, e.f.ExportJSON)
	if stalled == nil {
		t.Fatal("the receiver answered a stalled body at once")
	}
	status, reply, header := e.request(http.MethodPut, path, e.f.ExportJSON, nil)
	if status != http.StatusServiceUnavailable || header.Get("Retry-After") == "" {
		t.Errorf("a body sent while another request stages it was answered %d %s, want 503 with Retry-After", status, reply)
	}
	stalled.Close()

	// Cut off half way.
	c := e.dial()
	fmt.Fprintf(c, "PUT %s HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\nContent-Length: %d\r\n\r\n", path, e.addr, export.Authorization(token), file.Size)
	c.Write(e.f.ExportJSON[:file.Size/2])
	c.Close()

	status, reply, _ = e.request(http.MethodPost, export.CommitPath(m.Sequence), nil, nil)
	r := refused(t, "a commit after the body was cut off", status, reply, export.CodeIncomplete)
	if len(r.Needs) != 1 || r.Needs[0].SHA256 != file.SHA256 {
		t.Errorf("the incomplete refusal needs %+v, want export.json", r.Needs)
	}
	if hw := e.highWater(); hw != first.Sequence {
		t.Errorf("high-water moved to %d on refused bodies, want %d", hw, first.Sequence)
	}
	// The receiver removes what it read of the cut-off body once it sees
	// the connection close.
	waitFor(t, "no body left in staging", func(context.Context) bool {
		partial, _ := filepath.Glob(filepath.Join(e.state, "staging", "*", "bodies", "*"))
		return len(partial) == 0
	})
	e.put(m, export.ExportFile)
	e.commit(m)
}

// A manifest whose body is larger than --max-body, or whose bodies
// together are larger than --max-upload, is refused as too-large and
// staged not at all.
func TestAnOversizedUploadIsRefused(t *testing.T) {
	f := exporttest.New(t)
	size := func(name string) int64 { return int64(len(f.BodyBytes(t, name))) }
	for _, c := range []struct {
		what  string
		flags []string
	}{
		{"a body over --max-body", []string{"--max-body", strconv.FormatInt(size(export.ExportFile)-1, 10)}},
		{"bodies over --max-upload", []string{"--max-upload", strconv.FormatInt(size(export.ExportFile)+size("app")-1, 10)}},
	} {
		e := newEdge(t, f, c.flags...)
		signed, _ := e.signedNow()
		status, reply, _ := e.request(http.MethodPost, export.ManifestPath, signed, nil)
		refused(t, c.what, status, reply, export.CodeTooLarge)
		if staged := e.staged(); len(staged) != 0 {
			t.Errorf("%s: the refused manifest left %v staged", c.what, staged)
		}
	}
	// At the caps exactly, the upload is accepted.
	e := newEdge(t, f, "--max-body", strconv.FormatInt(max(size(export.ExportFile), size("app")), 10),
		"--max-upload", strconv.FormatInt(size(export.ExportFile)+size("app"), 10))
	e.upload()
}

// A saturated receiver answers 503 to a request past its slots, and
// valley-web, a process of its own over the same state directory, keeps
// serving every page. A request without the token is still refused as
// unauthenticated, taking no slot. Once the stalled requests go away, the
// receiver takes uploads again.
//
// Then every connection the receiver takes is held by a client that sends
// part of a request's headers and stops. valley-web keeps serving. A body
// sent meanwhile waits in the listen queue until the header timeout closes
// those connections, and no longer.
func TestASaturatedUploadLeavesValleyWebServing(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	e.upload()
	web := e.valleyWeb()
	signed, m := e.signedNow()
	e.post(signed)
	probe := export.BodyPath(m.Sequence, strings.Repeat("0", 64))

	stalled := e.stallAll(signed)
	status, _, header := e.request(http.MethodPut, probe, []byte("x"), nil)
	if status != http.StatusServiceUnavailable || header.Get("Retry-After") == "" {
		t.Errorf("a request past every slot was answered %d, want 503 with Retry-After", status)
	}
	res, err := client.Post("http://"+e.addr+export.ManifestPath, "", bytes.NewReader(signed))
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(res.Body)
	res.Body.Close()
	refused(t, "a manifest with no token while the receiver is saturated", res.StatusCode, reply, export.CodeUnauthenticated)
	e.serves(web)

	for _, c := range stalled {
		c.Close()
	}
	waitFor(t, "the receiver to take requests again", func(ctx context.Context) bool {
		status, _, _, err := e.do(ctx, http.MethodPut, probe, []byte("x"), nil)
		return err == nil && status == http.StatusNotFound
	})

	for range maxConns {
		fmt.Fprintf(e.dial(), "PUT %s HTTP/1.1\r\nHost: %s\r\n", probe, e.addr)
	}
	began := time.Now()
	e.serves(web)
	e.put(m, export.ExportFile)
	if took := time.Since(began); took < headerTimeout/2 || took > headerTimeout+5*time.Second {
		t.Errorf("a body sent behind a connection on every slot was answered after %v, want about the header timeout, %v", took, headerTimeout)
	}
	e.commit(m)
	e.serves(web)
}

// OPTIONS * is the receiver's to answer, as any other request is: refused
// as unauthenticated without the token, and 404 with it. Either is
// answered at once, without reading the body it declares, and its
// connection is closed. So OPTIONS * requests that withhold their bodies,
// one on every connection the receiver takes, hold none of them, and an
// upload goes through.
func TestOptionsStarHoldsNoConnection(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	conns := make([]net.Conn, maxConns)
	for i := range conns {
		auth := ""
		if i%2 == 1 {
			auth = "Authorization: " + export.Authorization(token) + "\r\n"
		}
		conns[i] = e.dial()
		fmt.Fprintf(conns[i], "OPTIONS * HTTP/1.1\r\nHost: %s\r\n%sContent-Length: 1\r\n\r\n", e.addr, auth)
	}
	e.upload()
	for i, c := range conns {
		c.SetReadDeadline(time.Now().Add(requestTimeout))
		res, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatalf("OPTIONS * on connection %d was not answered: %v", i, err)
		}
		reply, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if i%2 == 0 {
			refused(t, "OPTIONS * with no token", res.StatusCode, reply, export.CodeUnauthenticated)
		} else if res.StatusCode != http.StatusNotFound {
			t.Errorf("OPTIONS * with the token was answered %d, want 404", res.StatusCode)
		}
		if !res.Close {
			t.Errorf("the receiver kept the connection of OPTIONS * open")
		}
	}
}

// A manifest waits for the store's lock, which a commit holds throughout,
// and stages nothing once its client goes away. Here the test holds the
// lock, as a commit does.
func TestAManifestStopsWaitingWhenItsClientGoesAway(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	e.upload()
	lock, err := os.OpenFile(filepath.Join(e.state, "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	signed, _ := e.signedNow()
	c := e.dial()
	fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\nContent-Length: %d\r\n\r\n%s",
		export.ManifestPath, e.addr, export.Authorization(token), len(signed), signed)
	if answered(c, time.Second) {
		t.Fatal("the manifest was answered while the store's lock was held")
	}
	c.Close()
	waitFor(t, "the receiver to give the manifest up", func(context.Context) bool {
		return e.logged(export.ManifestPath, "waiting for the lock", "context canceled")
	})
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if staged := e.staged(); len(staged) != 0 {
		t.Errorf("a manifest whose client went away left %v staged", staged)
	}
	e.upload()
}

// A manifest whose upload would take the state directory past
// --max-storage is refused as too-large, and nothing is staged. The cap
// counts a repository for every project, so a manifest that names one
// bundle for many projects, which --max-upload counts once, is held to it
// too. An upload within the cap is accepted.
func TestAnUploadPastTheStorageCapIsRefused(t *testing.T) {
	e := newEdge(t, exporttest.New(t), "--max-storage", "4M")
	x := *e.f.Export
	x.Projects = append([]export.Project{}, x.Projects...)
	bundles := map[string]export.Body{}
	for _, p := range e.f.Manifest.Projects {
		bundles[p.Name] = p.Bundle
	}
	for i := range 6 {
		p := x.Projects[0]
		p.Name = fmt.Sprintf("%s-%d", p.Name, i)
		x.Projects = append(x.Projects, p)
		bundles[p.Name] = bundles[x.Projects[0].Name]
	}
	m, err := export.NewManifest(exporttest.Instance, exporttest.Destination, time.Now().UnixNano(), &x, e.f.ExportJSON, bundles)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CheckSizes(1<<30, 2<<30); err != nil {
		t.Fatalf("the manifest is not within --max-upload: %v", err)
	}
	signed, err := m.Sign(exporttest.SignerName, exporttest.Key())
	if err != nil {
		t.Fatal(err)
	}
	status, reply, _ := e.request(http.MethodPost, export.ManifestPath, signed, nil)
	refused(t, "a manifest naming one bundle for eight projects", status, reply, export.CodeTooLarge)
	if staged := e.staged(); len(staged) != 0 {
		t.Errorf("the refused manifest left %v staged", staged)
	}
	e.upload()
	e.upload()
}

// openPartials are the partial body files the receiver holds open, as
// /proc names them. A file deleted while it is open keeps its name there.
func (e *edge) openPartials() []string {
	e.t.Helper()
	fds, err := filepath.Glob(fmt.Sprintf("/proc/%d/fd/*", e.proc.cmd.Process.Pid))
	if err != nil {
		e.t.Fatal(err)
	}
	var open []string
	for _, fd := range fds {
		if target, err := os.Readlink(fd); err == nil && strings.Contains(target, "/.partial-") {
			open = append(open, target)
		}
	}
	return open
}

// A newer manifest stops the older upload's body requests before it
// deletes the older upload. Each is answered as superseded, and once the
// newer manifest is answered the receiver holds no partial file of the
// older upload open, where the disk it takes would be counted by nothing.
// The newer upload's request for the same body is then accepted, not
// answered 503 as a body another request is staging.
func TestANewerManifestStopsTheOlderUploadsBodies(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	older, om := e.manifest(time.Now().Add(-time.Minute).UnixNano(), exporttest.Key(), exporttest.Destination)
	e.post(older)
	file := body(t, om, export.ExportFile)
	stalled := e.stall(http.MethodPut, export.BodyPath(om.Sequence, file.SHA256), e.f.ExportJSON)
	if stalled == nil {
		t.Fatal("the receiver answered a stalled body at once")
	}
	waitFor(t, "the receiver to open the stalled body's partial file", func(context.Context) bool {
		return len(e.openPartials()) == 1
	})

	newer, nm := e.signedNow()
	e.post(newer)
	if open := e.openPartials(); len(open) != 0 {
		t.Errorf("once the newer manifest was answered, the receiver holds %q open", open)
	}
	stalled.SetReadDeadline(time.Now().Add(requestTimeout))
	res, err := http.ReadResponse(bufio.NewReader(stalled), nil)
	if err != nil {
		t.Fatalf("the older upload's stalled body was not answered: %v", err)
	}
	reply, _ := io.ReadAll(res.Body)
	res.Body.Close()
	refused(t, "the older upload's stalled body", res.StatusCode, reply, export.CodeSuperseded)
	if staged := e.staged(); len(staged) != 1 || staged[0] != strconv.FormatInt(nm.Sequence, 10) {
		t.Errorf("staging holds %v, want the newer upload alone", staged)
	}
	status, reply, _ := e.request(http.MethodPut, export.BodyPath(nm.Sequence, file.SHA256), e.f.ExportJSON, nil)
	if status != http.StatusNoContent {
		t.Errorf("the newer upload's export.json was answered %d %s, want 204", status, reply)
	}
	for _, n := range e.post(newer).Needs {
		e.put(nm, n.Name)
	}
	e.commit(nm)
}

// A generation a reader holds is kept past the activation that replaces
// it, and counts against --max-storage. While a reader holds a large one,
// a manifest whose upload would pass the cap is refused as too-large. Once
// the reader lets it go, the next manifest finds the room: the receiver
// deletes the generation before it refuses anything for space.
func TestAReleasedGenerationMakesRoomForAnUpload(t *testing.T) {
	e := newEdge(t, exporttest.New(t), "--max-storage", "6M")
	first := e.upload()
	reader, err := export.OpenReadOnly(e.state)
	if err != nil {
		t.Fatal(err)
	}
	g, release, err := reader.Open()
	defer release()
	if err != nil || g.Sequence != first.Sequence {
		t.Fatalf("the reader opened %v, %v; want the first generation", g, err)
	}
	second := e.upload()
	// A sparse file makes the held generation large: the cap counts a
	// file at its size, whatever blocks it takes.
	pad, err := os.Create(filepath.Join(g.Dir, "padding"))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(pad.Truncate(8<<20), pad.Close()); err != nil {
		t.Fatal(err)
	}

	signed, _ := e.signedNow()
	status, reply, _ := e.request(http.MethodPost, export.ManifestPath, signed, nil)
	refused(t, "a manifest while a reader holds a large generation", status, reply, export.CodeTooLarge)
	if hw := e.highWater(); hw != second.Sequence {
		t.Errorf("high-water is %d, want %d", hw, second.Sequence)
	}
	release()
	third := e.upload()
	if gens, _ := filepath.Glob(filepath.Join(e.state, "exports", "*")); len(gens) != 1 || filepath.Base(gens[0]) != strconv.FormatInt(third.Sequence, 10) {
		t.Errorf("the state directory holds the generations %v, want the third alone", gens)
	}
}

// waitFor polls done until it reports true, and fails the test unless it
// does within waitTimeout. done is given a context that ends then, which
// bounds whatever request it makes.
func waitFor(t *testing.T, what string, done func(ctx context.Context) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	for ctx.Err() == nil {
		if done(ctx) {
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The receiver stops on SIGTERM within its stop and drain timeouts, even
// with every connection it takes held: four by manifests stalled part
// way, which hold every request slot, and the rest by idle connections. A
// further connection waits in the listen queue.
func TestStopsOnSIGTERM(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	for range maxConns - maxRequests {
		e.idle()
	}
	signed, _ := e.signedNow()
	e.stallAll(signed)
	fmt.Fprintf(e.dial(), "OPTIONS * HTTP/1.1\r\nHost: %s\r\nContent-Length: 1\r\n\r\n", e.addr)
	began := time.Now()
	if err := e.proc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.proc.done:
	case <-time.After(stopTimeout + drainTimeout + 10*time.Second):
		t.Fatalf("valley-web-receive did not stop within %v of SIGTERM", stopTimeout+drainTimeout+10*time.Second)
	}
	if e.proc.err != nil {
		t.Errorf("valley-web-receive stopped with %v", e.proc.err)
	}
	if took := time.Since(began); took > stopTimeout+drainTimeout+5*time.Second {
		t.Errorf("valley-web-receive took %v to stop, past its stop and drain timeouts of %v and %v", took, stopTimeout, drainTimeout)
	}
}

// stallingGit is a git that stalls the first time it is asked to write a
// commit-graph, once it has fetched the bundle, and is the real git
// otherwise. The git that stalls is a shell that starts a child and waits
// for it. The child holds git's output open, so the receiver reads git's
// output to its end only once the child is gone too. stalled waits for the
// stall and returns the shell's process ID and the child's.
func stallingGit(t *testing.T) (git string, stalled func() (shell, child int)) {
	t.Helper()
	real, err := exec.LookPath(export.GitProgram)
	if err != nil {
		t.Fatal(err)
	}
	if real, err = filepath.Abs(real); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pids, once, git := filepath.Join(dir, "pids"), filepath.Join(dir, "stalled"), filepath.Join(dir, "git")
	script := `#!/bin/sh
for a; do
	if [ "$a" = commit-graph ] && mkdir '` + once + `' 2> /dev/null; then
		sleep 600 &
		echo $$ $! > '` + pids + `.tmp' && mv '` + pids + `.tmp' '` + pids + `'
		wait
	fi
done
exec '` + real + `' "$@"
`
	if err := os.WriteFile(git, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return git, func() (shell, child int) {
		t.Helper()
		waitFor(t, "the commit's git to stall", func(context.Context) bool {
			data, err := os.ReadFile(pids)
			_, scanErr := fmt.Sscan(string(data), &shell, &child)
			return err == nil && scanErr == nil
		})
		return shell, child
	}
}

// running reports whether the process pid has not exited. A zombie, which
// has exited and is not yet reaped, is not running.
func running(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	i := bytes.LastIndexByte(data, ')')
	state := strings.Fields(string(data[i+1:]))
	return len(state) > 0 && state[0] != "Z" && state[0] != "X"
}

// A commit whose client goes away stops the git it runs, with every
// process that git started, even one that holds git's output open. The
// request then ends: it gives back the store's lock and its request slot,
// and keeps the bundle it was sent. So the manifest posted again needs
// nothing, and the commit made again activates the upload.
func TestACommitWhoseClientGoesAwayStopsItsGit(t *testing.T) {
	git, stalled := stallingGit(t)
	e := newEdgeWithGit(t, exporttest.New(t), git)
	signed, m := e.signedNow()
	for _, n := range e.post(signed).Needs {
		e.put(m, n.Name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	committed := make(chan error, 1)
	go func() {
		_, _, _, err := e.do(ctx, http.MethodPost, export.CommitPath(m.Sequence), nil, nil)
		committed <- err
	}()
	shell, child := stalled()
	cancel()
	if err := <-committed; err == nil {
		t.Error("the commit was answered, though its client went away")
	}
	waitFor(t, "the commit's git and its child to be killed", func(context.Context) bool {
		return !running(shell) && !running(child)
	})
	waitFor(t, "the receiver to give the commit up", func(context.Context) bool {
		return e.logged(export.CommitPath(m.Sequence), "git commit-graph")
	})
	if needs := e.post(signed).Needs; len(needs) != 0 {
		t.Errorf("once the commit was given up the upload needs %+v, want nothing", needs)
	}
	e.commit(m)
}

// A stop that ends a commit waits for the commit's git to stop before the
// receiver exits, so neither git nor any process git started outlives
// it, even one that holds git's output open.
func TestAStopWaitsForTheGitACommitRuns(t *testing.T) {
	git, stalled := stallingGit(t)
	e := newEdgeWithGit(t, exporttest.New(t), git)
	signed, m := e.signedNow()
	for _, n := range e.post(signed).Needs {
		e.put(m, n.Name)
	}
	committed := make(chan error, 1)
	go func() {
		_, _, _, err := e.do(context.Background(), http.MethodPost, export.CommitPath(m.Sequence), nil, nil)
		committed <- err
	}()
	shell, child := stalled()

	if err := e.proc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.proc.done:
	case <-time.After(stopTimeout + drainTimeout + 10*time.Second):
		t.Fatalf("valley-web-receive did not stop within %v of SIGTERM", stopTimeout+drainTimeout+10*time.Second)
	}
	if e.proc.err != nil {
		t.Errorf("valley-web-receive stopped with %v", e.proc.err)
	}
	if running(shell) || running(child) {
		t.Errorf("the commit's git, process %d, or its child, process %d, outlived the receiver", shell, child)
	}
	if e.logged("requests were still running") {
		t.Error("the receiver exited before the commit it stopped had finished")
	}
	if err := <-committed; err == nil {
		t.Error("the commit was answered, though the receiver stopped during it")
	}
	if hw := e.highWater(); hw != 0 {
		t.Errorf("high-water is %d after a commit the stop ended, want 0", hw)
	}
}

// A command line it cannot run with gets the usage and exit status 2; a
// token file it cannot use, or verifiers it cannot read, exit status 1;
// --help gets the usage alone.
func TestUsage(t *testing.T) {
	tokenFile, verifiers := files(t)
	state := t.TempDir()
	base := []string{"--state", state, "--listen", "127.0.0.1:0", "--instance", "valley-host", "--destination", "example.org"}
	for _, args := range [][]string{
		{},
		append(base, "--verifiers", verifiers),
		append(base, "--token-file", tokenFile),
		{"--state", state, "--listen", "127.0.0.1:0", "--instance", "a b", "--destination", "example.org", "--verifiers", verifiers, "--token-file", tokenFile},
		append(base, "--verifiers", verifiers, "--token-file", tokenFile, "--max-body", "0"),
		append(base, "--verifiers", verifiers, "--token-file", tokenFile, "extra"),
	} {
		out, err := output(t, receiveCmd(args...))
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "usage: valley-web-receive") {
			t.Errorf("valley-web-receive %q gave %v:\n%s", args, err, out)
		}
	}
	short := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(short, []byte("too-short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		append(base, "--verifiers", verifiers, "--token-file", short),
		append(base, "--verifiers", tokenFile, "--token-file", tokenFile),
	} {
		out, err := output(t, receiveCmd(args...))
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Errorf("valley-web-receive %q gave %v:\n%s", args, err, out)
		}
		if bytes.Contains(out, []byte(token)) {
			t.Errorf("valley-web-receive %q printed the token", args)
		}
	}
	out, err := output(t, receiveCmd("--help"))
	if err != nil || !strings.HasPrefix(string(out), "usage: valley-web-receive") {
		t.Errorf("valley-web-receive --help gave %v:\n%s", err, out)
	}
}

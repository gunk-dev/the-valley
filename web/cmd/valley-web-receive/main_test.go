package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// The tests run the receiver as a process of its own: the test binary is
// valley-web-receive when VALLEY_WEB_RECEIVE_TEST_MAIN is set. Uploads are
// the shared fixture's export, under manifests signed now with the
// fixture's key, and they are accepted with real git. valley-web is the
// real program, which VALLEY_WEB names.
func TestMain(m *testing.M) {
	if os.Getenv("VALLEY_WEB_RECEIVE_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const token = "receiver-test-token-0123456789abcdef"

// edge is a receiver running over a state directory, and the fixture it
// is sent.
type edge struct {
	t     *testing.T
	f     *exporttest.Fixture
	state string
	addr  string
	cmd   *exec.Cmd
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

func receiveCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "VALLEY_WEB_RECEIVE_TEST_MAIN=1")
	return cmd
}

// newEdge starts a receiver over a new state directory, with the flags
// given after the ones every receiver here needs.
func newEdge(t *testing.T, f *exporttest.Fixture, more ...string) *edge {
	t.Helper()
	tokenFile, verifiers := files(t)
	state := filepath.Join(t.TempDir(), "state")
	cmd := receiveCmd(append([]string{"--state", state, "--listen", "127.0.0.1:0",
		"--instance", exporttest.Instance, "--destination", exporttest.Destination,
		"--verifiers", verifiers, "--token-file", tokenFile}, more...)...)
	return &edge{t: t, f: f, state: state, cmd: cmd, addr: start(t, cmd, "valley-web-receive")}
}

// start runs cmd and waits for it to say where it listens.
func start(t *testing.T, cmd *exec.Cmd, name string) string {
	t.Helper()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	lines := make(chan string, 1)
	go func() {
		r := bufio.NewReader(stderr)
		line, _ := r.ReadString('\n')
		lines <- line
		io.Copy(io.Discard, r)
	}()
	select {
	case line := <-lines:
		addr, ok := strings.CutPrefix(strings.TrimSpace(line), name+": listening on ")
		if !ok {
			t.Fatalf("%s printed %q", name, line)
		}
		return addr
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not say where it listens", name)
	}
	return ""
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

// request makes one request with the token, and returns the status and the
// reply.
func (e *edge) request(method, path string, body []byte, header http.Header) (int, []byte, http.Header) {
	e.t.Helper()
	req, err := http.NewRequest(method, "http://"+e.addr+path, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", export.Authorization(token))
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	reply, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.StatusCode, reply, res.Header
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
// is accepted.
func (e *edge) put(m *export.Manifest, name string) {
	e.t.Helper()
	b := body(e.t, m, name)
	status, reply, _ := e.request(http.MethodPut, export.BodyPath(m.Sequence, b.SHA256), e.f.BodyBytes(e.t, name), nil)
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
	return start(e.t, exec.Command(bin, "--state", e.state, "--listen", "127.0.0.1:0"), "valley-web")
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
		res, err := http.Get("http://" + addr + target)
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
		res, err := http.DefaultClient.Do(req)
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
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = io.ReadAll(res.Body)
	res.Body.Close()
	refused(t, "a body one byte long, sent with no length", res.StatusCode, reply, export.CodeSizeMismatch)

	// Cut off half way.
	c, err := net.Dial("tcp", e.addr)
	if err != nil {
		t.Fatal(err)
	}
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
	waitFor(t, "no body left in staging", func() bool {
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
// unauthenticated, taking no slot. Once the stalled uploads go away, the
// receiver takes uploads again.
func TestASaturatedUploadLeavesValleyWebServing(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	e.upload()
	web := e.valleyWeb()
	signed, m := e.signedNow()
	e.post(signed)
	file := body(t, m, export.ExportFile)
	probe := export.BodyPath(m.Sequence, strings.Repeat("0", 64))

	// Each stalled upload sends its headers and one byte of its body, and
	// holds a slot while the receiver waits for the rest. One answered at
	// once found no slot free, and another takes its place.
	var stalled []net.Conn
	for len(stalled) < maxRequests {
		c, err := net.Dial("tcp", e.addr)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(c, "PUT %s HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\nContent-Length: %d\r\n\r\n",
			export.BodyPath(m.Sequence, file.SHA256), e.addr, export.Authorization(token), file.Size)
		c.Write(e.f.ExportJSON[:1])
		c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := c.Read(make([]byte, 1)); err == nil {
			c.Close()
			continue
		}
		stalled = append(stalled, c)
	}
	waitFor(t, "the receiver to answer 503", func() bool {
		status, _, header := e.request(http.MethodPut, probe, []byte("x"), nil)
		return status == http.StatusServiceUnavailable && header.Get("Retry-After") != ""
	})
	req, err := http.NewRequest(http.MethodPost, "http://"+e.addr+export.ManifestPath, bytes.NewReader(signed))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
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
	waitFor(t, "the receiver to take requests again", func() bool {
		status, _, _ := e.request(http.MethodPut, probe, []byte("x"), nil)
		return status == http.StatusNotFound
	})
	e.put(m, export.ExportFile)
	e.commit(m)
	e.serves(web)
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The receiver stops cleanly on SIGTERM.
func TestStopsOnSIGTERM(t *testing.T) {
	e := newEdge(t, exporttest.New(t))
	if err := e.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := e.cmd.Wait(); err != nil {
		t.Errorf("valley-web-receive stopped with %v", err)
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
		out, err := receiveCmd(args...).CombinedOutput()
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
		out, err := receiveCmd(args...).CombinedOutput()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Errorf("valley-web-receive %q gave %v:\n%s", args, err, out)
		}
		if bytes.Contains(out, []byte(token)) {
			t.Errorf("valley-web-receive %q printed the token", args)
		}
	}
	out, err := receiveCmd("--help").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "usage: valley-web-receive") {
		t.Errorf("valley-web-receive --help gave %v:\n%s", err, out)
	}
}

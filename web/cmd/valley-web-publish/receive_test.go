package main

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// program is the path of a program of the web UI's that the variable env
// names: valley-web-receive or valley-web, as the nix check builds them.
func program(t *testing.T, env string) string {
	t.Helper()
	path := os.Getenv(env)
	if path == "" {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatalf("%s names no program, and VALLEY_TEST_TOOLS=required", env)
		}
		t.Skipf("%s names no program; the nix check provides one", env)
	}
	return path
}

// listening runs cmd, a server that prints "<name>: listening on ADDR"
// once it listens, and returns the address.
func listening(t *testing.T, name string, cmd *exec.Cmd) string {
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

// page is valley-web's status and page for a GET of target.
func page(t *testing.T, addr, target string) (int, string) {
	t.Helper()
	res, err := http.Get("http://" + addr + target)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}

// The real publisher uploads to the real receiver, a process of its own,
// and valley-web, another, serves what the receiver activated from its
// state directory. A pass with another token is refused as
// unauthenticated, and the edge keeps serving what it has. A pass after
// anyone's grant is withdrawn takes the project off the edge.
func TestPublishesToTheRealReceiver(t *testing.T) {
	v := newValley(t)
	receive, web := program(t, "VALLEY_WEB_RECEIVE"), program(t, "VALLEY_WEB")

	// The export key, made as a pass makes it, so the receiver can pin its
	// verifier line from the start.
	if err := os.MkdirAll(v.state, 0o700); err != nil {
		t.Fatal(err)
	}
	keys := &publisher{cfg: config{state: v.state}, signer: instanceName + "/web-export", stdout: io.Discard}
	if _, err := keys.loadKey(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	token, wrong := filepath.Join(dir, "token"), filepath.Join(dir, "wrong")
	v.write(token, "edge-token-0123456789abcdef0123456789\n")
	v.write(wrong, "another-token-0123456789abcdef0123456\n")
	state := filepath.Join(dir, "state")
	receiver := "http://" + listening(t, "valley-web-receive", exec.Command(receive,
		"--state", state, "--listen", "127.0.0.1:0", "--instance", instanceName, "--destination", destination,
		"--verifiers", filepath.Join(v.state, verifierFile), "--token-file", token))

	stdout, _ := v.publish("--receiver", receiver, "--receiver-token", token)
	if !strings.Contains(stdout, "the receiver activated sequence") {
		t.Errorf("the first pass did not say the receiver activated it:\n%s", stdout)
	}
	addr := listening(t, "valley-web", exec.Command(web, "--state", state, "--listen", "127.0.0.1:0"))
	milestone := "/app/knots/" + v.knots["milestone"]
	if status, body := page(t, addr, "/"); status != http.StatusOK || !strings.Contains(body, "Publish the plan") {
		t.Errorf("valley-web answered / with %d, and no milestone of app:\n%s", status, body)
	}
	if status, _ := page(t, addr, milestone); status != http.StatusOK {
		t.Errorf("valley-web answered %s with %d, want 200", milestone, status)
	}
	if status, _ := page(t, addr, "/site/knots/"+v.knots["site"]); status != http.StatusNotFound {
		t.Errorf("valley-web answered site's knot with %d, want 404", status)
	}

	// A comment changes the export, so the pass uploads.
	v.valley("app", "knot", "comment", v.knots["milestone"], "-m", "The edge is up.")
	_, _, err := v.tryPublish(nil, "--receiver", receiver, "--receiver-token", wrong)
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Errorf("a pass with another token ended with %v, want unauthenticated", err)
	}
	if status, body := page(t, addr, milestone); status != http.StatusOK || strings.Contains(body, "The edge is up.") {
		t.Errorf("after the refused pass, valley-web answered %s with %d, or with the refused comment", milestone, status)
	}

	v.grant("")
	v.publish("--receiver", receiver, "--receiver-token", token)
	if status, _ := page(t, addr, milestone); status != http.StatusNotFound {
		t.Errorf("valley-web answered %s with %d once anyone's grant was withdrawn, want 404", milestone, status)
	}
}

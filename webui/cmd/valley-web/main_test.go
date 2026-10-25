package main

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/webui/export"
	"the-valley/webui/export/exporttest"
)

// The test binary is valley-web when VALLEY_WEB_TEST_MAIN is set, so the
// tests run the real program as a process of its own.
func TestMain(m *testing.M) {
	if os.Getenv("VALLEY_WEB_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func valleyWeb(args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "VALLEY_WEB_TEST_MAIN=1")
	return cmd
}

// valley-web serves a state directory the fixture's export was uploaded
// to, on the address it prints, and stops cleanly on SIGTERM.
func TestServesTheStateDirectory(t *testing.T) {
	f := exporttest.New(t)
	state := t.TempDir()
	store, err := export.OpenStore(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.Upload(t, store)

	cmd := valleyWeb("--state", state, "--listen", "127.0.0.1:0")
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
	var addr string
	select {
	case line := <-lines:
		var ok bool
		if addr, ok = strings.CutPrefix(strings.TrimSpace(line), "valley-web: listening on "); !ok {
			t.Fatalf("valley-web printed %q", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("valley-web did not say where it listens")
	}

	for target, status := range map[string]int{
		"/":                     http.StatusOK,
		"/knots/app/oc-a000002": http.StatusOK,
		"/source/app/tree/" + f.Export.Projects[0].SourceTip + "/": http.StatusOK,
		"/source/site/": http.StatusNotFound,
	} {
		res, err := http.Get("http://" + addr + target)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != status {
			t.Errorf("GET %s answered %d, not %d:\n%s", target, res.StatusCode, status, body)
		}
		if !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "default-src 'self';") {
			t.Errorf("GET %s answered with no policy", target)
		}
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("valley-web stopped with %v", err)
	}
}

// A command line it cannot run with gets the usage and exit status 2;
// --help gets the usage alone.
func TestUsage(t *testing.T) {
	state := t.TempDir()
	for _, args := range [][]string{
		{},
		{"--state", state},
		{"--listen", "127.0.0.1:0"},
		{"--state", state, "--listen", "127.0.0.1:0", "extra"},
		{"--no-such-flag"},
	} {
		out, err := valleyWeb(args...).CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "usage: valley-web --state DIR --listen ADDR") {
			t.Errorf("valley-web %q gave %v:\n%s", args, err, out)
		}
	}
	out, err := valleyWeb("--help").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "usage: valley-web") {
		t.Errorf("valley-web --help gave %v:\n%s", err, out)
	}
}

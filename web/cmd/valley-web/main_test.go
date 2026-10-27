package main

import (
	"bufio"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// The test binary is valley-web when VALLEY_WEB_TEST_MAIN is set, so the
// tests run the real program as a process of its own. It runs the git
// VALLEY_WEB_TEST_GIT names, when that is set.
func TestMain(m *testing.M) {
	if os.Getenv("VALLEY_WEB_TEST_MAIN") == "1" {
		if git := os.Getenv("VALLEY_WEB_TEST_GIT"); git != "" {
			export.GitProgram = git
		}
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

// uploaded is a state directory the fixture's export was uploaded to, as
// a receiver uploads it.
func uploaded(t *testing.T) (*exporttest.Fixture, string) {
	t.Helper()
	f := exporttest.New(t)
	state := t.TempDir()
	store, err := export.OpenStore(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.Upload(t, store)
	return f, state
}

// start runs cmd and waits for it to say where it listens.
func start(t *testing.T, cmd *exec.Cmd) (addr string) {
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
		var ok bool
		if addr, ok = strings.CutPrefix(strings.TrimSpace(line), "valley-web: listening on "); !ok {
			t.Fatalf("valley-web printed %q", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("valley-web did not say where it listens")
	}
	return addr
}

// routes are a GET of each target, and the status it answers.
func routes(f *exporttest.Fixture) map[string]int {
	tip := f.Export.Projects[0].SourceTip
	return map[string]int{
		"/":                             http.StatusOK,
		"/app/knots/oc-a000002":         http.StatusOK,
		"/app/source/tree/" + tip + "/": http.StatusOK,
		"/app/source/blob/" + tip + "/docs/guide.md": http.StatusOK,
		"/app/source/log/" + tip:                     http.StatusOK,
		"/site/source/":                              http.StatusNotFound,
	}
}

// get checks that each target answers its status, with the policy.
func get(t *testing.T, addr string, targets map[string]int) {
	t.Helper()
	for target, status := range targets {
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
}

// valley-web serves a state directory the fixture's export was uploaded
// to, on the address it prints, and stops cleanly on SIGTERM.
func TestServesTheStateDirectory(t *testing.T) {
	f, state := uploaded(t)
	cmd := valleyWeb("--state", state, "--listen", "127.0.0.1:0")
	get(t, start(t, cmd), routes(f))

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("valley-web stopped with %v", err)
	}
}

// valley-web serves every page from a state directory it can only read,
// as a user other than the receiver's: no file or directory in it is
// writable, and git holds its repositories to be another user's. The
// receiver wrote it under umask 0077, so what it reads is readable by
// others because the receiver made it so. The deployment gives the server
// the state directory itself, here mode 0755. The test cannot become
// another user, so it makes the directory read as one does: each entry's
// owner and group bits are set to its bits for others, less write. git is
// told the repositories are another user's with
// GIT_TEST_ASSUME_DIFFERENT_OWNER, as git's own tests tell it, through a
// git that sets it and runs the real one, since valley-web passes git none
// of its own GIT_ variables.
func TestServesAReadOnlyStateDirectory(t *testing.T) {
	umask := syscall.Umask(0o077)
	f, state := uploaded(t)
	syscall.Umask(umask)
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	asOthers(t, state)
	real, err := exec.LookPath(export.GitProgram)
	if err != nil {
		t.Fatal(err)
	}
	if real, err = filepath.Abs(real); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nGIT_TEST_ASSUME_DIFFERENT_OWNER=1 exec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := valleyWeb("--state", state, "--listen", "127.0.0.1:0")
	cmd.Env = append(cmd.Env, "VALLEY_WEB_TEST_GIT="+wrapper)
	get(t, start(t, cmd), routes(f))

	out, err := valleyWeb("--state", filepath.Join(state, "missing"), "--listen", "127.0.0.1:0").CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Errorf("valley-web on a state directory that does not exist gave %v:\n%s", err, out)
	}
}

// asOthers makes every file and directory under root, root too, read by
// its owner as a user who is neither its owner nor in its group reads it,
// and writable by none. The test's cleanup makes it writable again, so it
// can be removed.
func asOthers(t *testing.T, root string) {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink == 0 {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	// Children first, so each directory can still be read while its
	// entries change.
	for i := len(paths) - 1; i >= 0; i-- {
		info, err := os.Lstat(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		others := info.Mode().Perm() & 0o005
		if err := os.Chmod(paths[i], others<<6|others<<3|others); err != nil {
			t.Fatal(err)
		}
	}
	if f, err := os.Create(filepath.Join(root, "probe")); err == nil {
		f.Close()
		t.Skip("the test runs as a user every file is writable by, such as root")
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

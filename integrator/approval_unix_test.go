//go:build unix

package main

// What the approval check does when git stops answering, run against a
// stand-in for git on the path.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// standInGit puts script on the path as git, in a directory of its own,
// which the script finds as $STAND_IN.
func standInGit(t *testing.T, script string) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STAND_IN", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// A context that ends while a read waits on git stops the read, and git is
// killed and reaped.
func TestAnApprovalCheckStopsWhenItsContextEnds(t *testing.T) {
	f := newApprovalFixture(t)
	q := f.approved(f.created(f.head()), 1)
	dir := standInGit(t, `echo $$ > "$STAND_IN/pid"
read -r line
echo "$line" > "$STAND_IN/asked"
exec sleep 1000
`)
	if err := syscall.Mkfifo(filepath.Join(dir, "asked"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan approvalResult, 1)
	go func() { done <- verifyApproval(ctx, f.repo, q) }()
	// The stand-in writes the command it read once it holds it, and then
	// answers nothing.
	asked, err := os.ReadFile(filepath.Join(dir, "asked"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(asked), "info ") {
		t.Fatalf("git was asked %q", asked)
	}
	cancel()
	if _, refusal, err := outcome(t, <-done); !errors.Is(err, context.Canceled) || refusal != nil {
		t.Fatalf("refusal %v, err %v, want the context's end", refusal, err)
	}
	text, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(text)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("git, process %d, is not reaped: kill answers %v", pid, err)
	}
}

// A git that exits without answering is an error carrying what it wrote
// to stderr.
func TestAnApprovalCheckSaysWhatAFailingGitSaid(t *testing.T) {
	f := newApprovalFixture(t)
	q := f.approved(f.created(f.head()), 1)
	standInGit(t, `echo "fatal: this git reads no batch commands" >&2
exit 129
`)
	_, refusal, err := verify(f, q)
	if err == nil || refusal != nil || !strings.Contains(err.Error(), "this git reads no batch commands") {
		t.Fatalf("refusal %v, err %v, want git's stderr", refusal, err)
	}
}

// Each way of failing to start git closes every descriptor it opened: no
// pipe can be made, one pipe is made and the second cannot be, and git
// does not start.
func TestAFailedStartClosesWhatItOpened(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	// A first pipe sets up the runtime's poller, which holds descriptors
	// of its own from then on.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = r.Close(), w.Close()
	for _, c := range []struct {
		name string
		free int // descriptor numbers left below the limit, or -1 for no limit
	}{{"no pipe", 0}, {"one pipe of two", 2}, {"git does not start", -1}} {
		t.Run(c.name, func(t *testing.T) {
			before := openDescriptors(t)
			if c.free >= 0 {
				lowered := limit
				lowered.Cur = uint64(unused(before, c.free))
				if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lowered); err != nil {
					t.Fatal(err)
				}
			}
			o, err := openObjectReader(context.Background(), missing)
			if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
				t.Fatal(err)
			}
			if err == nil {
				o.close()
				t.Fatal("git started")
			}
			if after := openDescriptors(t); !slices.Equal(after, before) {
				t.Errorf("descriptors %v after %v, and %v before", after, err, before)
			}
		})
	}
}

// openDescriptors is the descriptors this process holds, less the one
// that reads them.
func openDescriptors(t *testing.T) []int {
	d, err := os.Open("/dev/fd")
	if err != nil {
		t.Skipf("no /dev/fd: %v", err)
	}
	self := int(d.Fd())
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		t.Fatal(err)
	}
	var fds []int
	for _, n := range names {
		if fd, err := strconv.Atoi(n); err == nil && fd != self {
			fds = append(fds, fd)
		}
	}
	slices.Sort(fds)
	return fds
}

// unused is the descriptor number with n numbers below it that held does
// not use.
func unused(held []int, n int) int {
	for fd := 0; ; fd++ {
		if !slices.Contains(held, fd) {
			if n == 0 {
				return fd
			}
			n--
		}
	}
}

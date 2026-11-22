package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Both ways of reading processes, over real ones: /proc, which Linux has,
// and ps with lsof, which macOS reads them with. Each runs wherever it can.
// The worktree's name holds what lsof prints escaped.
func TestLingeringProcessesFindsWorkInsideDir(t *testing.T) {
	for _, r := range []struct {
		name  string
		ready func() error
		find  func(worktree string) ([]string, error)
	}{
		{
			name: "proc",
			ready: func() error {
				_, err := os.Stat("/proc/self/cwd")
				return err
			},
			find: lingeringProcesses,
		},
		{
			name: "ps",
			ready: func() error {
				for _, tool := range []string{"ps", "lsof"} {
					if _, err := exec.LookPath(tool); err != nil {
						return err
					}
				}
				return nil
			},
			find: func(worktree string) ([]string, error) {
				root, err := filepath.EvalSymlinks(worktree)
				if err != nil {
					return nil, err
				}
				procs, err := psProcesses()
				if err != nil {
					return nil, err
				}
				return lingering(root, os.Getpid(), procs)
			},
		},
	} {
		t.Run(r.name, func(t *testing.T) {
			if err := r.ready(); err != nil {
				if r.name == "ps" && os.Getenv("KLAUS_TEST_TOOLS") == "required" {
					t.Fatal(err)
				}
				t.Skip(err)
			}
			worktree := filepath.Join(t.TempDir(), "caf\u00e9 a\\b\nrun")
			sub := filepath.Join(worktree, "nested")
			if err := os.Mkdir(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			// A sibling sharing the worktree's name as a prefix is not inside it.
			sibling := worktree + "-other"
			if err := os.Mkdir(sibling, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(sibling) })

			startSleep := func(dir string) {
				c := exec.Command("sleep", "60")
				c.Dir = dir
				if err := c.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Process.Kill(); c.Wait() })
			}

			find := func() []string {
				got, err := r.find(worktree)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			if got := find(); len(got) != 0 {
				t.Fatalf("idle worktree reported %v", got)
			}
			startSleep(sibling)
			if got := find(); len(got) != 0 {
				t.Fatalf("process in sibling dir reported %v", got)
			}
			startSleep(sub)
			if got := find(); !slices.Equal(got, []string{"sleep"}) {
				t.Fatalf("lingeringProcesses = %v, want [sleep]", got)
			}
		})
	}
}

// ps and lsof as macOS prints them: "??" for no terminal and a command name
// with a space in it. This process's ancestors, a process on another
// terminal, and one beside the worktree are not lingering work.
func TestLingeringFromMacOSListing(t *testing.T) {
	ps := `    1     0 ??       launchd
  400     1 ttys001  login
  401   400 ttys001  zsh
  500   401 ttys001  klaus
  600     1 ??       nix daemon
  601   500 ttys001  go
  700     1 ttys002  vim
  900     1 ??       sleep
`
	lsof := "p401\nfcwd\nn/work/run\np500\nfcwd\nn/work/run\np600\nfcwd\nn/work/run/sub\n" +
		"p601\nfcwd\nn/work/run\np700\nfcwd\nn/work/run\np900\nfcwd\nn/work/run-other\n"
	got, err := lingering("/work/run", 500, fromPSAndLsof(ps, lsof))
	if want := []string{"go", "nix daemon"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("lingering = %v, %v, want %v", got, err, want)
	}
}

// Names as lsof prints them, in the C locale psProcesses runs it in.
func TestLsofName(t *testing.T) {
	for _, c := range []struct {
		printed, name string
		part          bool
	}{
		{printed: `/work/caf\xc3\xa9/run`, name: "/work/caf\u00e9/run"},
		{printed: `/work/a\\b`, name: `/work/a\b`},
		{printed: `/work/my run`, name: "/work/my run"},
		{printed: `/work/a\nb`, name: "/work/a\nb"},
		// ^ is printed as itself: ^H is literal, as a backspace prints as \b,
		// but ^A is either ^ and A or 0x01.
		{printed: `/work/^H`, name: "/work/^H"},
		{printed: `/work/^Arun`, name: "/work/", part: true},
		// lsof prints no \q, nor a backslash at the end.
		{printed: `/work/a\qb`, name: "/work/a", part: true},
		{printed: `/work/a\`, name: "/work/a", part: true},
	} {
		name, part := lsofName(c.printed)
		if name != c.name || part != c.part {
			t.Errorf("lsofName(%q) = %q, %v, want %q, %v", c.printed, name, part, c.name, c.part)
		}
	}
}

// A name lsofName reads only the start of is under the worktree when that
// start is, outside it when that start leaves it, and otherwise unknown: an
// error, beside the commands that are known, even one of the same name.
func TestLingeringWithPartialNames(t *testing.T) {
	ps := `  500     1 ttys001  klaus
  600     1 ??       make
  601     1 ??       sleep
  602     1 ??       make
  603     1 ??       vim
`
	lsof := "p600\nfcwd\nn/work/run/^Abuild\np601\nfcwd\nn/work/ru^An\n" +
		"p602\nfcwd\nn/work/r\\qun\np603\nfcwd\nn/home/^A/run\n"
	got, err := lingering("/work/run", 500, fromPSAndLsof(ps, lsof))
	if !slices.Equal(got, []string{"make"}) {
		t.Fatalf("lingering = %v, want [make]", got)
	}
	if err == nil {
		t.Fatal("working directories that may be under the worktree read as known")
	}
	for _, unknown := range []string{"pid 601", "pid 602"} {
		if !strings.Contains(err.Error(), unknown) {
			t.Errorf("error %q does not name %s", err, unknown)
		}
	}
	if strings.Contains(err.Error(), "pid 603") {
		t.Errorf("error %q names a process outside the worktree", err)
	}
}

// Without ps or lsof the processes cannot be read, and that is an error, not
// an empty list.
func TestPSProcessesFailsWithoutTheTools(t *testing.T) {
	ps, err := exec.LookPath("ps")
	if err != nil {
		if os.Getenv("KLAUS_TEST_TOOLS") == "required" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := psProcesses(); err == nil {
		t.Fatal("psProcesses with no ps on PATH read processes")
	}
	onlyPS := t.TempDir()
	if err := os.Symlink(ps, filepath.Join(onlyPS, "ps")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onlyPS)
	if _, err := psProcesses(); err == nil {
		t.Fatal("psProcesses with no lsof on PATH read processes")
	}
}

// A worktree that is gone holds nothing; one that cannot be resolved is an
// error.
func TestLingeringProcessesResolvesTheWorktree(t *testing.T) {
	dir := t.TempDir()
	if got, err := lingeringProcesses(filepath.Join(dir, "gone")); err != nil || len(got) != 0 {
		t.Fatalf("a missing worktree: %v, %v", got, err)
	}
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if _, err := lingeringProcesses(loop); err == nil {
		t.Fatal("a worktree in a symlink loop read as holding nothing")
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Only a successful evaluation's peak trains a project's estimate. One
// that failed or was killed stopped short of its high-water mark, so it
// makes the estimate unknown, and the project's next evaluation counts as
// the whole budget, until one succeeds; then the high-water marks the
// failures did not push out still count. The record survives a new pass.
func TestFailedEvaluationsMakeTheEstimateUnknown(t *testing.T) {
	state := t.TempDir()
	e, err := openEvaluations(state, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.record("app", 600, "ok"))
	must(e.record("site", 100, "ok"))
	for range 5 {
		must(e.record("app", 50, "killed"))
	}
	if got := e.estimate("app"); got != 1000 {
		t.Fatalf("after killed evaluations the estimate is %d, want the whole budget", got)
	}
	if e, err = openEvaluations(state, 1000, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.estimate("app"); got != 1000 {
		t.Fatalf("a new pass estimates %d, want the whole budget", got)
	}
	must(e.record("app", 0, "ok"))
	if got := e.estimate("app"); got != 1000 {
		t.Fatalf("an ok evaluation that measured nothing made the estimate %d", got)
	}
	must(e.record("app", 300, "ok"))
	if got := e.estimate("app"); got != 600 {
		t.Fatalf("after a successful evaluation the estimate is %d, want the high-water mark 600", got)
	}
	must(e.record("app", 200, "failed"))
	if got := e.estimate("app"); got != 1000 {
		t.Fatalf("after a failed evaluation the estimate is %d, want the whole budget", got)
	}
	if got := e.estimate("site"); got != 100 {
		t.Fatalf("another project's estimate is %d, want 100", got)
	}
}

// The cgroup is found where mountinfo says the cgroup2 hierarchy is
// mounted, under the mount's root, and not by assuming /sys/fs/cgroup.
func TestCgroupDir(t *testing.T) {
	const self = "0::/system.slice/valley-worker.service\n"
	mount := func(root, point string) string {
		return "25 1 0:22 / /proc rw - proc proc rw\n" +
			"30 25 0:26 " + root + " " + point + " rw,nosuid shared:4 - cgroup2 cgroup2 rw,nsdelegate\n"
	}
	for _, c := range []struct {
		why, cgroup, mountinfo, dir, top, err string
	}{
		{"the usual mount", self, mount("/", "/sys/fs/cgroup"), "/sys/fs/cgroup/system.slice/valley-worker.service", "/sys/fs/cgroup", ""},
		{"another mount point, escaped", self, mount("/", `/run/cg\040two`), "/run/cg two/system.slice/valley-worker.service", "/run/cg two", ""},
		{"a mount of a subtree", self, mount("/system.slice", "/mnt/slice"), "/mnt/slice/valley-worker.service", "/mnt/slice", ""},
		{"a mount of another subtree", self, mount("/system", "/mnt/s"), "", "", "no cgroup2 mount"},
		{"no cgroup2 mount", self, "25 1 0:22 / /proc rw - proc proc rw\n", "", "", "no cgroup2 mount"},
		{"only cgroup v1", "1:memory:/user.slice\n", mount("/", "/sys/fs/cgroup"), "", "", "names no cgroup2 cgroup"},
		{"outside the namespace", "0::/../other\n", mount("/", "/sys/fs/cgroup"), "", "", "names no cgroup2 cgroup"},
	} {
		dir, top, _, err := cgroupDir(c.cgroup, c.mountinfo)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err %v, want one saying %q", c.why, err, c.err)
			}
			continue
		}
		if err != nil || dir != c.dir || top != c.top {
			t.Errorf("%s: %q, %q, %v; want %q, %q", c.why, dir, top, err, c.dir, c.top)
		}
	}
}

// The limit is the smallest memory.max from the cgroup up to the mount's
// root, and the host's memory when none is smaller. "max" and a missing
// file are no limit; a memory.max that cannot be read, or says something
// else, means the limit is not known.
func TestCgroupLimit(t *testing.T) {
	top := t.TempDir()
	dir := filepath.Join(top, "slice", "service")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(at, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(at, "memory.max"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(dir, "max\n")
	write(filepath.Join(top, "slice"), "8000\n")
	limit, of, err := cgroupLimit(dir, top, "/slice/service", 10000)
	if err != nil || limit != 8000 || of != "the memory.max of the cgroup /slice" {
		t.Fatalf("limit %d from %q, %v; want 8000 from /slice", limit, of, err)
	}
	if limit, _, err = cgroupLimit(dir, top, "/slice/service", 5000); err != nil || limit != 5000 {
		t.Fatalf("limit %d, %v; want the host's 5000", limit, err)
	}
	write(dir, "lots\n")
	if _, _, err = cgroupLimit(dir, top, "/slice/service", 10000); err == nil {
		t.Fatal("a memory.max that says neither a number nor max gave a limit")
	}
	os.Remove(filepath.Join(dir, "memory.max"))
	if err := os.Mkdir(filepath.Join(dir, "memory.max"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err = cgroupLimit(dir, top, "/slice/service", 10000); err == nil {
		t.Fatal("a memory.max that cannot be read gave a limit")
	}
}

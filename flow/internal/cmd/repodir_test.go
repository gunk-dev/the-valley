package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a real git repo at dir with the given origin URL.
func initRepo(t *testing.T, dir, origin string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", origin},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// macOS resolves TMPDIR through a symlink; git reports the real path.
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving %s: %v", dir, err)
	}
	return real
}

func mustExistingRepoDir(t *testing.T, ref string) string {
	t.Helper()
	dir, err := existingRepoDir(ref)
	if err != nil {
		t.Fatalf("existingRepoDir(%q): %v", ref, err)
	}
	return dir
}

func TestExistingRepoDirUsesCurrentCheckout(t *testing.T) {
	dir := initRepo(t, filepath.Join(t.TempDir(), "widget"), "https://github.com/acme/widget.git")
	writeRegistry(t, withTempHome(t), nil)
	t.Chdir(dir)

	// No reference at all: whatever repo we are standing in.
	if got := mustExistingRepoDir(t, ""); got != dir {
		t.Errorf("existingRepoDir(\"\") = %q, want %q", got, dir)
	}
	// A reference the current checkout demonstrably satisfies.
	if got := mustExistingRepoDir(t, "acme/widget"); got != dir {
		t.Errorf("existingRepoDir(\"acme/widget\") = %q, want %q", got, dir)
	}
	// A different repo must not silently resolve to the current checkout.
	if got := mustExistingRepoDir(t, "acme/gadget"); got != "" {
		t.Errorf("existingRepoDir(\"acme/gadget\") = %q, want \"\"", got)
	}
	// Nor may a bare name, which says nothing about this checkout's origin.
	if got := mustExistingRepoDir(t, "widget"); got != "" {
		t.Errorf("existingRepoDir(\"widget\") = %q, want \"\"", got)
	}
}

func TestExistingRepoDirFindsRegisteredProject(t *testing.T) {
	root := t.TempDir()
	widget := initRepo(t, filepath.Join(root, "widget"), "git@github.com:acme/widget.git")
	local := initRepo(t, filepath.Join(root, "gizmo"), filepath.Join(root, "gizmo-origin.git"))
	writeRegistry(t, withTempHome(t), map[string]string{"widget": widget, "gizmo": local})
	// Outside any checkout, so only the registry can answer.
	t.Chdir(root)

	// Matched by origin slug, in every reference form.
	for _, ref := range []string{"acme/widget", "https://github.com/acme/widget", "git@github.com:acme/widget.git"} {
		if got := mustExistingRepoDir(t, ref); got != widget {
			t.Errorf("existingRepoDir(%q) = %q, want %q", ref, got, widget)
		}
	}
	// A project whose origin is not a GitHub URL can only be matched by name.
	if got := mustExistingRepoDir(t, "gizmo"); got != local {
		t.Errorf("existingRepoDir(\"gizmo\") = %q, want %q", got, local)
	}
	// A GitHub repo nobody has checked out stays unresolved (no cloning here).
	if got := mustExistingRepoDir(t, "acme/unknown"); got != "" {
		t.Errorf("existingRepoDir(\"acme/unknown\") = %q, want \"\"", got)
	}
}

// A project registered under a name that collides with a different GitHub repo
// must not be handed back for that repo — the origin disagrees.
func TestExistingRepoDirIgnoresNameCollisionWithOtherOrigin(t *testing.T) {
	root := t.TempDir()
	widget := initRepo(t, filepath.Join(root, "widget"), "https://github.com/acme/widget.git")
	writeRegistry(t, withTempHome(t), map[string]string{"widget": widget})
	t.Chdir(root)

	if got := mustExistingRepoDir(t, "other/widget"); got != "" {
		t.Errorf("existingRepoDir(\"other/widget\") = %q, want \"\"", got)
	}
}

// A registry at both its names is refused, naming both, by every lookup of
// a checkout through it, rather than taken for one with no projects.
func TestExistingRepoDirRefusesRegistryUnderBothNames(t *testing.T) {
	root := t.TempDir()
	widget := initRepo(t, filepath.Join(root, "widget"), "https://github.com/acme/widget.git")
	home := withTempHome(t)
	writeRegistry(t, home, map[string]string{"widget": widget})
	flow := filepath.Join(home, ".valley", "flow", "projects.json")
	if err := os.MkdirAll(filepath.Dir(flow), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flow, []byte(`{"projects": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	both := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), filepath.Join(home, ".klaus", "projects.json")) || !strings.Contains(err.Error(), flow) {
			t.Errorf("%s with a registry under both names: %v, want an error naming both", what, err)
		}
	}
	_, err := existingRepoDir("acme/widget")
	both("existingRepoDir", err)
	_, err = resolveRepoDirUncached(context.Background(), "widget")
	both("resolveRepoDir", err)
	_, err = reviewRepoSlug(context.Background(), "widget")
	both("reviewRepoSlug", err)
	_, err = trackChange(context.Background(), "widget", "nxvp", nil, nil, valleyListings{})
	both("trackChange", err)
}

func TestResolveRepoDirWithoutTargetOutsideRepo(t *testing.T) {
	writeRegistry(t, withTempHome(t), nil)
	t.Chdir(t.TempDir())

	_, err := resolveRepoDirUncached(context.Background(), "")
	if !errors.Is(err, errNoTargetRepo) {
		t.Errorf("error = %v, want errNoTargetRepo", err)
	}
}

func TestRepoSlug(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"acme/widget", "acme/widget"},
		{"https://github.com/acme/widget.git", "acme/widget"},
		{"git@github.com:acme/widget", "acme/widget"},
		{"widget", ""},              // bare project name, no owner
		{"", ""},                    // nothing to resolve
		{"/srv/git/widget.git", ""}, // a local path, not a GitHub repo
	}
	for _, tt := range tests {
		if got := repoSlug(tt.ref); got != tt.want {
			t.Errorf("repoSlug(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// A command that goes on without a registry it can't read stops at one
// under both names, naming both; one that doesn't parse it still goes on
// without, and none at all is the default registry.
func TestRegistryUnlessConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy, flow := filepath.Join(home, ".klaus", "projects.json"), filepath.Join(home, ".valley", "flow", "projects.json")

	if reg, err := registryUnlessConflict(); err != nil || reg == nil {
		t.Fatalf("no registry: %v, %v; want the default one", reg, err)
	}
	writeJSON(t, legacy, map[string]any{"projects": map[string]string{}})
	if err := os.WriteFile(legacy, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reg, err := registryUnlessConflict(); err != nil || reg != nil {
		t.Fatalf("unparsed registry: %v, %v; want none and no error", reg, err)
	}
	writeJSON(t, flow, map[string]any{"projects": map[string]string{}})
	_, err := registryUnlessConflict()
	if err == nil || !strings.Contains(err.Error(), legacy) || !strings.Contains(err.Error(), flow) {
		t.Fatalf("registry under both names: %v; want an error naming both", err)
	}
}

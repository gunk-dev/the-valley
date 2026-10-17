package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	buildTimeout   = 30 * time.Minute
	profileTimeout = 5 * time.Minute
)

// nixFeatures turns on what self-update uses of nix, whatever nix.conf
// says: flake references, and the nix command itself.
var nixFeatures = []string{"--extra-experimental-features", "nix-command flakes"}

// Real is the nix on PATH, acting on the user's default profile.
type Real struct{}

// Build builds installable without a result link and returns its output's
// store path. The git that fetches a git+ flake reference runs with gitEnv.
func (Real) Build(ctx context.Context, installable string) (string, error) {
	out, err := runNix(ctx, buildTimeout, gitEnv, "build", "--no-link", "--print-out-paths", installable)
	if err != nil {
		return "", err
	}
	paths := strings.Fields(out)
	if len(paths) == 0 {
		return "", errors.New("nix build printed no output path")
	}
	return paths[0], nil
}

// Elements returns the elements of the user's default nix profile, each
// name with its store paths.
func (Real) Elements(ctx context.Context) (map[string][]string, error) {
	out, err := runNix(ctx, profileTimeout, nil, "profile", "list", "--json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Elements json.RawMessage `json:"elements"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("parsing nix profile list: %w", err)
	}
	var elements map[string]struct {
		StorePaths []string `json:"storePaths"`
	}
	if len(list.Elements) > 0 {
		if err := json.Unmarshal(list.Elements, &elements); err != nil {
			return nil, fmt.Errorf("nix profile list has no named elements (needs Nix 2.20 or later): %w", err)
		}
	}
	byName := make(map[string][]string, len(elements))
	for name, e := range elements {
		byName[name] = e.StorePaths
	}
	return byName, nil
}

// Remove removes the element called name from the default profile.
func (Real) Remove(ctx context.Context, name string) error {
	_, err := runNix(ctx, profileTimeout, nil, "profile", "remove", name)
	return err
}

// Add adds storePath to the default profile.
func (Real) Add(ctx context.Context, storePath string) error {
	_, err := runNix(ctx, profileTimeout, nil, "profile", "add", storePath)
	return err
}

// System returns nix's name for this machine's platform, e.g. x86_64-linux.
func System() string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	return arch + "-" + runtime.GOOS
}

// runNix runs nix with env added to this process's environment, and
// returns its trimmed stdout. An error carries nix's error line.
func runNix(ctx context.Context, timeout time.Duration, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix", slices.Concat(nixFeatures, args)...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("nix %s timed out after %s", args[0], timeout)
		}
		return "", fmt.Errorf("nix %s: %w: %s", strings.Join(args[:min(len(args), 2)], " "), err, ErrorLine(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ErrorLine picks the line of a command's output that says what went wrong:
// nix's first "error:" line or valley's first "valley:" line, else the last
// non-empty line, capped in length.
func ErrorLine(out string) string {
	var last string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "valley:") {
			last = l
			break
		}
		if l != "" {
			last = l
		}
	}
	if r := []rune(last); len(r) > 300 {
		last = string(r[:299]) + "…"
	}
	return last
}

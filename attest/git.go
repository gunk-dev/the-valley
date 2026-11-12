package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"the-valley/note/statement"
)

// git runs a git command in repo and returns its stdout. Stderr is folded
// into the error, because a git failure that does not say what git said is
// a failure the reader has to reproduce by hand.
func git(repo string, args ...string) (string, error) {
	return gitInput(repo, nil, args...)
}

// gitCommand is every git this program runs to read or write the
// repository: with replacement refs off, and in a fixed environment.
//
// A replacement ref makes one object stand in for another wherever git
// looks it up. Left on, a refs/replace/* in the repository could make the
// tree this program digests, exports or verifies be some other tree than
// the revision names — so a verification could pass for a revision that
// is not the one attested, and a run could check one tree and sign for
// another.
//
// No GIT_ variable is inherited but command-scope configuration naming
// safe.directory, which a caller that does not own the repository needs
// (the integrator's unit supplies it this way). A GIT_DIR or
// GIT_OBJECT_DIRECTORY could point git at other objects, and a
// GIT_CONFIG_PARAMETERS could change how it reads them. System and user
// configuration are not read. The integrator's environment is fixed the
// same way (integrator/main.go), and sigverify's.
func gitCommand(repo string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"--no-replace-objects"}, args...)...)
	cmd.Dir = repo
	cmd.Env = gitEnvironment(os.Environ())
	return cmd
}

// gitRemote runs a git that talks to a remote: push, ls-remote and fetch.
// These are the gits this program runs that keep the person's own
// configuration — how a remote's URL is rewritten, which ssh command
// reaches it — because nothing they read is signed: what a fetch brings is
// only kept beside the new notes and pushed back, where the remote's hook
// checks it. Replacement refs are still off.
func gitRemote(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-replace-objects"}, args...)...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_NO_REPLACE_OBJECTS=1")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// gitEnvironment is the environment gitCommand runs git in: the caller's,
// less every GIT_ variable but safe.directory entries, with git's own
// configuration fixed.
func gitEnvironment(inherited []string) []string {
	vars := map[string]string{}
	var kept []string
	for _, kv := range inherited {
		name, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") {
			vars[name] = value
			continue
		}
		kept = append(kept, kv)
	}

	var safe []string
	count, _ := strconv.Atoi(vars["GIT_CONFIG_COUNT"])
	for i := 0; i < count; i++ {
		if strings.EqualFold(vars[fmt.Sprintf("GIT_CONFIG_KEY_%d", i)], "safe.directory") {
			safe = append(safe, vars[fmt.Sprintf("GIT_CONFIG_VALUE_%d", i)])
		}
	}
	if len(safe) > 0 {
		kept = append(kept, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(safe)))
		for i, dir := range safe {
			kept = append(kept,
				fmt.Sprintf("GIT_CONFIG_KEY_%d=safe.directory", i),
				fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, dir))
		}
	}

	return append(kept,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}

func gitInput(repo string, stdin []byte, args ...string) (string, error) {
	cmd := gitCommand(repo, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func gitLine(repo string, args ...string) (string, error) {
	out, err := git(repo, args...)
	return strings.TrimSpace(out), err
}

// gitTreeEntries reads the entries of a revision's tree, as valley-tree-v1
// reads them, with gitCommand's git.
func gitTreeEntries(repo, rev string) ([]statement.Entry, error) {
	return statement.GitTree(func(args ...string) *exec.Cmd { return gitCommand(repo, args...) }, rev)
}

// exportTree writes a revision's tree to dir. Checks run over the exported
// tree rather than the working tree, so what was checked is exactly what
// the subject digest names — an uncommitted edit cannot ride along inside
// an attestation.
func exportTree(repo, rev, dir string) error {
	cmd := gitCommand(repo, "archive", "--format=tar", rev)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(stdout)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("git archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue // git archive stamps the commit id here; not a tree entry
		}
		target := filepath.Join(dir, filepath.Clean("/"+h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("git archive: unsupported entry %s", h.Name)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// writeBlob stores content as a loose object and returns its id.
func writeBlob(repo string, content []byte) (string, error) {
	out, err := gitInput(repo, content, "hash-object", "-w", "--stdin")
	return strings.TrimSpace(out), err
}

// mkTree builds a tree object from named blobs and subtrees. Deterministic
// by construction: the attestation ref's value is a function of the
// statements under it and nothing else, so re-running attest over an
// unchanged tree produces the identical ref.
func mkTree(repo string, blobs, trees map[string]string) (string, error) {
	var lines []string
	for name, oid := range blobs {
		lines = append(lines, fmt.Sprintf("100644 blob %s\t%s", oid, name))
	}
	for name, oid := range trees {
		lines = append(lines, fmt.Sprintf("040000 tree %s\t%s", oid, name))
	}
	sort.Strings(lines)
	out, err := gitInput(repo, []byte(strings.Join(lines, "\n")+"\n"), "mktree")
	return strings.TrimSpace(out), err
}

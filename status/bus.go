package main

// valley tail and valley replay: thin verbs over the nats CLI, which
// anyone with plain nats and git could do by hand. They are the only
// verbs that need it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const needsNats = "this verb needs the nats CLI (natscli); nix run .#valley bundles it"

// runTail replaces this process with nats sub, on valley.> or the subject
// given, so signals and the exit status are nats's own. The server comes
// from the nats CLI's own config. It returns only when it could not start
// nats.
func runTail(args []string, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "valley: usage: valley tail [subject]")
		return 1
	}
	nats, err := exec.LookPath("nats")
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", needsNats)
		return 1
	}
	subject := "valley.>"
	if len(args) == 1 && args[0] != "" {
		subject = args[0]
	}
	err = syscall.Exec(nats, []string{"nats", "sub", subject}, os.Environ())
	fmt.Fprintf(stderr, "valley: %s: %s\n", nats, err)
	return 126
}

// runReplay publishes one ref-updated event per ref of the repository in
// the directory given, the current one by default, from git for-each-ref
// alone. A ref's prior value is not derivable from the repository, so old
// is the all-zero ID: the payload a push creating the ref would produce.
// A publish that fails ends the replay with nats's exit status.
func runReplay(g git, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "valley: usage: valley replay [repo-dir]")
		return 1
	}
	if _, err := exec.LookPath("nats"); err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", needsNats)
		return 1
	}
	dir := "."
	if len(args) == 1 && args[0] != "" {
		dir = args[0]
	}
	in := func(args ...string) (string, error) {
		return g.output(true, append([]string{"-C", dir}, args...)...)
	}
	if _, err := in("rev-parse", "--git-dir"); err != nil {
		fmt.Fprintf(stderr, "valley: not a git repository: %s\n", dir)
		return 1
	}
	// The repository's name: its work tree's, or a bare repository's less
	// .git, as the publisher hook names a repository a host serves.
	bare, _ := in("rev-parse", "--is-bare-repository")
	where := "--show-toplevel"
	if bare == "true" {
		where = "--absolute-git-dir"
	}
	top, err := in("rev-parse", where)
	if err != nil {
		fmt.Fprintf(stderr, "valley: not a git repository: %s\n", dir)
		return 1
	}
	repo := filepath.Base(top)
	if trimmed := strings.TrimSuffix(repo, ".git"); bare == "true" && trimmed != "" {
		repo = trimmed
	}
	refs, err := in("for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s: could not list its refs: %s\n", dir, err)
		return 1
	}
	count := 0
	for _, line := range strings.Split(refs, "\n") {
		oid, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		processStarted()
		pub := exec.Command("nats", "pub", "valley.git."+repo+".ref-updated", refUpdated(repo, ref, oid))
		pub.Stderr = stderr
		if err := pub.Run(); err != nil {
			return exitCode(err)
		}
		count++
	}
	fmt.Fprintf(stdout, "replayed %d refs of %s onto the bus\n", count, repo)
	return 0
}

// refUpdated is the ref-updated event a push creating ref at oid in repo
// publishes. The host's publisher hook (nix/valley-host.nix) builds the
// same payload, and the integrator publishes a landing's (integrator/bus.go);
// change one only with the others. A refname may hold ", which is
// escaped as the hook escapes it, and < > &, which are written as they
// are, as the hook writes them.
func refUpdated(repo, ref, oid string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(struct {
		Event string `json:"event"`
		Repo  string `json:"repo"`
		Ref   string `json:"ref"`
		Old   string `json:"old"`
		New   string `json:"new"`
	}{"ref-updated", repo, ref, strings.Repeat("0", len(oid)), oid})
	return strings.TrimSuffix(b.String(), "\n")
}

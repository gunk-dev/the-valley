// valley-restack-transport is how the restacker's end-to-end tests reach
// origin: a stand-in for ssh that serves each push with the real git
// receive-pack, which runs the repository's rendered pre-receive hook, and
// each fetch with the real git upload-pack. A test can hold one push or
// fetch at a point it chooses, or lose a push's answer, so a race or a
// lost connection happens exactly where the test wants it.
//
// git runs it as GIT_SSH_COMMAND, with GIT_SSH_VARIANT=simple, for a URL
// such as ssh://restacker@valley/srv/project.git:
//
//	valley-restack-transport restacker@valley "git-receive-pack '/srv/project.git'"
//
// The user in the URL is the principal. The transport sets
// VALLEY_PRINCIPAL to it for receive-pack, where the rendered hook reads
// it, as the git user's login shell sets it from the key sshd
// authenticated. A principal the client's own environment carries is
// dropped, as the shell drops it, and a URL with no user pushes with no
// principal.
//
// A barrier is armed by writing one line to $VALLEY_BARRIER/hold:
//
//	upload connect          the next fetch or ls-remote, before upload-pack starts
//	receive connect         the next push, before receive-pack starts
//	receive taken [PREFIX]  the next push that updates a ref under PREFIX, once
//	                        receive-pack has finished and before the client hears
//	receive drop [PREFIX]   the next such push: its answer is passed on with no
//	                        ref's status in it, as when origin's answer is lost
//
// The connection the barrier matches claims it, by renaming hold to held,
// so a barrier fires once. It writes what it matched to arrived. Unless it
// drops, it then waits for release to exist, for at most two minutes, and
// goes on.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// releaseWait is the longest a held connection waits for its release. A
// test that never releases fails rather than hangs.
const releaseWait = 2 * time.Minute

func main() {
	if err := serve(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "valley-restack-transport: %v\n", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: valley-restack-transport [user@]host command")
	}
	host, line := args[len(args)-2], args[len(args)-1]
	service, repo, err := parseCommand(line)
	if err != nil {
		return err
	}
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "VALLEY_PRINCIPAL=") {
			env = append(env, kv)
		}
	}
	if user, _, found := strings.Cut(host, "@"); found && user != "" {
		env = append(env, "VALLEY_PRINCIPAL="+user)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	b := armed(service)
	if b == nil || b.stage != "connect" && service != "receive" {
		return syscall.Exec(git, []string{"git", service + "-pack", repo}, env)
	}
	if b.stage == "connect" {
		if b.claim("connect") {
			if err := b.wait(); err != nil {
				return err
			}
		}
		return syscall.Exec(git, []string{"git", service + "-pack", repo}, env)
	}
	return proxy(git, repo, env, b)
}

// parseCommand reads the command git asks ssh to run: git-receive-pack or
// git-upload-pack, and the repository's path as git quotes it.
func parseCommand(line string) (service, repo string, err error) {
	program, quoted, _ := strings.Cut(line, " ")
	switch program {
	case "git-receive-pack":
		service = "receive"
	case "git-upload-pack":
		service = "upload"
	default:
		return "", "", fmt.Errorf("%q asks for neither receive-pack nor upload-pack", line)
	}
	repo, err = unquote(quoted)
	return service, repo, err
}

// unquote reads a path as git's sq_quote writes it: in single quotes, with
// each single quote and exclamation mark in it outside them, escaped.
func unquote(s string) (string, error) {
	var out strings.Builder
	for len(s) > 0 {
		switch {
		case s[0] == '\'':
			end := strings.IndexByte(s[1:], '\'')
			if end < 0 {
				return "", fmt.Errorf("an unterminated quote in %q", s)
			}
			out.WriteString(s[1 : 1+end])
			s = s[2+end:]
		case s[0] == '\\' && len(s) > 1:
			out.WriteByte(s[1])
			s = s[2:]
		default:
			out.WriteByte(s[0])
			s = s[1:]
		}
	}
	return out.String(), nil
}

// barrier is an armed barrier.
type barrier struct {
	dir, stage, prefix string
}

// armed is the barrier armed for a service, or nil.
func armed(service string) *barrier {
	dir := os.Getenv("VALLEY_BARRIER")
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "hold"))
	if err != nil {
		return nil
	}
	f := strings.Fields(string(data))
	if len(f) < 2 || f[0] != service {
		return nil
	}
	b := &barrier{dir: dir, stage: f[1]}
	if len(f) > 2 {
		b.prefix = f[2]
	}
	return b
}

// matches reports whether a push updating refs is the barrier's.
func (b *barrier) matches(refs []string) bool {
	for _, r := range refs {
		if strings.HasPrefix(r, b.prefix) {
			return true
		}
	}
	return false
}

// claim takes the barrier for this connection, and says what it matched.
// Only one connection's rename succeeds.
func (b *barrier) claim(what string) bool {
	if os.Rename(filepath.Join(b.dir, "hold"), filepath.Join(b.dir, "held")) != nil {
		return false
	}
	tmp := filepath.Join(b.dir, ".arrived")
	if os.WriteFile(tmp, []byte(what+"\n"), 0o644) != nil || os.Rename(tmp, filepath.Join(b.dir, "arrived")) != nil {
		return false
	}
	return true
}

// wait waits for the test to release the barrier.
func (b *barrier) wait() error {
	for deadline := time.Now().Add(releaseWait); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(b.dir, "release")); err == nil {
			return nil
		}
	}
	return fmt.Errorf("the barrier in %s was never released", b.dir)
}

// proxy serves a push through the real receive-pack and stands between it
// and the client. The side-band capabilities are taken out of
// receive-pack's advertisement, so its answer comes as plain pkt-lines.
// The commands the client sends are read for the refs they update, and
// passed on as they came, with the pack after them. receive-pack's answer
// is read whole before any of it is passed on, so a barrier that holds
// the push once origin has taken it holds the client too.
func proxy(git, repo string, env []string, b *barrier) error {
	cmd := exec.Command(git, "receive-pack", repo)
	cmd.Env, cmd.Stderr = env, os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	server := bufio.NewReader(out)
	stdout := bufio.NewWriter(os.Stdout)
	for {
		p, err := readPkt(server)
		if err != nil {
			return err
		}
		if p != nil {
			if refs, caps, ok := bytes.Cut(p, []byte{0}); ok {
				var kept []string
				for _, c := range strings.Fields(string(caps)) {
					if !strings.HasPrefix(c, "side-band") {
						kept = append(kept, c)
					}
				}
				p = []byte(string(refs) + "\x00" + strings.Join(kept, " ") + "\n")
			}
		}
		writePkt(stdout, p)
		if p == nil {
			break
		}
	}
	if err := stdout.Flush(); err != nil {
		return err
	}

	client := bufio.NewReader(os.Stdin)
	var refs []string
	for {
		p, err := readPkt(client)
		if err != nil {
			break
		}
		writePkt(in, p)
		if p == nil {
			break
		}
		command, _, _ := bytes.Cut(p, []byte{0})
		if f := strings.Fields(string(command)); len(f) == 3 {
			refs = append(refs, f[2])
		}
	}
	go func() {
		io.Copy(in, client)
		in.Close()
	}()

	var answer [][]byte
	for {
		p, err := readPkt(server)
		if err != nil {
			break
		}
		answer = append(answer, p)
	}
	waitErr := cmd.Wait()

	if b.matches(refs) && b.claim(b.stage+" "+strings.Join(refs, " ")) {
		switch b.stage {
		case "taken":
			if err := b.wait(); err != nil {
				return err
			}
		case "drop":
			var kept [][]byte
			for _, p := range answer {
				if !bytes.HasPrefix(p, []byte("ok ")) && !bytes.HasPrefix(p, []byte("ng ")) && !bytes.HasPrefix(p, []byte("option ")) {
					kept = append(kept, p)
				}
			}
			answer = kept
		}
	}
	for _, p := range answer {
		writePkt(stdout, p)
	}
	if err := stdout.Flush(); err != nil {
		return err
	}
	if waitErr != nil {
		os.Exit(1)
	}
	return nil
}

// readPkt reads one pkt-line, nil for a flush.
func readPkt(r *bufio.Reader) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	n, err := strconv.ParseUint(string(size[:]), 16, 16)
	if err != nil {
		return nil, err
	}
	if n < 4 {
		return nil, nil
	}
	p := make([]byte, n-4)
	_, err = io.ReadFull(r, p)
	return p, err
}

// writePkt writes one pkt-line, a flush for nil.
func writePkt(w io.Writer, p []byte) {
	if p == nil {
		io.WriteString(w, "0000")
		return
	}
	fmt.Fprintf(w, "%04x%s", len(p)+4, p)
}

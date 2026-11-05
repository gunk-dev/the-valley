package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"the-valley/telemetry"
)

// The git user's login shell. sshd runs it for every session as
// `shell -c "<the client's command>"`, and it runs git-shell after three
// things that make the push boundary hold whatever sshd was configured to
// pass through.
//
// The principal is derived here, from the key sshd authenticated, and from
// nowhere else. With ExposeAuthInfo on, sshd names that key in the file
// SSH_USER_AUTH points at. The key is looked up in the same files sshd
// authorized it from, in the same order, and the principal is the tag on
// the first entry naming it. A VALLEY_PRINCIPAL the session arrived with is
// dropped, so no client environment, sshd setting or ~/.ssh/environment can
// name a principal.
//
// Every GIT_ variable the session arrived with is dropped too. git reads
// configuration, hook locations and object directories from them, and none
// of that is the client's to choose.
//
// A push is refused until the host has converged. valley-init records the
// configuration it converged on in a file only it writes, and a push is let
// through only while that record names the configuration this shell was
// rendered for. A valley-init that failed, or has not yet run since a
// switch, therefore leaves no write path open, whatever hooks the
// repositories hold. The operator can hold pushes too, for as long as a
// file the shell is told about exists, whatever valley-init has recorded.
// Fetches stay open either way.
//
// One other command is the shell's own: the telemetry command
// (../telemetry), which a session with the observe or report grant uses to
// read the host's diagnostics or send its own. The shell parses it whole
// against the command's closed grammar, checks the grant in the telemetry
// grants file, and passes the collector one request line naming the
// principal it derived. Nothing of the command reaches git-shell or any
// other program, refused or not, and neither a hold nor a missing
// convergence stops it: diagnosing a paused host is when it is needed.

const shellUsage = `usage: valleyhook shell [flags] -- [-c COMMAND]

The valley git user's login shell: derives the pushing principal from the
key sshd authenticated, drops the GIT_ environment a session arrived with,
refuses a push until valley-init has converged on this configuration, and
then runs git-shell.

flags:
  --converged FILE       the record valley-init writes when it converges
  --expect ID            the configuration that record has to name
  --hold FILE            a file whose existence holds every push
  --authorized-keys FILE a file sshd authorizes the git user's keys from, in
                         the order sshd reads them. Repeatable.
  --telemetry-socket PATH
                         the telemetry collector's socket; without it, the
                         telemetry command is refused
  --telemetry-grants FILE
                         who holds the observe and report grants
`

// tagged is the principal option on a key's entry.
var tagged = regexp.MustCompile(`environment="VALLEY_PRINCIPAL=([^"]*)"`)

func shell(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, shellUsage) }
	converged := fs.String("converged", "", "the record valley-init writes when it converges")
	expect := fs.String("expect", "", "the configuration that record has to name")
	hold := fs.String("hold", "", "a file whose existence holds every push")
	telemetrySocket := fs.String("telemetry-socket", "", "the telemetry collector's socket")
	telemetryGrants := fs.String("telemetry-grants", "", "the telemetry grants")
	var keyFiles []string
	fs.Func("authorized-keys", "a file the git user's keys are authorized from; repeatable", func(path string) error {
		keyFiles = append(keyFiles, path)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *converged == "" || *expect == "" {
		fmt.Fprint(stderr, shellUsage)
		return 2
	}
	rest := fs.Args()

	if len(rest) == 2 && rest[0] == "-c" && telemetry.IsCommand(rest[1]) {
		principal := authenticatedPrincipal(os.Getenv("SSH_USER_AUTH"), keyFiles)
		return telemetryCommand(rest[1], principal, *telemetrySocket, *telemetryGrants, os.Stdin, os.Stdout, stderr)
	}

	if len(rest) == 2 && rest[0] == "-c" && isPush(rest[1]) {
		if *hold != "" {
			if _, err := os.Lstat(*hold); !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(stderr, "valley: pushes to this host are held by its operator; fetching is open")
				return 1
			}
		}
		if !hasConverged(*converged, *expect) {
			// The check worker (status/worker.go) tries a push refused
			// with these words again soon, so keep them as they are.
			fmt.Fprintln(stderr, "valley: pushes to this host are paused until valley-init converges on its configuration; fetching is open")
			return 1
		}
	}

	env := sessionEnvironment(os.Environ())
	if principal := authenticatedPrincipal(os.Getenv("SSH_USER_AUTH"), keyFiles); principal != "" {
		env = append(env, "VALLEY_PRINCIPAL="+principal)
	}

	gitShell, err := gitShellPath()
	if err != nil {
		fmt.Fprintf(stderr, "valley: %v\n", err)
		return 1
	}
	err = syscall.Exec(gitShell, append([]string{"git-shell"}, rest...), env)
	fmt.Fprintf(stderr, "valley: could not run git-shell: %v\n", err)
	return 1
}

// isPush reports whether a command git-shell would run is receive-pack.
// git-shell reads "git" followed by whitespace as "git-", and runs a
// command whose name is followed by a space or nothing; anything that
// could read as receive-pack is treated as one.
func isPush(command string) bool {
	if len(command) > 3 && strings.HasPrefix(command, "git") && strings.ContainsRune(" \t\n\r\v\f", rune(command[3])) {
		command = "git-" + command[4:]
	}
	return strings.HasPrefix(command, "git-receive-pack")
}

// hasConverged reads valley-init's record. Anything but a record naming
// the expected configuration — no file, an unreadable one, another
// configuration's — is not converged.
func hasConverged(path, expect string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(io.LimitReader(f, 256), 256).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return strings.TrimSpace(line) == expect
}

// sessionEnvironment is the session's environment less every GIT_
// variable and any principal it arrived with.
func sessionEnvironment(inherited []string) []string {
	var env []string
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") || name == "VALLEY_PRINCIPAL" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// authenticatedPrincipal is the principal the authenticated key acts as:
// the tag on the first entry naming that key, in the files sshd read, in
// order. A session sshd names no key for — no SSH_USER_AUTH, or no public
// key in it — and a key no file names, act as nobody.
func authenticatedPrincipal(authFile string, keyFiles []string) string {
	key := authenticatedKey(authFile)
	if key == "" {
		return ""
	}
	for _, path := range keyFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if keyBlob(line) != key {
				continue
			}
			if m := tagged.FindStringSubmatch(line); m != nil {
				return m[1]
			}
			return ""
		}
	}
	return ""
}

// authenticatedKey is the public key sshd says authenticated the session.
// ExposeAuthInfo writes one line per method that succeeded; a public key
// line is "publickey <type> <base64>". Two public keys naming different
// keys are refused as no key at all.
func authenticatedKey(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return ""
	}
	found := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "publickey" {
			continue
		}
		if found != "" && found != fields[2] {
			return ""
		}
		found = fields[2]
	}
	return found
}

// keyBlob is the base64 key in an authorized_keys line: the field after
// the first one naming a key type.
func keyBlob(line string) string {
	fields := strings.Fields(line)
	for i := 0; i+1 < len(fields); i++ {
		f := fields[i]
		if strings.HasPrefix(f, "ssh-") || strings.HasPrefix(f, "ecdsa-") || strings.HasPrefix(f, "sk-") {
			return fields[i+1]
		}
	}
	return ""
}

// gitShellPath is the git-shell beside the git this program was built
// with.
func gitShellPath() (string, error) {
	if filepath.IsAbs(gitProgram) {
		return filepath.Join(filepath.Dir(gitProgram), "git-shell"), nil
	}
	return exec.LookPath("git-shell")
}

// telemetryCommand answers a telemetry command: refused unless the host
// serves telemetry, the session's key names a principal, the command
// parses, and the principal holds the grant its operation needs; then
// relayed to the collector as one request line, followed for a report by
// the records on standard input, with the collector's answer copied back.
// It exits 0 only when that answer ended.
func telemetryCommand(cmd, principal, socket, grantsFile string, stdin io.Reader, stdout, stderr io.Writer) int {
	if socket == "" || grantsFile == "" {
		fmt.Fprintln(stderr, "valley: this host serves no telemetry")
		return 1
	}
	if principal == "" {
		fmt.Fprintln(stderr, "valley: telemetry is for a key that names a principal")
		return 1
	}
	req, err := telemetry.ParseCommand(cmd)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %v\n", err)
		return 2
	}
	req.Principal = principal
	g := grants{}
	if err := g.read(grantsFile); err != nil {
		fmt.Fprintf(stderr, "valley: the telemetry grants do not read: %v\n", err)
		return 1
	}
	if !g[principal][req.Grant()] {
		fmt.Fprintf(stderr, "valley: %s does not hold the telemetry %s grant\n", principal, req.Grant())
		return 1
	}
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		fmt.Fprintf(stderr, "valley: the telemetry collector is not answering: %v\n", err)
		return 1
	}
	defer conn.Close()
	line, err := json.Marshal(req)
	if err != nil {
		return 1
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(append(line, '\n')); err != nil {
		fmt.Fprintf(stderr, "valley: the telemetry collector is not answering: %v\n", err)
		return 1
	}
	conn.SetWriteDeadline(time.Time{})
	uc := conn.(*net.UnixConn)
	if req.Op == telemetry.OpReport {
		go func() {
			io.Copy(conn, io.LimitReader(stdin, telemetry.MaxReportBytes+1))
			uc.CloseWrite()
		}()
	} else {
		uc.CloseWrite()
	}
	tail := &lastBytes{}
	if _, err := io.Copy(io.MultiWriter(stdout, tail), conn); err != nil {
		return 1
	}
	// The collector's answer ends with an end line, and only an answer
	// that did exits 0: a refusal, an error, or a collector that stopped
	// half way does not read as success to the client.
	if !strings.Contains(string(tail.b), `"kind":"end"`) {
		return 1
	}
	return 0
}

// lastBytes keeps the last bytes written through it.
type lastBytes struct{ b []byte }

func (l *lastBytes) Write(p []byte) (int, error) {
	l.b = append(l.b, p...)
	if len(l.b) > 512 {
		l.b = l.b[len(l.b)-512:]
	}
	return len(p), nil
}

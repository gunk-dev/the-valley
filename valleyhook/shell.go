package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
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

	if len(rest) == 2 && rest[0] == "-c" && isPush(rest[1]) {
		if *hold != "" {
			if _, err := os.Lstat(*hold); !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(stderr, "valley: pushes to this host are held by its operator; fetching is open")
				return 1
			}
		}
		if !hasConverged(*converged, *expect) {
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

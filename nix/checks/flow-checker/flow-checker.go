// Command flow-checker holds what the flow checks (nix/checks/flow.nix)
// verify beside klaus's own tests:
//
//	flow-checker version WANT PROGRAM [ARG...]
//
// runs PROGRAM ARG... --version and fails unless it exits 0 having printed
// exactly the line WANT, and nothing on stderr, and
//
//	flow-checker sockets DIR
//
// fails if DIR holds a socket at any depth: a tmux server some test reached
// through the default socket directory rather than its own. Each failure
// names what it found and exits 1.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "flow-checker:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) >= 3 && args[0] == "version":
		return version(args[1], args[2:])
	case len(args) == 2 && args[0] == "sockets":
		return sockets(args[1])
	}
	return errors.New("usage: flow-checker version WANT PROGRAM [ARG...] | flow-checker sockets DIR")
}

// version fails unless command, a program and its arguments, given
// --version, exits 0 having printed exactly the line want, and nothing on
// stderr.
func version(want string, command []string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(command[0], append(command[1:], "--version")...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	name := strings.Join(cmd.Args, " ")
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	if got := string(out); got != want+"\n" || stderr.Len() > 0 {
		return fmt.Errorf("%s printed %q and %q on stderr, want %q and nothing", name, got, stderr.String(), want+"\n")
	}
	return nil
}

// sockets fails if dir holds a socket at any depth.
func sockets(dir string) error {
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSocket != 0 {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return fmt.Errorf("a test reached tmux through the default socket directory: %s", strings.Join(found, ", "))
	}
	return nil
}

// valleyhook is the pre-receive hook of a protected project on a valley
// host: the whole of what a push may write there. The NixOS module
// (nix/valley-host.nix) installs a two-line script as the hook, and that
// script only hands this program the pushing principal, the project's
// declared protection, and the grants files. Every decision about a ref is
// made here, so the policy lives in one place.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const usage = `usage: valleyhook pre-receive [flags] < ref updates

The pre-receive hook of a protected project on a valley host. git hands it
one line per ref a push would update: the old id, the new id and the
refname. It accepts the push only if the pushing principal may make every
one of those writes. One refusal refuses the whole push, because a
pre-receive hook answers for the push and not for each ref.

Who may write a ref, decided by the first rule that matches it:

  refs/replace/*              nobody. A replacement ref makes one object
                              stand in for another wherever git looks it
                              up, so it would change what every reader of
                              the repository sees.
  refs/the-valley/attestations/*
                              anyone, and only to create one. An
                              attestation is a record, and a record that
                              can be rewritten or dropped is not one.
  a protected ref             the project's declared writers.
  refs/the-valley/integration-requests/*
                              principals holding the request grant. They
                              may file, replace and withdraw a request.
  a ref an allow entry opens  the principals that entry names.
  refs/heads/*                anyone: these are topic branches.
  anything else               nobody. Tags, notes and every other namespace
                              stay closed until the project's protection
                              opens a pattern of them by name.

A delete is a write, and is decided like one.

The integrator's own writes are not pushes. A controller updates the refs
it writes on the host, and no push hook sees a local write.

A protection file or a grants file that cannot be read refuses the whole
push. The hook fails closed: a policy it cannot read is not a policy it
can apply.

flags:
  --project NAME      the project, named in what a refusal says
  --protection FILE   the project's protection block, as the host
                      declaration exports it (JSON)
  --grants FILE       grants, one "<grant> <principal>" per line. Repeat
                      the flag to read more than one file; what they grant
                      adds up. A file that does not exist grants nothing.
  --principal NAME    the pushing principal; empty for a key that names
                      none
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "pre-receive":
		os.Exit(preReceive(os.Args[2:], os.Stdin, os.Stderr))
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "valleyhook: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// preReceive runs the hook over the updates on stdin and returns its exit
// status: 0 accepts the push, anything else refuses it.
func preReceive(args []string, stdin io.Reader, stderr io.Writer) int {
	fs := flag.NewFlagSet("pre-receive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	project := fs.String("project", "", "the project, named in what a refusal says")
	protectionFile := fs.String("protection", "", "the project's protection block (JSON)")
	principal := fs.String("principal", "", "the pushing principal")
	var grantFiles []string
	fs.Func("grants", "a grants file; repeatable", func(path string) error {
		grantFiles = append(grantFiles, path)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" || *protectionFile == "" {
		fmt.Fprintf(stderr, "valleyhook: --project and --protection are required\n")
		return 2
	}

	// Everything the decision needs is read before any ref is looked at,
	// so a policy that cannot be read refuses the push as a whole rather
	// than part of the way through it.
	p := policy{project: *project, grants: grants{}}
	if err := readProtection(*protectionFile, &p.protection); err != nil {
		return failClosed(stderr, err)
	}
	for _, path := range grantFiles {
		if err := p.grants.read(path); err != nil {
			return failClosed(stderr, err)
		}
	}

	refused := false
	lines := bufio.NewScanner(stdin)
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) == "" {
			continue
		}
		fields := strings.Fields(lines.Text())
		if len(fields) != 3 {
			return failClosed(stderr, fmt.Errorf("git sent %q, which is not an old id, a new id and a refname", lines.Text()))
		}
		u := update{old: fields[0], new: fields[1], ref: fields[2]}
		if refusal := p.decide(*principal, u); refusal != "" {
			fmt.Fprintln(stderr, refusal)
			refused = true
		}
	}
	if err := lines.Err(); err != nil {
		return failClosed(stderr, err)
	}
	if refused {
		return 1
	}
	return 0
}

// failClosed refuses the whole push for a reason that is not about any one
// ref.
func failClosed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "valley: the push is refused because its policy could not be read: %v\n", err)
	return 1
}

// readProtection reads the project's protection block. A field this
// program does not know refuses the read: the block is exported from the
// schema this program ships beside, so an unknown field means the two have
// drifted apart, and the field could be a restriction the hook would
// otherwise skip.
func readProtection(path string, p *protection) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(p); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

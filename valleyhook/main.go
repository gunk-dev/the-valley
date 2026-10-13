// valleyhook is the pre-receive hook of every project a valley host
// serves: the whole of what a push may write there. The NixOS module
// (nix/valley-host.nix) installs a short script as the hook, and that
// script only hands this program the pushing principal, the project's
// declared push policy, the grants files and the keys attestations are
// checked against. Every decision about a ref is made here, so the policy
// lives in one place.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"the-valley/change"
)

const usage = `usage: valleyhook pre-receive [flags] < ref updates
       valleyhook shell [flags] -- [-c COMMAND]

shell is the git user's login shell; see "valleyhook shell -h".

The pre-receive hook of every project on a valley host. git hands it one
line per ref a push would update: the old id, the new id and the refname.
It accepts the push only if the pushing principal may make every one of
those writes. One refusal refuses the whole push, atomic or not, because a
pre-receive hook answers for the push and not for each ref.

A write is allowed only if every rule that applies to its ref allows it:

  refs/replace/*, refs/notes/*, and refs/the-valley/* other than the three
  namespaces below
                    nobody, whatever a declaration says. A replacement ref
                    makes one object stand in for another wherever git
                    looks it up; a note attaches text every reader sees;
                    the rest of the valley's namespace is the machinery's.
  a symbolic ref    nobody. A push to one would write the ref it points at.
  refs/heads/*      anyone: topic branches.
  refs/the-valley/attestations/<digest>/<key hash>
                    anyone, and only to create one. The ref must point at
                    a tree of notes about that digest, read within fixed
                    bounds, each opening under the --known-signers keys
                    the way attest verify opens it, with a verified
                    signature under that key hash.
  refs/the-valley/integration-requests/*
                    principals holding the request grant. They may file,
                    replace and withdraw a request.
  refs/the-valley/changes/main/<change ID>
                    a change record: a commit whose tree holds one file,
                    record.json. Any named principal may create one, and
                    owns it; its first version's head is the commit's one
                    parent. The owner or a holder of the request grant may
                    add a version, whose head is the commit's second parent
                    after the record it replaces, or withdraw the change,
                    with that record as the one parent. Only a holder of
                    the request grant may delete one. Every other write is
                    refused. A push may update at most 32 records, and a
                    repository may hold at most 2000.
  anything else     principals a named grant of the project opens the ref
                    to. Tags open only this way.
  a protected ref   also a declared writer of the project. Protection adds
                    this requirement and removes none of the ones above.

The integrator's own writes are not pushes. A controller updates the refs
it writes on the host, and no push hook sees a local write.

A push may update at most 1000 refs. One that updates more is refused as a
whole, at the first update past the bound, before any ref is decided.

A policy, grants or keys file that cannot be read refuses the whole push.
The hook fails closed: a policy it cannot read is not a policy it can
apply. A grants or keys file that does not exist grants nothing and names
no key.

flags:
  --project NAME        the project, named in what a refusal says
  --policy FILE         the project's push policy, as the host declaration
                        exports it (JSON): its protected refs, their
                        writers, and its named grants
  --grants FILE         grants, one "<grant> <principal>" per line. Repeat
                        the flag to read more than one file; what they
                        grant adds up.
  --known-signers FILE  verifier keys, one per line, that an attestation's
                        signature is checked against. Repeatable. With no
                        key named, no attestation is accepted.
  --principal NAME      the pushing principal; empty for a key that names
                        none
  --then FILE           a further pre-receive hook, run only once this one
                        accepts the push, with the same input. It can refuse
                        what this one accepts and never the reverse.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "pre-receive":
		os.Exit(preReceive(os.Args[2:], os.Stdin, os.Stderr, gitRepository{}))
	case "shell":
		os.Exit(shell(os.Args[2:], os.Stderr))
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "valleyhook: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// maxUpdates is how many refs one push may update.
const maxUpdates = 1000

// preReceive runs the hook over the updates on stdin and returns its exit
// status: 0 accepts the push, anything else refuses it.
func preReceive(args []string, stdin io.Reader, stderr io.Writer, repo repository) int {
	fs := flag.NewFlagSet("pre-receive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	project := fs.String("project", "", "the project, named in what a refusal says")
	policyFile := fs.String("policy", "", "the project's push policy (JSON)")
	principal := fs.String("principal", "", "the pushing principal")
	then := fs.String("then", "", "a further pre-receive hook")
	var grantFiles, keyFiles []string
	fs.Func("grants", "a grants file; repeatable", func(path string) error {
		grantFiles = append(grantFiles, path)
		return nil
	})
	fs.Func("known-signers", "a verifier keys file; repeatable", func(path string) error {
		keyFiles = append(keyFiles, path)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" || *policyFile == "" {
		fmt.Fprintf(stderr, "valleyhook: --project and --policy are required\n")
		return 2
	}

	// Everything the decision needs is read before any ref is looked at,
	// so a policy that cannot be read refuses the push as a whole rather
	// than part of the way through it.
	p := policy{project: *project, grants: grants{}, repo: repo}
	if err := readPolicy(*policyFile, &p.push); err != nil {
		return failClosed(stderr, err)
	}
	for _, path := range grantFiles {
		if err := p.grants.read(path); err != nil {
			return failClosed(stderr, err)
		}
	}
	for _, path := range keyFiles {
		keys, err := readVerifiers(path)
		if err != nil {
			return failClosed(stderr, err)
		}
		p.verifiers = append(p.verifiers, keys...)
	}

	// The updates are read one line at a time, and a push that updates
	// more refs than the bound is refused at the first line past it,
	// before anything more is kept. The rest of the input is read and
	// dropped, so that git is not left writing to a hook that has gone.
	var input bytes.Buffer
	var updates []update
	lines := bufio.NewScanner(stdin)
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) == "" {
			continue
		}
		if len(updates) == maxUpdates {
			_, _ = io.Copy(io.Discard, stdin)
			fmt.Fprintf(stderr, "valley: the push is refused: it updates more than %d refs, the most one push may\n", maxUpdates)
			return 1
		}
		fields := strings.Fields(lines.Text())
		if len(fields) != 3 {
			return failClosed(stderr, fmt.Errorf("git sent %q, which is not an old id, a new id and a refname", lines.Text()))
		}
		updates = append(updates, update{old: fields[0], new: fields[1], ref: fields[2]})
		input.WriteString(lines.Text() + "\n")
	}
	if err := lines.Err(); err != nil {
		return failClosed(stderr, err)
	}

	// The bounds on change records that are about the push as a whole are
	// checked before any record is read.
	records := 0
	for _, u := range updates {
		if strings.HasPrefix(u.ref, change.RefPrefix) {
			records++
			if isZero(u.old) && !isZero(u.new) {
				p.recordCreates++
			}
		}
	}
	if records > maxRecordUpdates {
		fmt.Fprintf(stderr, "valley: the push is refused: it updates %d change records, and a push may update at most %d\n", records, maxRecordUpdates)
		return 1
	}
	if p.recordCreates > 0 {
		var err error
		if p.existingRecords, err = repo.records(); err != nil {
			return failClosed(stderr, err)
		}
	}

	// Which of the refs are symbolic is read once for the whole push.
	refs := make([]string, len(updates))
	for i, u := range updates {
		refs[i] = u.ref
	}
	symrefs, err := repo.symbolic(refs)
	if err != nil {
		return failClosed(stderr, err)
	}
	p.symrefs = symrefs

	refused := false
	for _, u := range updates {
		if refusal := p.decide(*principal, u); refusal != "" {
			fmt.Fprintln(stderr, refusal)
			refused = true
		}
	}
	if refused {
		return 1
	}
	if *then != "" {
		return runThen(*then, input.Bytes(), stderr)
	}
	return 0
}

// runThen runs the composed hook over the same input, in this hook's
// environment, and its exit status is the push's.
func runThen(path string, input []byte, stderr io.Writer) int {
	cmd := exec.Command(path)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = os.Stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	default:
		return failClosed(stderr, fmt.Errorf("the composed hook %s did not run: %w", path, err))
	}
}

// failClosed refuses the whole push for a reason that is not about any one
// ref.
func failClosed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "valley: the push is refused because its policy could not be applied: %v\n", err)
	return 1
}

// readPolicy reads the project's push policy. A field this program does
// not know refuses the read: the policy is exported from the schema this
// program ships beside, so an unknown field means the two have drifted
// apart, and the field could be a restriction the hook would otherwise
// skip.
func readPolicy(path string, p *pushPolicy) error {
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

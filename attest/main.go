// attest composes, signs, stores and verifies a valley attestation: a
// host-signed statement that a check ran over a tree with a result
// (dcr-0de694f). The statement's shape is schema/attestation.cue; this
// program only composes documents that satisfy it.
package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `usage: attest <command> [flags]

  run       run a repository's checks and store a signed attestation
  verify    check an attestation's signature and re-check its digests
  digest    print the valley tree digest of a revision
  help      print this message

A statement is signed with a detached SSHSIG signature under the namespace
"` + defaultNamespace + `" and stored at a ref keyed by the subject
digest: ` + refPrefix + `/<tree digest>/<signer fingerprint>.
The host signs; there is no unsigned mode.

run flags:
  --repo DIR          repository to attest (default: the git toplevel here)
  --rev REV           revision whose tree is the subject (default: HEAD)
  --check NAME[=ATTR] flake check to run, repeatable; defaults to every
                      check the flake defines for this system
  --command NAME=CMD  effectful check to run, repeatable
  --environment NAME  environment an effectful statement names (default "local")
  --key PATH          ssh private key the host signs with (required)
  --namespace NS      SSHSIG namespace (default "` + defaultNamespace + `")
  --provenance FILE   json run provenance, carried in every statement
  --schema FILE       attestation schema to vet against
  --system SYSTEM     flake system (default: this machine's)
  --push REMOTE       publish: one atomic push of the branch and the refs
  --branch REF        branch pushed alongside (default: the current branch)

verify flags:
  --statement FILE        the statement, as stored
  --signature FILE        its detached signature
  --allowed-signers FILE  ssh allowed-signers file
  --principal ID          expected signer (default: whoever the file says)
  --namespace NS          SSHSIG namespace (default "` + defaultNamespace + `")
  --schema FILE           attestation schema to vet against
  --repo DIR              repository to re-check the recorded digests against
  --rev REV               revision in it (default: HEAD)

digest flags:
  --repo DIR          repository (default: the git toplevel here)
  --rev REV           revision (default: HEAD)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "digest":
		err = cmdDigest(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "attest: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "attest: %v\n", err)
		os.Exit(1)
	}
}

// repeatable collects a flag given more than once.
type repeatable []string

func (r *repeatable) String() string { return fmt.Sprint(*r) }
func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	return fs
}

func cmdDigest(args []string) error {
	fs := newFlagSet("digest")
	repo := fs.String("repo", "", "repository")
	rev := fs.String("rev", "HEAD", "revision")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := repoRoot(*repo)
	if err != nil {
		return err
	}
	entries, err := gitTreeEntries(root, *rev)
	if err != nil {
		return err
	}
	fmt.Printf("%s:%s\n", treeScheme, treeDigest(entries))
	return nil
}

func repoRoot(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	return gitLine(dir, "rev-parse", "--show-toplevel")
}

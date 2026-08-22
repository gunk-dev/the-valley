// identity compiles a valley instance's identity registry — the declared
// CUE document naming who the instance grants something to (dcr-b87f6e8) —
// into the artifacts its enforcement boundaries check.
//
// It renders, and enforces nothing itself. sshd refuses a key that is not
// in the authorized_keys it writes, and the integrator refuses evidence
// signed by nobody in the known-signers it writes; this program is the step
// between the document and those two gates.
package main

import (
	"fmt"
	"os"
)

const usage = `usage: identity <command> [flags]

  compile  render the registry at a repository's tip into the artifacts
           the enforcement boundaries check
  help     print this message

The registry is one declared CUE document in the instance repository
(dcr-b87f6e8), read from that repository's integrated tip and never from a
working tree: an edit in a branch or a worktree governs nothing until it
lands. Two artifacts come out of it.

The known-signers file is every attestation-capable key, written as the
note format's verifier keys. It is both the integrator's acceptance list
and any reader's ` + "`attest verify --known-keys`" + `, so it holds every key that
signs — the integrator's own included. A key's signing name is recorded per
key, because the note format's key hash binds the name to the key: the same
key published under a second name is a second, unrelated verifier key.

The authorized_keys file is every key of every principal holding a grant at
a boundary of kind "git-push", each line tagging its key with the
principal's name. sshd puts that tag in the environment of the receive-pack
the pre-receive hook runs under, which is the only thing that can tell one
pusher from another over a shared git user.

Expiry is enforced here, because a document has no clock. An entry whose
expiry has arrived is omitted from both artifacts and noted on stderr, and
the rest of the registry still compiles — so access ends at the first
convergence after expiry.

One state is refused rather than compiled: a registry where, after the
expiry cut, no remaining principal holds a grant at a boundary of kind
"registry". Nobody would govern the registry's own stream, so the whole
render fails with an error naming governanceOrphaned and the last good
artifacts stand. The rule is here and not in the schema for the same reason
expiry is — the state is reached by the clock, and a document has none.

A render that fails leaves the last good artifacts exactly as they were.
Both are computed in full before either is written, and each is replaced by
rename. A schema violation, an unreachable repository or a bug here costs
the compilation and never the git user's access.

flags:
  --repo DIR             the bare instance repository carrying the registry
  --ref REF              the ref whose tip is read (default refs/heads/main)
  --dir DIR              the registry's directory in that tip (default
                         identity)
  --schema FILE          the identity schema (default
                         $VALLEY_IDENTITY_SCHEMA)
  --known-signers FILE   where the verifier keys are written
  --authorized-keys FILE where the tagged authorized_keys is written
  --now YYYY-MM-DD       the day expiry is judged against (default: today,
                         UTC)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "compile":
		err = compile(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "identity: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "identity: %v\n", err)
		os.Exit(1)
	}
}

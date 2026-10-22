// identity compiles a valley instance's identity registry — the declared
// CUE document naming who the instance grants something to (dcr-b87f6e8) —
// into the artifacts its enforcement boundaries check.
//
// It renders, and enforces nothing itself. sshd refuses a key that is not
// in the authorized_keys it writes, the integrator refuses evidence signed
// by nobody in the known-signers it writes, and the pre-receive hook
// refuses an integration request or a restack write from anybody not in
// the grants it writes; this program is the step between the document and
// those three gates. It also writes the two approval signers files, which
// no gate reads.
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
lands. Three artifacts come out of it, and two approval signers files when
asked for.

The known-signers file is every attestation-capable key, written as the
note format's verifier keys. It is both the integrator's acceptance list
and any reader's ` + "`attest verify --known-keys`" + `, so it holds every key that
signs — the integrator's own included. A key's signing name is recorded per
key, because the note format's key hash binds the name to the key: the same
key published under a second name is a second, unrelated verifier key.

The authorized_keys file is every key of every principal holding a grant at
a boundary of kind "git-push", each line tagging its key with the
principal's name. The git user's shell reads the tag off the entry of the
key sshd authenticated and hands it to the pre-receive hook, which is the
only thing that can tell one pusher from another over a shared git user.

The grants file is every grant the pre-receive hook checks, one line per
holder: "request <principal>" for each principal holding the approve grant,
at a boundary of kind "approve" or of kind "request", and "restack
<principal>" for each holding one at a boundary of kind "restack". The two
kinds are one grant: the approve grant, also accepted as request. A
principal holding both is written once, and a registry using either kind
compiles the same file. The hook takes writes to the integration request
namespace only from a holder of the approve grant. It takes a version
added to another principal's ready change, and a restack report, only from
one named under restack.

The two approval signers files hold the keys whose signed approvals
count. They are written as the note format's verifier keys, and each line
is named by the principal who approves with its key. No gate reads them.

approval-signers lists every key of every unexpired human holding the
approve grant. approval-signers-hardware lists the keys from it whose
entry carries a YubiKey attestation that verifies and whose touch policy
is always: the YubiKey asks for a touch before every signature.

An attestation is two PEM certificates: the one the YubiKey wrote for the
PIV slot holding the key, and the YubiKey's own f9 certificate, which
signed it. It verifies when the slot certificate chains through the f9
certificate and Yubico's PIV intermediates to Yubico Attestation Root 1,
and the key it names is the entry's public key. This program carries that
root and those intermediates, and a registry cannot add others. An
attestation that does not verify fails the render. So does one stating a
PIN or touch policy other than the values Yubico specifies: 1, 2 and 3.
Each attestation that verifies is reported on stderr with the YubiKey's
serial number, firmware, PIN policy and touch policy, for whoever reviews
the enrollment.

A key that approves belongs to one principal: one also listed under
another principal fails the render.

The registry is read with git's replacement refs off and with no GIT_
variable or user or system configuration inherited, so the registry
compiled is the one the tip holds and not an object something else has
been made to stand in for.

Expiry is enforced here, because a document has no clock. An entry whose
expiry has arrived is omitted from every artifact and noted on stderr, and
the rest of the registry still compiles — so access ends at the first
convergence after expiry.

One key authorized as two principals is refused too, whether both entries
are the registry's or one is the host's own (--declared-keys). sshd
authorizes a key by the first entry that names it, so which principal such
a key pushes as would depend on the order of lines and files.

One state is refused rather than compiled: a registry where, after the
expiry cut, no remaining principal holds a grant at a boundary of kind
"registry". Nobody would govern the registry's own stream, so the whole
render fails with an error naming governanceOrphaned and the last good
artifacts stand. The rule is here and not in the schema for the same reason
expiry is — the state is reached by the clock, and a document has none.

A render that fails leaves the last good artifacts exactly as they were.
All of them are computed in full before any is written, and each is
replaced by rename. A schema violation, an unreachable repository or a bug
here costs the compilation and never the git user's access.

flags:
  --repo DIR             the bare instance repository carrying the registry
  --ref REF              the ref whose tip is read (default refs/heads/main)
  --dir DIR              the registry's directory in that tip (default
                         identity)
  --schema FILE          the identity schema (default
                         $VALLEY_IDENTITY_SCHEMA)
  --known-signers FILE   where the verifier keys are written
  --authorized-keys FILE where the tagged authorized_keys is written
  --grants FILE          where the grants the pre-receive hook checks are
                         written
  --approval-signers FILE
                         where the verifier keys of every approval key are
                         written. None is written without it.
  --approval-signers-hardware FILE
                         where the verifier keys of the hardware approval
                         keys are written. None is written without it.
  --declared-keys FILE   the authorized_keys lines the host declares by
                         hand. A registry key that is also declared under
                         another tag, or untagged, fails the render: sshd
                         reads the declared entry first, so the key would
                         never push as its registry principal.
  --now YYYY-MM-DD       the day expiry, and the validity of an
                         attestation's certificates, are judged against
                         (default: today, UTC)
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

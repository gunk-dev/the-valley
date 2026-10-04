# sigverify

sigverify verifies OpenSSH signatures and refuses any FIDO security-key signature made without a
touch. It reads standard SSHSIG, the format `ssh-keygen -Y sign` writes and git uses for SSH-signed
tags. It is a Go library (`the-valley/sigverify`) and a command (`sigverify`).

sigverify judges one signature at a time and keeps no state. What a host must do around it to gate
releases safely is in [The caller's contract](#the-callers-contract).

## Why the verifier must check for a touch

The operator's approvals and host releases are meant to be authorized by a touch on a hardware key.
Agents and the interactive coordinator run as the operator's Unix user. They can do anything the
operator's files allow, so the touch is the one step they cannot perform.

A security key signs authenticator data, not the message itself. That data holds the message's hash
and a flags byte. The user-presence bit (UP, `0x01`) is set only when someone touched the key. The
user-verification bit (UV, `0x04`) is set only when the key also checked a PIN or biometric. Both
bits are inside the signed bytes, so nobody can change them after signing.

Whether the key asks for a touch is decided by a flag in the key stub, the private key file on disk.
Anything running as the operator can clear that flag, either with
`ssh-keygen -p -O no-touch-required` (OpenSSH 10.5) or by editing the stub's bytes. The plugged-in
key then signs without a touch, and the signature records UP as clear. Clearing the stub's
verify-required flag the same way drops the PIN, and the signature records UV as clear.

Standard verification never looks at the bits:

- `ssh-keygen -Y verify` logs the flags at debug level and accepts the signature.
- `git verify-tag` with `gpg.format=ssh` calls `ssh-keygen -Y verify`, so it accepts it too.
- An allowed-signers file cannot ask for a touch. Its only options are `cert-authority`,
  `namespaces`, `valid-after` and `valid-before`.

So the presence check has to be in the verifier. sigverify does it on every security-key signature,
and nothing turns it off.

## Signer classes

Every line of the allowed-signers file names a signer of one class:

- **`fido-sk`**: a FIDO security key, `sk-ssh-ed25519@openssh.com` or
  `sk-ecdsa-sha2-nistp256@openssh.com`. Its signatures carry UP and UV, and UP is always required.
- **`tkey-signer`**: a Tillitis TKey, on a line marked `tkey-signer`. The line's key must be
  `ssh-ed25519`.
- **`software`**: any other key: `ssh-ed25519`, ECDSA, or RSA of at least 2048 bits. Its signatures
  prove nothing about presence.

A TKey signature is plain `ssh-ed25519` and carries no flags. It can still be trusted to need a
touch, for two reasons. The stock `tkey-device-signer` app requires a touch for every signature. And
the TKey derives its ed25519 key from the hash of the app it runs, so an app built to skip the touch
has a different key. The `tkey-signer` line pins the key, and so pins the app. The marker may carry
the app's version for bookkeeping, as `tkey-signer="1.0.0"`. sigverify reports the version and never
compares it with anything. Upgrading the app changes the key, so it is a planned trust-root
rotation.

A class marker is an enrollment assertion: sigverify believes it and cannot check it. A
`tkey-signer` line holding a software key makes that software key pass a TKey-only call. A `fido-sk`
line holding a key made in software makes it pass as a security key, with whatever flags its maker
chose. Nothing in a signature's bytes shows any of these:

- that the key lives in genuine hardware
- which app a TKey runs, or that it is the version the line records
- that a TKey was touched for this signature, or that its user was verified
- that a touch was an informed approval of what was signed

So every line must come from a trusted enrollment of genuine hardware. Requirement 3 of
[the caller's contract](#the-callers-contract) says what that means.

Each call names the classes it accepts. By default only `fido-sk` signers verify. A call accepts
other classes only when it asks for them, with `--allow-class`. Giving `--allow-class` replaces the
default. So:

- A release or an approval uses the default. A TKey listed in the same file cannot sign it.
- A trust-root change passes `--allow-class tkey-signer`. A security key cannot sign it.
- `--allow-non-sk` adds `software`. It never makes an unmarked key count as a TKey: only a
  `tkey-signer` line does that.

A TKey cannot prove user verification, so `--require-uv` refuses TKey signatures.

## What is verified

A signature is accepted only if every one of these holds, checked in this order:

1. It is a well-formed SSHSIG, armored or raw, with hash `sha256` or `sha512`.
2. Its namespace is the one the caller names. The git-tag command always uses `git`.
3. The signature verifies over the message.
4. A line of the allowed-signers file lists the key, and all of these hold for that line:
   - Its class is one the call accepts.
   - Its principals match the principal the caller names. A principal is required. `--any-principal`
     skips this check, and then even a line whose principals are all negated, such as `!*`,
     authorizes its key.
   - Its `namespaces`, if present, match the namespace.
   - The verify time falls between its `valid-after` and `valid-before`.

   Principal and namespace patterns use OpenSSH's own matching rules, and the tests compare them
   with `ssh-keygen`.

5. A `fido-sk` signature has UP set.
6. The signature has UV set, if the caller passes `--require-uv` or any matching line carries
   `verify-required`. Only a `fido-sk` signature can.

A success reports the principal, the signer class, the key type, the key's SHA256 fingerprint and,
for a security key, the authenticator flags and the signature counter.

sigverify trusts the allowed-signers file completely. Anyone can make a software key in the
security-key format, and its signatures would carry whatever flags its maker chose. sigverify does
not check the enrollment attestation that would tell the two apart. So whoever can write the file
can sign anything, and whoever enrolls a key decides what it counts as. Requirements 1 and 3 of the
caller's contract are about this. sigverify also does not track the signature counter, so it does
not detect a replayed signature or a cloned key.

## What is refused

Each refusal has a stable reason that callers can match on.

| Reason                  | Meaning                                                               |
| ----------------------- | --------------------------------------------------------------------- |
| `malformed`             | The signature, or the tag object holding it, cannot be read.          |
| `unsigned`              | The git tag has no signature, or is a lightweight tag.                |
| `wrong-namespace`       | The signature was made for another namespace.                         |
| `unsupported-key`       | The key type is not one listed above, such as a certificate.          |
| `bad-signature`         | The signature does not verify over this message.                      |
| `unknown-key`           | No allowed-signers line lists the key.                                |
| `class-not-allowed`     | The key is listed, but only in a class the call does not accept.      |
| `principal-not-allowed` | The key is listed, but not for the requested principal.               |
| `namespace-not-allowed` | The key's line restricts it to other namespaces.                      |
| `not-yet-valid`         | The verify time is before the key's `valid-after`.                    |
| `expired`               | The verify time is after the key's `valid-before`.                    |
| `no-user-presence`      | A genuine security-key signature made without a touch.                |
| `no-user-verification`  | UV is required and the signature does not prove it.                   |
| `tag-name-mismatch`     | The tag object was signed under another name than the one it has now. |
| `target-not-commit`     | The signed tag points at a tag, tree or blob, not a commit.           |
| `target-missing`        | The commit the signed tag points at is not in the repository.         |

A `no-user-presence` refusal means the key really did sign, without a touch. Something running as
the key's owner is using the key, and the refusal names the key's fingerprint on stderr for that
reason.

## Encodings

sigverify accepts one encoding for each signature, and refuses the rest:

- Armored base64 must be canonical: no stray padding bits, and nothing but line breaks inside the
  armor.
- Nothing but whitespace may follow the armor footer.
- Integers must be in their minimal encoding, without redundant leading zero bytes.
- An RSA signature must be exactly as long as the key's modulus.

One field is accepted in any form. The SSHSIG envelope has a reserved field, which `ssh-keygen`
reads and ignores, and so does sigverify. The bytes a key signs always hold an empty reserved field,
so whatever the envelope carries there cannot change what was signed.

These rules leave signature bytes malleable in ways no verifier can prevent. An ECDSA signature has
two valid forms, and the reserved field can hold anything. None of this changes what was signed.

## Differences from ssh-keygen

sigverify is stricter than `ssh-keygen -Y verify` in these ways:

- It enforces UP, and UV when required.
- It requires a principal unless told otherwise.
- It refuses an allowed-signers file with any line it cannot parse, including unknown options.
  ssh-keygen skips such a line, so a typo could silently drop a signer's restrictions.
- It refuses an allowed-signers file with a `no-touch-required` option on any line.
- It refuses an allowed-signers file that holds a NUL byte anywhere. ssh-keygen reads each line as a
  C string, which ends at the first NUL, so it rejects a line like `*,!release<NUL>x K` as having no
  key. Read byte by byte, the same line would admit `release`.
- It adds two allowed-signers options, `verify-required` and `tkey-signer`. `verify-required` is the
  keyword sshd uses in `authorized_keys`. ssh-keygen does not know either one, and skips any line
  carrying them.
- It does not verify certificates. A `cert-authority` line never matches, so a certificate signature
  is refused.
- It refuses RSA keys under 2048 bits, and the encodings described above.

The git-tag command also differs from `git verify-tag`:

- It judges the validity window at the current time, or at `--verify-time`. git uses the tagger
  date, which the signer chooses, so a key past its `valid-before` could sign a backdated tag.
- It refuses a tag whose signed name differs from the ref it was found under. Without this check, a
  signed tag for an old release could be published again under a new release's name.
- It requires the tag to point at a commit, and that commit to be in the repository.
- It looks the tag up only as `refs/tags/NAME`. A name git would resolve elsewhere, such as a
  remote-tracking ref, is not a tag.
- It runs git with replacement refs disabled. A replacement ref makes git read one object in place
  of another, so it could otherwise substitute a different tag object or commit.
- It runs git in an environment of its own, inheriting nothing from the caller. Variables such as
  `GIT_DIR`, `GIT_OBJECT_DIRECTORY` or `GIT_CONFIG_PARAMETERS` could otherwise send git to other
  objects. User and system git configuration are not read.
- The Nix package compiles the absolute path of its git into the binary, so no `PATH` decides which
  git runs.

git refuses a repository owned by a user other than the one running it, and sigverify does not
override that.

## Using the command

```
sigverify verify --namespace NS --signature FILE --allowed-signers FILE \
    (--principal ID | --any-principal) [--allow-class CLASS]... [--allow-non-sk] \
    [--require-uv] [--verify-time TIME] < message

sigverify git-tag --repo DIR --tag NAME --allowed-signers FILE \
    (--principal ID | --any-principal) [--allow-class CLASS]... [--allow-non-sk] \
    [--require-uv] [--verify-time TIME]
```

The exit status is 0 for verified, 1 for refused, and 2 for an error. An error means nothing was
judged: bad usage, an unreadable or invalid allowed-signers file, a missing tag, a git failure, or a
report that could not be written to stdout.

The output depends on the exit status:

- **0:** stdout is `verified`, followed by lines of the form `name value`. The whole report goes out
  in one write. If that write fails, the exit status is 2, never 0.
- **1:** stdout is exactly one line, `refused <reason>`. stderr repeats the reason and explains the
  refusal in a sentence, as `sigverify: refused (<reason>): <sentence>`. Nothing about a refused
  tag's target appears anywhere, because none of it is authenticated.
- **2:** stderr says what went wrong. sigverify writes nothing to stdout on an error, with one
  exception: a report write that fails partway leaves the part already written on stdout.

So the rule for callers is plain: **discard stdout whenever the exit status is not 0.** Everything
worth logging about a refusal or an error is on stderr.

A verified release tag looks like this:

```
$ sigverify git-tag --repo /srv/git/cosmo.git --tag v2026.10.04 \
    --allowed-signers /etc/cosmo/release-signers --principal release@cosmo --require-uv
verified
namespace git
principal release@cosmo
class fido-sk
key-type sk-ssh-ed25519@openssh.com
fingerprint SHA256:...
flags 0x05
user-presence yes
user-verification yes
counter 42
tag v2026.10.04
commit 5d1c...
```

The report's lines, and their order, are fixed:

1. `verified`
2. `namespace`, `principal`, `class`, `key-type` and `fingerprint`
3. for a `fido-sk` signer: `flags`, `user-presence`, `user-verification` and `counter`
4. for a `tkey-signer` line that records a version: `signer-app`
5. for git-tag only: `tag`, then `commit`, which is always the last line

Each name appears at most once. A git-tag report always holds exactly one `tag` line and one
`commit` line. Both come from the signed tag object, read after its signature verified.

The flake exposes the command as `packages.<system>.sigverify`, for `x86_64-linux` and
`aarch64-linux`. Its checks are defined for both systems.

## The caller's contract

sigverify answers one question: does this signature verify against this file, under this policy?
These requirements are for the host-side caller that turns that answer into an activated release,
such as cosmo's converge. Each one closes a hole that sigverify cannot close by itself. The caller
must meet all of them before a host activates anything on sigverify's word.

1. **Keep every trust root out of the operator account's reach.** The operator account must not be
   able to write any of these:
   - the allowed-signers file
   - every parent directory of that file, and the target of any symlink on its path
   - the sigverify binary
   - the invocation policy: the flags the caller passes
   - the activation service
   - the rollback state of requirement 8

   Each must be owned by root and not writable by the operator, or be in the Nix store of the
   running system. Never take any of them from the candidate release being verified. Whoever can
   write the allowed-signers file can list a software key in security-key format and sign any
   release with UP and UV set.

2. **Protect the repository and the build until activation.** The operator account must not be able
   to write any of these, from fetch through activation:
   - the repository's object storage, including any alternates it borrows objects from
   - the repository's local configuration and hooks
   - the checkout of the verified commit
   - every input of the build

   sigverify reads the objects as they are when it runs. Whoever can change the object storage, the
   checkout or the build inputs afterwards can change what gets built under the verified commit id.
   Disabling replacement refs does not prevent that.

3. **Enroll every signer through a trusted ceremony.** Every allowed-signers line is an enrollment
   assertion, and sigverify cannot check it (see [Signer classes](#signer-classes)). So each line
   must record a key that was enrolled from genuine hardware:
   - a `fido-sk` line, from a security key in hand, with the public key taken from that key at
     enrollment
   - a `tkey-signer` line, from a TKey in hand running the intended touch-requiring signer app, with
     the public key taken from that TKey under that app

   File ownership does not establish any of this. A root-owned file can still list a key that never
   lived in hardware.

4. **Accept exactly the classes and options a release needs.** `--allow-class`, `--allow-non-sk` and
   `--any-principal` each make sigverify accept more:
   - `--allow-class` replaces the default class, and repeating it adds more classes.
   - `--allow-non-sk` adds software keys, which prove nothing about presence.
   - `--any-principal` ignores the principals field.

   A release caller passes exactly what it intends. For a release, that is:
   - `--principal` with the release principal
   - the default class, `fido-sk`, so no `--allow-class` other than `--allow-class fido-sk`
   - `--require-uv`, or a `verify-required` mark on every release signer in the protected file
   - never `--allow-non-sk` or `--any-principal`

5. **Use a clock the operator account cannot set.** sigverify judges `valid-after` and
   `valid-before` at the host's current time, or at `--verify-time`. Whoever sets that time can
   bring an expired signer back, or bring a future one forward. So the host's clock must be one the
   operator account cannot change, or the caller passes `--verify-time` from a source it trusts.
   Never pass the tagger date: the signer chooses it.

6. **Require the complete success report.**
   - Act only on exit status 0. Discard stdout whenever the exit status is not 0.
   - Require the whole git-tag report: the first line is `verified`, there is exactly one `tag` line
     and exactly one `commit` line, and `commit` is the last line (see
     [Using the command](#using-the-command)).
   - Require the `tag` line to name the tag the caller asked for, and the `commit` line to hold a
     full hexadecimal commit id.
   - Treat any other output as a failure.

7. **Apply exactly what was verified.**
   - Use only the commit id on the `commit` line. Never resolve the tag name again, because a ref
     can move after verification.
   - Keep replacement refs disabled through fetch, checkout and build, with
     `GIT_NO_REPLACE_OBJECTS=1` or `git --no-replace-objects`. A replacement ref makes git produce a
     different commit's contents under the verified commit id.
   - Run git with a sanitized environment throughout: no inherited `GIT_*` variables, and no user or
     system configuration.

8. **Refuse rollback.**
   - **Parse release names one way.** Derive the release stream and sequence number from the
     verified tag name, and from nothing else. Use one strict grammar, with exactly one spelling for
     each stream and number: for example, a decimal number with no sign, no spaces and no leading
     zeros. Refuse a tag name that does not parse. Two different tag names must never give the same
     stream and number.
   - **Start from a trusted floor.** A host that has never accepted a release takes its starting
     floor from protected provisioning, such as the Nix store of its running system. It never takes
     it from the first release it sees.
   - **Compare under a lock.** Under an exclusive activation lock, compare the candidate with the
     last accepted release in durable, protected state. Reject a lower sequence. Reject an equal
     sequence that names a different commit. Accept an equal sequence with the same commit only as
     an explicit retry of that release.
   - **Record before activating.** Write the accepted sequence and commit to that state crash-safely
     before activating. Hold the lock until activation finishes, so an older activation cannot
     complete after a newer one.
   - **Fail closed on missing or corrupt state.** If the rollback state is missing, unreadable or
     inconsistent, activate nothing. Never rebuild it from the candidate release or from the
     repository. Recovery is a documented procedure in which the operator, present at the host,
     restores the floor from a trusted record.
   - **Roll back forward.** An authorized rollback is a newly signed release with a higher sequence
     that points at the older commit.

9. **Never identify a release by its signature bytes.** Do not use the raw signature, or the bytes
   of the tag object, to identify a release or to detect a replay. The same signed content can be
   encoded more than one way (see [Encodings](#encodings)). Identify a release by its verified tag
   name and commit id.

## Tests

`nix flake check` runs two checks. `sigverify-unit` runs the Go tests. `sigverify-e2e` runs the
packaged command over a real signed tag, with an empty environment and no git on `PATH`. Both are
defined for `x86_64-linux` and `aarch64-linux`. `nix flake check` builds the checks for the
machine's own system.
`nix build .#checks.aarch64-linux.sigverify-unit .#checks.aarch64-linux.sigverify-e2e` runs the ARM
ones on an aarch64 builder, or on an x86 machine with aarch64 emulation.

The tests need no hardware. Security-key signatures come from two sources:

- `internal/skforge` derives keys from seed strings and makes signatures with chosen flags. It
  rebuilds the signed bytes independently of the verifier, and it can take a signature apart and
  alter one field.
- OpenSSH's `sk-dummy` is the software authenticator from OpenSSH's own regression suite. The checks
  build it from the same source as the `ssh-keygen` they run. With it, real `ssh-keygen` makes
  touchless signatures for ed25519-sk and ecdsa-sk keys: from keys generated with
  `no-touch-required`, and from stubs whose flags are cleared after the keys were made. A cleared
  verify-required flag gives a signature without UV.

Some tests record that `ssh-keygen -Y verify` or `git verify-tag` accepts a signature sigverify
refuses. Those expectations are in subtests named `interop: ...`. If a future OpenSSH or git starts
refusing silent signatures, only those subtests fail. That failure is a change upstream, not a
sigverify regression.

The tests compare sigverify's verdicts with `ssh-keygen -Y verify` across namespaces and validity
windows, and with `ssh-keygen -Y match-principals` across principal patterns and a line holding a
NUL byte. Outside nix, `go test ./...` skips the `sk-dummy` cases unless `SIGVERIFY_SK_PROVIDER`
names the provider.

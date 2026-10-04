# sigverify

sigverify verifies OpenSSH signatures and refuses any FIDO security-key signature made without a
touch. It reads standard SSHSIG, the format `ssh-keygen -Y sign` writes and git uses for SSH-signed
tags. It is a Go library (`the-valley/sigverify`) and a command (`sigverify`).

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
key then signs without a touch, and the signature records UP as clear.

Standard verification never looks at the bit:

- `ssh-keygen -Y verify` logs the flags at debug level and accepts the signature.
- `git verify-tag` with `gpg.format=ssh` calls `ssh-keygen -Y verify`, so it accepts it too.
- An allowed-signers file cannot ask for a touch. Its only options are `cert-authority`,
  `namespaces`, `valid-after` and `valid-before`.

So the presence check has to be in the verifier. sigverify does it on every security-key signature,
and nothing turns it off.

## What is verified

A signature is accepted only if every one of these holds, checked in this order:

1. It is a well-formed SSHSIG, armored or raw, with hash `sha256` or `sha512`.
2. Its namespace is the one the caller names. The git-tag command always uses `git`.
3. Its key is a security key (`sk-ssh-ed25519@openssh.com` or `sk-ecdsa-sha2-nistp256@openssh.com`).
   Software keys (`ssh-ed25519`, ECDSA, and RSA of at least 2048 bits) are accepted only when the
   caller allows them.
4. The signature verifies over the message.
5. A line of the allowed-signers file lists the key. That line's principals must match the requested
   principal, if one is given. Its `namespaces`, if present, must match the namespace. The verify
   time must fall between its `valid-after` and `valid-before`. These rules match
   `ssh-keygen -Y verify`.
6. A security-key signature has UP set.
7. The signature has UV set, if the caller requires it or the key's allowed-signers line carries
   `verify-required`.

A success reports the principal, the key type, the key's SHA256 fingerprint, the authenticator flags
and the signature counter.

sigverify trusts the allowed-signers file to list security keys that really live in hardware. Anyone
can make a software key in the security-key format, and its signatures would carry whatever flags
its maker chose. sigverify does not check the enrollment attestation that would tell the two apart,
so the file must list only keys the operator enrolled. It also does not track the signature counter,
so it does not detect a replayed signature or a cloned key.

## What is refused

Each refusal has a stable reason that callers can match on.

| Reason                  | Meaning                                                               |
| ----------------------- | --------------------------------------------------------------------- |
| `malformed`             | The signature, or the tag object holding it, cannot be read.          |
| `unsigned`              | The git tag has no signature, or is a lightweight tag.                |
| `wrong-namespace`       | The signature was made for another namespace.                         |
| `unsupported-key`       | The key type is not one listed above, such as a certificate.          |
| `not-security-key`      | A software key signed, and software keys are not allowed.             |
| `bad-signature`         | The signature does not verify over this message.                      |
| `unknown-key`           | No allowed-signers line lists the key.                                |
| `principal-not-allowed` | The key is listed, but not for the requested principal.               |
| `namespace-not-allowed` | The key's line restricts it to other namespaces.                      |
| `not-yet-valid`         | The verify time is before the key's `valid-after`.                    |
| `expired`               | The verify time is after the key's `valid-before`.                    |
| `no-user-presence`      | A genuine security-key signature made without a touch.                |
| `no-user-verification`  | UV is required and the signature does not prove it.                   |
| `tag-name-mismatch`     | The tag object was signed under another name than the one it has now. |

A `no-user-presence` refusal means the key really did sign, without a touch. Something running as
the key's owner is using the key, and the refusal names the key's fingerprint for that reason.

## Differences from ssh-keygen

sigverify is stricter than `ssh-keygen -Y verify` in these ways:

- It enforces UP, and UV when required.
- It refuses an allowed-signers file with any line it cannot parse, including unknown options.
  ssh-keygen skips such a line, so a typo could silently drop a signer's restrictions.
- It refuses an allowed-signers file with a `no-touch-required` option on any line.
- It adds one allowed-signers option, `verify-required`. This is the keyword sshd uses in
  `authorized_keys`. ssh-keygen does not know it and skips any line carrying it.
- It does not verify certificates. A `cert-authority` line never matches, so a certificate signature
  is refused.
- It refuses RSA keys under 2048 bits.

The git-tag command also differs from `git verify-tag`:

- It judges the validity window at the current time, or at `--verify-time`. git uses the tagger
  date, which the signer chooses, so a key past its `valid-before` could sign a backdated tag.
- It refuses a tag whose signed name differs from the ref it was found under. Without this check, a
  signed tag for an old release could be published again under a new release's name.
- It looks the tag up only as `refs/tags/NAME`. A name git would resolve elsewhere, such as a
  remote-tracking ref, is not a tag.

## Using the command

```
sigverify verify --namespace NS --signature FILE --allowed-signers FILE \
    [--principal ID] [--require-uv] [--allow-non-sk] [--verify-time TIME] < message

sigverify git-tag --repo DIR --tag NAME --allowed-signers FILE \
    [--principal ID] [--require-uv] [--allow-non-sk] [--verify-time TIME]
```

The exit status is 0 for verified, 1 for refused, and 2 for an error. An error means nothing was
judged: bad usage, an unreadable or invalid allowed-signers file, a missing tag, or a git failure.

Stdout starts with `verified` or `refused <reason>`. Lines of the form `name value` follow, and
stderr explains a refusal in a sentence. A verified release tag looks like this:

```
$ sigverify git-tag --repo /srv/git/cosmo.git --tag v2026.10.04 --allowed-signers /etc/cosmo/release-signers
verified
namespace git
principal release@cosmo
key-type sk-ssh-ed25519@openssh.com
fingerprint SHA256:...
security-key yes
flags 0x01
user-presence yes
user-verification no
counter 42
tag v2026.10.04
object 5d1c...
object-type commit
```

A caller should check out the commit in the `object` line, not whatever the tag name resolves to
later. A ref can move between verifying and checking out, but the verified object id cannot change.

The flake exposes the command as `packages.<system>.sigverify`, with git on its PATH, for
`x86_64-linux` and `aarch64-linux`.

## Tests

`nix flake check` runs two checks. `sigverify-unit` runs the Go tests. `sigverify-e2e` runs the
packaged command over a real signed tag.

The tests need no hardware. Security-key signatures come from two sources:

- `internal/skforge` derives keys from seed strings and makes signatures with chosen flags. It
  rebuilds the signed bytes independently of the verifier.
- OpenSSH's `sk-dummy` is the software authenticator from OpenSSH's own regression suite. The checks
  build it from the same source as the `ssh-keygen` they run. With it, real `ssh-keygen` produces
  real touchless signatures, through a key generated with `no-touch-required` and through a stub
  whose flag is cleared after the key was made. `ssh-keygen -Y verify` and `git verify-tag` accept
  both kinds, and the tests require sigverify to refuse them.

Outside nix, `go test ./...` skips the `sk-dummy` cases unless `SIGVERIFY_SK_PROVIDER` names the
provider. The tests also compare sigverify's verdict with `ssh-keygen -Y verify` across principal
patterns, namespaces and validity windows.

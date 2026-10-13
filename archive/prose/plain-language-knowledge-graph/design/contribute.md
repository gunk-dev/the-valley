# Contributor protocol

A contributor publishes a signed change, supplies the required verification evidence, and explicitly
requests integration. The same protocol applies to humans and agents.

Publishing a topic branch makes the work available for review. Requesting integration asks the
integrator to evaluate it and, if policy is satisfied, update the protected branch.

## Publishing and requesting integration

Commit the work with native SSH commit signing, then push the topic branch to the project's
canonical `origin`:

```sh
git push --set-upstream origin <branch>
```

Use the valley review helper to inspect the published change:

```sh
nix run .#valley -- review <branch>
```

The helper fetches and reviews `origin/<branch>` against `origin/main`. At its prompt:

- `[s]kip` leaves the change available for later review.
- `[b]ase`, when offered, rebases it in a temporary worktree, pushes with a lease, and returns to
  review of the resulting diff.
- `[a]sk` runs the required checks, signs their results, and publishes an integration request.
  Choose it when the change is ready to be evaluated for landing.
- `[r]eject` deletes the remote branch after confirmation.

The review prompt's `[a]sk` action currently targets `main`. It reads the project policy from the
target's integrated tip and the floor from the owning valley's integrated repository. It runs checks
over a temporary checkout of the published branch, not over the reviewer's working files.

When checks are required, `VALLEY_ATTEST_KEY` must name the host's Ed25519 signing-key file.
`VALLEY_ATTEST_NAME` optionally selects the signing name. The integrator must recognize that signer.
This configuration is separate from native Git commit signing.

The evidence and request are sent in one `git push --atomic`. The branch is already on `origin`, so
it is not part of that second push. A check or signing failure publishes no request. An atomic-push
failure publishes neither the request nor new evidence on the remote.

The CLI does not write `main`. The integrator evaluates the request and records its outcome. GitHub,
when configured as a publication mirror, is not part of this review or integration path.

## Records in the protocol

| Record                  | Purpose                                                                                    |
| ----------------------- | ------------------------------------------------------------------------------------------ |
| Signed commit           | Attribute the authored repository change                                                   |
| Check statement         | Identify the checked tree, check, result, and supplied provenance                          |
| Signed note             | Attribute that statement to its signer and permit additional signatures over the same text |
| Attestation ref         | Store evidence by tree digest and signer                                                   |
| Integration request ref | Name the submitted head and target stream                                                  |

Attestations are stored at:

```text
refs/the-valley/attestations/<tree digest>/<signer key hash>
```

A request is a ref pointing to the change's head:

```text
refs/the-valley/integration-requests/<target>/<change>
```

The current review helper derives the change name from the branch name, replacing slashes with
dashes, and prints the resulting request ref. The integrator derives the delta from the head and its
merge base with the target.

A policy that requires no check still permits a request. The helper reports that case and sends the
request without check evidence or an attestation key. The integrator records explicitly that no
checks were required.

## Using the lower-level helper

`nix run .#attest -- run` runs checks and stores signed notes. Its `--push` option atomically
publishes the topic branch and evidence, but does not request integration. Native Git can publish a
request ref when the caller has assembled the required evidence. `valley review` provides the
current combined evidence-and-request workflow.

[Verification](./verification.md) defines the helper options, signed bytes, and storage.
[Integration](./integration.md) defines request evaluation and evidence transfer.

## Enforcement and planned additions

Protected refs are reserved for the integrator. Attestation refs are create-only through the receive
boundary. Other namespaces currently follow push access; the
[project-scoped permissions](../.the-valley/decisions/dcr-e544f20-access-is-verbs-on-projects.md)
need additional enforcement.

Submitting a request is separate from supplying human approval. The
[core decision](../.the-valley/decisions/dcr-9c75283-substrate-adoption-and-the-core.md) requires
signed physical approval by default, with policy exemptions for ordinary work. That mechanism must
be available before the requirement is activated. Clicking `[a]sk` does not itself produce that
future approval evidence.

External transparency publication and inclusion proofs are planned for
[Phase 6](./roadmap.md#phase-6--trust-backstop). Current local attestations do not include that
guarantee.

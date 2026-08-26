---
type: bug
id: bd-7b7d58f
status: open
title: Evidence that expired cannot be replaced at the head it was made over
created: 2026-08-26
source: teaching [a]sk to resubmit a stale request, 2026-08-26
---

# A re-attestation over the same tree cannot be published

An attestation ref is keyed by the tree it is about and the key that signed it, and the namespace is
create-only. So a signer gets one attestation per tree, and re-attesting a tree that signer has
already attested has nowhere to go.

That is a hole exactly where [integration.md](../../design/integration.md)'s rule 6 says to
re-attest. An effectful check transfers only while its observation is younger than the validity
window the policy declares for it. When the window closes the tree is still the right tree, and the
answer the design gives is a fresh observation over the same head. But a fresh observation is a
different statement: the effectful predicate records the instant it was observed, so the note
differs, so the ref keyed by that tree and that key would have to move. It cannot. The ref is a tree
object rather than a commit, so git refuses the update as needing force before any hook sees it, and
the pre-receive hook refuses it again as an update to a create-only ref.

The two rules are each right on their own. Evidence with a validity window is what makes an
effectful check mean anything ([[dcr-f41f718]],
[decisions/dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md)).
A record that can be rewritten or dropped is not a record, which is why the namespace admits
creations only ([[dcr-0de694f]],
[decisions/dcr-0de694f-phase2-attestation-shape.md](../decisions/dcr-0de694f-phase2-attestation-shape.md)).
What is missing is a place for the second observation of one tree to live.

Only effectful checks are affected. A pure check's statement is a function of its inputs and nothing
else, so re-running it over an unchanged tree produces the identical ref and the push is idempotent
by construction.

## Why it is not yet biting

No policy in the valley declares a validity window, and rule 4 re-demands a check whose window
nobody wrote down. So no evidence has expired yet, and the path that would wedge is a path nothing
takes. The first declared window makes it live.

The neighbouring case is already handled and is worth separating from this one. Where the target
moved through a required check's input closure, the cure is a new head rebased onto the moved
target, and a new head means a new tree, a new digest, and a ref nothing holds yet. `valley`'s [a]sk
replaces the request ref under a lease and publishes the new evidence beside the old, which works.
The wedge here is only the same-head case.

## Directions, not decisions

- **Key the ref by the statement as well as the tree and the signer.** A second observation is a
  second statement about the same tree, and giving it its own ref keeps every record standing and
  keeps the namespace create-only. It costs the integrator a choice: several admissible statements
  for one check and one signer, where today the ref layout makes that unrepresentable.
- **Say that re-attestation is a new subject.** An observation instant is arguably part of what is
  being attested to, not metadata about it, and a subject that includes it is a different subject
  with a different digest. This is the smallest change to the refs and the largest to what a subject
  digest means.
- **Let the window be re-opened rather than re-observed.** A statement that a prior observation
  still holds is itself a signed act, and it is about a different thing than the check was, so it is
  a different subject and does not collide. This is the direction that leaves both existing rules
  untouched.

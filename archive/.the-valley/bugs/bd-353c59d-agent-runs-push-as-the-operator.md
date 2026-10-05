---
type: bug
id: bd-353c59d
status: open
title: Agent runs push as the operator, so write protection does not constrain the local fleet
created: 2026-08-07
source: rejection test on the pilot host, 2026-08-07
---

# Agent runs push as the operator

An agent run dispatched on the host works in a git worktree beside the operator's own, and pushes
over SSH with the operator's key. The pre-receive hook reads the principal from the key that
authenticated the push, so every push from a locally dispatched run arrives as the operator. The
operator is a declared writer of the protected projects. The invariant therefore admits every write
the local fleet makes, and constrains nothing it does.

The actors this leaves unconstrained are the ones the approval-gate thinking most wants constrained.
The motivating failure behind [[ida-b7025b5]]
([ideas/ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))
is an approval gate administered by the agent it gates. Write protection is a gate of exactly that
kind: it decides whether a push to a protected ref lands. An agent run holding the operator's key
does not have to defeat it, because the gate cannot tell the run apart from the human whose key it
carries.

The evidence is a deliberate rejection test. A push from an agent worktree wrote a protected ref and
the hook admitted it. The hook was right to: it was asked whether the operator may write that ref,
and the operator may. The mechanism did what it says. What it says is narrower than the protection
the host appears to have.

The test-design lesson is separable and worth stating on its own: a rejection test must control the
identity it tests with, or it tests the wrong claim.

## Why it is acceptable today

There is one operator. Agent runs are dispatched only by that operator, on that operator's own
machine, and the protected write that surfaced this was the test's own. Nothing has yet acted under
the operator's principal that the operator did not start, so the grant the runs inherit is currently
no wider in effect than it is in name.

## The gate

The invariant needs identities behind it before an agent run acts with meaningful autonomy against a
protected project — a run left to work unattended, a run dispatched by another run, or any run whose
pushes are not watched as they happen. At that point a run must authenticate as its own principal,
so that what the hook decides about it is a decision about the run and not about the human who
started it.

## Directions, not decisions

These are existing threads, and this bug is what pulls them together.

- **Registry entries for the machines and runs that push.** [[dcr-b87f6e8]]
  ([decisions/dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
  already makes a principal a declared entry holding its own keys, its grants, and an expiry where
  the kind warrants one. A run that is its own principal is an entry of that shape, and expiry is
  what keeps the registry from accreting one per run forever.
- **Per-run delegation.** [[ida-a8243d2]]
  ([ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  gives a run a bundle of permissions delegated from whoever initiated it, narrowable at every hop.
  That is the shape the writer grant wants: a run pushes with authority derived from the operator's,
  attenuated to what the run was dispatched to do.
- **Not crediting the machine at all.** [[bd-8a591dc]]
  ([bd-8a591dc-machine-credentials-never-expire.md](./bd-8a591dc-machine-credentials-never-expire.md))
  observes that a machine the integrator fetches from needs to be reachable, not authorized to push.
  A run that never holds a pushing credential cannot inherit the operator's, and the question of
  which principal its push carries stops arising.

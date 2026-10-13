---
type: idea
id: ida-5006606
status: adopted
title: "Let clients discover a project's owning valley from the target tree"
created: 2026-08-22
source: review of the asker's policy defaults, 2026-08-22
---

# Let clients discover a project's owning valley from the target tree

A project carries a `policy/valley` file naming its owning valley's repository. Clients follow it to
find the governing policy. Ownership itself is declared by the valley; the project's pointer is a
discovery aid.

The planned valley membership declaration arrives with support for multiple valleys. Until then,
host configuration supplies the ownership declaration used by the running integration service.

## Discovery

The pointer contains one repository address in a form accepted by `git fetch`. Policy tools fetch
that repository and read `policy/instance` from its integrated tip.

A valley repository carries its own `policy/instance` and can use that directly. It needs no pointer
to itself. Principals governed by a valley need fetch access to its policy repository.

Once the authoritative membership declaration exists, a client must check that it agrees with the
pointer. A mismatch must produce an error.

## Match the integrator's inputs

A client must derive checks from the same sources as the integrator:

- Project policy from the target branch's tip.
- The ownership pointer from that same tip.
- The floor from the owning valley's integrated tip.

Working-tree files and the proposed branch do not supply these inputs. Review should therefore give
the same answer from any checkout, including a stale one.

A missing project layer adds nothing to the floor. Missing or unreadable governing inputs must be
reported under the current resolver's rules.
[[dcr-9c75283]](../decisions/dcr-9c75283-substrate-adoption-and-the-core.md) defines the separate
core-based bootstrap design and its activation prerequisite.

Tools should default to these real governing sources. Alternate sources remain useful for explicit
inspection or testing, but must be selected deliberately.

## Authority and later discovery

A proposed edit to `policy/valley` is evaluated under the policy that already governs the target. It
cannot use its own replacement pointer to choose the rules for admitting itself. The running
integrator also has its configured ownership source.

Once repository addressing reliably identifies the valley, discovery may be possible from the clone
URL. The explicit pointer could then be retired. Until that mechanism exists, the pointer lets
`valley checks` and review's `[a]sk` find the floor.

## Open questions

Each derivation currently fetches the valley repository again. Frequent automated use needs an
appropriate caching and freshness policy.

The pointer identifies a repository but assumes `refs/heads/main`. It cannot yet select another
integrated ref.

[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md) defines policy
composition, and [[dcr-9b5da04]](../decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md) defines
project ownership and hosting.

---
type: idea
id: ida-1ec03b1
status: exploring
title: Verification policy is path-scoped — required attestations are a function of the diff
created: 2026-07-06
source: conversation, 2026-07-06
---

# Verification policy is path-scoped

The integrator derives a change's required attestation set from the **actual tree diff**, never from
the contributor's claim about the change. Policy maps path classes to required checks:

- `.the-valley/**` only → signature + knowledge lint: frontmatter vetted against a CUE `#Node`
  schema (types, status enums, id derived from slug, filename coherence), reference integrity
  (`[[wiki-links]]`, `blocked_by` ids, and relative links all resolve), and prose format (below).
- Code → the full check suite ([verification.md](../../design/verification.md)).
- Mixed → the max of everything touched, automatically.

Because routing is computed from the tree, there is no "node door" to smuggle code through. Mixed
commits are a feature, not a hole — "a landed change closes the outcome it serves" wants code and
node flip in one commit, and that commit simply carries code-level requirements. This is also the
argument for knowledge staying in-tree rather than on its own ref: one protection mechanism,
proportionate checks, no split in the one-clone durability story.

The policy itself is data — path classes and required checks — so it is a CUE document, versioned
in-repo and validated like everything else. One schema language then covers config, events, and
knowledge.

The knowledge lint has three lifetimes: a `nix flake check` derivation today (no integrator needed),
the required attestation for `.the-valley/**` diffs at the integrator rung
([architecture.md](../../design/architecture.md)), and a pure, witness-re-derivable check under the
trust backstop. Same derivation throughout.

Prose format (decided 2026-07-17): markdown prose is filled paragraphs hard-wrapped at 100
characters by a deterministic formatter. The check is formatter idempotence — formatting the tree
changes nothing — which is deterministic, judgment-free, and off the shelf. Rationale: humans read
these files raw, and filled paragraphs are the readable shape in a terminal; semantic line breaks
(one sentence per line) diff better but read badly raw, and no deterministic formatter can enforce
them. Hard wrap's cost — a one-word edit can re-flow a paragraph in the diff — is mitigated at
review time by delta's word-level highlighting in `valley review`. The corpus reformat is one
mechanical sweep in a single commit, so blame churn is contained.

## Related

- Extends the one-structural-invariant idea (only the integrator writes protected refs) one level
  down, path-wise within a ref.
- The scheduling context this serves: [[ida-3145b7a]] — cheap node integration keeps the graph a
  live coordination surface, not ceremony.

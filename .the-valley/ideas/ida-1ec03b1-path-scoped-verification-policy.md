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
  schema (types, status enums, id derived from slug, filename coherence) and reference integrity
  (`[[wiki-links]]`, `blocked_by` ids, and relative links all resolve).
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

Addendum (2026-07-17, owner observation): the prose corpus has very long lines — whole paragraphs on one line, node files past 900 characters — the kind of thing lint, formatting, and a resubmit CI check would normally catch, and nothing here checks yet. This slots straight into the structure above: a prose-format rule is a missing member of the knowledge lint's check set, with the same three lifetimes (flake check today, attestation later). Candidate convention, undecided: semantic line breaks — one sentence per line — rather than a hard wrap. It renders identically, and it aligns diffs, conflicts, and blame to sentence boundaries, which is worth real money in `valley review` and `[b]` rebases. When the check lands, the corpus reformat is one mechanical sweep, kept to a single commit so blame churn is contained.

## Related

- Extends the one-structural-invariant idea (only the integrator writes protected refs) one level
  down, path-wise within a ref.
- The scheduling context this serves: [[ida-3145b7a]] — cheap node integration keeps the graph a
  live coordination surface, not ceremony.

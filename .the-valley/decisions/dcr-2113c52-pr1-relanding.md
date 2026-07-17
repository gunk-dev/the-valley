---
type: decision
id: dcr-2113c52
status: decided
title: PR #1 disposition — re-land thin at altitude, preserve full prose as idea nodes
created: 2026-07-02
source: PR #1
---

# PR #1 disposition

PR #1 ("Reframe the-valley as a recursive, transparent outcome-production engine") was written
against the pre-restructure docs and became unmergeable after the three-altitude restructure (PR #3)
pruned the doc set it modified. Disposition:

- Its ideas were **re-landed thin, at the right altitude**, in PR #5: the outcome-DAG bet and the
  federation paragraph in `design/architecture.md`, the self-transparency stub, and the matching
  open-question entries.
- The **full prose is preserved as idea nodes** (this PR): [[ida-eac723e]] (outcome DAG) and
  [[ida-8482624]] (federation/groups), with the substance the self-transparency stub had dropped
  appended to [self-transparency.md](../../design/self-transparency.md).
- `outcomes.md` and `federation.md` **return as design docs** when their rungs on the
  [scenario ladder](../../design/user-scenarios.md) — S3+ for the scheduler, S5+ for federation —
  are the top priority; the nodes are the source material for that re-fleshing.

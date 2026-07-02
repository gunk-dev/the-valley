# .the-valley — knowledge v0

The knowledge-graph convention from [user-scenarios.md § S1](../design/user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them): **a directory convention, not a system**. Issues, outcomes, ideas, and decisions live with the repo as plain files, cloned and backed up by the same motion that protects the code.

## The convention

One file per node. Each file is markdown with YAML frontmatter carrying the structured layer:

```yaml
---
type: idea            # outcome | bug | idea | decision
id: ida-eac723e       # <prefix>-<short hash>
status: adopted       # see enums below
title: One-line human title
created: 2026-07-02
source: PR #1         # optional — where the content came from
---
```

One typed edge exists now: **outcome nodes carry `blocked_by`** (a list of node ids) in frontmatter, because the live outcome-DAG experiment ([ideas/ida-3145b7a-demand-pressure.md](./ideas/ida-3145b7a-demand-pressure.md)) requires it. The other typed edges (`closes`, `supersedes`, …) come later; until then, prose links in the body are enough.

## Types, directories, prefixes

| Type | Directory | Prefix | Status enum |
| --- | --- | --- | --- |
| outcome | `outcomes/` | `oc-*` | `open` \| `in-progress` \| `done` \| `abandoned` |
| bug | `bugs/` | `bd-*` | `open` \| `closed` |
| idea | `ideas/` | `ida-*` | `raw` \| `exploring` \| `adopted` \| `superseded` |
| decision | `decisions/` | `dcr-*` | `proposed` \| `decided` \| `superseded` |

IDs are short and hash-derived (e.g. first 7 hex chars of a hash of the slug) — coordination-free, at the cost of prettiness. Filenames are `<id>-<slug>.md`.

## The interface

- **Creating a node is a commit.** Closing one is a commit that flips `status`.
- **Listing is `ls`. Search is `grep`. History is `git log`.**
- No indexer, no events, no validation. **The schemas above are documentation, not enforcement** — nothing checks them until there is an integrator to enforce them.

# Project knowledge

A unified, git-native, markdown-based knowledge graph for everything in a project that isn't executable code and isn't user-facing documentation: bugs, ideas, principles, decisions, retrospectives, outcomes, threads. Equally consumable by humans and AI agents.

Supersedes the original "issues" scope. Issue tracking is one slice of this; treating it in isolation misses the larger problem — a project's institutional knowledge has no good home today and is invisible to agents.

## Core model

Every artifact is a **typed node**, stored as a single markdown file:

- **Frontmatter (YAML)** carries the structured layer: type, id, status, typed edges, type-specific fields. This is the source of truth for the graph.
- **Body (markdown)** carries prose. `[[id]]` references in the body are *human ergonomics only* — hyperlinks for editors that render them, grep targets for humans. They are not parsed into edges.

Single source of truth for the graph (frontmatter); no parser magic; agents and tooling read structured data without ambiguity.

## Layout

```
.the-valley/
  bugs/         bd-*.md
  principles/   prn-*.md
  decisions/    dcr-*.md
  retros/       rtr-*.md
  ideas/        ida-*.md
  outcomes/     oc-*.md
  threads/      thr-*.md
  schemas/      scm-*.cue
```

Lives at the root of each repo. Gets cloned with the code. No special storage.

IDs are short, hash-derived, type-prefixed (`bd-a3f2`). Type is inferable from the prefix without parsing.

## Examples

**A bug:**

```markdown
---
type: bug
id: bd-a3f2
title: Race in integrator queue on same-target requests
status: open
severity: high
created_at: 2026-05-17T10:00:00Z
created_by: ssh:SHA256:abc...
blocked_by: [bd-9c1d]
blocks: []
related_to: [prn-002]
---

When two integration-requested events arrive concurrently for the same
target ref, the dequeue logic has a window where both can be picked up by
parallel handlers. See [[thr-2026-05-17-integrator-race]].
```

**A principle:**

```markdown
---
type: principle
id: prn-002
title: Local checks must be reproducible
status: active
applies_to: [verification, contribute]
enforced_by: [check:reproducibility-audit]
---

All canonical derivations must be bit-reproducible. The reproducibility-audit
check runs nightly and emits a divergence event on drift. Reproducibility is
the prerequisite for transparency log witnessing — without it, the witness's
"I got hash X" matches nobody.

See [[verification.md]] for detail.
```

## Typed edges (declared in frontmatter)

The vocabulary differs per node type; common edges:

| Edge | Meaning |
| --- | --- |
| `blocks` / `blocked_by` | Work-layer dependency |
| `related_to` | Untyped reference; "see also" |
| `supersedes` / `superseded_by` | Replaces an earlier node; preserves history |
| `applies_to` | Scope of a principle or decision |
| `enforced_by` | Link from a principle to a check or attestation type |
| `closes` | A commit/attestation that resolves a bug or outcome |
| `motivates` / `motivated_by` | A problem node (e.g. a `bug`) motivates an outcome; the outcome is `motivated_by` it |
| `references` | Inbound citation from anywhere |

## Outcomes and the generative reading of the graph

The `outcome` node (`oc-*`, the type formerly sketched as `task`) is the **central generative node type** — the unit of work production. An outcome is a thing someone wants to exist that does not yet; closing it is the system's reason to act. A `bug` is one *kind of problem* that motivates an outcome, not the unit of work itself; the two stay distinct (see the decision flip in [openquestions.md](./openquestions.md)).

The two link explicitly via the `motivates` / `motivated_by` edge: a `bug` `motivates` an `oc-*`, and the outcome is `motivated_by` the bug. They keep **separate lifecycles**, and `closes` always targets the outcome, never the bug directly. When an attestation lands carrying `closes: [oc-…]`, the integrator's success event flips *only* the outcome's status; a controller subscribed to that `node-status-changed` then resolves the motivating bug — auto-closing it when the closed outcome was its sole motivated-by, or leaving it open when other outcomes still reference it (one bug can motivate several fixes). This keeps the work unit (the outcome the DAG schedules on) and the problem record (the bug) from being conflated: a bug is not "done" because one of the outcomes it spawned landed, only when nothing it motivates remains open.

Two edges, read generatively rather than descriptively, turn this graph into a scheduler:

- `blocked_by` / `blocks` between outcomes **are** a dependency DAG — an outcome is `blocked_by` whatever must complete first.
- `closes` + the attestation-success → `node-status-changed` event (below) is how an outcome **finalizes** a node and removes a blocking edge from its parent.

Read descriptively, this is just "what blocks what." Read generatively, the open outcomes are a *production graph the system has pressure to complete*: each unfinished outcome is latent demand for an agent run, and a scheduler controller dispatches against the unblocked frontier. That reading — priority propagation, frontier dispatch, klaus-shaped scheduling — is worked out in [outcomes.md](./outcomes.md).

## Schemas (CUE, per type)

Each node type has a CUE schema in `.the-valley/schemas/`. Schemas declare required and optional fields, enum values for status, allowed edge types, etc.

Schemas are themselves versioned with the project. The integrator validates any change introducing or updating a node against its type's schema and rejects malformed ones — schema drift can't happen silently.

Agents producing nodes are handed the schema in their prompt context. Producing valid frontmatter becomes a structured task, not freeform writing.

## Navigation

### Humans

- **File browsing.** `ls .the-valley/bugs/` shows all bugs. `grep -r foo .the-valley/` for full-text. Standard tools, no install.
- **Editor rendering.** Any markdown editor renders bodies; many resolve `[[]]` references natively.
- **TUI.** A `valley browse <id>` command renders a node with its inbound/outbound edges, navigable by ID.
- **Web view.** A static site generated periodically from the graph; clickable, no live server required.

### Agents

- **Direct read.** `cat .the-valley/bugs/bd-a3f2.md` and parse frontmatter. Trivial for any LLM.
- **Index query.** A small indexer reads all nodes into a queryable store (SQLite at smallest scale; in-memory is fine). Exposes traversal queries: "open bugs blocked on prn-002, sorted by severity." The index is a *projection* — rebuildable from source, nothing depends on its correctness.
- **Bus subscription.** Node mutations emit events (`node-created`, `node-updated`, `node-status-changed`, `node-linked`). Agents subscribe for reactive behavior — flag drift, file follow-up bugs, dispatch work.

## Composition with the rest of the architecture

- **Identity.** Same SSH keys as commits. A node's `created_by` is a key fingerprint; updates are git commits, natively signed.
- **Attestation.** A code commit's attestation can include `closes: [bd-a3f2]`; the integrator's success event emits a `node-status-changed` for the bug.
- **Threads.** Discussion of any node is a thread (`thr-*`); threads are themselves nodes, can scope to any other node or set of nodes. Replaces what GitHub calls "comments."
- **Generated `AGENTS.md` / `CLAUDE.md`.** Rendered as a projection — render all `status: active` principles as a markdown file at the repo root for any agent walking in cold. Generated, not authored.
- **Integration policy.** Active principles with `enforced_by` can require the integrator to refuse changes that don't carry the named attestation. Knowledge becomes load-bearing on integration.

## Anti-rot mechanisms

Mostly for later, but worth naming so the design can grow into them:

- **Decay.** Last-touched timestamps + a controller that fires `node-stale` events past a threshold. Surfaces notes for review before they silently die.
- **Supersession is explicit.** Decisions and principles use `superseded_by` rather than silent rewrite; the old node remains as history.
- **Backlink hygiene.** Updating a node enqueues its backlinks as "needs-revisit" candidates. Agents or humans triage.
- **Read tracking.** Agent lookups and human view-opens are logged; surfaces what is actually being consulted. Unread knowledge is dead knowledge.
- **Agent-flagged drift.** Subscribers can emit drift signals: "new code in repo X violates principle prn-002." The graph's value is reinforced precisely because agents *use* it for their work, and that use produces feedback when stated knowledge and reality diverge.

## What this is not

- **Not a wiki.** Wikis are unstructured prose; this is a typed graph.
- **Not Obsidian/org-mode.** Those are personal knowledge tools that happen to use plain text. This is a project substrate that exposes a knowledge-tool-shaped surface. Multi-party, signed, composed with the rest of the architecture, agent-first.
- **Not Jira.** No workflow state machines, no swimlanes, no estimation. Minimum metadata, maximum composability.

## Out of scope for v1

- Cross-repo nodes.
- Rich attachments (images, binaries). Body-only markdown; attachments later via content-addressed blobs.
- Migration from existing trackers.

## Open questions

See [openquestions.md](./openquestions.md) — items raised here live under *Knowledge graph specifics*, *Cross-repo coordination*, and *Storage, retention, and evolution*.

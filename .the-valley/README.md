# .the-valley — knowledge v0

The knowledge-graph convention from
[user-scenarios.md § S1](../design/user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them):
**a directory convention, not a system**. Issues, outcomes, ideas, and decisions live with the repo
as plain files, cloned and backed up by the same motion that protects the code.

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

Two typed edges exist, each a list of node ids in frontmatter.

**Outcome nodes carry `blocked_by`**: the nodes an outcome waits on. The live outcome-DAG experiment
([ideas/ida-3145b7a-demand-pressure.md](./ideas/ida-3145b7a-demand-pressure.md)) requires it.

**Idea and decision nodes carry `supersedes`**: the nodes this one replaces. Ideas and decisions are
the only types that carry it, because they are the only two whose status enum has a `superseded`
value for the replaced node to land in. The lint requires that status to be set, so a supersession
cannot be half-declared. Declaring it is the machine-readable half of the resolution rule below; the
prose still has to be corrected too.

The remaining typed edges (`closes`, …) come later; until then, prose links in the body are enough.

## Types, directories, prefixes

| Type     | Directory    | Prefix  | Status enum                                       |
| -------- | ------------ | ------- | ------------------------------------------------- |
| outcome  | `outcomes/`  | `oc-*`  | `open` \| `in-progress` \| `done` \| `abandoned`  |
| bug      | `bugs/`      | `bd-*`  | `open` \| `closed`                                |
| idea     | `ideas/`     | `ida-*` | `raw` \| `exploring` \| `adopted` \| `superseded` |
| decision | `decisions/` | `dcr-*` | `proposed` \| `decided` \| `superseded`           |

IDs are short and hash-derived (e.g. first 7 hex chars of a hash of the slug) — coordination-free,
at the cost of prettiness. Filenames are `<id>-<slug>.md`.

The same table in checkable form is [`schema/node.cue`](../schema/node.cue), which the knowledge
lint vets every node's frontmatter against.

## Writing a node

**The graph at any commit presents the design as it stands now.** It carries no history of how the
thinking got there. That history is in git history, and only there.

- **Restate, don't accrete.** When thinking evolves, rewrite the node as the cleanest statement of
  current understanding. No Addendum, Update, Revision or Correction sections, ever, and no
  superseded framing left standing beside its replacement.
- **A node never defines the design by contrast with an earlier or rejected version of itself.** No
  "not one", no "rather than", no "as described above, but actually". A decision node records what
  was decided, not the options weighed against it.
- **Rationale stays; deliberation goes.** This is the distinction that makes the rule above usable.
  A reason stated as a property of the current design belongs in the node; a record of a fork being
  weighed and settled does not. When unsure, state the reason positively as a fact about the design
  rather than as a comparison against something not chosen.
- **A node must not assert something and then correct itself later in the same document.** State it
  correctly where a reader meets it first. A reader who stops halfway must not be left with the
  superseded version.
- **Replacing something includes deleting what it replaces, in the same change.** This covers the
  files a node points at as much as its prose — a superseded example left beside its replacement is
  the same defect as a superseded paragraph.
- **Where two documents make incompatible claims, the more recently written claim is the current
  one.** `git blame` the lines carrying each claim rather than the files: a formatting sweep or an
  edit elsewhere touches a file without changing what it asserts. Correcting the stale claim belongs
  to the change that found the contradiction, not to a later one — that is the rule above, applied
  across two documents instead of within one. The rule's limit, stated plainly: recency establishes
  which claim was written later, never which one is right. Where the newer claim is the mistake, the
  resolution is still a change that leaves the corpus saying one thing, and never an annotation
  explaining the discrepancy.

Two rules that govern the design documents govern nodes identically, and are stated once, where they
already live:

- **Disembodied voice** — [AGENTS.md](../AGENTS.md), writing standard. A node speaks about
  the-valley itself: no names, no second person, no reference to project participants. Roles are
  fine, as are external prior art and its authors. Frontmatter `source:` follows the same rule; it
  names a date and a venue, never a person.
- **Prose format** — [[ida-1ec03b1]]
  ([ideas/ida-1ec03b1-path-scoped-verification-policy.md](./ideas/ida-1ec03b1-path-scoped-verification-policy.md)).
  Markdown prose is filled paragraphs hard-wrapped at 100 characters. `nix run .#fmt` rewraps; the
  flake's `prose-format` check enforces it.

## The interface

- **Creating a node is a commit.** Closing one is a commit that flips `status`.
- **Listing is `ls`. Search is `grep`. History is `git log`.**
- No indexer and no events.
- **The convention is checked, not enforced.** `nix flake check` runs `knowledge-lint`: every node's
  frontmatter vetted against [`schema/node.cue`](../schema/node.cue), every filename agreeing with
  the frontmatter it carries, and every `[[wiki-link]]`, `blocked_by` id, `supersedes` id and
  relative link resolving. A `supersedes` id must further name a node whose own status is
  `superseded`. It reports; nothing stops a broken graph from landing until there is an integrator
  to stop it.

The lint is not this repo's alone. The flake exposes it as `lib.knowledgeLint`, so any project that
keeps a graph gets the same check by taking the-valley as a flake input and instantiating it over
its own tree — see the comment on that output in [flake.nix](../flake.nix) for the whole of a
consuming flake, and [[ida-a9e274c]]
([ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md](./ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md))
for why the derivation travels rather than the convention.

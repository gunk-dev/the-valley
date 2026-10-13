# .the-valley — knowledge graph v0

The knowledge graph implements the file-based project memory introduced in
[Scenario S1](../design/user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them).

It is a lightweight directory convention rather than a complex database service. Issues, outcomes,
ideas, and architectural decisions live directly within the git repository as plain markdown files.
They are versioned, cloned, and backed up alongside source code.

## Frontmatter schema & structure

Each knowledge graph node is a markdown file with structured YAML frontmatter:

```yaml
---
type: idea            # outcome | bug | idea | decision
id: ida-eac723e       # <prefix>-<7-char-hash>
status: adopted       # enum value based on node type
title: One-line summary of the node
created: 2026-07-02
source: PR #1         # optional provenance or venue
---
```

### Typed graph relationships

1. **`blocked_by` (Outcome nodes):** A list of node IDs that must be resolved before this outcome
   can proceed. A dependency is considered satisfied when the target node reaches a terminal status:
   - Ideas: `graduated`, `superseded`, or `discarded`.
   - Decisions: `decided` or `superseded`.
   - Bugs: `closed`.
   - Outcomes: `done` or `abandoned`
     ([dcr-593d3d1](./decisions/dcr-593d3d1-ideas-are-transitional.md)).
2. **`supersedes` (Idea and Decision nodes):** Lists node IDs that this node replaces. The
   referenced nodes must have their status set to `superseded`.
3. **`graduated_into` (Graduated Idea nodes):** Specifies the decision node ID or relative path of
   the design document that formalizes the idea
   ([dcr-593d3d1](./decisions/dcr-593d3d1-ideas-are-transitional.md)). This field is mandatory
   whenever an idea's status is set to `graduated`.

## Node types and naming

| Type     | Directory    | Prefix  | Valid Status Enums                                                              |
| -------- | ------------ | ------- | ------------------------------------------------------------------------------- |
| outcome  | `outcomes/`  | `oc-*`  | `open` \| `in-progress` \| `done` \| `abandoned`                                |
| bug      | `bugs/`      | `bd-*`  | `open` \| `closed`                                                              |
| idea     | `ideas/`     | `ida-*` | `raw` \| `exploring` \| `adopted` \| `graduated` \| `superseded` \| `discarded` |
| decision | `decisions/` | `dcr-*` | `proposed` \| `decided` \| `superseded`                                         |

Node IDs consist of the type prefix followed by the first 7 hexadecimal characters of the SHA-256
hash of the slug. Files are named `<id>-<slug>.md`.

To generate a node ID for a given slug:

```bash
printf '%s' '<slug>' | sha256sum | cut -c1-7
```

All node frontmatter is validated against [`schema/node.cue`](../schema/node.cue) by the repository
lint.

## Writing standards for nodes

The knowledge graph represents the design of the valley as it stands today. Historical evolution
lives in git history, not in accreted document updates.

- **State current truth directly:** When understanding evolves, rewrite the node to reflect the
  current design. Do not append "Update", "Revision", or "Addendum" sections.
- **Positive assertions:** Describe what the system is and how it functions rather than contrasting
  it with rejected or obsolete proposals.
- **Immediate accuracy:** State requirements and facts accurately at the beginning of the document.
  Do not present outdated assumptions only to correct them later in the text.
- **Clean replacement:** When a decision or idea supersedes an older concept, delete obsolete files
  and update all cross-references in the same change.
- **Disembodied voice:** Nodes describe the system and its components directly without personal
  names or second-person framing ([AGENTS.md](../AGENTS.md)).
- **Formatting:** Paragraphs are hard-wrapped at 100 characters. Run `nix run .#fmt` to format files
  automatically.

## Operations & validation

- **Create a node:** Commit a new markdown file in the appropriate directory.
- **Update status:** Edit the frontmatter `status` field and commit.
- **Search & navigation:** Use standard file-system and git commands (`ls`, `grep`, `git log`).
- **Validation:** `nix flake check` runs `knowledge-lint`, verifying:
  - YAML frontmatter matches [`schema/node.cue`](../schema/node.cue).
  - Filenames match frontmatter IDs and slugs.
  - All wiki-links (`[[id]]`), markdown links, `blocked_by`, `supersedes`, and `graduated_into`
    targets resolve correctly.
  - Any node listed in `supersedes` has its own status set to `superseded`.

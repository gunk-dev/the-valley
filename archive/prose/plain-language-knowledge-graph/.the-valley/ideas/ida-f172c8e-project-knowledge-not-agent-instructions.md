---
type: idea
id: ida-f172c8e
status: exploring
title: "Give humans and agents access to the same project knowledge"
created: 2026-07-17
source: design conversation, 2026-07-17
---

# Give humans and agents access to the same project knowledge

Project knowledge should have one durable home that both humans and agent harnesses can discover and
navigate. Writing conventions, architecture, requirements, and design rationale apply to all
contributors.

The current repository uses [AGENTS.md](../../AGENTS.md) for writing and document conventions, with
[CLAUDE.md](../../CLAUDE.md) as a harness bridge. The proposal is to make that hookup automatic
while keeping the underlying knowledge independent of any particular harness.

The-valley could generate any required harness files from project-owned sources. A harness-specific
file would then be an adapter, rather than a second place where project rules evolve.

## Session memory

Private session memory should not become the only record of a project decision. Durable project
understanding belongs in the graph or design documents.

Session memory can retain pointers and temporary working context. Project-specific portions should
be reconstructible from the durable record where possible. Information that does not belong to a
repository may still need another appropriate home.

## Open questions

Where should the current AGENTS.md content live? [contribute.md](../../design/contribute.md) is one
candidate. How should the engine supply relevant knowledge to different harnesses without
duplicating it?

Until that mechanism exists, the current instruction files remain in use.
[[ida-4557af7]](ida-4557af7-spec-driven-iteration.md) provides the related idea of deriving
implementation artifacts from a durable specification, and
[[ida-3e87f5c]](ida-3e87f5c-self-describing-projects.md) applies repository-carried description to
project identity.

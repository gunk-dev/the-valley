# Issues

**Status: placeholder.** Initial framing and open questions; not yet designed in detail.

This document will define a minimal issue-tracking system — bugs, feature ideas, tasks, agent prompts — that is equally consumable by humans and by AI agents. The driving constraints are: structured data (machine-queryable), git-native (no separate platform), and small (no Jira-shaped surface area).

## In scope

- **Data model.** What an "issue" is: fields, statuses, links, attribution.
- **Storage.** Where issues live and how they sync.
- **Event model.** How issue activity flows into the bus and into threads (per [feedback](./feedback.md)).
- **Human interface.** Probably a small TUI plus a static web view generated from the structured data. Not a full PM tool.
- **Agent interface.** Structured query and structured write. Agents must be able to ask "what's blocked on me?" and "what depends on this commit?" without scraping HTML.

## Initial framing

Borrow shape from beads (Steve Yegge's agent-first issue tracker):

- Issues are structured data with hash-based IDs (e.g., `bd-a3f2`).
- A dependency graph with typed links: `blocks`, `blocked-by`, `related-to`, `supersedes`, `closes`, `referenced-by`.
- Status is explicit and bounded: `open`, `in-progress`, `blocked`, `resolved`, `won't-fix`, etc.
- Issues hold the *minimum* metadata: title, body (markdown), status, links, owner (optional), labels (optional), attestable activity log.

Divergences from beads:

- **No Dolt dependency.** Issues are just git-tracked data — JSONL files in a `.the-valley/issues/` directory, or content-addressed blobs under a `refs/the-valley/issues/*` namespace. Structured queries are over the JSONL or a periodically-rebuilt index.
- **First-class on the bus.** Issue events (`issue-opened`, `issue-commented`, `issue-status-changed`, `issue-linked`) flow into the same event substrate as code events. Threads can scope to an issue exactly as they scope to a commit or a change.
- **Attribution via attestation, not just commit messages.** A commit's attestation can include a `closes: [bd-a3f2, bd-9c1d]` field; the integrator's success event triggers the issue's status transition.

## Why git-native + structured + event-driven

- **Distributable.** Clone the repo and you have the issues. No platform API to scrape, no migration story when changing forges.
- **Auditable.** Issue history is git history. Forensics work the same way as for code.
- **Equally usable by humans and agents.** Same data, two interfaces. Agents query JSONL or the bus; humans use a TUI or web view over the same files. No "official" interface that agents have to scrape.
- **Composable with the rest of the architecture.** Issues participate in threads, attestations, integrator events, the priority router — same machinery.

## Open questions

- **Identity for issue authors.** Same SSH keys as for commits, presumably. A `signed comment` is just a signed JSON object.
- **Body storage.** Markdown inline in the JSON, or as separate content-addressed blobs referenced by hash? Inline is simpler; blob is cleaner for large bodies and binary attachments.
- **Cross-repo issues.** A bug in `repo-a` blocking work in `repo-b`. Defer: v1 is per-repo; cross-repo linking can come later via stable issue IDs that include a repo namespace.
- **Search and indexing.** Terminal grep over JSONL works at small scale. For larger scale, a periodically-rebuilt SQLite or similar, treated as a derived projection (rebuildable from the source-of-truth JSONL).
- **Agent-author etiquette.** When an agent files an issue from observation (e.g., "I noticed test X is flaky"), it should attest its identity and its source signal. Probably the same attestation machinery as for commits.
- **Notifications and routing.** Issue events feed the priority router exactly as other events do. The "I'm watching this issue" semantics are subscriptions on the bus, not a separate watchlist table.
- **Migration from existing trackers.** GitHub Issues, Linear, etc. Out of scope for v1; doable later via importer that emits historical events.
- **Beads compatibility.** If we use beads' field shape (hash IDs, link types), an agent fluent in beads is fluent in this system with no retraining. Worth deliberately staying compatible at the data-model level even if we don't depend on the beads binary.

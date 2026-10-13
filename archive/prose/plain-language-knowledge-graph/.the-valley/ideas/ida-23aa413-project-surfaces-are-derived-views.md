---
type: idea
id: ida-23aa413
status: exploring
title: "Provide a web interface for browsing, reviewing, and working on projects"
created: 2026-08-02
source: design conversation, 2026-08-02
---

# Provide a web interface for browsing, reviewing, and working on projects

A project needs an interface for reading sources and diffs, monitoring pending changes, and working
with its knowledge graph. It must support private access and allow selected views to be shared
publicly.

The graph particularly needs help with navigation and review. Its ideas, decisions, bugs, and
outcomes preserve project knowledge after the conversations that produced them are gone. A reader
should be able to follow relationships, understand a node change, and notice overlapping concepts
without manually searching many files.

Browsing is part of the GitHub exit. A project whose useful public view still depends on GitHub
retains a practical dependence on it.

## Data and operations

The interface displays state held in Git, the knowledge graph, attestations, and the event log.
Those remain the durable sources. The interface must not introduce a separate authoritative copy of
project state.

Editing is compatible with this model. An interface can author a commit and submit it through the
same contribution path as another client.

Every operation offered through the interface must also be available to other clients and agents.
This keeps the system usable from automation and allows the interface to be replaced.

The [architecture](../../design/architecture.md#review-approval-and-feedback) describes review as
observation and feedback. A web client makes those capabilities easier for a person to use. Its
implementation should follow the project's language policy and
[minimality constraint](../../design/requirements.md#constraints).

## Public access

A public view introduces access beyond the private tailnet. The design must specify which data may
be exposed and who may authorize that exposure. Publishing a view must not implicitly publish all
repository, graph, or run data.

Authentication should use the registry model in
[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md). Credential lifetime
remains part of the access design; see
[[bd-8a591dc]](../bugs/bd-8a591dc-machine-credentials-never-expire.md).

## Approval in the browser

The interface is a candidate place for human approval. A person reading a change could use a local
hardware-backed credential to approve that exact change, satisfying the physical-presence
requirement in [[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md).

The assertion must be bound to the reviewed change. Its envelope and verification rules still need a
design: a browser-mediated assertion does not have the raw Ed25519 shape of the current signed-note
format. [[ida-d2dc957]](ida-d2dc957-attestations-ride-on-a-transparent-leaf.md) records that format
question.

## Open questions

- What scope and sequence should the first interface have?
- What may a public view expose, and who authorizes sharing it?
- Should the first version author changes or begin with browsing and review?
- Should the human-approval mechanism be delivered through this interface?

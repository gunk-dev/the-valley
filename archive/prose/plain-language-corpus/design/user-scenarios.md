# User Scenarios

This document defines the user scenarios that drive system requirements.

The scenarios form an escalating ladder ordered by actors and trust. Each scenario describes the
human and agent experience in the problem space, independent of underlying implementation
mechanisms.

A scenario receives full acceptance criteria only when it becomes the active development priority.
Earlier rungs are validated through real use before later rungs are detailed.

## The ladder

Each rung introduces new participants or removes trust assumptions. Each rung also establishes a
corresponding increment in the project's knowledge graph.

| Level  | Scenario Description                                                                         | Grounded In                                     | Knowledge Graph Increment                                                              |
| :----- | :------------------------------------------------------------------------------------------- | :---------------------------------------------- | :------------------------------------------------------------------------------------- |
| **S1** | Sovereign, permanent hosting on owned infrastructure.                                        | `the-valley` repository as the pilot.           | Issues, outcomes, ideas, and decisions live in the repository as plain files.          |
| **S2** | Instant integration in seconds; no waiting on remote CI.                                     | Daily solo development.                         | No new schema requirements.                                                            |
| **S3** | Autonomous agents land verified changes unsupervised.                                        | `klaus` agent orchestration workflows.          | Agents read and write knowledge nodes; tasks target outcome nodes.                     |
| **S4** | Automated builds, deployments, and notifications from a single causal event log.             | `cosmo` (NixOS infra) and `reel-life` services. | Knowledge changes emit observable events; merged code closes outcome nodes.            |
| **S5** | Semi-trusted collaborators contribute without platform accounts or admin overhead.           | First external collaborator.                    | No new schema requirements.                                                            |
| **S6** | Regressions are automatically attributed, reverted, and recorded as durable incident memory. | Real-world service regressions.                 | Incidents generate permanent knowledge graph nodes with blame and uncertainty records. |
| **S7** | Safe contributions from untrusted public contributors.                                       | Deferred public scale.                          | Optional future extension.                                                             |

---

## S1 — my repos live on my infrastructure and I can never lose them

### User Experience

Daily development workflows remain familiar: `clone`, `edit`, `commit`, and `push`.

The fundamental difference is where repositories live. The canonical copy of each repository lives
on hardware controlled by the operator. Integrated changes are automatically replicated to
independent physical locations—including offsite storage—within minutes. Public hosts like GitHub
act only as read-only downstream mirrors. If the primary host fails, the entire repository and
knowledge history can be fully restored from the offsite backup within 24 hours.

Project knowledge—outcomes, bug reports, decisions, and ideas—lives in the repository alongside
source code, backed up and versioned in the same atomic commit stream.

### Scope Decisions

- **Sovereign Hosting:** Repositories are hosted as bare git repositories accessible over Tailscale
  on the operator's machine (`classic-laddie`). Direct pushes to `main` are used during this
  bootstrap stage.
- **Mirror-First Migration:** Repositories dual-push to GitHub as a transitional mirror during
  migration. The canonical origin switches per repository once operational durability is verified.
- **Durability Invariant:** Recovery Point Objective (RPO) is near zero for integrated commits.
  Integrated commits are replicated across multiple locations within minutes. Recovery Time
  Objective (RTO) is under 24 hours. Durability is proven by performing and verifying an actual
  restore drill.
- **Agent Workflows:** Autonomous agents (via `klaus`) push topic branches to the sovereign host.
  During S1, the human operator manually inspects diffs and merges changes, establishing the
  baseline manual effort that automated integration (S2/S3) will eliminate.
- **Knowledge Graph v0:** Knowledge nodes are plain markdown files in `.the-valley/` with YAML
  frontmatter. Creation, updates, and resolutions are standard git commits. Structure is validated
  locally using `lib.knowledgeLint`.

### Acceptance Criteria

- [x] Canonical origin for `the-valley` is hosted on `classic-laddie`, with GitHub configured as a
      mirror.
- [x] Integrated changes replicate to offsite backup storage within minutes.
- [x] A complete disaster recovery restore from offsite backup has been executed and verified.
- [ ] A continuous week of real development (human and agent) occurs on the pilot repository without
      relying on GitHub.
- [x] An autonomous agent change lands end-to-end via direct repository push.
- [x] Issues and tasks are tracked and resolved using in-repo knowledge nodes.
- [ ] The migration and restore runbook is documented and repeatable for subsequent repositories.

---

## The rest of the ladder

### S2: Instant Integration via Local Verification

**Goal:** The feedback loop for landing changes drops from several minutes on remote cloud CI
runners to seconds on local machines. **Key Challenge:** Verification executed on the authoring
machine must produce signed cryptographic attestations that are sufficiently trustworthy for the
integrator to accept without re-running entire test suites synchronously.

### S3: Unsupervised Autonomous Agent Contributions

**Goal:** Autonomous agents author, verify, and land changes without human intervention, maintaining
identical safety and verification guarantees. **Key Challenge:** Non-human authorship must carry
tamper-evident cryptographic provenance that explicitly binds the executing agent, prompt brief, and
attested test results.

### S4: Unified Event-Driven Causality

**Goal:** Landed changes automatically trigger downstream derivations, deployments, and
notifications across homelab infrastructure without manual triggers. **Key Challenge:** Maintaining
an immutable, queryable causal event history where any state change can be traced back to its root
commit.

### S5: Frictionless Collaborator Access

**Goal:** External collaborators contribute to repositories without requiring the operator to
administer third-party platform accounts or complex role-based access control (RBAC) matrices. **Key
Challenge:** Granting, bounding, and revoking cryptographic push authority cleanly using simple key
registries and branch policies.

### S6: Automated Regression Attribution and Incident Memory

**Goal:** When a deployment fails or degrades, the system identifies the offending change, assists
in reversion, and records the failure mode directly into the knowledge graph. **Key Challenge:**
Attribution under uncertainty—handling noisy monitoring signals honestly without generating false
attributions.

### S7: Safe Public Contributions (Deferred)

**Goal:** Enable safe contributions from anonymous or untrusted public contributors. **Key
Challenge:** Sybil-resistant gating and sandboxing without reintroducing centralized platform
infrastructure.

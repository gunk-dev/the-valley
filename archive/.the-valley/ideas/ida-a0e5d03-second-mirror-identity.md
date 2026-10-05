---
type: idea
id: ida-a0e5d03
status: raw
title: The second mirrored project needs a mirror identity the schema does not declare
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# The second mirrored project needs an identity the schema does not describe

[schema/valley.cue](../../schema/valley.cue)'s `mirrors` field declares URLs and says outright that
credentials are the host's concern and are not declared there. That holds exactly as long as the
host mirrors one repository. GitHub accepts a given deploy key on one repository only, and
the-valley's mirror has already consumed the host's replication identity — the single
`valley-git-ssh-key` the git user pushes with. A second mirrored project cannot authenticate without
a decision about identity, so cosmo's migration is where the deferral runs out.

The fork is already recorded as deferred, at step 4 of qinling's migrate-and-restore runbook: GitHub
allows a deploy key on only one repo, so the next repo needs either per-repo keys with ssh host
aliases or a machine-user account — unresolved, decide at the second migration. cosmo is the second
migration. This node states the fork and does not take it.

## The two ways out

- **Per-repository deploy keys plus ssh host aliases.** Each mirror URL names an alias, and the git
  user's ssh config maps each alias to its own key. Blast radius stays per repository, and no GitHub
  account is created. The cost is that the fanout lives in ssh config the schema cannot see, so the
  declaration and the thing that makes it work drift apart silently — a mirror URL that names an
  unconfigured alias looks fine and fails only in a log line, because replication is best-effort.
- **One GitHub machine user.** A single account whose key authorizes every mirrored repository. One
  credential, one rotation, no per-repo fanout. The cost is a second GitHub identity to own and
  audit, and a credential whose reach grows with every repository added.

## What it costs the schema either way

If a mirror has to name the identity it authenticates with, that is the first field that pushes
machine integration into a domain model that has deliberately kept it out. The current split is
explicit: the-valley defines what a host serves, and consumers supply data directory, unix user, and
SSH keys ([[dcr-0f5d9b1]]
([dcr-0f5d9b1-cue-config-host-module.md](../decisions/dcr-0f5d9b1-cue-config-host-module.md))). An
identity _name_ is arguably not a credential and could sit in the model as a key the installer
resolves — but the same argument was available for every deployment concern the split rejected, so
it deserves the decision it has not had.

The alternative is to keep the model silent and let the installer route by mirror URL, which keeps
the schema clean at the price of a declaration that cannot be validated on its own.

## Related

- [[oc-87deec8]]
  ([oc-87deec8-valley-can-host-cosmo.md](../outcomes/oc-87deec8-valley-can-host-cosmo.md)) — the
  outcome this blocks; the only one of its blockers that is Phase 0 work.
- [[bd-8a591dc]]
  ([bd-8a591dc-machine-credentials-never-expire.md](../bugs/bd-8a591dc-machine-credentials-never-expire.md))
  — whichever way the fork is taken, the credential it creates is another permanent grant.

---
type: idea
id: ida-a0e5d03
status: raw
title: "Choose how multiple GitHub mirrors authenticate"
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# Choose how multiple GitHub mirrors authenticate

Mirroring several projects to GitHub requires a credential arrangement that works across those
repositories. The current mirror declaration lists URLs and leaves credentials to the host.

The deployment uses a repository deploy key for the-valley's mirror. That key cannot simply be
registered as the deploy key for another GitHub repository. The migration needs a deliberate choice
before adding a mirror that depends on it.

## Options

Per-repository keys can use SSH host aliases to select the correct key for each mirror URL. This
limits each credential to one repository. It also creates host configuration that must stay
consistent with the mirror declarations. An unresolved alias may fail only during a best-effort
push.

A machine account can use one credential across the repositories granted to that account. This
simplifies key routing and rotation, but creates another account to operate and a credential with
access to several repositories.

## Declaration boundary

A mirror could name a credential identity that the installer resolves. Such a name would not contain
the secret, but it would extend the domain model into an area currently owned by deployment
configuration.

Alternatively, the installer could continue selecting credentials from the mirror URL. That
preserves the current split but prevents the mirror declaration alone from establishing that
authentication is configured.

[[dcr-0f5d9b1]](../decisions/dcr-0f5d9b1-cue-config-host-module.md) defines the schema and
host-integration boundary. This node records the choice without settling it.

[[oc-87deec8]](../outcomes/oc-87deec8-valley-can-host-cosmo.md) tracks the migration dependency. Any
resulting machine credential also needs the expiry and revocation treatment described in
[[bd-8a591dc]](../bugs/bd-8a591dc-machine-credentials-never-expire.md).

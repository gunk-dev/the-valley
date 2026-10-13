---
type: bug
id: bd-d853d9c
status: open
title: "Authenticate and authorize access to the event bus"
created: 2026-07-17
source: security review of phase1/event-log, 2026-07-17
---

# Authenticate and authorize access to the event bus

The bus accepts unauthenticated local connections. A local process can publish forged events,
subscribe to traffic, or use JetStream administration to purge or delete the stream.

Localhost binding limits network access but does not separate trusted publishers from other
processes on the host. This matters on a machine that executes agent work.

## Required boundary

Before widening `services.valley.bus.listen` or letting an automated consumer act on bus data, the
bus needs distinct permissions for publishing, subscribing, and administration.

Hooks and the integrator need permission to publish their event types. A client tailing events needs
subscribe-only access. Stream administration must be restricted separately.
[[dcr-62ecc36]](../decisions/dcr-62ecc36-signal-contracts.md) describes the subject permissions for
domain events and metrics.

Listener expansion and authentication must be deployed together. If the firewall trusts the tailnet
interface, widening the listener without authorization exposes the bus to other tailnet devices.

## Recovery and consumer behavior

Repository events can be reconstructed from Git, which limits the effect of losing the bus
projection. That does not prevent a forged event from misleading a reader or an automated consumer.

The current integrator polls repository state; it publishes outcome events rather than depending on
an unauthenticated event as authority to land. New consumers still need the authorization boundary
before acting.

Consumers must read the schema-validated repository field in the payload. They must not infer the
repository by splitting subject tokens: a project name may itself contain dots.

[[bd-500adf7]](bd-500adf7-bus-shares-git-user.md) tracks the separate filesystem isolation of the
bus process.

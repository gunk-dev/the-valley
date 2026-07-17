---
type: bug
id: bd-d853d9c
status: open
title: The event bus is unauthenticated — a gate before consumers act or the listener widens
created: 2026-07-17
source: security review of phase1/event-log, 2026-07-17
---

# The event bus is unauthenticated

Any local process can connect to the bus on localhost — publish forged events, subscribe, and reach
the JetStream admin API (purge or delete the stream). The valley host is exactly the kind of machine
where semi-trusted code runs routinely: dispatched agents work on it all day, and they are the
processes a forged ref-updated event should be assumed to come from.

Acceptable today by design: events are a rebuildable projection with git as the source of truth, and
the only consumer is a human watching valley tail. A forged event can mislead an observer, not
corrupt state.

The gate: before any automated consumer acts on an event, or before services.valley.bus.listen
widens beyond localhost, the bus needs authorization — publisher credentials for the ref-updated
hook, subscribe-only permissions for tails, JetStream administration restricted. Widening the
listener and adding auth must land together: if the tailnet interface is trusted by the firewall,
widening alone exposes the bus to every tailnet device.

Related: consumers must trust the schema-validated repo payload field, never parse subject tokens —
project names may contain dots, so tokenizing valley.git.<repo>.ref-updated is ambiguous.

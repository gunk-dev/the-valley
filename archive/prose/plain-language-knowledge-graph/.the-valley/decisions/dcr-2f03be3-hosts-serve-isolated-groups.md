---
type: decision
id: dcr-2f03be3
status: superseded
title: "Shared-host isolation is defined for valleys"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Shared-host isolation is defined for valleys

[[dcr-9b5da04]](dcr-9b5da04-hosts-serve-isolated-valleys.md) is the current decision on hosting
multiple valleys on one machine. This node is superseded.

That decision defines separate Git users, repositories, service state, and registry-derived
permissions for each valley. It also covers integrator identity, prevention of governance lockout,
and the limits of isolation from the machine's owner.

Bus scoping and the detailed layout for multiple valleys remain open there.
[[ida-8482624]](../ideas/ida-8482624-federation-groups.md) considers cooperation between
independently hosted valleys.

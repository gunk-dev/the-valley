---
type: bug
id: bd-500adf7
status: open
title: "The bus service shares the repository owner's Unix identity"
created: 2026-07-17
source: security review of phase1/event-log, 2026-07-17
---

# The bus service shares the repository owner's Unix identity

The `valley-bus` service runs as the Unix user that owns the repositories. Its systemd sandbox
restricts writes to the bus store, but the repository data directory remains readable to that user.
A compromised bus process can therefore read private repository contents.

The loopback listener limits network reachability. It does not remove the filesystem access if the
process is compromised.

The bus needs its own user before its network exposure is widened. The integrator already uses a
separate user, but its repository group grants the access it needs to write refs. Giving the bus
that same group would also grant repository access.

The bus should have access only to its own service state, without membership in the shared
repository group. Its store directory needs to be separated from the repository data directory so
those permissions can be granted independently.

[[bd-d853d9c]](bd-d853d9c-bus-unauthenticated.md) tracks bus authentication.
[[dcr-9b5da04]](../decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md) extends this separation to
multiple valleys on one host.

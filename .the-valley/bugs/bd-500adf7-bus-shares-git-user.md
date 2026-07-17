---
type: bug
id: bd-500adf7
status: open
title: The bus service runs as the git user — repositories are readable by a compromised server
created: 2026-07-17
source: security review of phase1/event-log, 2026-07-17
---

# The bus service runs as the git user

The valley-bus unit runs as the same user that owns the repositories. The systemd sandbox confines
writes to the stream's store directory, but ProtectHome does not cover the data directory, so a
compromised nats-server can read every private repository. Acceptable while the bus listens only on
localhost; before the bus becomes network-reachable, the service needs its own user — or the
repositories made inaccessible to it, which is awkward while the store directory nests inside the
data directory.

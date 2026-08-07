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
localhost; before the bus becomes network-reachable, the service needs its own user.

The integrator now runs under its own unix user, which is the first half of that split and settles
its shape. The bus cannot take the same shape, though. The integrator reaches the repositories
through the git group, and the repositories it serves are group-shared so it can write refs — so
putting the bus in that group would now grant it write as well as read. The bus needs a user in no
shared group, and that in turn needs its store directory moved out from under the data directory:
storage can only be granted on its own if it does not sit inside the repositories'.

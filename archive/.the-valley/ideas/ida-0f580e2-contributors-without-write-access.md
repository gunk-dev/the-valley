---
type: idea
id: ida-0f580e2
status: exploring
title: Contributors need no repository write access at all
created: 2026-08-27
source: design conversation, 2026-08-27
---

# Contributors need no repository write access at all

Eventually contributors need no repository write access at all. A contribution arrives one of two
ways: a patch submitted on an API, or a pull request in the term's original sense — the contributor
serves their own branch and the valley pulls it. The tailnet is what makes the second easy on
addressability and access: every contributor machine has a stable name, serving a read-only branch
from it is trivial, and reachability is governed by tailnet ACLs rather than provisioned keys on the
host.

The shared request-ref namespace ([design/integration.md](../../design/integration.md)) is a race
surface precisely because contributors write into it. A pull model dissolves that class, since each
contribution lives under its contributor's own address until the integrator fetches it.

## Related

- The namespace this removes the race surface from:
  [design/integration.md](../../design/integration.md)

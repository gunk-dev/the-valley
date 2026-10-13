---
type: idea
id: ida-0f580e2
status: exploring
title: "Accept contributions without granting repository write access"
created: 2026-08-27
source: design conversation, 2026-08-27
---

# Accept contributions without granting repository write access

A contributor could submit a patch through an API or serve a branch for the valley to fetch. Either
approach would allow contributions without giving the contributor write access to the hosted
repository.

A tailnet could make contributor-hosted branches practical by providing stable machine names and
controlling reachability. The contributor would expose a read-only branch; the valley would fetch
the proposed change.

This would also remove contributor writes to the shared integration-request ref namespace. Each
contribution would remain at its own address until fetched, avoiding races caused by contributors
updating the same request refs.

The submission and authorization details remain to be designed.
[integration.md](../../design/integration.md) describes the current request namespace.

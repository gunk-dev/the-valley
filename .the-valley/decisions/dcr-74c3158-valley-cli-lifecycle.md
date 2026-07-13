---
type: decision
id: dcr-74c3158
status: decided
title: "valley CLI: born as a shell script in-engine, graduates deliberately"
created: 2026-07-13
source: ratified by patflynn
---

# valley CLI: born as a shell script in-engine, graduates deliberately

**Decided by patflynn, 2026-07-13.**

The `valley` CLI lives at [`bin/valley`](../../bin/valley) in the engine repo because its verbs are engine-generic — `pending` and `review` serve any valley project's integrator, not just this one. It ships two ways: run directly from any checkout, or as the flake package (`nix run .#valley`). It is deliberately a shell script — runs from any checkout, no machinery — the portability stance of [[ida-b9f646c]].

**Tripwires.** It graduates to its own repo and a real language the moment ANY of these is crossed: it grows past ~300 lines; it needs persistent state, config, or a daemon; it needs dependencies beyond git + coreutils (Phase 1's `valley tail` will trip this); its behavior gets subtle enough to need real tests; it needs nontrivial parsing or concurrency.

Graduation is a deliberate act recorded as a decision node — precedent: klaus, born in cosmo, graduated deliberately. The anti-goal this guards against: a massive bash project that no human — and sometimes no machine — can understand.

## Related

- The portability stance this instantiates: [[ida-b9f646c]] ([ideas/ida-b9f646c-nix-backend-not-substrate.md](../ideas/ida-b9f646c-nix-backend-not-substrate.md))

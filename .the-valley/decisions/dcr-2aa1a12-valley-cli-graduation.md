---
type: decision
id: dcr-2aa1a12
status: proposed
title: valley crossed its first graduation tripwire — graduate, or renegotiate the tripwire
created: 2026-07-17
source: Phase 1 landing, 2026-07-17
---

# valley crossed its first graduation tripwire

The dependency tripwire in [[dcr-74c3158]]
([dcr-74c3158-valley-cli-lifecycle.md](./dcr-74c3158-valley-cli-lifecycle.md)) is crossed: the
`tail` and `replay` verbs need the nats CLI, beyond git and coreutils. The line-count tripwire
(~300) is close behind. Per the lifecycle policy, crossing triggers a deliberate decision, recorded
here.

Options:

1. **Graduate now** — own repo, a real language (Go is the house pattern). Buys tests and robust
   parsing; costs ceremony while the verb surface is six verbs in one file.
2. **Stay embedded, dependency bundled** — `nix run .#valley` already wraps natscli, so the
   dependency arrives cleanly; re-arm the tripwire on the next accretion (state, a daemon, subtle
   behavior needing tests).
3. **Split** — graduate the bus-facing verbs (`tail`, `replay`) into the future graduated tool,
   keep the review verbs embedded.

Status: proposed — undecided until reviewed.

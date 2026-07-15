---
type: idea
id: ida-4557af7
status: exploring
title: "Spec-driven iteration: capture a tight spec and let it drive implementation over time"
created: 2026-07-15
source: owner noodle — captured imperfect on purpose ("even if it's imperfect, it's there to be polished")
---

# Spec-driven iteration

**Owner framing, close to verbatim.** The current work is prototyping; long-term, the project may want to capture and iterate a *tight spec* for the-valley, and use the spec to drive implementation iteration over time.

**Proof of pattern, already shipped.** The CUE host schema is spec-driven development in one corner: [schema/valley.cue](../../schema/valley.cue) is "the canonical domain model … deliberately not Nix" ([roadmap](../../design/roadmap.md)), the installers are implementations consuming it, `cue vet` at build time is the conformance gate, and [[ida-b9f646c]] ([ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md)) pins the implementability floor: every schema realizable by a shell script on Debian. The generalization: what CUE does for the host declaration, some checkable spec should do for each component as it hardens — the event log's wire format, the attestation's claim structure, the integrator's invariants. The roadmap's CUE-schemas cross-cutting thread ([roadmap § cross-cutting threads](../../design/roadmap.md#cross-cutting-threads)) is the first planned instance.

**Agent-era economics.** When implementation labor is cheap and regenerable (agents), the artifacts worth hand-polishing are the spec and its conformance suite; implementations become derived, re-derivable, even disposable. This extends the repo's re-derivation discipline — requirements re-derived from the ladder ([[oc-49555c7]], [oc-49555c7-requirements-from-ladder.md](../outcomes/oc-49555c7-requirements-from-ladder.md)), roadmap re-derived from requirements ([[oc-2fbcd7b]], [oc-2fbcd7b-roadmap-rederived.md](../outcomes/oc-2fbcd7b-roadmap-rederived.md)) — one layer down: code re-derived from spec, with the conformance suite as the integrator's gate for regenerated implementations.

**The tension to hold.** The ladder's own rule is anti-big-spec-upfront: acceptance detail arrives only when a rung becomes top priority. So the likely landing is *not* one grand spec but a spec per component, at the moment it crosses from prototype to load-bearing. Prototyping and spec-driven are phases of each *part*, not of the project.

## Open questions

- Language and form per spec kind: CUE for data shapes; what for behavior and invariants?
- Where conformance suites live and who runs them — the integrator's gate?
- How spec changes are governed — they are the highest-leverage changes in the repo.
- Relation to the change model ([[ida-93e4f91]], [ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)): does a spec change invalidate pending changes against implementations of that spec?

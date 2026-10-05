---
type: bug
id: bd-5cb2034
status: open
title: Standing demand pressure risks runaway agent dispatch loops without hard budget latches
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Demand pressure risks runaway dispatch loops without budget latches

The system design introduces standing demand pressure as an anti-stall mechanism ([[ida-3145b7a]]).
A scheduler controller continuously reconciles the outcome DAG, detecting stalled or unblocked
frontier nodes and dispatching autonomous agents (klaus) to unblock them ([[ida-eac723e]]). When
automated dispatch operates without strict global spending ceilings, decomposition recursion limits,
and execution circuit breakers, standing demand pressure risks triggering runaway LLM token
expenditure and severe semantic drift.

## The failure modes of autonomous anti-stall

Autonomous AI agents fail frequently and unpredictably. When an agent fails to complete an
outcome—by producing unparseable diffs, failing test assertions, or introducing conflicting
changes—standing demand pressure treats the failure as a stall condition to be actively reconciled.

Without hard protective latches, this reconciliation loop creates two failure modes:

1. **Runaway retry storms and financial burn.** An agent run that fails verification or dies
   unexpectedly loses its in-progress lease. The scheduler immediately marks the outcome as open on
   the unblocked frontier and dispatches a fresh agent run. Under unattended operation, this loop
   can execute continuously overnight, consuming hundreds of dollars in API credits on an unsolvable
   task or a broken environment.
2. **Recursive decomposition and semantic drift.** When an agent attempts to resolve a complex
   outcome by decomposing it into multiple sub-outcomes in the graph, the newly created nodes become
   part of the frontier and trigger subsequent agent dispatches. If sub-agents further decompose
   their assigned tasks without human architectural oversight, the outcome graph rapidly expands
   with hallucinated or redundant work, drifting far from the operator's actual requirements.

## Why this is acceptable today

Agent runs are currently dispatched interactively by the human operator using explicit run-budget
arguments. The autonomous background scheduler described in [[ida-eac723e]] and [[ida-3145b7a]] is
still an exploratory design and has not been wired to automated loop daemons. The risk is an
existential operational threat that must be resolved before automated dispatch is activated.

## Directions, not decisions

- **Global financial circuit breakers.** Enforce hard token and dollar spending ceilings (per hour
  and per day) at the actuator boundary ([[ida-f1b39e8]]). When the ceiling is reached, all
  automated dispatch halts until a human resets the latch.
- **Strict retry caps and backoff.** Limit automated retry attempts per outcome to a small fixed
  count (e.g., two attempts). Upon repeated failure, the outcome is marked as blocked on human
  review rather than returned to the frontier.
- **Decomposition approval gates.** Restrict autonomous agents from adding new blocking outcome
  nodes to the graph without explicit human sign-off, or limit recursive decomposition depth to a
  maximum of one level.
- **Dead-letter outcomes.** Move persistently failing or cycling outcomes to an explicit dead-letter
  queue, isolating them from standing demand pressure.

## Related

- Demand pressure: [[ida-3145b7a]]
  ([ida-3145b7a-demand-pressure.md](../ideas/ida-3145b7a-demand-pressure.md))
- Outcome DAG scheduler: [[ida-eac723e]]
  ([ida-eac723e-outcome-dag.md](../ideas/ida-eac723e-outcome-dag.md))
- Actuator effect boundary: [[ida-f1b39e8]]
  ([ida-f1b39e8-outbound-effects-pass-through-an-actuator.md](../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md))
- Incident memory: [design/roadmap.md](../../design/roadmap.md#phase-7--feedback--incident-memory)

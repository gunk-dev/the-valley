---
type: idea
id: ida-1bda403
status: exploring
title: "Support different models and agent harnesses"
created: 2026-07-15
source: design conversation, 2026-07-15
---

# Support different models and agent harnesses

The-valley should accept agent work independently of the model provider or harness used to produce
it. Its contribution contract is a signed change serving an outcome, as described in
[[ida-93e4f91]](ida-93e4f91-changes-not-branches.md). Execution software and provider authentication
belong to the machine running the agent.

Subscription-based execution is also worth supporting where possible. Subscription access often ties
together a provider, model, harness, and authentication method, so it may need a different execution
arrangement from a metered API. That arrangement remains open.

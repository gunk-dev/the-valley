---
type: idea
id: ida-1bda403
status: exploring
title: "Vendor/harness-agnostic agent runs, with a path to subscriptions"
created: 2026-07-15
source: owner design conversation
---

# Vendor/harness-agnostic agent runs

For LLM-agent executions, the-valley is vendor- and harness-agnostic. The substrate's contract is a
signed change against an outcome, per changes-not-branches ([[ida-93e4f91]],
[ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)); which model or
harness produced the change is a machine-layer concern.

Ideally there is also a way to run agent-runs on the-valley using subscription plans, not just
metered APIs — nontrivial because subscription entitlements couple harness, model, and provider auth
as a bundle.

---
type: bug
id: bd-8a591dc
status: closed
title: "Machine access now expires through registry compilation"
created: 2026-07-31
source: provisioning a disposable agent VM against the pilot host, 2026-07-31
---

# Machine access now expires through registry compilation

Machine access needs a declared lifetime so a disposable machine does not leave an indefinite grant
behind. The registry and compiler provide that mechanism, which is the resolution recorded by this
closed bug.

[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) requires expiry for
machine and service entries. The [identity compiler](../../identity/) reads the integrated registry
and omits expired entries from generated access artifacts.

Provisioning access is a registry change. It does not require rebuilding the primary host for each
machine.

## Expiry and recovery behavior

Removal takes effect on the first successful convergence after expiry. It is not an instantaneous
credential expiry enforced independently by SSH.

A failed compilation leaves the last valid artifacts in place. This preserves a recovery path but
can delay access removal. Keys declared directly by the host remain available outside the generated
registry artifacts during this deployment stage.

The registry therefore resolves the lack of any declared machine lifetime. Continued convergence and
the handling of fallback keys remain part of operating that mechanism.

## Related work

Qinling's `dcr-34b3f6d` separates keys by capability, including authentication, decryption, and
human signing. That separation complements expiry by limiting what a credential can do.

Short-lived certificates remain a possible next step. Their issuer and request-authentication design
are still open in [[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md).

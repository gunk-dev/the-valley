---
type: decision
id: dcr-0f5d9b1
status: decided
title: "Define host configuration in CUE and ship the host module from the-valley"
created: 2026-07-06
---

# Define host configuration in CUE and ship the host module from the-valley

The-valley owns the reusable host module and the CUE schema that describes its configuration. A
deployment enables that module and supplies the machine-specific settings.

The flake exports the implementation as `nixosModules.valley-host`. Consumers such as cosmo
configure its data directory, Unix user, SSH keys, and other host integration. Keeping the module
here lets deployments share the same definition of a valley host.

CUE is the canonical configuration format. The domain model must be usable by tools that do not run
Nix. The NixOS module validates and exports the CUE at build time; where Nix needs the resulting
data, it reads the exported value. It does not maintain a second schema. Configuration and
[event contracts](../../design/roadmap.md#work-across-phases) use the same schema language.

Configuration declares projects. A project's Git store is a nested field in that declaration. The
current project boundary is one repository, as specified in
[[dcr-5da1f36]](dcr-5da1f36-project-is-repo.md); possible additional stores are explored in
[[ida-48c8868]](../ideas/ida-48c8868-stores-beyond-git.md).

Projects can declare push mirrors. Mirroring is best-effort and propagates deletions within the
declared publication scope. [[dcr-24d62f7]](dcr-24d62f7-publication-mirror-not-review-queue.md)
defines that scope as `main` and tags. [[oc-9949561]](../outcomes/oc-9949561-push-replication.md)
covers the replication outcome.

Access controls must correspond to an enforceable boundary. A single Git user with unrestricted
git-shell cannot enforce per-project permissions. The access model and its forced-command
enforcement are defined in [[dcr-e544f20]](dcr-e544f20-access-is-verbs-on-projects.md) and
[[dcr-b87f6e8]](dcr-b87f6e8-identity-is-a-governed-registry.md).

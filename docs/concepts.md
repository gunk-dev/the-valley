# Concepts and glossary

the-valley separates software work into clear, composable pieces: changes with stable identity
across revisions, check evidence signed over content trees, a pre-receive hook and integrator on the
host, project storage outside `main` for records and the plan graph, and explicit principals and
grants. Every section below states what a concept means first, then how git, Nix, and CUE implement
it, and distinguishes what the code always enforces from what an instance configures and what is not
yet built.

## A change: ready, approve, submitted

A **change** is one unit of proposed work with a stable identity across revisions. An author creates
a change, revises it until it is complete, and marks it **ready** (`valley ready`) to put a specific
version up for review. Each revision is a numbered version (`v1`, `v2`, …) that records the proposed
source commit (`head`), title, branch name, and uploader; the latest version is the **active
version** ([change/record.go:19-33](../change/record.go), [97-120](../change/record.go)), and its
**base** is the `main` commit that version was branched or rebased from
(`git merge-base origin/main <head>`), which review and integration use to measure what the change
modifies and whether `main` has moved since. If the author needs to revise further before the change
lands, they can pause it back to `draft` (`valley unready`), or mark it `withdrawn`
(`valley withdraw`) to abandon it; readying a `draft` or `withdrawn` change again at its active
version's head re-readies that same version number without creating a new version
([status/ready.go:223-232](../status/ready.go),
[valleyhook/record.go:281-319](../valleyhook/record.go)).

An operator lists ready changes with `valley pending` and inspects one with
`valley review <change>`, which offers an interactive prompt. So a reviewer does not have to re-read
an entire change from scratch after a rebase or revision, `valley review` compares the active
version against its earlier **approved version** when one can be compared
([status/compare.go:1-46](../status/compare.go), [bin/valley:976-1059](../bin/valley)): if the file
delta is unchanged (a clean restack), it prints a short summary with any landings between the two
bases and pages nothing; if the version changed files, it pages the comparison (the files whose
change differs, the range-diff between the two versions, and the shown version's diff of those
files); and if there is no approved version or no comparison can be made, it pages the full diff
(`[v]iew full diff` is available at the prompt in every case). The operator approves the change with
`valley approve <change>` (or by selecting `[a]pprove` in `valley review`), which runs and signs all
required checks ([attest/run.go:85](../attest/run.go)) and writes an integration request binding
that one exact commit.

Once the integrator verifies that commit's required check evidence and integrates it into `main`,
the change version is **submitted**: `landed` when its commit is on `main`'s first-parent history
with no merge commit above it, or `landed-recorded` when the integrator merged it onto a newer
`main` as a single-parent commit carrying a verified transfer statement and a `land` outcome record
that merges to the same tree ([rebase/snapshot.go:10-34](../rebase/snapshot.go),
[164-191](../rebase/snapshot.go), [242-299](../rebase/snapshot.go)). To keep operators and tooling
from acting on a guess when a landing's evidence is incomplete or unverified, the CLI reports the
version as **uncertain** whenever neither `landed` nor `landed-recorded` holds and the outcome
record says `failed`, or says `land` after the request ref was consumed (including when signer
verification environment variables are unset for a merged landing;
[rebase/snapshot.go:250-255](../rebase/snapshot.go)).

_How git and Nix realize this:_ To give every change a stable identity across rebases and a short
name for everyday CLI commands, each change has a 32-letter **Change ID** drawn from `k`–`z`
([change/id.go:19-46](../change/id.go)) and a short CLI **handle** — the shortest prefix (at least 4
letters) unique among changes currently recorded on `origin`, which can lengthen if a colliding
Change ID is recorded later ([change/id.go:92-112](../change/id.go)). Without `--stamp` or `--head`,
`valley ready [<branch>]` records the remote branch tip (`origin/<branch>`), which must already be
pushed to `origin` ([status/ready.go:146-165](../status/ready.go)). For a brand-new change
(`before == nil`) whose local head commit is not yet on `origin`, `valley ready --stamp` amends the
local commit with a `Change-Id: <id>` trailer and pushes the branch before writing `v1`
([status/ready.go:192-204](../status/ready.go), [451-453](../status/ready.go)). Across later
revisions or rebases, `valley ready` keeps the same Change ID if the commit carries a `change-id`
commit header (written by `jj`) or `Change-Id` trailer, or if you pass `--change <id>`
([status/ready.go:386-415](../status/ready.go)). Change records live as `record.json` commits at
`refs/the-valley/changes/main/<id>` (`valleyhook` accepts change records only for `target: "main"`;
[change/id.go:15-30](../change/id.go), [valleyhook/record.go:188-193](../valleyhook/record.go)), and
approvals write an **integration request** ref at
`refs/the-valley/integration-requests/<target>/c-<id>` (or `<branch>` with `/` replaced by `-` for
an unrecorded branch; [valleyhook/policy.go:37-39](../valleyhook/policy.go)). On the client, this
flake's `valley` package (`nix develop` or `packages.<system>.valley`) wraps `bin/valley` with
`valley-go`, `attest`, `cue`, `git`, `less` (for paging diffs and taking review notes), and
`natscli`, while running `bin/valley` directly from a checkout uses those tools plus `ssh` and
`coreutils` from `PATH`, and `nix` with flakes enabled builds pure checks
([nix/packages.nix:57-88](../nix/packages.nix)).

## Checks and evidence

Every change **requires** a set of checks determined by the files it modifies. So an instance can
enforce baseline verification across every hosted project while letting individual repositories add
stricter rules, two CUE policy layers govern verification in `valley checks`, `valley approve`, and
the integrator: an **instance floor** (`policy/instance/`) required of every project the integrator
judges on the host, and an optional **project policy** (`policy/project/`) that can only add checks
or tighten rules, never weaken the floor ([composed/composed.go:46-106](../composed/composed.go),
[integrator/policy.go:81-121](../integrator/policy.go)). If a project has no `policy/project/`
directory, the instance floor governs verification alone
([integrator/policy.go:21-23](../integrator/policy.go)). To map changed files to the checks they
require, policy groups file globs into **path classes**, and any changed path that matches no
declared class falls under `unclassified` and requires the checks listed in `unclassified` (which
may be empty; [schema/verification.cue:62-67](../schema/verification.cue),
[integrator/policy.go:273-276](../integrator/policy.go)). To express when a future restacker may
carry an approval across a rebase, each path class can also declare a `carry` mode (`same-paths`,
`same-class`, or `exact`); `valley checks` reports the composed carry mode, while acting on `carry`
to carry approval automatically across restacks is **not built yet**
([schema/verification.cue:300-308](../schema/verification.cue),
[docs/architecture.md:629-653](./architecture.md#policy)).

Checks come in two kinds: **pure checks**, which build Nix flake check targets via `nix build`
(`runner: "nix"`, `<root>#checks.<system>.<attr>`; [attest/check.go:138](../attest/check.go), with
sandboxing determined by the machine's Nix configuration), and **effectful checks**, which run
commands (`runner: "command"`) that may touch external state and whose evidence is accepted only
when its age is within a policy-defined `validity` window. To prove that a check passed on a
specific source tree without re-running the check on the server, `valley approve` invokes `attest`
to run each required check and produce **evidence**: a cryptographic **attestation** signed with the
supplied SSH key (`--key` or `VALLEY_ATTEST_KEY`) over the content digest of the source tree
(`valley-tree-v1`); `valleyhook` verifies that the signature matches the note's self-declared key
hash on push, and the integrator checks that the signer appears in its trusted `known-signers` file
when verifying evidence. When `main` has moved ahead of a change's base, the integrator never
re-executes checks itself; instead, it reuses existing evidence if the check transfers: a pure check
transfers if the landed tree is identical (`unmoved`) or if recomputing the check's Nix evaluation
on the landed tree yields both the same input-closure digest (`valley-inputs-v1`) and the same Nix
derivation digest (`closure`; [integrator/refs.go:388-390](../integrator/refs.go),
[integrator/verdict/verdict.go:406-417](../integrator/verdict/verdict.go)); an effectful check
transfers (`untouched`) only if the net diff on `main` between the attested base and current `main`
tip (`base..tip`) touches none of the path classes that required that check for this change, the
policy sets a non-zero `validity` window, and the attestation age is strictly within that window
([integrator/refs.go:377-382](../integrator/refs.go),
[integrator/verdict/verdict.go:425-440](../integrator/verdict/verdict.go)).

_How git realizes this:_ Signed notes live under
`refs/the-valley/attestations/<tree-digest>/<key-hash>`. So readers can verify that `main`'s new
tree satisfied policy even when a landing was merged onto a newer `main`, on a landing — whether
fast-forward or merged — the integrator signs a **transfer statement**
(`the-valley/integration/transfer/v1`, recording the landed tree, the attested base tree, the policy
digest, and the per-check verdicts) and countersigns the contributor's notes before `git update-ref`
advances `main`, then stores that signed evidence afterward
([integrator/refs.go:466-518](../integrator/refs.go),
[integrator/transfer.go:43-97](../integrator/transfer.go)). Because storing the evidence, consuming
the request, and writing the outcome follow `update-ref` rather than sharing an atomic multi-ref
transaction, a failure after `update-ref` can leave `main` advanced with incomplete records
(fast-forward landings still report `landed`, while merged landings can report `uncertain` when the
required evidence is missing).

## The host, the hook, and the integrator

A **valley host** is a server (configured on NixOS via `services.valley`) that stores repositories
and enforces project rules. Like a valley, the host is the low place where streams of work meet. Two
components guard it:

- **The pre-receive hook (`valleyhook`)** inspects every SSH push before git accepts it
  ([valleyhook/policy.go:88-221](../valleyhook/policy.go)). To let the host pause all incoming SSH
  pushes during initialization or maintenance, a **push hold** refuses every push across all
  projects whenever the sentinel file `${dataDir}/.valley-hold` (default `/srv/git/.valley-hold`) is
  present ([nix/valley-host.nix:489-495](../nix/valley-host.nix)). Whether a branch such as
  `refs/heads/main` is protected is a **deployment choice** in `valley.cue`: if a project declares a
  `protection` block, `protection.refs` defaults to `["refs/heads/main"]` and only principals listed
  in `protection.writers` may push to those refs over SSH; if `protection.refs` matches
  `refs/heads/main` and `protection.writers` is empty, no SSH push may update `main`, whereas a
  project with no `protection` block leaves all branches open
  ([valleyhook/policy.go:12-16](../valleyhook/policy.go), [186-188](../valleyhook/policy.go)).
  Unprotected topic branches (`refs/heads/*`) are open to any authorized key; outside
  `refs/heads/*`, the four admitted `refs/the-valley/*` namespaces, and the exact
  `refs/the-valley/knots` ref, refs (including `refs/tags/*`) require a project-level ref grant
  (`grants.<name>: {refs, writers}` in `valley.cue`) unless categorically forbidden
  ([valleyhook/policy.go:18-24](../valleyhook/policy.go), [124-225](../valleyhook/policy.go)). On
  `refs/the-valley/*`, the hook enforces schema and permission rules: any named principal may create
  a change record (becoming its `owner`); only the change's `owner` or a `request` grant holder may
  update a change record, except that a `restack` grant holder may add a single version to a `ready`
  change keeping its active branch and title
  ([valleyhook/record.go:160-198](../valleyhook/record.go), [264-272](../valleyhook/record.go));
  only a `request` holder may delete a change record or write/delete integration requests
  ([valleyhook/policy.go:151-158](../valleyhook/policy.go),
  [valleyhook/record.go:361-370](../valleyhook/record.go)); and nobody may push to
  `integration-outcomes` or `projects` ([valleyhook/policy.go:129-133](../valleyhook/policy.go)).
- **The integrator** verifies approved changes and lands them onto `main`; it runs as an optional
  host service (`services.valley.integrator.enable`) on a configurable timer
  (`services.valley.integrator.interval`, default `15s`;
  [nix/valley-host.nix:1220-1230](../nix/valley-host.nix)). On each pass, it deletes pending
  requests for changes that are currently `withdrawn` and records `voided-withdrawn` (if a withdrawn
  change is readied again before that pass runs, the old request remains standing and can land; to
  revoke an approval immediately, a `request` holder must delete the request ref;
  [status/ready.go:350-355](../status/ready.go), [integrator/refs.go:118](../integrator/refs.go),
  [integrator/records.go:385-442](../integrator/records.go)). For each remaining request, the
  integrator refuses if the request's commits include a merge (`refused-merge`) or a version head of
  a `draft` or `withdrawn` change, or if a `c-<id>` request's change is not `ready` or does not list
  the requested head among its versions (`refused-not-ready`;
  [integrator/records.go:286-320](../integrator/records.go), [454-466](../integrator/records.go)).
  Otherwise it evaluates policy and signed check evidence (and Nix closure equivalence when `main`
  has moved) and lands the change on `main` via local `git update-ref` (which runs on the host and
  bypasses `pre-receive`). If `main` has not moved since the change's base, it advances `main`
  directly to the approved commit; if `main` has moved, it merges the tree via `git merge-tree` and
  creates a **new single-parent commit** on top of `main`
  ([integrator/refs.go:399-439](../integrator/refs.go)). It re-reads the change records right before
  advancing `main`, though an `unready` or `withdraw` push arriving in the narrow instant between
  that final read and `update-ref` will not stop the landing
  ([integrator/refs.go:475-490](../integrator/refs.go)). Finally, it writes an **outcome record**
  (`land`, `stale`, `reject`, `refused-not-ready`, `refused-merge`, `voided-withdrawn`, or `failed`)
  at `refs/the-valley/integration-outcomes/<target>/<change>`
  ([integrator/refs.go:43-44](../integrator/refs.go),
  [rebase/outcome.go:10-52](../rebase/outcome.go)).

## Records, project storage, the plan graph, and knots

A repository has two parts: **the source** (`main` and release tags) and **project storage** (the
operational records around the source: change records, check attestations, integration requests,
integration outcomes, restack reports, and the plan graph). Keeping project storage outside `main`
means updating a plan item, recording a check attestation, or marking a change ready never dirties
`main` or triggers source-level rebuilds.

Within project storage, the **plan graph** tracks what the project intends to do and why. Each item
in the graph is a **knot** — named after the knotted cords used to keep records before writing. A
knot has one of four types, with `owner` optional on every type
([knot/model.go:19-139](../knot/model.go)):

- **`outcome`** — a target result or piece of work to complete (`oc-*` in `outcomes/`, with state
  `open`, `in-progress`, `done`, or `abandoned`); root `outcome` knots (those with no `parent`) are
  displayed as milestones in `valley plan` ([status/plan.go:6-7](../status/plan.go)).
- **`bug`** — a defect to fix (`bd-*` in `bugs/`, with state `open` or `closed`).
- **`idea`** — an exploration or proposal (`ida-*` in `ideas/`, with state `raw`, `exploring`,
  `adopted`, `graduated`, `superseded`, or `discarded`); when `graduated`, its `graduated_into`
  field names the local `outcome` or `question` it became
  ([knot/model.go:130-132](../knot/model.go), [knot/graph.go:395-404](../knot/graph.go)).
- **`question`** — an open decision work can wait on (`qn-*` in `questions/`, with state `open`,
  `answered`, or `dropped`).

To show how work breaks down, what blocks it, and which changes implement it, knots connect through
`parent` edges (to a local `outcome`), `blocked_by` edges (on `outcome` and `bug`, naming local
knots or `<project>:<id>` across projects), `graduated_into` links (on a `graduated` `idea`),
`changes` links (`valley:<project>/<change-id>` on `outcome`, `bug`, and `question`), and `[[id]]`
wiki-links in prose ([knot/model.go:84-139](../knot/model.go),
[knot/graph.go:376-409](../knot/graph.go)). Local `parent` edges and local `blocked_by` edges are
each checked separately for cycles — so a parent `outcome` may be `blocked_by` one of its own child
knots — while cross-project `blocked_by` references are checked for syntax only
([knot/graph.go:376-470](../knot/graph.go)).

_How git realizes this:_ Project storage lives under `refs/the-valley/*`, and knots live as Markdown
files with canonical YAML frontmatter (`<type-dir>/<id>-<slug>.md`, plus append-only comments at
`comments/<id>/<stamp>-<op>.md`) in a commit tree at `refs/the-valley/knots`
([knot/model.go:46-77](../knot/model.go), [knot/graph.go:45-64](../knot/graph.go)). Because a plain
`git clone` fetches only `refs/heads/*` and `refs/tags/*`, and the host's mirror publisher sends
only `main` and tags (`refs/heads/main` and `refs/tags/*`;
[nix/valley-host.nix:252-265](../nix/valley-host.nix)), cloning from a push mirror (such as GitHub)
does not fetch the host's plan graph or change records; running `valley plan` or `valley status`
against a valley host clone fetches the `refs/the-valley/*` refs each command needs directly from
`origin`.

## Principals and grants

A **principal** is a named actor — a human or an agent — authenticated by an SSH key when pushing
over SSH, or identified by a cryptographic signing key when verifying check attestations. When an
SSH key is tagged with a principal name in `authorized_keys`, the host passes that name to
`valleyhook`; an untagged key in `services.valley.authorizedKeys` can still push to open topic
branches and attestations as an anonymous pusher, but cannot write change records, restack reports,
integration requests, or knots ([nix/valley-host.nix:506-507](../nix/valley-host.nix),
[valleyhook/policy.go:151-173](../valleyhook/policy.go),
[valleyhook/record.go:160-163](../valleyhook/record.go)). A **grant** is a capability assigned to a
principal at a named **boundary**. Two boundary grants govern `valleyhook` on `refs/the-valley/*`:

- **`request`** — allows a principal to write or delete integration requests
  (`refs/the-valley/integration-requests/<target>/<change>`), update any change record, and delete
  change records ([valleyhook/policy.go:151-158](../valleyhook/policy.go),
  [valleyhook/record.go:194-198](../valleyhook/record.go), [361-370](../valleyhook/record.go)). Who
  holds `request` is a **deployment choice** (for example, in the reference deployment only the
  human operator holds `request`).
- **`restack`** — allows a principal to push restack reports
  (`refs/the-valley/restack/<target>/<id>`) and add a single restacked version to another owner's
  `ready` change while keeping its active branch and title
  ([valleyhook/policy.go:165-169](../valleyhook/policy.go),
  [valleyhook/record.go:268-272](../valleyhook/record.go)). (The restack report schema, hook
  validation, CLI display, and the read-only planner `valley-restacker once --report-only` are
  implemented; the restacker's write pass and host service are **not built yet**.)

_How Nix and CUE realize this:_ To manage principals and grants as reviewed changes in the instance
repository rather than only through NixOS rebuilds, a host can enable
`services.valley.identity.enable` to compile `identity/*.cue` from the instance repository on a
timer into `/var/lib/valley-identity/`, or declare keys, grants, and trusted signers statically in
NixOS (`services.valley.authorizedKeys`, `services.valley.grants`,
`services.valley.integrator.knownSignersFile`; compiled `authorized_keys` and `grants` supplement
any static `authorizedKeys` and `grants`, while compiled `known-signers` replaces
`services.valley.integrator.knownSignersFile`, which must be unset when both `identity.enable` and
`integrator.enable` are true; [nix/valley-host.nix:624-635](../nix/valley-host.nix),
[988-1010](../nix/valley-host.nix), [1260-1280](../nix/valley-host.nix),
[1416-1420](../nix/valley-host.nix), [1449-1455](../nix/valley-host.nix)). In `identity/*.cue`,
boundaries use four kinds ([schema/identity.cue:143](../schema/identity.cue)): `git-push` authorizes
SSH pushes (compiled into `authorized_keys`), `request` and `restack` authorize integration requests
and restack updates (compiled into `grants`), and `registry` is checked by the identity compiler,
which rejects compiling a registry with no unexpired principal holding a `registry` grant
(`governanceOrphaned`; [identity/render.go:100-143](../identity/render.go)) — neither `valleyhook`
nor the integrator enforces `registry` grants on pushes or landings.

## Glossary

| Term                         | Plain definition                                                                                                                                                                                                                                                                                                                                                                                    | Full explanation                                                                                                                                                                             |
| :--------------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Active version**           | The current version (`vN`) of a change up for review or revision (`versions[active-1]` in the change record, which is always the highest-numbered version `versions[len-1]`).                                                                                                                                                                                                                       | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |
| **Approved version**         | In `valley review`, the greatest prior version of a revised change whose exact head commit carries an unambiguous integration request or `land`, `stale`, or `reject` outcome record on `origin` (never falling back to an older version if the greatest version with evidence is ambiguous).                                                                                                       | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Review against the approved version](./architecture.md#review-against-the-approved-version)                       |
| **Attestation**              | A signed statement recording that a check passed on a specific content tree (`valley-tree-v1`), formatted as `the-valley/attestation/v1` and stored under `refs/the-valley/attestations/<tree-digest>/<key-hash>`.                                                                                                                                                                                  | [Checks and evidence](#checks-and-evidence), [Evidence: attest and notes](./architecture.md#evidence-attest-and-notes)                                                                       |
| **Base**                     | The `main` commit a change version was branched or rebased from (`git merge-base origin/main <head>`).                                                                                                                                                                                                                                                                                              | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Review against the approved version](./architecture.md#review-against-the-approved-version)                       |
| **Boundary**                 | A named scope in the identity registry (`kind: "git-push"`, `"request"`, `"restack"`, or `"registry"`) where a principal holds a grant.                                                                                                                                                                                                                                                             | [Principals and grants](#principals-and-grants), [Identity](./architecture.md#identity)                                                                                                      |
| **Change**                   | A tracked unit of proposed work with a stable 32-letter Change ID (`k`–`z`), moving through `draft`, `ready`, or `withdrawn` states across numbered versions (`v1`, `v2`, …) until submitted to `main`.                                                                                                                                                                                             | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |
| **Change ID**                | A 32-character identifier drawn from letters `k` through `z` (`[k-z]{32}`) that uniquely names a change across revisions and rebases.                                                                                                                                                                                                                                                               | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |
| **Change record**            | The record tracking a change's target branch (`main`), owner, state, and version history (`valley-change-record/v1`, stored as `record.json` in a commit at `refs/the-valley/changes/main/<id>`).                                                                                                                                                                                                   | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |
| **Check**                    | An executable verification step that a change requires based on the path classes its diff touches (including `unclassified` for paths matching no declared class).                                                                                                                                                                                                                                  | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                              |
| **Draft**                    | A change state meaning work is in progress or paused (`valley unready`) and not currently up for integration.                                                                                                                                                                                                                                                                                       | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |
| **Effectful check**          | A check (`runner: "command"`) that may depend on external state; its evidence transfers across `main` movement (`untouched`) only if the net diff on `main` (`base..tip`) touches none of the path classes that required that check for this change and the observation age is strictly within the policy's `validity` window.                                                                      | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                              |
| **Evidence**                 | Signed check attestations stored in project storage that prove a change's required checks passed.                                                                                                                                                                                                                                                                                                   | [Checks and evidence](#checks-and-evidence), [Evidence: attest and notes](./architecture.md#evidence-attest-and-notes)                                                                       |
| **Grant**                    | A capability (`request` or `restack` in `valleyhook`, `git-push` for SSH key authorization, or `registry` for the identity compiler's orphan check) assigned to a principal statically in NixOS or compiled from the identity registry, or a project-level ref grant (`grants.<name>`) in `valley.cue`.                                                                                             | [Principals and grants](#principals-and-grants), [Identity](./architecture.md#identity)                                                                                                      |
| **Handle**                   | The shortest prefix of a Change ID (at least 4 letters) unique among changes currently recorded on `origin`; it can lengthen if a colliding Change ID is recorded later.                                                                                                                                                                                                                            | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |
| **Identity registry**        | The declarative source of principals, keys, and boundary grants (`identity/*.cue` in the instance repository) that a host can compile (`services.valley.identity.enable`) into SSH `authorized_keys`, `known-signers`, and `grants`.                                                                                                                                                                | [Principals and grants](#principals-and-grants), [Identity](./architecture.md#identity)                                                                                                      |
| **Instance**                 | A deployment of one or more projects governed by a shared floor verification policy and identity configuration (in the reference deployment, the `qinling` repository).                                                                                                                                                                                                                             | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                              |
| **Instance floor**           | The mandatory baseline verification policy (`policy/instance/`) that the integrator and CLI check tools require every project in an instance to satisfy; project policy (`policy/project/`) is optional and can only narrow (strengthen) the floor.                                                                                                                                                 | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                              |
| **Integration request**      | The record written by a `request` grant holder (`valley approve`) authorizing the integrator to verify and land one exact commit onto `<target>` (such as `main`), stored as a commit ref at `refs/the-valley/integration-requests/<target>/<change>`.                                                                                                                                              | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [The integrator](./architecture.md#the-integrator)                                                                 |
| **Integrator**               | The optional host service (`services.valley.integrator.enable`) that polls integration requests (default every `15s`), verifies policy and check evidence, lands passing ready changes onto `main`, writes transfer statements, and records outcomes.                                                                                                                                               | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [The integrator](./architecture.md#the-integrator)                                                          |
| **Knot**                     | A single item in a project's plan graph — typed as `outcome` (`oc-*`), `bug` (`bd-*`), `idea` (`ida-*`), or `question` (`qn-*`) — stored as `<type-dir>/<id>-<slug>.md` under `refs/the-valley/knots`.                                                                                                                                                                                              | [Records, project storage, the plan graph, and knots](#records-project-storage-the-plan-graph-and-knots), [The plan graph](./architecture.md#the-plan-graph)                                 |
| **Landed / Landed-recorded** | The two states of a submitted change version: `landed` when its commit is on `main`'s first-parent history with no merge commit above it; `landed-recorded` when a single-parent commit on `main` carries a verified transfer statement and `land` outcome record for that version's head and merges to the same tree.                                                                              | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [The integrator](./architecture.md#the-integrator)                                                                 |
| **Outcome record**           | The integrator's recorded result for an integration request (`land`, `stale`, `reject`, `refused-not-ready`, `refused-merge`, `voided-withdrawn`, or `failed`), stored as a single-line blob at `refs/the-valley/integration-outcomes/<target>/<change>`.                                                                                                                                           | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [The integrator](./architecture.md#the-integrator)                                                          |
| **Path class**               | A named group of file glob patterns in verification policy mapped to the checks required when a change modifies matching files (with unmatched paths falling into `unclassified`).                                                                                                                                                                                                                  | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                              |
| **Plan graph**               | The directed graph of knots stored at `refs/the-valley/knots` and viewed with `valley plan`; local `parent` and local `blocked_by` edges are each checked separately for cycles.                                                                                                                                                                                                                    | [Records, project storage, the plan graph, and knots](#records-project-storage-the-plan-graph-and-knots), [The plan graph](./architecture.md#the-plan-graph)                                 |
| **Principal**                | A named human or agent identity associated with SSH public keys or attestation signing keys.                                                                                                                                                                                                                                                                                                        | [Principals and grants](#principals-and-grants), [Identity](./architecture.md#identity)                                                                                                      |
| **Project storage**          | Operational records kept outside `main` on a valley host — change records, attestations, integration requests, integration outcomes, restack reports, and knots under `refs/the-valley/*` (not sent by the host's mirror publisher or fetched by a plain `git clone`).                                                                                                                              | [Records, project storage, the plan graph, and knots](#records-project-storage-the-plan-graph-and-knots), [The source and project storage](./architecture.md#the-source-and-project-storage) |
| **Pure check**               | A Nix flake check (`runner: "nix"`, built via `nix build`) whose evidence never expires with time and transfers across `main` movement if the tree is `unmoved` or if both its input-closure digest (`valley-inputs-v1`) and derivation digest match (`closure`).                                                                                                                                   | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                              |
| **Push hold**                | A host-wide pause on incoming SSH pushes across all projects, active while the sentinel file `${dataDir}/.valley-hold` (default `/srv/git/.valley-hold`) is present.                                                                                                                                                                                                                                | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [valley-init and the push hold](./architecture.md#valley-init-and-the-push-hold)                            |
| **Ready**                    | The change state set by `valley ready`, declaring that the active version is complete and awaiting operator review and approval.                                                                                                                                                                                                                                                                    | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |
| **Restack report**           | A record of an automated restack outcome for a change, displayed by `valley status` and `valley pending` and validated by `valleyhook` (`valley-restack-report/v1`, stored as `report.json` in a commit at `refs/the-valley/restack/<target>/<id>`; the read-only planner `valley-restacker once --report-only` is implemented, while the write pass and host restacker service are not built yet). | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [Restack reports](./architecture.md#restack-reports)                                                        |
| **Submitted**                | A change version that has been integrated into `main` (either `landed` or `landed-recorded`).                                                                                                                                                                                                                                                                                                       | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [The integrator](./architecture.md#the-integrator)                                                                 |
| **Transfer statement**       | A signed attestation (`the-valley/integration/transfer/v1`) signed by the integrator before `update-ref` and stored afterward on a completed landing (both fast-forward and merged), binding the landed tree and commit to the attested base tree, policy digest, and per-check verdicts.                                                                                                           | [Checks and evidence](#checks-and-evidence), [The integrator](./architecture.md#the-integrator)                                                                                              |
| **Uncertain**                | The landing state reported when neither `landed` nor `landed-recorded` holds and either the outcome record says `failed`, or it says `land` while the request ref was consumed (or signer verification environment variables are unset for a merged landing).                                                                                                                                       | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Listing changes](./architecture.md#listing-changes)                                                               |
| **`valley-tree-v1`**         | A content digest of a source tree independent of git commit metadata (remaining identical across commit amends or rebases only when the resulting tree's contents, paths, and modes are unchanged), computed with SHA-256 over a canonical manifest of files, executable bits, and symlinks (`<mode> <sha256> <path-len> <path>`).                                                                  | [Checks and evidence](#checks-and-evidence), [attest/digest.go:5-18](../attest/digest.go)                                                                                                    |
| **`valleyhook`**             | The Go pre-receive hook binary that validates every SSH push to a valley host against ref protection, grants, and record/knot schemas.                                                                                                                                                                                                                                                              | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [What a push may write](./architecture.md#what-a-push-may-write)                                            |
| **Withdrawn**                | A change state set by `valley withdraw`, taking a change off the review queue; the integrator deletes pending requests for a withdrawn change on its next pass if the change is still withdrawn.                                                                                                                                                                                                    | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                 |

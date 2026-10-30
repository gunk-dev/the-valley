# Concepts and glossary

the-valley separates software work into clear, composable pieces: changes with stable identity
across revisions, check evidence signed over content trees, a pre-receive hook and integrator on the
host, project storage outside `main` for records and the plan graph, and explicit principals and
grants. Every section below states what a concept means first, then how git, Nix, and CUE implement
it, and distinguishes what the code always enforces from what an instance configures and what is not
built.

## A change: ready, approve, submitted

A **change** is one unit of proposed work with a stable identity across revisions. An author creates
a change, revises it until it is complete, and marks it **ready** (`valley ready`) to put a specific
version up for review. Each revision is a numbered version (`v1`, `v2`, …) that records the proposed
source commit (`head`), title, branch name, and uploader; the latest version is the **active
version**, and its **base** is the `main` commit that version was branched or rebased from
(`git merge-base origin/main <head>`), which review and integration use to measure what the change
modifies and whether `main` has moved since. If the author needs to revise further before the change
lands, they can pause it back to `draft` (`valley unready`), or mark it `withdrawn`
(`valley withdraw`) to abandon it; readying a `draft` or `withdrawn` change again at its active
version's head re-readies that same version number without creating a new version.

An operator lists ready changes with `valley pending` and inspects one with
`valley review <change>`, which offers an interactive prompt. So a reviewer does not have to re-read
an entire change from scratch after a rebase or revision, `valley review` compares the active
version against its earlier **approved version** when one can be compared: if the file delta is
unchanged (a clean restack), it prints a short summary with any landings between the two bases and
pages nothing; if the version changed files, it pages the comparison (the files whose change
differs, the range-diff between the two versions, and the shown version's diff of those files); and
if there is no approved version or no comparison can be made, it pages the full diff
(`[v]iew full diff` is available at the prompt in every case). The operator approves the change with
`valley approve <change>` (or by selecting `[a]pprove` in `valley review`), which reuses eligible
evidence already on `origin` for the commit's content tree, runs and signs the remaining required
checks, and writes an integration request binding that one exact commit.

Once the integrator verifies that commit's required check evidence and integrates it into `main`,
the change version is **submitted**: `landed` when its commit is on `main`'s first-parent history
with no merge commit above it, or `landed-recorded` when a single-parent commit on `main` carries a
verified transfer statement, the integrator's records bind the version's head to that commit, and
the head merges to the same tree. The record is the **landing record** for the head, or for a
landing made before landing records existed, a `land` outcome record. The integrator lands every
change as one single-parent commit of its own, so every change it lands reads `landed-recorded`. To
keep operators and tooling from acting on a guess when a landing's evidence is incomplete or
unverified, the CLI reports the version as **uncertain** whenever neither `landed` nor
`landed-recorded` holds and the outcome record says `failed`, or says `land` after the request ref
was consumed, or a landing record names the head (including when signer verification environment
variables are unset for a recorded landing).

_How git and Nix realize this:_ To give every change a stable identity across rebases and a short
name for everyday CLI commands, each change has a 32-letter **Change ID** drawn from `k`–`z` and a
short CLI **handle** — the shortest prefix (at least 4 letters) unique among changes currently
recorded on `origin`, which can lengthen if a colliding Change ID is recorded later. Without
`--stamp` or `--head`, `valley ready [<branch>]` records the remote branch tip (`origin/<branch>`),
which must already be pushed to `origin`. For a change with no record yet whose local head commit is
not on `origin`, `valley ready --stamp` amends the local commit with a `Change-Id: <id>` trailer and
pushes the branch before writing `v1`. Across later revisions or rebases, `valley ready` keeps the
same Change ID if the commit carries a `change-id` commit header (written by `jj`) or `Change-Id`
trailer, or if you pass `--change <id>`. Change records live as `record.json` commits at
`refs/the-valley/changes/main/<id>` (`valleyhook` accepts change records only for `target: "main"`),
and approvals write an **integration request** ref at
`refs/the-valley/integration-requests/<target>/c-<id>` (or `<branch>` with `/` replaced by `-` for
an unrecorded branch). On the client, this flake's `valley` package (`nix shell .#valley` or
`packages.<system>.valley`) wraps `bin/valley` with `valley-go`, `attest`, `cue`, `git`, `less` (for
paging diffs and taking review notes), and `natscli`, while running `bin/valley` directly from a
checkout uses those tools plus `ssh` and `coreutils` from `PATH`, and `nix` with flakes enabled
builds pure checks ([`nix/packages.nix`](../nix/packages.nix)).

Follow [Authoring a change](./guide.md#authoring-a-change) to put these steps into practice,
[Reviewing and approving changes](./guide.md#reviewing-and-approving-changes) to review one, or the
[CLI reference](./reference.md#cli-reference) for exact flags and exit codes.

## Checks and evidence

Every change **requires** a set of checks determined by the files it modifies. So an instance can
enforce baseline verification across every hosted project while letting individual repositories add
stricter rules, two CUE policy layers govern verification in `valley checks`, `valley approve`, and
the integrator: an **instance floor** (`policy/instance/`) required of every project the integrator
judges on the host, and an optional **project policy** (`policy/project/`) that can only add checks
or tighten rules, never weaken the floor. If a project has no `policy/project/` directory, the
instance floor governs verification alone. To map changed files to the checks they require, policy
groups file globs into **path classes**, and any changed path that matches no declared class falls
under `unclassified` and requires the checks listed in `unclassified` (which may be empty). To
express how far an approval may carry across a rebase, each path class can also declare a `carry`
mode (`same-paths`, `same-class`, or `exact`); `valley checks` reports the composed carry mode,
while acting on `carry` to carry approval automatically across restacks is **not built**
([`schema/verification.cue`](../schema/verification.cue)).

Checks come in two kinds: **pure checks**, which build Nix flake check targets via `nix build`
(`runner: "nix"`, `<root>#checks.<system>.<attr>`, with sandboxing determined by the machine's Nix
configuration), and **effectful checks**, which run commands (`runner: "command"`) that may touch
external state and whose evidence is accepted only when its age is within a policy-defined
`validity` window. To prove that a check passed on a specific source tree without re-running the
check on the server, `valley approve` invokes `attest` to run each required check and produce
**evidence**: a cryptographic **attestation** signed with the supplied SSH key (`--key` or
`VALLEY_ATTEST_KEY`) over the content digest of the source tree (`valley-tree-v1`). On push,
`valleyhook` verifies the note against the host's trusted signers and requires a verified signature
matching the key hash in the ref name. A host with no trusted signers accepts no attestations. The
integrator also checks the signer against its trusted `known-signers` file when verifying evidence.
When `main` has moved ahead of a change's base, the integrator never re-executes checks itself;
instead, it reuses existing evidence if the check transfers: a pure check transfers if the landed
tree is identical (`unmoved`) or if recomputing the check's Nix evaluation on the landed tree yields
both the same input-closure digest (`valley-inputs-v1`) and the same Nix derivation digest
(`closure`); an effectful check transfers (`untouched`) only if the net diff on `main` between the
attested base and current `main` tip (`base..tip`) touches none of the path classes that required
that check for this change, the policy sets a non-zero `validity` window, and the attestation age is
strictly within that window.

_How git realizes this:_ Signed notes live under
`refs/the-valley/attestations/<tree-digest>/<key-hash>`. On every landing, the integrator signs a
**transfer statement** (`the-valley/integration/transfer/v1`, recording the landed tree, the
attested base tree, the policy digest, and the per-check verdicts) and countersigns the
contributor's notes. It then moves `main`, stores that signed evidence, writes the landing and
outcome records, and deletes the request in one ref transaction, so all of them change or none does.
Readers verify from the transfer statement that `main`'s new tree satisfied policy.

Use [Client setup](./guide.md#client-setup) to configure signing,
[Recovering from failure](./guide.md#recovering-from-failure) when a check or landing fails, or
[Wire formats and JSON schemas](./reference.md#wire-formats-and-json-schemas) for attestation and
digest formats.

## The host, the hook, and the integrator

A **valley host** is a server (configured on NixOS via `services.valley`) that stores repositories
and enforces project rules. Like a valley, the host is the low place where streams of work meet. Two
components guard it:

- **The pre-receive hook (`valleyhook`)** inspects every SSH push before git accepts it
  ([`valleyhook/policy.go`](../valleyhook/policy.go)). To let the host pause all incoming SSH pushes
  during initialization or maintenance, the git user's login shell enforces a **push hold**. It
  refuses every push across all projects whenever the sentinel file `${dataDir}/.valley-hold`
  (default `/srv/git/.valley-hold`) is present. Whether a branch such as `refs/heads/main` is
  protected is a **deployment choice** in `valley.cue`: if a project declares a `protection` block,
  `protection.refs` defaults to `["refs/heads/main"]` and only principals listed in
  `protection.writers` may push to those refs over SSH; if `protection.refs` matches
  `refs/heads/main` and `protection.writers` is empty, no SSH push may update `main`, whereas a
  project with no `protection` block leaves all branches open. Unprotected topic branches
  (`refs/heads/*`) are open to any authorized key; outside `refs/heads/*`, the four admitted
  `refs/the-valley/*` namespaces, and the exact `refs/the-valley/knots` ref, refs (including
  `refs/tags/*`) require a project-level ref grant (`grants.<name>: {refs, writers}` in
  `valley.cue`) unless categorically forbidden. On `refs/the-valley/*`, the hook enforces schema and
  permission rules: any named principal may create a change record (becoming its `owner`); only the
  change's `owner` or an `approve` grant holder may update a change record, except that a `restack`
  grant holder may add a single version to a `ready` change keeping its active branch and title;
  only an `approve` holder may delete a change record or write/delete integration requests; and
  nobody may push to `integration-outcomes`, `integration-landings` or `projects`.
- **The integrator** verifies approved changes and lands them onto `main`; it runs as an optional
  host service (`services.valley.integrator.enable`) that polls at a configurable interval
  (`services.valley.integrator.interval`, default `15s`). On each pass, it deletes pending requests
  for changes that are currently `withdrawn` and records `voided-withdrawn` (if a withdrawn change
  is readied again before that pass runs, the old request remains standing and can land; to revoke
  an approval immediately, an `approve` holder must delete the request ref). For each remaining
  request, the integrator refuses if the request's commits include a merge (`refused-merge`) or a
  version head of a `draft` or `withdrawn` change, or if a `c-<id>` request's change is not `ready`
  or does not list the requested head among its versions (`refused-not-ready`). Otherwise it
  evaluates policy and signed check evidence (and Nix closure equivalence when `main` has moved) and
  lands the change on `main` via a local ref transaction (which runs on the host and bypasses
  `pre-receive`). Every change lands as a **new single-parent commit** the integrator writes on top
  of `main`, with the approved commit's tree when `main` has not moved since the change's base, and
  otherwise with the tree `git merge-tree` builds. It re-reads the change records right before the
  transaction, though an `unready` or `withdraw` push arriving in the narrow instant between that
  final read and the transaction will not stop the landing. It writes an **outcome record** (`land`,
  `stale`, `reject`, `refused-not-ready`, `refused-merge`, `voided-withdrawn`, or `failed`) at
  `refs/the-valley/integration-outcomes/<target>/<change>`, and for each landing a **landing
  record** at `refs/the-valley/integration-landings/<target>/<head>`.

See [Project storage ref layout and bounds](./reference.md#project-storage-ref-layout-and-bounds)
for every ref namespace, write rule, and size limit.

## Records, project storage, the plan graph, and knots

A repository has two parts: **the source** (`main` and release tags) and **project storage** (the
operational records around the source: change records, check attestations, integration requests,
integration outcomes, restack reports, and the plan graph). Keeping project storage outside `main`
means updating a plan item, recording a check attestation, or marking a change ready never dirties
`main` or triggers source-level rebuilds.

Within project storage, the **plan graph** tracks what the project intends to do and why. Each item
in the graph is a **knot** — named after the knotted cords used to keep records before writing. A
knot has one of four types, with `owner` optional on every type
([`knot/model.go`](../knot/model.go)):

- **`outcome`** — a target result or piece of work to complete (`oc-*` in `outcomes/`, with state
  `open`, `in-progress`, `done`, or `abandoned`); root `outcome` knots (those with no `parent`) are
  displayed as milestones in `valley plan`.
- **`bug`** — a defect to fix (`bd-*` in `bugs/`, with state `open` or `closed`).
- **`idea`** — an exploration or proposal (`ida-*` in `ideas/`, with state `raw`, `exploring`,
  `adopted`, `graduated`, `superseded`, or `discarded`); when `graduated`, its `graduated_into`
  field names the local `outcome` or `question` it became.
- **`question`** — an open decision work can wait on (`qn-*` in `questions/`, with state `open`,
  `answered`, or `dropped`).

To show how work breaks down, what blocks it, and which changes implement it, knots connect through
`parent` edges (to a local `outcome`), `blocked_by` edges (on `outcome` and `bug`, naming local
knots or `<project>:<id>` across projects), `graduated_into` links (on a `graduated` `idea`),
`changes` links (`valley:<project>/<change-id>` on `outcome`, `bug`, and `question`), and `[[id]]`
wiki-links in prose. Local `parent` edges and local `blocked_by` edges are each checked separately
for cycles — so a parent `outcome` may be `blocked_by` one of its own child knots — while
cross-project `blocked_by` references are checked for syntax only.

_How git realizes this:_ Project storage lives under `refs/the-valley/*`, and knots live as Markdown
files with canonical YAML frontmatter (`<type-dir>/<id>-<slug>.md`, plus comment files at
`comments/<id>/<stamp>-<op>.md`) in a commit tree at `refs/the-valley/knots`. Knot and comment files
are never deleted, and each write adds a commit to `refs/the-valley/knots`, so the ref's history
keeps every earlier text of a knot and its comments. Because a plain `git clone` fetches only
`refs/heads/*` and `refs/tags/*`, and the host's mirror publisher sends only `main` and tags
(`refs/heads/main` and `refs/tags/*`), cloning from a push mirror (such as GitHub) does not fetch
the host's plan graph or change records; running `valley plan` or `valley status` against a valley
host clone fetches the `refs/the-valley/*` refs each command needs directly from `origin`.

Read [Planning work with knots](./guide.md#planning-work-with-knots) to create and update the plan,
or [Plan graph and knots](./reference.md#plan-graph-and-knots-refsthe-valleyknots) for the knot file
and frontmatter contract.

## Principals and grants

A **principal** is a named actor — a human or an agent — authenticated by an SSH key when pushing
over SSH, or identified by a cryptographic signing key when verifying check attestations. When an
SSH key is tagged with a principal name in `authorized_keys`, the host passes that name to
`valleyhook`; an untagged key in `services.valley.authorizedKeys` can still push to open topic
branches and attestations as an anonymous pusher, but cannot write change records, restack reports,
integration requests, or knots. A **grant** is a capability assigned to a principal at a named
**boundary**. Two boundary grants govern `valleyhook` on `refs/the-valley/*`:

- **`approve`** (also accepted as `request`) — allows a principal to write or delete integration
  requests (`refs/the-valley/integration-requests/<target>/<change>`), update any change record, and
  delete change records. The two names are one grant. A principal holding either holds it, and a
  principal holding both holds it once. Who holds `approve` is a **deployment choice** (for example,
  a deployment may give it to the human operator alone).
- **`restack`** — allows a principal to push restack reports
  (`refs/the-valley/restack/<target>/<id>`) and add a single restacked version to another owner's
  `ready` change while keeping its active branch and title. The restacker (`valley-restacker`) needs
  this grant, and its principal holds it only when the host assigns it explicitly: the host's
  `grants.restack` list is empty by default. The restacker runs on a host under
  `services.valley.restacker` (`implemented`; enabling it, enrolling its principal, and granting it
  `restack` are a **deployment choice**).

A **read grant** is the instance's consent for a principal to read a project at an edge, the web
server a valley host publishes to. Evaluating read access takes two consents (`implemented`): the
project's own `policy/read` names the principal as a reader, and the instance's registry grants the
principal read access to that project, or to all projects, at the edge. **`anyone`** is the reader
who presents no credential. It has its own entry in the registry with only read grants and no SSH
key, whereas an untagged SSH key in `authorizedKeys` pushes as an anonymous pusher. A project's
**effective readers** at an edge are the principals that both consents authorize there, including
`anyone`, and a project is **public** at an edge when both consents authorize `anyone` to read it
there. The consent evaluator treats the operator the same as any other named principal
(`implemented`). The web publisher exports every project with an effective reader, and its
authorization names each project's effective readers (`implemented`). A reader can sign in at the
edge with a read link, and `valley-web` serves that reader's requests as the link's principal
(`implemented`; see [Read access](./architecture.md#read-access)).

_How Nix and CUE realize this:_ To manage principals and grants as reviewed changes in the instance
repository rather than only through NixOS rebuilds, a host can enable
`services.valley.identity.enable` to compile `identity/*.cue` from the instance repository on a
timer into `/var/lib/valley-identity/`, or declare keys, grants, and trusted signers statically in
NixOS (`services.valley.authorizedKeys`, `services.valley.grants`,
`services.valley.integrator.knownSignersFile`; compiled `authorized_keys` and `grants` supplement
any static `authorizedKeys` and `grants`, while compiled `known-signers` replaces
`services.valley.integrator.knownSignersFile`, which must be unset when both `identity.enable` and
`integrator.enable` are true). In `identity/*.cue`, a boundary has one of five kinds
([`schema/identity.cue`](../schema/identity.cue)): `git-push` authorizes SSH pushes (compiled into
`authorized_keys`), `approve` (also accepted as `request`) and `restack` authorize integration
requests and restack updates (compiled into `grants`), `edge-read` names a web destination for read
grants (read by `valley-web-publish` and `valley-read-coverage`), and `registry` is checked by the
identity compiler, which rejects compiling a registry with no unexpired principal holding a
`registry` grant (`governanceOrphaned`) — neither `valleyhook` nor the integrator enforces
`registry` grants on pushes or landings.

Follow [Running a valley host](./guide.md#running-a-valley-host) to configure a deployment, or
[CUE configuration schemas](./reference.md#cue-configuration-schemas) for the host and identity
schema definitions.

## Glossary

| Term                         | Plain definition                                                                                                                                                                                                                                                                                                                                                                         | Full explanation                                                                                                                                                                            |
| :--------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Active version**           | The current version (`vN`) of a change up for review or revision (`versions[active-1]` in the change record, which is always the highest-numbered version `versions[len-1]`).                                                                                                                                                                                                            | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                |
| **Approved version**         | In `valley review`, the greatest prior version of a revised change whose exact head commit carries an unambiguous integration request or `land`, `stale`, or `reject` outcome record on `origin` (never falling back to an older version if the greatest version with evidence is ambiguous).                                                                                            | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Review against the approved version](./architecture.md#review-against-the-approved-version)                      |
| **Attestation**              | A signed statement recording that a check passed on a specific content tree (`valley-tree-v1`), formatted as `the-valley/attestation/v1` and stored under `refs/the-valley/attestations/<tree-digest>/<key-hash>`.                                                                                                                                                                       | [Checks and evidence](#checks-and-evidence), [Signed notes](./reference.md#signed-notes-and-attestation-statement-the-valleyattestationv1)                                                  |
| **Base**                     | The `main` commit a change version was branched or rebased from (`git merge-base origin/main <head>`).                                                                                                                                                                                                                                                                                   | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Review against the approved version](./architecture.md#review-against-the-approved-version)                      |
| **Boundary**                 | A named scope in the identity registry (`kind: "git-push"`, `"request"`, `"restack"`, or `"registry"`) where a principal holds a grant.                                                                                                                                                                                                                                                  | [Principals and grants](#principals-and-grants), [Identity registry](./reference.md#identity-registry-schemaidentitycue)                                                                    |
| **Change**                   | A tracked unit of proposed work with a stable 32-letter Change ID (`k`–`z`), moving through `draft`, `ready`, or `withdrawn` states across numbered versions (`v1`, `v2`, …) until submitted to `main`.                                                                                                                                                                                  | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                |
| **Change ID**                | A 32-character identifier drawn from letters `k` through `z` (`[k-z]{32}`) that uniquely names a change across revisions and rebases.                                                                                                                                                                                                                                                    | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Naming rules](./reference.md#naming-changes-handles-branches-and-requests)                                       |
| **Change record**            | The record tracking a change's target branch (`main`), owner, state, and version history (`valley-change-record/v1`, stored as `record.json` in a commit at `refs/the-valley/changes/main/<id>`).                                                                                                                                                                                        | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change record](./reference.md#change-record-valley-change-recordv1)                                              |
| **Check**                    | An executable verification step that a change requires based on the path classes its diff touches (including `unclassified` for paths matching no declared class).                                                                                                                                                                                                                       | [Checks and evidence](#checks-and-evidence), [Verification policy](./reference.md#verification-policy-schemaverificationcue)                                                                |
| **Draft**                    | A change state meaning work is in progress or paused (`valley unready`) and not currently up for integration.                                                                                                                                                                                                                                                                            | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                |
| **Effectful check**          | A check (`runner: "command"`) that may depend on external state; its evidence transfers across `main` movement (`untouched`) only if the net diff on `main` (`base..tip`) touches none of the path classes that required that check for this change and the observation age is strictly within the policy's `validity` window.                                                           | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                             |
| **Evidence**                 | Signed check attestations stored in project storage that prove a change's required checks passed.                                                                                                                                                                                                                                                                                        | [Checks and evidence](#checks-and-evidence), [Evidence: attest and notes](./architecture.md#evidence-attest-and-notes)                                                                      |
| **Grant**                    | A capability (`approve`, also accepted as `request`, or `restack` in `valleyhook`, `git-push` for SSH key authorization, or `registry` for the identity compiler's orphan check) assigned to a principal statically in NixOS or compiled from the identity registry, or a project-level ref grant (`grants.<name>`) in `valley.cue`.                                                     | [Principals and grants](#principals-and-grants), [Host configuration](./reference.md#host-configuration-schemavalleycue)                                                                    |
| **Group**                    | A set of projects declared in `valley.cue` under a permanent identifier, with a URL `name` and optional `aliases`. Every web URL for a project in the group starts with `/<group>/<project>/…`, and each group alias redirects to `<group>`. Group members and stewards are `not built`.                                                                                                 | [Groups and web URLs](./architecture.md#groups-and-web-urls), [Host configuration](./reference.md#host-configuration-schemavalleycue)                                                       |
| **Handle**                   | The shortest prefix of a Change ID (at least 4 letters) unique among changes currently recorded on `origin`; it can lengthen if a colliding Change ID is recorded later.                                                                                                                                                                                                                 | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Naming rules](./reference.md#naming-changes-handles-branches-and-requests)                                       |
| **Identity registry**        | The declarative source of principals, keys, and boundary grants (`identity/*.cue` in the instance repository) that a host can compile (`services.valley.identity.enable`) into SSH `authorized_keys`, `known-signers`, and `grants`.                                                                                                                                                     | [Principals and grants](#principals-and-grants), [Identity registry](./reference.md#identity-registry-schemaidentitycue)                                                                    |
| **Instance**                 | A deployment of one or more projects governed by a shared floor verification policy and identity configuration.                                                                                                                                                                                                                                                                          | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                             |
| **Instance floor**           | The mandatory baseline verification policy (`policy/instance/`) that the integrator and CLI check tools require every project in an instance to satisfy; project policy (`policy/project/`) is optional and can only narrow (strengthen) the floor.                                                                                                                                      | [Checks and evidence](#checks-and-evidence), [Verification policy](./reference.md#verification-policy-schemaverificationcue)                                                                |
| **Integration request**      | The record written by an `approve` grant holder (`valley approve`) authorizing the integrator to verify and land one exact commit onto `<target>` (such as `main`), stored as a commit ref at `refs/the-valley/integration-requests/<target>/<change>`.                                                                                                                                  | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Ref layout](./reference.md#project-storage-ref-layout-and-bounds)                                                |
| **Integrator**               | The optional host service (`services.valley.integrator.enable`) that polls integration requests (default every `15s`), verifies policy and check evidence, lands passing ready changes onto `main`, writes transfer statements, and records outcomes.                                                                                                                                    | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [The integrator](./architecture.md#the-integrator)                                                         |
| **Knot**                     | A single item in a project's plan graph — typed as `outcome` (`oc-*`), `bug` (`bd-*`), `idea` (`ida-*`), or `question` (`qn-*`) — stored as `<type-dir>/<id>-<slug>.md` under `refs/the-valley/knots`.                                                                                                                                                                                   | [Records, project storage, the plan graph, and knots](#records-project-storage-the-plan-graph-and-knots), [Plan graph and knots](./reference.md#plan-graph-and-knots-refsthe-valleyknots)   |
| **Landed / Landed-recorded** | The two states of a submitted change version: `landed` when its commit is on `main`'s first-parent history with no merge commit above it; `landed-recorded` when a single-parent commit on `main` carries a verified transfer statement, the landing record (or, for an older landing, the `land` outcome record) binds that version's head to it, and the head merges to the same tree. | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [The integrator](./architecture.md#the-integrator)                                                                |
| **Landing record**           | The integrator's record that one head landed, naming the commit it landed as and the request name it landed under, stored as a commit at `refs/the-valley/integration-landings/<target>/<head>`; a later landing under the same request name leaves it in place.                                                                                                                         | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator)                                                                                                             |
| **Outcome record**           | The integrator's recorded result for an integration request (`land`, `stale`, `reject`, `refused-not-ready`, `refused-merge`, `voided-withdrawn`, or `failed`), stored as a single-line blob at `refs/the-valley/integration-outcomes/<target>/<change>`.                                                                                                                                | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [Outcome record](./reference.md#integrator-outcome-record-refsthe-valleyintegration-outcomestargetrequest) |
| **Path class**               | A named group of file glob patterns in verification policy mapped to the checks required when a change modifies matching files (with unmatched paths falling into `unclassified`).                                                                                                                                                                                                       | [Checks and evidence](#checks-and-evidence), [Verification policy](./reference.md#verification-policy-schemaverificationcue)                                                                |
| **Plan graph**               | The directed graph of knots stored at `refs/the-valley/knots` and viewed with `valley plan`; local `parent` and local `blocked_by` edges are each checked separately for cycles.                                                                                                                                                                                                         | [Records, project storage, the plan graph, and knots](#records-project-storage-the-plan-graph-and-knots), [Plan graph and knots](./reference.md#plan-graph-and-knots-refsthe-valleyknots)   |
| **Principal**                | A named human or agent identity associated with SSH public keys or attestation signing keys.                                                                                                                                                                                                                                                                                             | [Principals and grants](#principals-and-grants), [Identity](./architecture.md#identity)                                                                                                     |
| **Project storage**          | Operational records kept outside `main` on a valley host — change records, attestations, integration requests, integration outcomes and landings, restack reports, and knots under `refs/the-valley/*` (not sent by the host's mirror publisher or fetched by a plain `git clone`).                                                                                                      | [Records, project storage, the plan graph, and knots](#records-project-storage-the-plan-graph-and-knots), [Ref layout](./reference.md#project-storage-ref-layout-and-bounds)                |
| **Pure check**               | A Nix flake check (`runner: "nix"`, built via `nix build`) whose evidence never expires with time and transfers across `main` movement if the tree is `unmoved` or if both its input-closure digest (`valley-inputs-v1`) and derivation digest match (`closure`).                                                                                                                        | [Checks and evidence](#checks-and-evidence), [Policy](./architecture.md#policy)                                                                                                             |
| **Push hold**                | A host-wide pause on incoming SSH pushes across all projects, active while the sentinel file `${dataDir}/.valley-hold` (default `/srv/git/.valley-hold`) is present.                                                                                                                                                                                                                     | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [valley-init and the push hold](./architecture.md#valley-init-and-the-push-hold)                           |
| **Ready**                    | The change state set by `valley ready`, declaring that the active version is complete and awaiting operator review and approval.                                                                                                                                                                                                                                                         | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                |
| **Restack report**           | A record of an automated restack outcome for a change, displayed by `valley status` and `valley pending` and validated by `valleyhook` (`valley-restack-report/v1`, stored as `report.json` in a commit at `refs/the-valley/restack/<target>/<id>`, written by `valley-restacker`).                                                                                                      | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [Restack report](./reference.md#restack-report-valley-restack-reportv1)                                    |
| **Submitted**                | A change version that has been integrated into `main` (either `landed` or `landed-recorded`).                                                                                                                                                                                                                                                                                            | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [The integrator](./architecture.md#the-integrator)                                                                |
| **Transfer statement**       | A signed attestation (`the-valley/integration/transfer/v1`) signed by the integrator before a landing and stored in the landing's ref transaction, one per landed commit, binding the landed tree and commit to the attested base tree, policy digest, and per-check verdicts.                                                                                                           | [Checks and evidence](#checks-and-evidence), [Transfer statement](./reference.md#integrator-transfer-statement-the-valleyintegrationtransferv1)                                             |
| **Uncertain**                | The landing state reported when neither `landed` nor `landed-recorded` holds and either the outcome record says `failed`, or it says `land` while the request ref was consumed, or a landing record names the head (or signer verification environment variables are unset for a recorded landing).                                                                                      | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Listing changes](./architecture.md#listing-changes)                                                              |
| **`valley-tree-v1`**         | A content digest of a source tree independent of git commit metadata (remaining identical across commit amends or rebases only when the resulting tree's contents, paths, and modes are unchanged), computed with SHA-256 over a canonical manifest of files, executable bits, and symlinks (`<mode> <sha256> <path-len> <path>`).                                                       | [Checks and evidence](#checks-and-evidence), [Canonical digest manifests](./reference.md#canonical-digest-manifests-valley-tree-v1-valley-inputs-v1-valley-delta-v1)                        |
| **`valleyhook`**             | The Go pre-receive hook binary that validates every SSH push to a valley host against ref protection, grants, and record/knot schemas.                                                                                                                                                                                                                                                   | [The host, the hook, and the integrator](#the-host-the-hook-and-the-integrator), [Ref layout](./reference.md#project-storage-ref-layout-and-bounds)                                         |
| **Web name**                 | The second segment of a project's web URLs, `/<group>/<web name>/…`. It defaults to the project's identifier unless `valley.cue` sets `name`, and it must be unique within the project's group. When a project's web name changes, its old web names in `formerNames` redirect to the new web name.                                                                                      | [Groups and web URLs](./architecture.md#groups-and-web-urls), [Host configuration](./reference.md#host-configuration-schemavalleycue)                                                       |
| **Withdrawn**                | A change state set by `valley withdraw`, taking a change off the review queue; the integrator deletes pending requests for a withdrawn change on its next pass if the change is still withdrawn.                                                                                                                                                                                         | [A change: ready, approve, submitted](#a-change-ready-approve-submitted), [Change records](./architecture.md#change-records)                                                                |

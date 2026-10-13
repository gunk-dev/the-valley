# User scenarios

These scenarios describe what a user needs from the-valley. They begin with one developer and their
agents, then introduce collaboration and less trusted contributors.

Only S1 has an acceptance checklist so far. Later scenarios gain detailed criteria as work reaches
them. Their ordering describes the intended progression; the [roadmap](./roadmap.md) records
implementation and validation.

## The ladder

| ID  | User experience                                                                              | First application             | Project knowledge needed                                                 |
| --- | -------------------------------------------------------------------------------------------- | ----------------------------- | ------------------------------------------------------------------------ |
| S1  | My repositories are under my control, and I can recover them after a failure.                | The-valley itself             | Issues, outcomes, ideas, and decisions survive alongside code            |
| S2  | I submit a verified change and see it integrated within seconds.                             | Daily solo work               | Existing knowledge remains usable through the integration process        |
| S3  | I dispatch an agent and its authorized work can land without further supervision.            | Klaus                         | Agents read and update project knowledge; work is assigned to an outcome |
| S4  | Builds, deployments, and notifications follow changes, and I can find out why each happened. | Cosmo and reel-life           | The history includes knowledge changes and completed outcomes            |
| S5  | I invite a collaborator and give them bounded, revocable access.                             | The first invited contributor | The same project record is available to authorized collaborators         |
| S6  | I can identify a bad deployment, recover, and retain what was learned.                       | A reel-life regression        | Incidents include attribution, uncertainty, and the response             |
| S7  | Unknown contributors can participate safely.                                                 | Deferred                      | Undetermined                                                             |

## S1 — repositories under my control and recoverable

Daily work still consists of cloning, editing, and pushing. The primary repository lives on
infrastructure the operator controls. GitHub can remain a publication mirror during migration.

Within minutes of integration, a change exists in at least two independent places, one offsite. If
the primary host fails, a full restore from the offsite copy is possible within a day. That recovery
must have been tested. Pending work receives periodic backup, but does not yet have the same
replication guarantee as integrated work.

Issues, ideas, outcomes, and decisions travel with the code. Moving the repository must not leave
behind the knowledge needed to work on it. Agents must continue to contribute during the move, even
while parts of their workflow still require manual help.

The initial hosting and migration choices are recorded in
[roadmap Phase 0](./roadmap.md#phase-0--durable-self-hosting). The
[contributor protocol](./contribute.md) describes the current submission process.

### Acceptance criteria

A checked box records observed behavior. It is not a claim that configuration alone establishes the
result.

- [x] The-valley's canonical origin is classic-laddie; GitHub is a mirror.
- [x] Every integrated change is present in at least two independent locations within minutes,
      verified by checking both. Pending branches receive periodic offsite backup, as specified by
      the
      [durability scope](../.the-valley/decisions/dcr-24d62f7-publication-mirror-not-review-queue.md).
- [x] A full restore from the offsite copy has been performed and verified.
- [ ] A week of real human and agent work on the pilot repository has been completed without
      touching GitHub.
- [x] A Klaus agent change has landed end to end through the initial direct-push workflow.
- [x] A real issue has been opened, worked, and closed as an in-repository node, including an agent
      dispatched against a node instead of a GitHub issue.
- [ ] A migration and restore runbook exists and is repeatable for the next repository.

## The rest of the ladder

### S2 — prompt integration

After completing the required verification, a contributor submits a change and sees it integrated
within seconds. The system uses work already performed during authorship when that evidence still
applies. The contributor does not wait for a separate service to repeat it. The central requirement
is knowing when local verification is trustworthy and remains relevant to the change that will land.

### S3 — unattended agent contributions

The operator dispatches work to an agent. The resulting change meets the same integration
requirements as the operator's own work and can land without someone supervising the remaining
steps. The record identifies the agent run and its delegated authority. Project knowledge provides
both the assignment and the place to record what the work changed or discovered.

### S4 — automatic work with an explainable history

A change lands, a build runs, a deployment follows, and the appropriate notification arrives. These
steps need no manual coordination. When an effect is surprising, its cause can be traced through a
shared history. A Cosmo deployment or reel-life release is an initial example. Changes to project
knowledge and completion of the requested outcome belong in that history too.

### S5 — invited collaboration

A second person can contribute without the operator creating accounts in a hosted platform. The
operator trusts them with a defined scope of work, rather than every capability in the system.
Access is easy to grant, limit, and revoke. This is the first scenario that requires the workflow to
work for a collaborator outside the operator's own agents.

### S6 — incident recovery and memory

A deployment causes a regression. The system helps identify the responsible change, revert it, and
record the incident. The record links the evidence, the suspected cause, the uncertainty in that
attribution, and the recovery. Later contributors can use what was learned.

### S7 — open contribution

Unknown contributors can submit work without receiving authority over the project. This remains
deferred and may never become a supported scenario. It identifies a possible future limit of the
trust model; public discovery and spam resistance remain outside the current requirements.

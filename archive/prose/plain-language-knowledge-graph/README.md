# the-valley

> "Whatever."

The-valley is a project to make software development easier for people and the agents working with
them. It brings together source management, verification, automation, and project knowledge on
infrastructure the operator controls.

The immediate aim is practical: use it for daily work, get changes into use quickly, and move the
projects in `gunk-dev/` and `patflynn/` off GitHub. A usable product interface should make the
system itself easier to understand and improve.

## Why build this

GitHub ties repository hosting to its approach to checks, review, automation, and project
management. That combination adds friction when a project needs a different workflow.

Checks often run in a separate environment with its own configuration and debugging process.
Contributors wait for that environment to repeat work they have already done locally. Agents must
fit their work into interfaces organized around a person managing a pull request. Meanwhile, the
reasoning behind a project is spread across issues, documents, conversations, and people's memory.

The-valley explores whether small, replaceable tools can meet these needs more directly. A
contributor should be able to verify a change where they write it, submit it with evidence, and see
it integrated promptly when the requirements are met. People and agents should have access to the
same project knowledge and the same account of what happened.

## Where this goes

Software development is the first use case for a broader idea: organize work around outcomes that
someone wants, record what each outcome depends on, and use those dependencies to decide what can be
worked on next. The system should help complete the work as well as describe it.

That includes work on the-valley itself. Its requirements, design, implementation, and operational
problems belong in the same record. Experience building useful applications should improve the
tools, and better tools should make the applications easier to build.

The [outcome scheduling proposal](./.the-valley/ideas/ida-eac723e-outcome-dag.md) develops this
direction. It is broader than the current implementation.

## Constraints

The project is open source. Its parts should remain understandable, replaceable, and usable without
buying into an entire platform. Project state must survive the loss of a host or service.

The reference implementation uses Nix. The underlying contracts must remain implementable with other
tools. Shared coordination is acceptable where it is needed, but its purpose and limits must be
explicit.

[Requirements](./design/requirements.md) states these constraints more precisely.

## Status

The-valley hosts its own repository and knowledge graph. The repository includes working tools for
local verification, signed evidence, and integration, along with the host configuration and event
projection they use. Several implementation and validation gaps remain.

The [roadmap](./design/roadmap.md) distinguishes what has been built from what has been validated.
The [open questions](./design/openquestions.md) and [bug nodes](./.the-valley/bugs/) record the
unresolved parts of the design.

## Reading the project

| Document                                           | Purpose                                                           |
| -------------------------------------------------- | ----------------------------------------------------------------- |
| [User scenarios](./design/user-scenarios.md)       | What people need to be able to do                                 |
| [Requirements](./design/requirements.md)           | What must hold for those scenarios to work                        |
| [Architecture](./design/architecture.md)           | The main design choices and their reasons                         |
| [Contributor protocol](./design/contribute.md)     | How to publish a change and request integration                   |
| [Verification](./design/verification.md)           | Checks, statements, signatures, and their limits                  |
| [Integration](./design/integration.md)             | How the integrator evaluates and lands a request                  |
| [Self-transparency](./design/self-transparency.md) | A proposed requirement for recording changes to the system itself |
| [Open questions](./design/openquestions.md)        | Decisions and implementation questions still needing work         |
| [Roadmap](./design/roadmap.md)                     | Incremental delivery and validation                               |
| [Knowledge graph](./.the-valley/README.md)         | Ideas, decisions, bugs, and outcomes, with a reading guide        |

# House rules

These rules apply across every project. Each project's `AGENTS.md` keeps only what is specific to
it.

## Writing

- **Code comments** say what a reader cannot infer from the code: purpose, reasons, constraints, and
  non-obvious consequences. Leave out what the code shows.
- **User-facing docs** (READMEs, guides) tell a clear, simple, concise story of the typical,
  well-trodden user journeys. They show how simple the platform is and how easy it is to use.
  Detailed configuration, options, and corner cases belong in reference docs. Concision matters
  most.
- **Reference docs** hold the complete, precise detail.

All three share these rules:

- one voice, describing the system as it is;
- no dates, change or knot IDs, quotes attributed to people, "previously", or "no longer";
- reasons for a change go in its commit message;
- when behaviour changes, rewrite the affected text as if it had been designed that way;
- prefer merging and deleting to adding.

Guidance for agents gives instructions, not lessons: name the command to run and the outcome to
expect, and leave how the machinery works to the tools and their reference docs. Write "list the
checks your change requires with `valley checks --names origin/main HEAD`", not an account of which
policy layer requires which check.

## Tests

Tests let the author verify that a module behaves correctly, without mirroring its implementation or
chasing coverage. Give each user-visible behaviour one end-to-end path at the outermost layer that
shows it, and inner layers focused tests of their own logic. Tests across component boundaries are
the most valuable, and the code they run requires them. Assert outcomes, not wording, and wait on
events rather than fixed windows. Do not re-prove upstream software; check rendered configuration at
evaluation time rather than booting a machine. A test's run time is part of the change's cost.

## Carrying cost

Code and tests are carried for the project's lifetime. When a change's cost in code, tests, check
time, or new concepts outweighs what it delivers, stop instead of pushing through and report what
the implementation revealed about the problem or platform; that counts as success. Do not add a
recurring job for a problem that happened once; fix it by hand and note how. Keep migrations light,
adding cutover steps only to prevent lockout, data loss, or a bad state.

## Language

Write logic — commands, controllers, deployment, and verification — in Go or declarative Nix, where
behaviour can be tested and errors are explicit. Keep Bash to thin glue that sets up an environment
and runs a program, and add no logic to existing scripts.

## Events, not timers

Wake on changes to the files and refs you read, not on a clock. Reread durable state on wakeup so a
missed event loses no correctness, and retry failures when a relevant input changes rather than
after a fixed delay. Calendar work belongs on a timer; any other poll, interval, or sleep must state
why no event serves.

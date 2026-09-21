# Verification that can travel with a change

The-valley's strongest verification idea is that a check result should remain useful when work moves
between machines, authors, and integration attempts. A developer should not have to repeat an
expensive computation merely because another service wants to see it happen. That idea has strong
foundations in build systems, incremental computation, and database concurrency control. Its
difficult part is establishing what the computation actually means, who can be trusted to report it,
and when its result still applies.

This chapter's research conclusion is favorable to portable evidence and selective reuse. It is
considerably less favorable to treating signed local execution plus eventual auditing as a
substitute for a protected verification boundary in adversarial settings. Those are separate
architectural choices. The first does not require the second.

This is a research assessment of repository commit `5bb3a7736fe49b0cc4914299b142c0c9bafb6870`, with
external sources consulted on 2026-09-20. It is not a change to project policy. **Established
result** means a result or construction in the cited literature; **documented behavior** means a
system's published contract; **inference** means this report's reasoning about the-valley;
**recommendation** means a proposed direction; **hypothesis** means something requiring measurement.
The [annotated source register](./sources-verification.md) records reading depth and limitations.

## 1. What exists, and what remains a claim

The repository contains substantial verification machinery. The
[attestation helper](../../attest/run.go) exports a committed tree, runs selected checks, records
their subjects and results, signs statements, and stores them in Git refs.
[Verification](../../attest/verify.go) checks statement syntax, schema, signatures, and optionally
the subject tree. It does not independently execute the claimed check. The
[integrator](../../integrator/refs.go) computes a candidate landing, derives requirements from its
own policy sources, gathers evidence, and asks the
[verdict library](../../integrator/verdict/verdict.go) whether that evidence transfers. Its
protected-ref update uses compare-and-swap.

The distinction between implementation and aspiration matters. The
[roadmap](../../design/roadmap.md#phase-6--trust-backstop) places witness rebuilding and the
transparency log in a later phase. A signed statement about execution is implemented. The inspected
implementation does not yet provide independent re-verification of those local execution claims. Nor
is a quantitative assurance level established merely by describing a future trust score.

The current design separates two claims. A pure check claims that a fixed computation over fixed
inputs produced a result. An effectful check claims that an environment observed a result at a
particular time. That distinction is valuable. It should be supplemented by several independent
acceptance questions:

| Question                                                      | What can answer it?                                                    | What does not answer it?          |
| ------------------------------------------------------------- | ---------------------------------------------------------------------- | --------------------------------- |
| Are these the bytes the signer signed?                        | Signature verification and unambiguous encoding                        | Whether the signer told the truth |
| Does the evidence describe the required computation?          | Binding to the approved definition, inputs, platform, and policy       | A familiar check name             |
| Does the required computation support the claimed result?     | A trusted execution boundary, independent execution, or suitable proof | A self-reported executable hash   |
| Is that computation an adequate test of the desired property? | Specification, test design, review, and empirical validation           | Reproducibility alone             |
| Is accepting it authorized now?                               | Current authorization and revocation policy                            | An old valid signature alone      |

These are not five competing definitions of trust. They are five obligations that an acceptance
decision can accidentally collapse into one green result.

A successful independent rerun supports the claimed result under the rerun's assumptions. It does
not prove that the signer's particular historical execution occurred. A policy concerned only with
the result may find the rerun sufficient. A policy requiring evidence of who executed what at a
particular time needs a trustworthy contemporaneous observation as well.

The current implementation is a useful base because it already exposes the subject, predicate,
signer, and transfer decision separately. The research priority is to make each obligation explicit
at those existing boundaries, before adding a reputation system whose score might conceal which
obligation remains unsatisfied.

## 2. When equal inputs justify reusing a result

For a deterministic function, equal inputs imply equal outputs. The engineering work is deciding
what counts as the function and all its inputs. Consider a check that compiles a package and runs
its tests. Its inputs include the package source, tests, compiler, libraries, flags, generated data,
and any runtime behavior on which the answer depends. If the check consults a remote feature flag or
the host clock, those observations also matter.

Dolstra's Nix thesis gives the foundational approach: describe builds as functions of explicit
dependencies and identify derivations so their results can be reused and composed. Its discussion of
purity also recognizes that hashing cannot itself prevent undeclared environmental influence. The
lesson is an explicit model with stated assumptions, not a proof that every command launched by Nix
is pure. [V01 — Dolstra, 2006](https://edolstra.github.io/pubs/phd-thesis.pdf)

Three terms need separate meanings. **Hermeticity** restricts what a computation can observe.
**Determinism** says repeated execution under the specified conditions has the same result.
**Reproducibility** says another party can recreate the specified artifacts. The Reproducible Builds
definition deliberately includes source, environment, and instructions, and compares selected
artifacts rather than every incidental log.
[V09 — Reproducible Builds definitions](https://reproducible-builds.org/docs/definition/)

A sandbox can improve hermeticity while leaving scheduling nondeterminism. A deterministic program
can reproducibly calculate the wrong answer. A complete source archive can become impossible to
rebuild when an essential external input disappears. These are different failure modes and should
produce different diagnoses.

The empirical evidence supports Nix's practical value without proving universal purity. Malka,
Zacchiroli, and Zimmermann recreated historical build environments and rebuilt 14,452 of 14,461
previously successful jobs from one old revision. The experiment reused cached dependencies; it did
not rebuild the complete historical dependency closure from source. It also left bit-for-bit
artifact reproducibility outside scope. Its preliminary failure analysis identifies sandbox leakage
and flaky tests. A rebuild-success percentage must not be presented as a byte-reproducibility
percentage.
[V10 — Reproducibility of Build Environments through Space and Time](https://arxiv.org/html/2402.00424v1)

### A concrete limitation of the current digest

The helper's [`drvInputs`](../../attest/check.go) queries direct references of a derivation.
[`inputsDigest`](../../attest/digest.go) hashes their sorted path strings. Separately, `evalClosure`
hashes the derivation file. This is a commitment to Nix's symbolic dependency identity. It is not an
independent observation and hash of every byte actually read during execution.

That distinction does not make the design automatically unsound. A dependency graph can be
represented transitively by hashes that name other hashed objects. However, correctness then depends
on what those names guarantee. Nix distinguishes content-addressed store objects from
input-addressed objects; one must not silently equate a recipe-derived path with a measured
output-content hash.
[V06 — Nix content-addressing documentation](https://nix.dev/manual/nix/2.28/store/store-object/content-address)

**Inference:** the reportable claim should be narrower than “the complete observed input closure has
this digest.” It is “this runner resolved this derivation and these dependency identities.” An
implementation that wants the stronger claim must establish the store's integrity, the addressing
modes it permits, the execution restrictions, and the role of substituted dependencies. The output
digest helps compare outputs. It does not retroactively establish how an untrusted machine produced
them.

The Nix manual documents configurable sandbox paths, exceptions for fixed-output derivation
networking, and signature requirements for importing non-content-addressed paths. Those controls
demonstrate that the daemon configuration and cache trust are part of the execution boundary. They
cannot be inferred from the word `nix` in a statement.
[V04 — Nix configuration reference](https://nix.dev/manual/nix/2.28/command-ref/conf-file)

## 3. The closest intellectual relatives are build systems and selective testing

_Build Systems à la Carte_ separates scheduling from deciding whether existing results remain valid.
It also distinguishes dependencies known before execution from dependencies discovered during
execution, and traces that validate a result from traces that recover a cached result. This
vocabulary is directly useful: the-valley's integrator combines an integration scheduler with a
validator of previously recorded computation. Those components can evolve independently.
[V02 — Mokhov, Mitchell, and Peyton Jones, 2018](https://www.microsoft.com/en-us/research/uploads/prod/2018/03/build-systems.pdf)

The lesson from self-adjusting computation is that incrementality requires dependency information
whose collection is itself sound. Acar and colleagues combine recorded dynamic dependencies with
memoization to update computations after changes. Their results show substantial speedups on
suitable workloads, alongside tracing and storage costs. They do not establish that an arbitrary
shell process's reads can be represented cheaply or completely.
[V03 — Acar et al., 2009](https://www.philinelabs.net/~ktangwon/papers/toplas09.pdf)

Safe regression-test selection asks a closely related question: which old tests must execute again
after a program changes? Rothermel and Harrold prove safety under explicit conditions, where safety
means retaining every previously available test capable of exposing a fault in the modified program.
This does not imply that the available test suite detects every fault. That boundary is especially
useful for interpreting the-valley's claims about semantic conflicts.
[V27 — Rothermel and Harrold, 1997](https://www.cs.purdue.edu/homes/xyzhang/spring07/Papers/p173-rothermel.pdf)

**Inference:** the-valley can aim for two progressively stronger statements:

1. Every reused result equals the result that its approved check would produce on the candidate
   landing.
2. The approved collection of checks is adequate for the project's acceptance requirements.

The first is an evidence-reuse property. The second is a software-assurance property. A perfect
implementation of the first leaves the second open. Documentation saying semantic interference is
“caught by construction” should specify that construction's test coverage and dependency
assumptions.

### The economic tradeoff is the size of the dependency set

Suppose a spelling check reads all of a repository because its derivation takes the repository root
as its source. A change to a Go package now changes the spelling check's input identity, even if the
check examines only Markdown. Reverification is conservative and potentially wasteful. Filtering the
source set to Markdown can improve reuse, but only if configuration, dictionaries, includes, and
file discovery remain accounted for.

Conversely, suppose a test enumerates a directory and reads whichever configuration files exist. A
trace that records only the files opened on the previous run may miss a newly added file. The
directory listing, including the prior absence of a name, was an input too. “Track files read” is
not yet a complete dependency model.

Bazel's documentation gives practical examples of hermeticity failures, including host tools,
timestamps, and writing into the source tree. Its remote cache distinguishes action-result metadata
from content-addressed output storage. This is relevant prior art for both disciplined dependency
declarations and the separate trust required in the mapping from an action to its claimed output.
[V07 — Bazel hermeticity](https://bazel.build/basics/hermeticity),
[V08 — Bazel remote caching](https://bazel.build/remote/caching)

**Recommendation:** keep conservative whole-check invalidation as the baseline. Introduce finer
source sets only with tests that attempt to change omitted inputs. Compare saved execution against
the cost of maintaining those declarations. Precise reuse is valuable when it is demonstrably
cheaper than rerunning the check; it is not an end in itself.

## 4. A signed executable hash is not evidence that the executable ran

The [verification mechanism stack](../../design/verification.md#the-mechanism-stack) overstates
several guarantees. These overstatements matter because they concern the adversarial case that local
verification must eventually address.

Consider this failure trace:

1. An attester has an authorized signing key and control of its own machine.
2. It computes the expected tree and derivation identifiers without executing the check.
3. It writes the identifier of the approved attestation tool into a statement.
4. It invents a passing result and signs the statement directly.

No modified-tool hash must appear. The attacker is choosing the statement's bytes. A verifier learns
that the key endorsed those bytes, not that the named tool produced them. Local root can likewise
suppress local observations. A sandbox controlled by the same adversary is not an independent
observer of that adversary.

This is a protocol-level counterexample, not a claim that the existing operator is malicious. The
current trust model can be entirely appropriate for an operator's own devices and agents. The
inaccurate part is claiming that these mechanisms force a malicious signer to leave detectable
evidence of how it lied.

Hardware-backed remote attestation offers a different trust boundary. The RATS architecture
separates an attester's evidence, a verifier's appraisal, reference values, and the relying party's
decision. A protected measurement can support a claim about an execution environment when its
binding and freshness are established. It still needs an appraisal policy and a trusted measurement
chain. [V23 — RFC 9334](https://www.rfc-editor.org/rfc/rfc9334.html)

**Recommendation:** keep three deployment profiles conceptually distinct: trusted personal
attesters; delegated builders whose signing authority is protected from submitted code; and
independent witnesses that check selected claims. A project may combine them. A numeric score should
never make the first profile appear cryptographically equivalent to the second.

Independent execution also needs an independence definition. Two witnesses using the same
compromised compiler or binary cache can agree on a malicious artifact. Thompson's compiler attack
is the classic illustration of a toolchain reproducing a compromise even when inspected source no
longer contains it.
[V24 — Reflections on Trusting Trust](https://www.cs.cmu.edu/~rdriley/487/papers/Thompson_1984_ReflectionsonTrustingTrust.pdf)

The practical requirement is not unlimited diversity at any cost. It is identifying which common
failures a second witness is intended to exclude: a dishonest author, a corrupted machine, a
poisoned cache, a compromised compiler, or a flawed test. Each requires a different kind of
independence.

## 5. What the supply-chain standards already solve

The supply-chain ecosystem contains several distinct layers. Treating them as interchangeable makes
both adoption and criticism less precise.

| Prior art         | Its useful contribution                                                                       | Its boundary                                                              |
| ----------------- | --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| in-toto           | Authorized steps and continuity between their inputs and outputs                              | Correctness still depends on the authorized process and trust assumptions |
| in-toto Statement | A subject digest and a typed claim about it                                                   | The predicate defines the claim's meaning                                 |
| DSSE              | A signed encoding that binds payload bytes and payload type                                   | An envelope does not evaluate the payload's truth                         |
| SLSA Build track  | Requirements for provenance generation and builder isolation                                  | A level is about meeting requirements, not counting security mechanisms   |
| Sigstore          | Identity-linked signing and transparent signature history                                     | Consumers still decide acceptable identities and claims                   |
| TUF               | Secure distribution of current trusted metadata despite several compromise and replay threats | It does not execute a build or determine whether a test is adequate       |

The in-toto paper is particularly relevant because it treats the connections between steps as
security-critical. A project layout defines authorized functionaries and artifact rules; signed
links record their work. This is close to the-valley's desire to compose evidence from multiple
tools without giving a single forge ownership of the workflow.
[V15 — Torres-Arias et al., 2019](https://www.usenix.org/system/files/sec19-torres-arias.pdf)

The Statement specification already separates subject from predicate type. Pure verification and
effectful observation can therefore be different predicates without abandoning interoperability. The
[current local statement schema](../../schema/attestation.cue) has a similar conceptual separation,
though its serialized protocol is its own.
[V16 — in-toto Statement v1](https://in-toto.io/Statement/v1)

DSSE authenticates the payload's exact serialized bytes together with its type, using an unambiguous
encoding. It does not require all producers to canonicalize an application document into a universal
byte representation before verifying the supplied bytes. The-valley's text format can still have
independent benefits for readability and deterministic production; those benefits should justify its
maintenance cost.
[V18 — DSSE protocol](https://github.com/secure-systems-lab/dsse/blob/master/protocol.md)

**Recommendation:** preserve a precise internal claim model and implement an explicit interoperable
representation at the boundary. Re-encoding a statement changes its signed bytes. An adapter should
either preserve the original signed object or produce a new, clearly attributed statement; it should
not pretend signatures survive arbitrary translation.

### The SLSA comparison needs correction

The claim of being “roughly SLSA Level 3” is not supported by the mechanism stack. SLSA v1.2 Build
L3 requires provenance strongly resistant to forgery by tenants, a trusted control plane, protected
signing material, and build isolation. Its hosted requirement excludes builds on an individual's
workstation. Reproducibility and periodic sampling do not, by themselves, satisfy those
requirements. [V17 — SLSA v1.2 build requirements](https://slsa.dev/spec/v1.2/build-requirements)

This is not a reason to imitate a hosted forge. It is a reason to describe the actual trust
boundary. The-valley could integrate evidence from a conforming builder, and the specification
allows systems involving independent rebuilders as build platforms. Any level claim would still
require a concrete mapping of the entire deployment to the requirements. A per-check attestation is
also a different object from complete provenance for a released artifact.

Sigstore's use of short-lived certificates and transparency improves identity binding and
auditability. Its documentation explicitly assigns monitoring responsibilities to users. It does not
turn a signature into proof of successful computation. Whether to use its public services, privately
operate components, or retain local keys is a deployment decision with operational and trust
consequences. [V19 — Sigstore security model](https://docs.sigstore.dev/about/security/)

## 6. Integration is concurrency control over evidence, with qualifications

Kung and Robinson's optimistic concurrency control separates speculative work from validation and
publication. Transactions proceed on local state; validation checks the conditions required for a
serial order. Their argument assumes individual transactions preserve the relevant integrity
conditions. Mapping this to code integration is fruitful only if the-valley states what its checks
establish and what their dependency sets include.
[V11 — Kung and Robinson, 1981](https://www.eecs.harvard.edu/~htk/publication/1981-tods-kung-robinson.pdf)

The project already has the essential outline. Prepare a candidate against a target snapshot.
Determine which results remain applicable. Publish only if the target still has the expected value.
A lost compare-and-swap race causes reconsideration. This avoids silently landing a candidate
validated against the wrong target.

However, the analogy does not make software meaning transactional. A check may omit an important
behavior. Two changes can preserve every tested property while jointly violating an untested one.
Crystal's empirical work established that collaboration conflicts include build and test failures
beyond overlapping textual edits, and explored speculative analysis for earlier detection. It
supports testing combinations, not the inference that clean textual merges or finite tests establish
semantic compatibility.
[V12 — Brun et al., 2011](https://people.cs.umass.edu/~brun/pubs/pubs/Brun11fse.pdf)

### Reuse needs a computation identity and an acceptance identity

A useful proposed model has two independent questions:

```text
Computation identity:
  approved check definition + resolved inputs + runner semantics + platform

Acceptance decision:
  computation result + applicable policy + authorized signer(s)
  + freshness/revocation state + candidate landing
```

This is a design proposal, not a new wire format. A policy change need not invalidate a genuinely
identical computation. It may change whether that result is sufficient. Conversely, preserving a
check's display name must not conceal a change to its executable definition.

There are three distinct failure traces worth testing against the implementation:

**Candidate-controlled no-op.** Policy requires `unit-tests`; the candidate changes that derivation
to create an output immediately. The attestation truthfully reports success. A witness faithfully
rebuilds the no-op and agrees. The repository already records this exposure in
[bd-eaefe82](../../.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md). Its
suggestion that witness re-derivation would eventually detect a check that computes nothing does not
follow: detecting the wrong check requires comparing against an approved definition or semantic
expectation.

**Same tree, different named computation.** The helper accepts a check name mapped to an attribute.
The evidence collector extracts the name and result but does not retain the attribute in the
verdict's evidence structure. The pure `unmoved` rule accepts an otherwise admissible passing
statement when the subject tree is unchanged, without recomputing the expected derivation. Thus a
same-tree shortcut has a stronger precondition than the implementation visibly establishes: the
statement must name the computation policy required. This is a static code-review finding, not a
reported end-to-end exploit.

**Effectful label substitution.** The verdict retains an effectful observation's result and
timestamp, but not the command or environment identity recorded in its statement. It can therefore
decide freshness without itself comparing those fields to the required command. Again, upstream
guarantees could change the assessment; the inspected verdict path does not establish that binding.
A trustworthy signer can still make a configuration mistake, so this issue is not limited to
malicious keys.

**Recommendation:** establish computation identity before applying any transfer shortcut. Keep it
separate from the policy digest in the transfer record. Resolve instance-mandated definitions from
an independently authorized source. For changes to checks, run both the incumbent acceptance check
and the proposed check where feasible; authorize the transition explicitly when compatibility is
impossible. This permits improvements to tests without letting a proposal silently lower its own
bar.

### Cheap validation is a hypothesis about the runner

`attest inputs` calls `nix eval` and is described as building nothing. Nix's import-from-derivation
feature can pause evaluation to realize a derivation. Its manual explicitly documents disabling this
behavior. Therefore evaluation-only latency needs an enforced restriction, rather than a comment in
the integrator.
[V05 — Nix import from derivation](https://nix.dev/manual/nix/2.28/language/import-from-derivation)

Even with that restriction, evaluation, source materialization, and hashing have costs. A serial
controller can bottleneck while doing those operations before its small atomic ref update. The size
of the atomic operation and the end-to-end service rate are different measurements. The
[current closure collector](../../integrator/evidence.go) runs an `attest inputs` subprocess per
pure check, so repeated exports and evaluation are concrete costs to measure.

### Merge queues are a baseline and a possible complement

Zuul tests speculative combinations in parallel. Later queued changes include earlier ones; a failed
predecessor causes affected work to restart. Its adjustable window limits wasted speculation.
GitHub's merge queue likewise validates temporary combinations incorporating the target and
preceding queued work. These are substantial existing solutions to integration throughput, not
merely serialized CI gates.
[V13 — Zuul project gating](https://zuul-ci.org/docs/zuul/latest/gating.html),
[V14 — GitHub merge queues](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)

**Hypothesis:** a hybrid may improve some mixed workloads. Reuse unchanged per-check evidence first.
Speculatively execute only invalidated checks on likely future landings. Reduce speculation when
failures or conflicts rise. Reserve an integration opportunity for changes that repeatedly lose
races. The hybrid adds scheduling and bookkeeping costs, and failed speculation can waste execution.
It should be compared experimentally; there is no basis yet for claiming one queue policy works best
for every repository.

## 7. Effectful checks are observations with assumptions

A check against a live API may be useful even though it cannot be replayed exactly. Its result means
the observed interaction succeeded under the conditions recorded. A ten-minute acceptance window
means the project accepts an observation up to ten minutes old. It does not mean the service
remained healthy during those ten minutes.

The repository's path-class rule adds a separate assumption: intervening changes outside the class
do not affect the observation's applicability. That assumption can be wrong if a shared
configuration file, service dependency, or credential policy sits outside the class. The time bound
and the dependency scope should be reviewed independently.

The RATS freshness discussion similarly recognizes that state can change immediately after evidence
is produced; freshness is a policy choice about acceptable recentness.
[V23 — RFC 9334, section 10](https://www.rfc-editor.org/rfc/rfc9334.html#section-10) That is a
useful interpretation for ordinary test observations too, without claiming they are hardware
attestations.

The [current verdict](../../integrator/verdict/verdict.go) rejects observations whose calculated age
reaches the validity window. Static inspection shows no rejection of negative age. A future
timestamp is consequently a concrete test case. The larger issue is defining acceptable clock skew,
which clock supplies time, and whether a trusted receipt or challenge is required. A sender's
signature authenticates its stated time; it does not make the clock accurate.

Not every nondeterministic check belongs in the same category as a live API test. Luo and colleagues
studied flaky-test fixes and identified multiple causes of unstable outcomes. A race or test-order
dependence can exist inside an otherwise isolated environment. Marking the wrapper `nix` does not
eliminate that behavior.
[V26 — Luo et al., 2014](https://mir.cs.illinois.edu/marinov/publications/LuoETAL14FlakyTestsAnalysis.pdf)

**Recommendation:** define admissible result types. A deterministic check can have an equality-based
result. A statistical benchmark may need a sample size, distribution summary, and acceptance rule. A
live-service observation needs environment and time context. A flaky deterministic check should
trigger investigation of its purity claim, rather than acquire an arbitrary expiration that conceals
the defect.

## 8. Transparency records claims; revocation changes what may rely on them

A transparency log can make a submitted statement discoverable and make alterations detectable. It
does not determine whether the statement is true. Certificate Transparency also distinguishes
inclusion from consistent views of history; a dishonest log can present conflicting histories unless
other participants compare or witness checkpoints. Monitors must look for the failures that matter.
[V21 — RFC 9162, security considerations](https://www.rfc-editor.org/rfc/rfc9162.html#section-11)

Tessera is a plausible implementation component, not a complete operational policy. Its documented
lifecycle distinguishes sequencing an entry, integrating it into the tree, and publishing a signed
checkpoint. Its publishing machinery supports witness policies. The-valley would still need to
choose which stage permits integration and what happens when publication or witnesses are
unavailable. [V22 — Tessera documentation](https://github.com/transparency-dev/tessera)

There is a related crash boundary in the existing integrator. [`land`](../../integrator/refs.go)
moves the protected ref before storing the transfer evidence. It reports subsequent failures, but a
crash can still leave a landed change without the intended durable record. Composing and signing
before the ref move reduces one failure class; it does not atomically publish evidence alongside the
landing.

**Recommendation:** investigate a Git ref transaction covering the target and evidence refs, or a
durable intent record with recovery. For an external log, specify a two-stage protocol with a
recoverable intermediate state. The experiment is a process kill at every persistence boundary,
followed by a new controller reconstructing what happened. “Every landing has evidence” should be
tested as a storage invariant.

Revocation presents another distinction. An old statement can remain authentic while becoming
unacceptable. A signing key may be revoked; a check definition may be disallowed; a vulnerability
database may advance; or a policy may require a stronger builder. TUF's role separation, version
checks, expiration, and key-update procedures are important prior art for distributing current trust
information and resisting stale metadata.
[V20 — TUF specification](https://theupdateframework.github.io/specification/latest/)

**Inference:** a useful revocation record names the affected authority or statement, the reason, the
effective interval, and the dependent decisions requiring reconsideration. A reverse index from
attestations to transfers, releases, and deployments becomes operationally important. Pure
computation results need not expire merely because time passes, but permission to rely on them can
change.

Revocation also does not undo consequences. An already deployed secret leak cannot be reversed by
withdrawing an attestation. The architecture's tradeoff therefore needs an explicit exposure budget:
which actions may proceed before independent verification, how far their outputs may propagate, and
which actions require verification first.

## 9. What random witness sampling can and cannot promise

Sampling can make repeated dishonesty increasingly likely to be discovered. It cannot guarantee
discovery of a particular unsampled statement. The following calculation is this report's elementary
probability model, not an empirical estimate of the-valley's security.

Suppose each false statement is independently sampled with probability `p`. Suppose a sampled false
statement is always detected by an honest witness, and the attacker cannot choose its behavior after
learning the sample decision. After `m` false statements:

```text
P(no detection) = (1 - p)^m
P(at least one detection) = 1 - (1 - p)^m
```

At `p = 0.01`, one false statement has a 99% chance of escaping this sampling scheme. One hundred
false statements have about a 36.6% chance of all escaping. Reaching a 95% probability of detecting
at least one requires 299 false statements. The expected number of false statements up to and
including first detection is 100; that expectation is not a maximum.

If an audit detects a sampled false statement with probability `q`, independent of selection,
replace `p` by `p*q`. A witness that reruns a candidate-controlled no-op has `q = 0` for the defect
“this check does not test what policy intended.” Increasing sampling does not fix that defect.

These equations also exclude correlated witness failures, compromised samplers, adaptive attacks,
and delayed audit execution. A high historical success score gives no mathematical assurance against
a signer that behaves honestly until one valuable attack. Multiple new identities can undermine
reputation unless identity creation itself has a controlled cost or authorization boundary.

**Recommendation:** select audits after the statement is irrevocably committed, using randomness the
producer cannot predict or steer cheaply. Separate two aims: estimating routine error rates and
deterring targeted abuse. Random sampling serves the first well under suitable assumptions. The
second may require mandatory independent verification for high-impact changes, protected builder
keys, or limits on what unverified outputs can authorize.

A confidence statement also needs a denominator. If no failures appear in `n` independent,
representative audits, a one-sided 95% upper bound for a stable defect rate is `1 - 0.05^(1/n)`,
approximately `3/n` for large `n`. With 300 clean audits, this is about 1%. It says nothing
comparable about an adaptive adversary's next submission. Calling it an attester's universal trust
probability would be misleading.

Finally, sampling without a response mechanism only produces observations. Useful operation requires
a disagreement state, preservation of both runs, diagnosis of nondeterminism, and a rule for pausing
dependent decisions. Automatic punishment on first mismatch risks treating an honest witness that
exposed a flaky check as the attacker.

## 10. A research program that can discriminate between designs

The most useful next work is a small set of falsifiable experiments. Each should compare a concrete
baseline, measure costs, and exercise a failure mode that normal successful builds would miss.

| Experiment                      | Baseline and variation                                                                                | What would change the design decision?                                                                           |
| ------------------------------- | ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Reuse on real history           | Recheck everything; whole-tree cache; current per-check transfer; narrower source sets                | Saved compute and landing latency justify declaration complexity without changed results                         |
| Controlled interference         | Changes to imports, generated files, directory membership, lockfiles, platform, and check definitions | Any false reuse requires repairing the dependency or identity model before optimizing further                    |
| Same-tree evidence substitution | Keep subject and check name; alter attribute, command, environment, or approved definition            | Every mismatch is rejected before the unchanged-tree shortcut                                                    |
| Local forgery                   | An authorized key signs a fabricated passing statement and the approved tool identifier               | Demonstrates exactly which deployment profiles need a separate execution authority                               |
| Genuine witness independence    | Rebuild with a fresh store; shared cache; separate cache; altered environment                         | Distinguishes actual independent execution from repeated trust in the same cached artifact                       |
| Purity stress                   | Vary clock, CPU count, scheduling, locale, kernel, and allowed sandbox settings                       | Determines which checks qualify for deterministic reuse and what platform identity must include                  |
| Evaluation cost                 | Warm/cold caches, import-from-derivation, large flakes, many required checks                          | Establishes whether the integrator must cache evaluation, parallelize preparation, or restrict runner features   |
| Queue contention                | Replay identical arrivals through sequential, speculative, reuse-only, and hybrid queues              | Compare completed landings, wasted computation, tail latency, and starvation rather than only average throughput |
| Evidence durability             | Crash before and after each ref, evidence, memo, and log publication operation                        | Recovery restores a complete account without silently reclassifying an incomplete landing                        |
| Revocation response             | Revoke a signer or definition after several dependent integrations and releases                       | Every affected dependency is found, and consumers converge within a measured interval                            |

**Hypothesis:** dependency breadth and evaluation overhead will decide the value of selective reuse
more often than signature-verification cost. A source filter that inadvertently includes the entire
repository may erase most of the intended advantage. This should be established by historical replay
before designing elaborate attester economics.

**Hypothesis:** checks that use many global fixtures will benefit more from speculation and batching
than from finer evidence transfer. Conversely, isolated documentation and component checks should
transfer frequently. A single aggregate cache-hit rate could hide this split; results should be
reported per check family and by computation cost.

One experiment must address the meaning of “passed.” The oracle literature studies how tests obtain
an expected answer, including specifications, contracts, derived oracles, and metamorphic relations.
It shows why automating execution does not finish automating judgment.
[V25 — Barr et al., 2015](https://discovery.ucl.ac.uk/id/eprint/1471263/1/06963470.pdf)

For the-valley, mutate both code and its candidate-supplied tests. Compare incumbent tests,
candidate tests, external invariants, and independent review. The aim is not to assign one universal
quality score. It is to find changes for which execution evidence looks impeccable while the
intended requirement is no longer being tested. This is especially relevant when the same agent can
write the implementation, test, and account of success.

## 11. The promising solution space

The strongest version of this architecture makes evidence portable while keeping acceptance
contextual. Execution produces a claim with a precise computation identity. Integration checks
whether that computation still applies and whether its evidence is currently acceptable. Release or
deployment can impose stronger requirements than integration without discarding the earlier
evidence.

Several design choices remain legitimate:

- A personal installation can trust local machine keys and use occasional independent checks to
  catch mistakes.
- A collaborative project can accept local results for fast feedback while requiring a protected
  builder for selected acceptance predicates.
- A high-assurance release can require several independent rebuilds, durable transparency evidence,
  and explicit authorization of check-definition changes.
- A low-conflict project can use a simple queue and rely on build-cache reuse, avoiding a more
  sophisticated integrator until measurements justify one.

The strongest objection is that existing build caches and merge queues may already capture most of
the performance benefit. The-valley must demonstrate an additional benefit in portable authority,
partial re-attestation, host independence, or explanatory records. Moving familiar mechanisms into
new formats without improving one of those outcomes would not justify their operational cost.

The corresponding opportunity is substantial. A transfer statement can explain exactly why earlier
evidence still supports a new landing. That is more informative than a historical green badge
detached from its assumptions. The research recommendation is to pursue that explanation as the core
contribution, with conservative identity binding and explicit trust profiles. Reputation, universal
purity claims, and mandatory transparency infrastructure should follow only where their measured
benefit and threat model warrant them.

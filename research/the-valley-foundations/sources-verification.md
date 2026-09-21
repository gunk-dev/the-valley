# Annotated sources: verification and integration

These sources support
[Verification that can travel with a change](./03-verification-and-integration.md). IDs are local to
this report. All external resources were accessed on **2026-09-20**. Publication dates identify
papers; documentation entries are living documents unless a version is specified. “Read” records the
portions examined, not a claim that every cited work was read cover to cover. The annotations
distinguish a source's contribution from this report's assessment of its relevance.

The search followed three connected lines: sound reuse of computation, authentication of claims
about computation, and validation at an integration boundary. Academic papers supplied models and
limits; official documentation supplied operational contracts. No citation count, product marketing
claim, or search-result summary alone was treated as proof of a system's security.

## V01 — Nix's deployment model

[The Purely Functional Software Deployment Model](https://edolstra.github.io/pubs/phd-thesis.pdf) —
Eelco Dolstra, PhD thesis, Utrecht University, 2006.

**Read:** contents and targeted passages on trust, content addressing, and purity, especially
§6.2–6.3 and §7.1.3–7.1.5. **Contribution:** makes functional deployment and dependency identity the
basis for reliable reuse. **Limitation:** a foundational model and implementation account, not proof
of every current Nix build's determinism. **Relevance:** the-valley's equality argument should
retain the purity assumptions that accompany the original model.

## V02 — Build-system decomposition

[Build Systems à la Carte](https://www.microsoft.com/en-us/research/uploads/prod/2018/03/build-systems.pdf)
— Andrey Mokhov, Neil Mitchell, and Simon Peyton Jones, _Proceedings of the ACM on Programming
Languages_, ICFP, 2018.

**Read:** task and build abstractions, dynamic dependencies, and scheduler/rebuilder sections,
including concrete systems. **Contribution:** separates execution order from deciding whether
results need replacement. **Limitation:** its computational model is not an adversarial attestation
protocol. **Relevance:** supplies a clearer decomposition for integration scheduling, evidence
validation, and caching.

## V03 — Dynamic dependency tracking

[An Experimental Analysis of Self-Adjusting Computation](https://www.philinelabs.net/~ktangwon/papers/toplas09.pdf)
— Umut A. Acar, Guy E. Blelloch, Matthias Blume, Robert Harper, and Kanat Tangwongsan, _ACM TOPLAS_
32(1), article 3, 2009.

**Read:** abstract, introduction, and overview of dynamic dependence graphs and change propagation.
**Contribution:** combines dependency tracking and memoization to reuse parts of changing
computations. **Limitation:** selected computations and an instrumented model; no arbitrary-shell
completeness guarantee. **Relevance:** exposes the tradeoff between dependency precision,
instrumentation cost, and reuse.

## V04 — Nix's actual sandbox and cache controls

[Nix 2.28.8 Reference Manual: nix.conf](https://nix.dev/manual/nix/2.28/command-ref/conf-file) — Nix
contributors, versioned documentation.

**Read:** configuration precedence, `require-sigs`, `sandbox`, `sandbox-fallback`, and
`sandbox-paths`. **Contribution:** specifies concrete isolation and substitution controls, including
exceptions. **Limitation:** documentation does not establish how a particular host is configured or
whether it is trustworthy. **Relevance:** runner identity must include or constrain the execution
boundary; invoking Nix alone does not attest those settings.

## V05 — Evaluation can trigger builds

[Nix 2.28.8 Reference Manual: Import From Derivation](https://nix.dev/manual/nix/2.28/language/import-from-derivation)
— Nix contributors, versioned documentation.

**Read:** complete explanatory text, example, and diagrams. **Contribution:** describes evaluation
pausing to realize a store object and the switch that disables this. **Limitation:** establishes a
possible behavior, not that the-valley's current checks exercise it. **Relevance:** the integrator's
“builds nothing” contract needs an enforced runner restriction and a regression case.

## V06 — What a Nix content address means

[Nix 2.28.8 Reference Manual: Content-Addressing Store Objects](https://nix.dev/manual/nix/2.28/store/store-object/content-address)
— Nix contributors, versioned documentation.

**Read:** complete page, including references, self-references, and addressing methods.
**Contribution:** specifies content identity and distinguishes content-addressed from
input-addressed objects. **Limitation:** a store-object identity is not an execution proof.
**Relevance:** prevents overinterpreting a digest of derivation reference names as a measurement of
all runtime input bytes.

## V07 — Practical hermeticity

[Hermeticity](https://bazel.build/basics/hermeticity) — Bazel contributors, living documentation.

**Read:** definition, benefits, non-hermeticity examples, and troubleshooting. **Contribution:**
identifies concrete environmental dependencies that undermine reuse. **Limitation:** engineering
guidance, not a formal guarantee for every Bazel action. **Relevance:** a useful checklist for
constructing adversarial dependency tests, especially around host tools, timestamps, and source-tree
writes.

## V08 — Action results versus stored bytes

[Remote Caching](https://bazel.build/remote/caching) — Bazel contributors, living documentation.

**Read:** overview of actions and cache storage, plus cache backend and access-control passages.
**Contribution:** separates the action-result mapping from content-addressed output storage.
**Limitation:** shared caching relies on suitable reproducibility and access assumptions.
**Relevance:** a digest can authenticate output bytes while leaving the claimed connection to a
computation dependent on another authority.

## V09 — Reproducibility's scope

[Definitions](https://reproducible-builds.org/docs/definition/) — Reproducible Builds project,
living documentation.

**Read:** full definition and explanations. **Contribution:** defines reproducibility in terms of
specified source, environment, instructions, and artifacts. **Limitation:** the definition itself
provides neither a measurement nor a guarantee. **Relevance:** separates artifact agreement from
incidental logs, execution truth, and functional correctness.

## V10 — Historical Nix rebuildability

[Reproducibility of Build Environments through Space and Time](https://arxiv.org/html/2402.00424v1)
— Julien Malka, Stefano Zacchiroli, and Théo Zimmermann, ICSE-NIER, 2024; DOI
10.1145/3639476.3639767.

**Read:** abstract, introduction, definitions, and targeted methods/results/failure passages.
**Contribution:** measures historical environment reconstruction and successful rebuilding.
**Limitation:** reuses cached dependencies, leaves bit-for-bit artifact reproducibility for later
work, and gives preliminary failure classifications. **Relevance:** supports practical optimism
about functional deployment without establishing full source-closure rebuildability or deterministic
outputs.

## V11 — Optimistic validation

[On Optimistic Methods for Concurrency Control](https://www.eecs.harvard.edu/~htk/publication/1981-tods-kung-robinson.pdf)
— H. T. Kung and John T. Robinson, _ACM TODS_ 6(2), 1981; DOI 10.1145/319566.319567.

**Read:** read/write phases, serial-equivalence validation, and passages on moving work outside the
critical section. **Contribution:** validates speculative transactions before publishing their
writes. **Limitation:** assumes transactions preserve the specified integrity conditions.
**Relevance:** provides the integration analogy and its often omitted precondition: checks must
actually establish the properties being preserved.

## V12 — Conflicts beyond text

[Proactive Detection of Collaboration Conflicts](https://people.cs.umass.edu/~brun/pubs/pubs/Brun11fse.pdf)
— Yuriy Brun, Reid Holmes, Michael D. Ernst, and David Notkin, ESEC/FSE, 2011.

**Read:** abstract and introductory study/Crystal discussion; dataset not reanalyzed. Contribution
and limitations: [S20](sources-systems.md#s20--proactive-collaboration-conflicts).

## V13 — Speculative project gating

[Project Gating](https://zuul-ci.org/docs/zuul/latest/gating.html) — Zuul contributors, living
documentation.

**Read:** speculative testing, failed predecessors, windows, and cross-project dependencies.
Contribution and limitations: [S12](sources-systems.md#s12--zuul).

## V14 — A hosted merge-queue baseline

[Managing a merge queue](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)
— GitHub, living documentation.

**Read:** overview, required-check behavior, and operation description. **Contribution:** validates
candidates with the target branch and preceding queued work. **Limitation:** documented product
behavior is not a workload-independent performance result. **Relevance:** comparisons with hosted CI
should include contemporary merge queues rather than assume every forge serially reruns isolated
branches.

## V15 — End-to-end workflow integrity

[in-toto: Providing farm-to-table guarantees for bits and bytes](https://www.usenix.org/system/files/sec19-torres-arias.pdf)
— Santiago Torres-Arias, Hammad Afzali, Trishank Karthik Kuppusamy, Reza Curtmola, and Justin
Cappos, USENIX Security, 2019.

**Read:** introduction, definitions, threat model, security goals, and project-owner-compromise
passage. **Contribution:** binds authorized supply-chain steps and artifact flows through signed
metadata. **Limitation:** compromised policy authority or unsuitable authorized checks remain
fundamental limits. **Relevance:** close prior art for composing evidence across independently
operated tools.

## V16 — Typed claims about immutable subjects

[Statement layer specification, v1](https://in-toto.io/Statement/v1) — in-toto contributors, living
specification.

**Read:** schema and subject/predicate field semantics. **Contribution:** binds a typed predicate to
artifact digests. **Limitation:** predicate meaning and acceptance policy remain external to the
generic statement layer. **Relevance:** the pure/effectful distinction can be expressed
interoperably rather than treated as an objection to typed supply-chain attestations.

## V17 — SLSA's actual requirements

[SLSA v1.2: Build requirements](https://slsa.dev/spec/v1.2/build-requirements) — SLSA
community/OpenSSF, approved versioned specification.

**Read:** requirements table, provenance generation, and isolation. **Contribution:** defines
concrete Build-level obligations. **Limitation:** conformity requires deployment assessment.
**Relevance:** local signatures and sampling do not justify the repository's approximate Level 3
claim.

## V18 — Signing without confusing payload types

[DSSE Protocol](https://github.com/secure-systems-lab/dsse/blob/master/protocol.md) — DSSE
contributors, living specification.

**Read:** pre-authentication encoding, signing, verification, and multi-signature procedures.
**Contribution:** binds payload type and exact payload bytes in a signature envelope.
**Limitation:** says nothing about the truth of the payload. **Relevance:** clarifies what canonical
serialization solves and what an interoperability adapter must preserve.

## V19 — Identity and transparency services

[Security Model](https://docs.sigstore.dev/about/security/) — Sigstore contributors, living
documentation.

**Read:** identity, trust-root, Fulcio, certificate-transparency, and short-lived-certificate
explanations. **Contribution:** connects identity-based signing with an auditable history.
**Limitation:** identity and transparency do not establish computation correctness; monitoring
remains necessary. **Relevance:** an optional signing ecosystem, with trust and operational choices
separate from the-valley's evidence semantics.

## V20 — Updating trust safely

[The Update Framework Specification](https://theupdateframework.github.io/specification/latest/) —
TUF contributors, living specification.

**Read:** roles and delegation, client update/expiration passages, and key-rotation procedures.
**Contribution:** addresses secure delivery of trusted metadata amid replay, staleness, and partial
compromise. **Limitation:** cannot make an authorized artifact correct or recover automatically from
every root compromise. **Relevance:** authoritative revocation distribution needs more than
appending a negative statement to a log.

## V21 — Transparency's explicit limits

[RFC 9162: Certificate Transparency Version 2.0](https://www.rfc-editor.org/rfc/rfc9162.html) — Ben
Laurie, Eran Messeri, and Rob Stradling, IETF Experimental RFC, 2021.

**Read:** overview and §11 security considerations, especially detection and misbehaving logs.
**Contribution:** distinguishes logged evidence, monitoring, and inconsistent log views.
**Limitation:** a certificate protocol; direct reuse requires adaptation, and some gossip mechanisms
are outside its scope. **Relevance:** inclusion is not correctness, completeness of observation, or
agreement on one history.

## V22 — A transparency implementation component

[Tessera](https://github.com/transparency-dev/tessera) — transparency.dev contributors, living
repository documentation.

**Read:** README lifecycle sections on sequencing, integration, and checkpoint publication.
**Contribution:** supplies a tile-based log library with distinct durability/publication stages and
witness-policy support. **Limitation:** does not choose application acceptance or outage policy.
**Relevance:** the-valley must specify which completed stage authorizes an action and how incomplete
publication is recovered.

## V23 — Evidence, appraisal, and freshness

[RFC 9334: Remote ATtestation procedureS (RATS) Architecture](https://www.rfc-editor.org/rfc/rfc9334.html)
— Henk Birkholz, Dave Thaler, Michael Richardson, Ned Smith, and Wei Pan, IETF Informational
RFC, 2023.

**Read:** role definitions, conceptual messages, trust relationships, and freshness passages.
**Contribution:** separates evidence production from appraisal and reliance. **Limitation:**
architecture rather than a universal hardware or protocol guarantee. **Relevance:** clarifies the
additional boundary needed for measured execution and the policy nature of freshness.

## V24 — Common-mode toolchain compromise

[Reflections on Trusting Trust](https://www.cs.cmu.edu/~rdriley/487/papers/Thompson_1984_ReflectionsonTrustingTrust.pdf)
— Ken Thompson, _Communications of the ACM_ 27(8), 1984; DOI 10.1145/358198.358210.

**Read:** compiler-backdoor construction and concluding discussion. **Contribution:** demonstrates a
compromise that reproduces through a compiler without remaining in inspected source. **Limitation:**
a construction illustrating possibility, not a prevalence measurement. **Relevance:** independent
witnesses sharing one compromised toolchain do not provide independence against that compromise.

## V25 — The meaning of a passing test

[The Oracle Problem in Software Testing: A Survey](https://discovery.ucl.ac.uk/id/eprint/1471263/1/06963470.pdf)
— Earl T. Barr, Mark Harman, Phil McMinn, Muzammil Shahbaz, and Shin Yoo, _IEEE Transactions on
Software Engineering_ 41(5), 2015.

**Read:** abstract, introduction, and metamorphic-relations discussion. **Contribution:** organizes
ways of deciding whether observed behavior is correct. **Limitation:** does not supply an automatic
universal oracle. **Relevance:** reliable evidence of execution must be distinguished from adequate
evidence of requirement satisfaction.

## V26 — Nondeterministic tests

[An Empirical Analysis of Flaky Tests](https://mir.cs.illinois.edu/marinov/publications/LuoETAL14FlakyTestsAnalysis.pdf)
— Qingzhou Luo, Farah Hariri, Lamyaa Eloussi, and Darko Marinov, FSE, 2014.

**Read:** abstract and introduction describing the study and failure implications. **Contribution:**
examines 201 likely flaky-test-fixing commits across 51 projects. **Limitation:** historical sample;
this report does not extrapolate its prevalence to the-valley. **Relevance:** repeated-run
disagreement can expose unstable tests rather than dishonest signers, requiring diagnosis before
reputation changes.

## V27 — Safe selection is relative to an existing test suite

[A Safe, Efficient Regression Test Selection Technique](https://www.cs.purdue.edu/homes/xyzhang/spring07/Papers/p173-rothermel.pdf)
— Gregg Rothermel and Mary Jean Harrold, _ACM TOSEM_ 6(2), 1997; DOI 10.1145/248233.248262.

**Read:** abstract and analytical evaluation of safety, precision, efficiency, and generality.
**Contribution:** provides conditional safety guarantees for selecting regression tests.
**Limitation:** controlled-regression assumptions and the original suite's coverage remain
essential. **Relevance:** evidence reuse needs a no-missed-retest argument; it cannot inherit a
no-missed-bug guarantee.

## Local implementation evidence and unresolved work

The chapter inspected [attestation execution](../../attest/run.go),
[Nix invocation and dependency collection](../../attest/check.go),
[digest construction](../../attest/digest.go), [signature verification](../../attest/verify.go),
[evidence gathering](../../integrator/evidence.go),
[policy composition](../../integrator/policy.go),
[verdict rules](../../integrator/verdict/verdict.go), [ref publication](../../integrator/refs.go),
[transfer statements](../../integrator/transfer.go), and the
[attestation schema](../../schema/attestation.cue). The reading was static code analysis, not
penetration testing or a formal verification of those programs.

The implementation findings are deliberately narrower than a complete audit. Same-tree computation
binding, effectful-command binding, future timestamps, and the landing/evidence crash boundary are
proposed regression or fault-injection cases. No exploit success rate or deployment-wide exposure is
claimed. The source set establishes strong precedents but does not establish novelty of the-valley's
particular combination; that would require a more systematic survey of research prototypes and
industrial integration systems.

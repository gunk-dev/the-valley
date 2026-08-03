// The statement a Phase 2 attestation signs: a typed, versioned record of
// what check ran, over what tree, with what result (dcr-0de694f). The
// statement is the only thing signed. The envelope around it — a signed
// note, whose text is the statement and whose signature lines sit beneath
// it — and the git ref that note is stored at are carriage, and neither is
// described here.
//
// The statement is self-contained: a verifier that holds these bytes and
// the tree they name needs nothing else to understand the claim. That is
// why the pure-versus-effectful distinction of design/verification.md is
// `predicateType` — inside the signed bytes — rather than a property of
// where the statement was found.
//
// This file is the gate a statement passes on the way in and on the way
// out, and it reads JSON. What a signature covers is the statement's
// written form, which is lines (design/verification.md); the two carry the
// same fields, and the constraints below on what a value may be are what
// keep every validated statement writable as lines.
//
// Like the host and event schemas, this file is deliberately not Nix. A
// statement travels between machines and outlives the tool that wrote it.
//
//   Validate a statement:  cue vet -c schema/attestation.cue <statement>.json
package attestation

// #Subject identifies what is attested to, as a digest set: several named
// digests of the same object, under different naming schemes. `primary`
// names the member that IS the identity, and it is always a
// content-addressed digest of the tree — which is what makes an
// attestation survive a rebase, and what binds it to the base the change
// was produced against (ida-cbcbb3c). Every other member is advisory: a
// commit hash is useful for finding the change and is never relied on for
// identity, because identity may not be coupled to git (ida-51605e8).
#Subject: {
	// The naming scheme that identifies the subject. Only a
	// content-addressed tree digest may hold this position; widening it
	// means adding a second content-addressing scheme, deliberately not
	// before one exists.
	primary: "valley-tree-v1"

	// The digests. The primary member must be present — a subject
	// identified only by advisory members has no identity at all.
	digest: #DigestSet
	digest: (primary): _
}

// #DigestSet is a set of digests of one object, keyed by the scheme that
// produced them. Closed: an unrecognised scheme is a verifier that cannot
// check what it was handed, so it fails vet rather than being carried
// through as an opaque string.
#DigestSet: {
	// The valley tree digest, defined in design/verification.md. SHA-256
	// over a canonical manifest of the tree's entries, so it is a function
	// of the tree alone: no commit, no author, no time, no path outside
	// the tree.
	"valley-tree-v1"?: #Sha256

	// SHA-256 over a canonical manifest of a pure check's input paths,
	// defined alongside the tree digest.
	"valley-inputs-v1"?: #Sha256

	// SHA-256 over an object's bytes.
	sha256?: #Sha256

	// A git object id, advisory only. Both hash algorithms git ships are
	// admitted, because which one a repository uses is not the
	// attestation's concern.
	"git-sha1"?:   =~"^[0-9a-f]{40}$"
	"git-sha256"?: =~"^[0-9a-f]{64}$"
}

#Sha256: =~"^[0-9a-f]{64}$"

// #Line is what a statement's every string value is held to: text that one
// line can carry. Not empty, and holding no ASCII control character.
//
// This is a property of the signed bytes rather than a stylistic
// preference. A statement is written out as lines, and the form has no
// escapes: a value is written as its own bytes, which is what leaves two
// implementations nothing to disagree about. So a value carrying a newline
// has no written form at all, and one that is empty would leave an
// invisible trailing space behind. Both fail here, before a run has built
// anything, rather than in a renderer after a check has passed.
#Line: =~"^[^\\x00-\\x1f\\x7f]+$"

// #KeySegment is the shape of every field name a statement carries: a
// letter, then letters, digits and dashes.
//
// This too is about the signed bytes. A field's name is written into the
// key that names its line, dots separate one segment of that key from the
// next, a space separates the key from the value, and a segment that is a
// number is an array index. A name holding any of those would be a line
// that reads back as something other than what was written.
//
// Every name in this file is fixed here and satisfies it, so no statement
// today can carry one that does not. The constraint is written down anyway,
// because the first map keyed by a caller rather than by this file is where
// the property silently stops holding.
#KeySegment: =~"^[A-Za-z][A-Za-z0-9-]*$"

// #SegmentKeyed is how a map with caller-supplied keys is written. Nothing
// uses it yet: every map the statement carries today — the digest sets, the
// provenance — is keyed by names this file fixes. The direction that
// introduces one is the ecosystem-specific key-value context strings of
// ida-d2dc957, and whatever field carries them unifies with this, so a key
// with no written form fails `cue vet` rather than reaching a signer:
//
//   context: #SegmentKeyed & {[string]: #Line}
#SegmentKeyed: {[#KeySegment]: _}

// The two kinds of claim of design/verification.md. A pure check's
// attestation is re-derivable: it carries input, derivation and output
// digests, and any verifier can re-run and confirm. An effectful check's
// attestation is a notarization: a named environment ran the check at a
// stated time and observed a result, and trust in it is trust in the
// signer.
#PredicateType: "the-valley/check/pure/v1" | "the-valley/check/effectful/v1"

// #PureCheck is the re-derivable claim. The three digests are what a
// verifier re-derives against: it evaluates the same check over the same
// subject tree, and must arrive at the same derivation and the same
// output.
#PureCheck: {
	check:  #CheckRun
	result: #Result

	// The check's inputs, as the runner named them.
	inputs: #DigestSet

	// The derivation that was built.
	derivation: #DigestSet

	// What the build produced.
	output: #DigestSet
}

// #EffectfulCheck is the notarization. It carries no re-derivation
// digests, because there is nothing to re-derive: an effectful check is
// not bit-reproducible, which is the whole reason it is a separate
// predicate type.
#EffectfulCheck: {
	check:  #CheckRun
	result: #Result

	// The environment the check ran in, named by whoever sealed it. A
	// verifier can do nothing with this but read it; it is what the
	// notarization is a notarization *of*.
	environment: #Line

	// When the result was observed, RFC 3339 in UTC. Present here and
	// absent from #PureCheck on purpose: a pure claim that carried a time
	// would stop being a function of its inputs.
	observedAt: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"
}

// #CheckRun is the check that ran, named the way the verification policy
// of schema/verification.cue names it, so the two documents agree on what
// a check is called.
#CheckRun: {
	// The check's name in the project's policy.
	name: #Name

	// The runner kind. "nix" builds a flake check attribute and claims
	// purity strongly; "command" runs a command line and claims nothing
	// beyond having run it.
	runner: "nix" | "command"

	// The flake check attribute, for the nix runner.
	attribute?: #Name

	// The command line, for the command runner. One line: a check whose
	// command spans several has no written form, so it is refused here
	// rather than in a renderer after the check has run.
	command?: #Line

	if runner == "nix" {
		attribute: #Name
		command?:  _|_
	}
	if runner == "command" {
		command:    #Line
		attribute?: _|_
	}
}

// A check either passed or it did not. The helper publishes nothing when a
// check fails, but "failed" is admitted here because a statement that
// could not record a failure would be a schema that only permits good
// news.
#Result: "passed" | "failed"

// #Provenance is the record of the run that produced the tree, carried as
// content. An agent run holds no key (ida-a8243d2), so none of this is
// self-asserted: it is attributed inside the host's signed statement
// (ida-45178f6), and it is true exactly to the extent the host is trusted.
#Provenance: {
	// The harness that ran the agent.
	harness?: #Line

	// The model the harness drove.
	model?: #Line

	// Digests of the prompt and of the context the run was given. Digests
	// rather than content: the value is committed to by the signature
	// while the text itself stays wherever it lives.
	prompt?:  #DigestSet
	context?: #DigestSet

	// The delegation chain, as recorded. Phase 2 records; it does not
	// check. Verifying a chain belongs to an enforcement point, and the
	// first one is Phase 3's integrator.
	delegation?: [...#Delegation]
}

// #Delegation is one step of a recorded chain: who acted, and what the
// step conveyed. Both are free strings because no naming scheme for
// principals has been decided, and pinning one here would decide it.
#Delegation: {
	principal: #Line
	grant?:    #Line
}

// Names are lowercase, dash-separated, and short — the same shape as check
// names in schema/verification.cue and project names in schema/valley.cue.
#Name: =~"^[a-z0-9][a-z0-9-]*$"

// ----------------------------------------------------------------------
// The statement.
//
// The top level of this package is itself a statement, so a composed
// statement is validated by vetting it against this file directly. The
// parts above are definitions; the document they make is here.

// The document kind, versioned. A verifier reads this before anything
// else, so a future revision can be told apart without guessing.
statementType: "the-valley/attestation/v1"

// What the statement is about.
subject: #Subject

// The kind of claim, versioned. The version is part of the name, so a
// statement says which revision of a claim shape it was written against,
// and the format can grow without invalidating what already exists.
predicateType: #PredicateType

// The run that produced the tree, carried as content. Optional, and every
// field inside it is optional: nothing in the reference implementation
// collects these values today, so they are caller-supplied or absent
// rather than invented.
provenance?: #Provenance

// The claim itself. Its shape follows from the predicate type, and it is
// constrained only through that type — which is the mechanism that puts
// the pure-versus-effectful distinction inside the signed bytes. A
// statement claiming purity cannot omit the digests a verifier re-derives
// against, and one claiming a notarization cannot smuggle them in.
if predicateType == "the-valley/check/pure/v1" {
	predicate: #PureCheck
}
if predicateType == "the-valley/check/effectful/v1" {
	predicate: #EffectfulCheck
}

// A file's top level cannot be closed (embedding #Statement would open it
// instead — embedding lifts closedness), so reject stray top-level fields
// explicitly: anything but the statement's fields — a `predicat:` typo, a
// carriage concern like `signature:` — conflicts with this sentinel and
// fails vet with an error naming the field.
[!="statementType" & !="subject" & !="predicateType" & !="predicate" & !="provenance"]: "INVALID: unknown top-level field; only \"statementType\", \"subject\", \"predicateType\", \"predicate\" and \"provenance\" are allowed"

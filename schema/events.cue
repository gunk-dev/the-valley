// The event vocabulary of a valley host: what its bus carries. Phase 1
// (design/roadmap.md) defined exactly one event; later phases add an event
// or a field at a time, never more. Phase 3 adds the integrator's two
// outcomes, #IntegrationSucceeded and #RequestStale.
//
// Events are a projection of git, never a second source of truth. Every
// field here is derivable from the repository alone, so replaying a repo's
// refs reproduces the same events — the phase's determinism criterion.
// Wall-clock time, hostnames, and other machine facts are deliberately
// absent: any of them would make replay produce different events.
//
// Like the host schema, this file is not Nix. The NixOS module's hook is
// one publisher; any other publisher or consumer reads the same file.
//
//   Validate a payload:  cue vet -d '#RefUpdated' schema/events.cue <payload>.json
package valley

// #Integration is what every integrator outcome carries: which project,
// which change, and which stream the change was judged against. The
// integrator is a single writer per target stream, so these three name the
// commit point exactly.
//
// The change is named by the last segment of its integration-request ref,
// `refs/the-valley/integration-requests/<target>/<change>`, which is where
// the request is durable. The bus is a projection of that ref, never a
// second place a request lives.
#Integration: {
	// The project name on the publishing host — the same name
	// #Host.projects keys on, and the <repo> token of the subject.
	repo: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

	// The change, as its request ref names it.
	change: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

	// The protected ref the change targets.
	target: =~"^refs/"
}

// #IntegrationSucceeded is published when a change lands: the target ref
// moved from `old` to `new`, and every check the policy required carried
// evidence that stood. It is published once per landing, on the subject
// valley.git.<repo>.integration-succeeded.
//
// Both object ids are read from the ref write itself, so the event is a
// projection of what git already records: replaying the target's history
// reproduces the same pair.
#IntegrationSucceeded: {
	#Integration

	// The event's type, and its subject's last token.
	event: "integration-succeeded"

	// The target ref before and after the fast-forward.
	old: #ObjectId
	new: #ObjectId

	// The checks whose evidence transferred, in the order the integrator
	// judged them. Every check the policy required is here, because a
	// change with an invalidated check does not land.
	transferred: [...#CheckName]
}

// #RequestStale is published when a change cannot land against the current
// tip: either its delta no longer applies, or evidence for at least one
// required check no longer transfers (dcr-439b771). It is published once
// per judgement, on the subject valley.git.<repo>.request-stale, and the
// request ref stays where it is so the change can be resubmitted.
//
// Staleness is the unified failure mode: there is no rejection event here,
// because a change whose evidence has moved is not wrong, it is behind.
#RequestStale: {
	#Integration

	// The event's type, and its subject's last token.
	event: "request-stale"

	// The target tip the request was judged against. A reader comparing
	// this with the target's current value can tell whether the judgement
	// is still the current one.
	tip: #ObjectId

	// Why the evidence did not stand.
	//
	//   "conflict" — the delta does not apply cleanly to the tip.
	//     Resolving it produces a tree nobody has authored, and new
	//     authorship needs new evidence, so every required check is named.
	//   "evidence"  — the delta applies, and the named checks are the ones
	//     whose evidence did not transfer.
	reason: "conflict" | "evidence"

	// Exactly the checks that need new evidence. Retry is partial: a
	// check not named here does not need re-running.
	checks: [...#CheckName]
}

// A check's name, the same shape #Name carries in
// schema/verification.cue — the policy names the checks, and an event
// naming one has to agree with it.
#CheckName: =~"^[a-z0-9][a-z0-9-]*$"

// #RefUpdated is published once per ref a push updates, on the subject
// valley.git.<repo>.ref-updated: the ref's name and its object id before
// and after. Creation and deletion carry the all-zero id on the
// corresponding side — git's own convention. A replay of a repo's current
// refs emits one event per existing ref with the all-zero id as `old`,
// because a ref's prior value is not derivable from the repository.
#RefUpdated: {
	// The event's type, and its subject's last token.
	event: "ref-updated"

	// The project name on the publishing host — the same name
	// #Host.projects keys on, and the <repo> token of the subject.
	repo: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

	// The full ref name, e.g. "refs/heads/main".
	ref: =~"^refs/"

	// Object ids: lowercase hex, 40 (sha1) or 64 (sha256) digits,
	// all-zero for "no value on this side".
	old: #ObjectId
	new: #ObjectId
}

#ObjectId: =~"^([0-9a-f]{40}|[0-9a-f]{64})$"

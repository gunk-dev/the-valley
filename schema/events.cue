// The event vocabulary of a valley host: what its bus carries. Phase 1
// (design/roadmap.md) defines exactly one event; later phases add an event
// or a field at a time, never more.
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

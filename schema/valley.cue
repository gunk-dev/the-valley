// Package valley is the canonical domain model of a valley host: what the
// host serves, independent of any machine that serves it.
//
// This file is deliberately not Nix. The NixOS module in this repo is one
// installer that consumes the model (`cue vet` + `cue export` at build
// time); other installers and tools read the same file. Deployment concerns
// — data directory, unix user, SSH keys — are machine integration and live
// with the installer, never here.
package valley

// #Host is a complete valley host declaration: the set of projects the
// host serves, and the durability policy for the data behind them.
#Host: {
	// projects maps a project name to its declaration. Names must be
	// filesystem- and URL-safe because they become on-disk directory
	// names ("<name>.git") and clone paths.
	projects: [=~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"]: #Project

	// The host's offsite-backup policy. Optional: a declaration without
	// it is a host without offsite backup, and evaluates exactly as it
	// did before this field existed.
	backup?: #Backup
}

// #Project is the unit of declaration. A project *has* stores; git is the
// only store type today, and it is nested rather than top-level on
// purpose — the valley is not committed to being git-only.
#Project: {
	// The project's git store.
	git: {
		// Whether the host serves a git repository for this project.
		// Disabling never deletes anything: an existing repository is
		// left untouched on disk, merely unmanaged.
		enable: bool | *true
	}

	// Publication-mirror URLs. Every push to the primary replicates main
	// and the tags to each URL, force-updating and propagating deletions.
	// Only those: a mirror publishes what has been integrated, and topic
	// branches awaiting review are not published. Any other branch on a
	// mirror is removed. Remote-only namespaces (e.g. GitHub's
	// refs/pull/*) are never touched. Replication is best-effort: a dead
	// mirror never rejects the primary push. Credentials are the host's
	// concern (the installer documents how); they are not declared here.
	mirrors: [...string] | *[]

	// The project's write protection. Optional: a project without it has
	// no protected refs, so its branches are open to every push. Every
	// other rule of the push policy (#Protection describes them) applies
	// to it unchanged.
	protection?: #Protection

	// Named grants, each opening ref patterns the push policy closes by
	// default to the principals it names. A signed release tag pushed by a
	// person is the case they exist for: tags take no push except through
	// one. Empty by default, so a project opens nothing it does not name.
	grants: [#GrantName]: #RefGrant
}

// #RefGrant is one named grant: ref patterns, and the principals who may
// write the refs they match. A grant only adds writers. A protected ref it
// covers still takes a declared writer too.
#RefGrant: {
	// The refs opened, as full refnames with `*` matching any characters.
	refs: [#GrantPattern, ...#GrantPattern]

	// Who may write them. At least one: a grant that opens a pattern to
	// nobody opens nothing.
	writers: [#PrincipalName, ...#PrincipalName]
}

// #GrantName names a grant. It appears in what a refusal says.
#GrantName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

// #GrantPattern is a pattern a grant may name. The namespaces whose rule
// is fixed are refused: branches are open already, replacement refs and
// notes are closed to every push, and the valley's own namespace has rules
// of its own. A declaration that tries to open one fails rather than
// reading as a grant the hook never applies.
#GrantPattern: #RefPattern & !~"^refs/(heads|replace|notes|the-valley)/"

// #Protection states which of a project's refs are protected: closed to
// every push but a declared writer's. It is the declared half of the one
// structural git invariant, which the pre-receive hook (valleyhook/)
// enforces on every project a host serves, protected or not.
//
// The hook holds every push to an allowlist, and a write must pass every
// rule that applies to its ref:
//
//   - replacement refs (refs/replace/*), notes (refs/notes/*) and the
//     valley's own namespace (refs/the-valley/*, but for the two below)
//     take no push at all;
//   - a ref that is symbolic in the repository takes no push;
//   - topic branches (refs/heads/*) are open;
//   - attestation refs (refs/the-valley/attestations/<digest>/<key hash>)
//     may be created by anyone, never updated or deleted, and only
//     pointing at notes about that digest signed under that key hash;
//   - integration requests (refs/the-valley/integration-requests/*) take
//     writes only from a principal holding the request grant (dcr-e544f20);
//   - every other ref, tags included, takes a named grant of the project
//     (#Project.grants) that opens it to the pusher;
//   - a protected ref also takes a declared writer.
//
// Only the protected set, its writers and the project's grants are
// declared. The other rules are not choices: the namespaces they cover are
// fixed by the contributor protocol (design/contribute.md), and the closed
// ones change what every reader of the repository sees. Protection only
// adds a requirement — a writer of a pattern covering integration requests
// still needs the request grant. All policy beyond the invariant lives in
// the integrator, never here (design/architecture.md, _a pull-based
// integrator_).
#Protection: {
	// The refs closed to everyone but a declared writer. Patterns are
	// matched against the full refname, so they begin with "refs/" — a
	// bare branch name would protect nothing — and `*` matches any
	// characters, path separators included.
	refs: [#RefPattern, ...#RefPattern] | *["refs/heads/main"]

	// The principals allowed to push them. Empty by default: a protected
	// ref admits no pushes at all. The integrator's local ref write is
	// then the only path onto it, and that privilege is the machinery's
	// — it is a write made on the host, which no push hook governs — so
	// it is never declared here. A writers list is the deliberate
	// exception that punches a named hole in the wall; the transition
	// arrangement used one for the operator.
	writers: [...#PrincipalName] | *[]
}

// #RefPattern is a full refname, optionally with `*` wildcards. The
// characters git forbids in a refname — space, ~, ^, :, ?, [, \ — are
// rejected here too, so a pattern that could never match a real ref fails
// the declaration rather than sitting dead in a hook.
#RefPattern: =~"^refs/[^ ~^:?\\\\[]+$"

// #PrincipalName names a principal — a human, machine, or service the
// instance grants something to — in the instance's identity registry
// (dcr-b87f6e8). The registry's compilation (identity/) binds each name to
// the keys that act as it, and on a host that compiles no registry the
// installer does — the NixOS module in this repo maps each name to its
// keys by hand. The shape is the project-name shape, for the same reason: a
// name travels into files and command lines.
#PrincipalName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

// #Backup is the durability policy for the host's data: that an offsite
// backup exists, how often it runs, and how long snapshots are kept. It is
// deliberately host-level — one data directory, one repository — because
// that is the host's reality today; per-project backup is a possible
// growth path (a `backup` on #Project), not a present need. Only policy
// is declared here: the repository URL, credentials, and host-key pinning
// are machine integration and live with the installer, never here.
#Backup: {
	// Whether offsite backup runs for the host's data. Disabling never
	// deletes anything: an existing backup repository is left untouched,
	// merely no longer written to or pruned.
	enable: bool | *true

	// The backup target kind. restic over sftp (a Hetzner Storage Box,
	// dcr-d7952bc) is the only target today; an object-store target
	// would widen this to a disjunction ("restic-s3") — deliberately not
	// before an implementation exists.
	target: "restic-sftp"

	// How often a snapshot is taken. "nightly" is the only cadence
	// today; its exact wall-clock rendering is the installer's choice.
	cadence: "nightly"

	// How many snapshots to keep per tier when pruning (restic
	// --keep-daily/--keep-weekly/--keep-monthly semantics). Every tier
	// keeps at least one snapshot — a zero tier would silently thin
	// history, so it is rejected rather than rendered.
	retention: {
		daily:   int & >0 | *7
		weekly:  int & >0 | *4
		monthly: int & >0 | *6
	}
}

// The top level of this package is itself a host declaration: a config
// file evaluated together with this schema is validated against #Host —
// unknown fields and unsafe project names are rejected.
//
//   cue vet -c schema/valley.cue <config>.cue
//   cue export schema/valley.cue <config>.cue
projects: #Host.projects

// Mirrors #Host.backup. CUE cannot reference an optional field, so the
// constraint is restated against the same definition.
backup?: #Backup

// A file's top level cannot be closed (embedding #Host would open it
// instead — embedding lifts closedness), so reject stray top-level fields
// explicitly: anything but the #Host fields — a `project:` typo, a
// deployment concern like `user:` — conflicts with this sentinel and
// fails vet with an error naming the field.
[!="projects" & !="backup"]: "INVALID: unknown top-level field; only \"projects\" and \"backup\" are allowed"

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

	// Push-mirror URLs. Every push to the primary is replicated to each
	// URL via `git push --mirror`, which propagates deletions — the
	// correct semantic for a mirror. Replication is best-effort: a dead
	// mirror never rejects the primary push. Credentials are the host's
	// concern (the installer documents how); they are not declared here.
	mirrors: [...string] | *[]
}

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

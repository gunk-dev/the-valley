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
// host serves.
#Host: {
	// projects maps a project name to its declaration. Names must be
	// filesystem- and URL-safe because they become on-disk directory
	// names ("<name>.git") and clone paths.
	projects: [=~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"]: #Project
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

// The top level of this package is itself a host declaration: a config
// file evaluated together with this schema is validated against #Host —
// unknown fields and unsafe project names are rejected.
//
//   cue vet -c schema/valley.cue <config>.cue
//   cue export schema/valley.cue <config>.cue
projects: #Host.projects

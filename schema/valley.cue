// Package valley is the canonical domain model of a valley host: what the
// host serves, independent of any machine that serves it.
//
// This file is deliberately not Nix. The NixOS module in this repo is one
// installer that consumes the model (`cue vet` + `cue export` at build
// time); other installers and tools read the same file. Deployment concerns
// — data directory, unix user, SSH keys — are machine integration and live
// with the installer, never here.
package valley

import "strings"

// #Host is a complete valley host declaration: the set of projects the
// host serves, the groups they belong to, and the durability policy for
// the data behind them.
#Host: {
	// projects maps a project name to its declaration. Names must be
	// filesystem- and URL-safe because they become on-disk directory
	// names ("<name>.git") and clone paths. A project's name is its
	// identifier: its repository, its plan graph, its policy/read and its
	// read grants all name it by it. Its web name, name, is the
	// identifier unless the declaration gives another.
	projects: [ID=#ProjectName]: #Project & {name: *ID | #ProjectName}

	// groups maps each group's identifier to its declaration. The
	// identifier never changes, and the projects of the group name it.
	// The group's name and aliases are what its web URLs say, and they
	// may change. Optional: a declaration without groups evaluates as it
	// did before groups existed, and its projects have no web URLs.
	groups: [#GroupID]: #Group

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
	// and the tags to each URL, force-updating them. Only those: a mirror
	// publishes what has been integrated, and topic branches awaiting
	// review are not published. The host changes nothing else on a
	// mirror: the only deletions it makes there are tags deleted on the
	// primary. So a repository that becomes a mirror keeps every ref it
	// already holds; delete what must not be published when declaring it
	// (docs/architecture.md shows how). Replication is best-effort: a dead
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

	// The identifier of the group the project belongs to, which groups
	// declares. A project's web URLs open with its group's name and its
	// own web name: /<group>/<name>/…. Optional: a project in no group has
	// no web URL, so the web publisher publishes nothing of it.
	group?: #GroupID

	// The project's web name: the second segment of its web URLs. It is
	// the project's identifier unless the declaration gives another, and
	// it may change while the identifier does not. A web name is unique
	// within its group, ignoring case, and two groups may each hold a
	// project named app. A project in a group has a #WebName, and a
	// project in no group has no web name but its identifier.
	name: #ProjectName
	if group != _|_ {
		name: #WebName
	}

	// The project's former web names within its group. A web URL under
	// one, /<group>/<former>/…, answers with a permanent redirect to the
	// same path under /<group>/<name>/…, so a project that is renamed
	// keeps its former name here and its links keep working. They share
	// the group's names with its projects' web names. Empty by default. A
	// project with former names belongs to a group.
	formerNames: [...#WebName] | *[]

	// The project's short aliases. Each is a first segment of a web URL:
	// /<alias>/… answers with a permanent redirect to the same path under
	// /<group>/<name>/…. Empty by default. A project with aliases belongs
	// to a group.
	aliases: [...#WebName] | *[]
}

// #ProjectName names a project on the host.
#ProjectName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

// #GroupID is a group's identifier: the name the declaration keys it by,
// and its projects name it by. It never changes, and no URL shows it.
#GroupID: #WebName

// #Group is one group of projects. Its members and stewards are not
// declared yet: a group is its name and its aliases.
#Group: {
	// The group's name: the first segment of its projects' web URLs.
	name: #WebName

	// Other first segments for the group. A web URL under an alias
	// answers with a permanent redirect to the same path under name, so
	// a group that is renamed keeps its former name here and its links
	// keep working. Empty by default.
	aliases: [...#WebName] | *[]
}

// #WebName is a name a web URL holds as one segment: a group's name or
// alias, or a project's web name, former name or alias. It has the
// project-name shape and at most
// 64 characters, so it never starts with "-", and the web server's own
// paths, under /-/, are never a name's.
#WebName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$" & strings.MaxRunes(64)

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
//     valley's own namespace (refs/the-valley/*, but for the four below)
//     take no push at all;
//   - a ref that is symbolic in the repository takes no push;
//   - topic branches (refs/heads/*) are open;
//   - attestation refs (refs/the-valley/attestations/<digest>/<key hash>)
//     may be created by anyone, never updated or deleted, and only
//     pointing at notes about that digest signed under that key hash;
//   - integration requests (refs/the-valley/integration-requests/*) take
//     writes only from a principal holding the approve grant, also
//     accepted as request (dcr-e544f20);
//   - change records (refs/the-valley/changes/main/<change ID>) move only
//     by the transitions the hook allows;
//   - the plan graph (refs/the-valley/knots, that name exactly) takes a
//     named principal's write of one commit on its tip that leaves the
//     graph valid, and is never deleted;
//   - every other ref, tags included, takes a named grant of the project
//     (#Project.grants) that opens it to the pusher;
//   - a protected ref also takes a declared writer.
//
// Only the protected set, its writers and the project's grants are
// declared. The other rules are not choices: the namespaces they cover are
// fixed by the contributor protocol (archive/design/contribute.md), and the closed
// ones change what every reader of the repository sees. Protection only
// adds a requirement — a writer of a pattern covering integration requests
// still needs the approve grant. All policy beyond the invariant lives in
// the integrator, never here (archive/design/architecture.md, _a pull-based
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

	// The backup target kind. restic over sftp (dcr-d7952bc) is the
	// only target today; an object-store target
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

// Mirrors #Host.groups.
groups: #Host.groups

// Every group a project names is declared, and a project with a web name
// of its own, former names or aliases is in a group. A project that
// breaks one of these lands in _groupless as a sentinel string against a
// `bool`, and fails vet naming the project.
_groupless: [string]: bool
_groupless: {
	for n, p in projects {
		if p.group != _|_ if groups[p.group] == _|_ {
			"\(n)": "INVALID: the project's group is not declared under groups"
		}
		if p.group == _|_ if p.name != n {
			"\(n)": "INVALID: a project with a web name belongs to a group"
		}
		if p.group == _|_ if len(p.formerNames) > 0 {
			"\(n)": "INVALID: a project with former names belongs to a group"
		}
		if p.group == _|_ if len(p.aliases) > 0 {
			"\(n)": "INVALID: a project with aliases belongs to a group"
		}
	}
}

// The web names and former names of a group's projects share one
// namespace: the second segment of the group's web URLs. Two of them that
// differ at most in case collide, and so does a name given twice. Each
// claim lands in _secondSegments under its group's identifier and its
// lowercased name, with who makes it, and two claims of one name in one
// group conflict and fail vet naming both. Projects in different groups
// never collide.
_secondSegments: [string]: [string]: string
_secondSegments: {
	for n, p in projects if p.group != _|_ {
		(p.group): (strings.ToLower(p.name)): "the web name of project \(n)"
		for i, f in p.formerNames {
			(p.group): (strings.ToLower(f)): "former name \(i) of project \(n)"
		}
	}
}

// Group names, group aliases and projects' aliases share one namespace:
// the first segment of a web URL. Two of them that differ at most in case
// collide, and so does a name given twice. Each claim lands in
// _firstSegments under its lowercased name, with who makes it, and two
// claims of one name conflict and fail vet naming both.
_firstSegments: [string]: string
_firstSegments: {
	for id, g in groups {
		(strings.ToLower(g.name)): "the name of group \(id)"
		for i, a in g.aliases {
			(strings.ToLower(a)): "alias \(i) of group \(id)"
		}
	}
	for n, p in projects for i, a in p.aliases {
		(strings.ToLower(a)): "alias \(i) of project \(n)"
	}
}

// Mirrors #Host.backup. CUE cannot reference an optional field, so the
// constraint is restated against the same definition.
backup?: #Backup

// A file's top level cannot be closed (embedding #Host would open it
// instead — embedding lifts closedness), so reject stray top-level fields
// explicitly: anything but the #Host fields — a `project:` typo, a
// deployment concern like `user:` — conflicts with this sentinel and
// fails vet with an error naming the field.
[!="projects" & !="groups" & !="backup"]: "INVALID: unknown top-level field; only \"projects\", \"groups\" and \"backup\" are allowed"

// A worked valley host declaration: one project with a publication mirror,
// and a backup. This file is the single domain input: the NixOS
// module validates it against schema/valley.cue and provisions the host
// from it. Machines never redefine what is declared here.
//
// Validate:  cue vet -c schema/valley.cue examples/host.cue
// Export:    cue export schema/valley.cue examples/host.cue
package valley

projects: {
	// The project (archive/design/user-scenarios.md § S1), with a
	// publication mirror: every push to the primary replicates main and
	// the tags there, best-effort.
	// Topic branches awaiting review stay on the primary (dcr-24d62f7).
	//
	// git.enable defaults to true — git is the only store type today.
	"the-valley": {
		mirrors: ["git@mirror.example.org:the-valley.git"]
	}
}

// The durability policy (dcr-d7952bc): nightly encrypted restic
// snapshots of the host's data to an sftp server.
// cadence and retention take the defaults — nightly, 7 daily / 4 weekly /
// 6 monthly. The repository URL, credentials, and pinned host key are
// machine integration, supplied by the consumer to the installer
// (services.valley.backup.*), never declared here.
backup: {
	target: "restic-sftp"
}

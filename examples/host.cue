// The valley host declaration for the-valley itself — the Phase 0 pilot
// (design/roadmap.md). This file is the single domain input: the NixOS
// module validates it against schema/valley.cue and provisions the host
// from it. Machines never redefine what is declared here.
//
// Validate:  cue vet -c schema/valley.cue examples/host.cue
// Export:    cue export schema/valley.cue examples/host.cue
package valley

projects: {
	// The pilot repo (design/user-scenarios.md § S1). GitHub is retained
	// as a transitional push mirror during migration: every push to the
	// primary is replicated there, best-effort.
	//
	// git.enable defaults to true — git is the only store type today.
	"the-valley": {
		mirrors: ["git@github.com:gunk-dev/the-valley.git"]
	}
}

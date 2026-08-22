// the-valley's project layer: what this one project declares above the
// floor. The floor is the owning valley's (qinling policy/instance); this
// layer binds the-valley alone, and a project can only add to its own
// layer (dcr-f41f718).
package verification

project: {
	// the-valley keeps a knowledge graph, so it takes the lint the docs
	// template offers rather than declining it.
	#docs
}

// The Go, Nix, CUE and shell sources in this tree are covered by no class
// on purpose: the deriver reports them as unclassified, which is the hole
// announcing itself, and closing it waits on classes whose checks can
// answer for them.

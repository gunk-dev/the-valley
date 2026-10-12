// The project layer of the same sample policy, living in the project's own
// tree rather than in the instance repository. The directory convention is
// the same one; the authority is not. A project can only add to its own
// directory, and adding to the instance's floor directory is a change
// landing in the instance repository.
package verification

project: {
	// Selecting the project type is embedding its definition. Every
	// project in the group can read this template; only the ones that
	// write this line are affected by it.
	#docs

	// A check the floor and the template know nothing about. A project may
	// always add classes and checks.
	checks: "shellcheck": {
		runner:    "nix"
		attribute: "shellcheck"
	}

	// A check that installs the scripts in a virtual machine boots a VM and
	// takes minutes, so it is marked slow. It is owed exactly as a quick
	// check is.
	checks: "install-vm": {
		runner:    "nix"
		attribute: "install-vm"
		cost:      "slow"
	}
	classes: scripts: {
		paths: "bin/**":        true
		requires: "shellcheck": true
		requires: "install-vm": true
	}

	// The template offered the knowledge lint as a default, so this project
	// can decline it and vet still passes. Declining either of the floor's
	// checks the same way does not.
	classes: knowledge: requires: "knowledge-lint": false
}

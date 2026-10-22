// Approval case 4. No layer says anything about hardware keys, so the
// composed policy has no `approval` field and does not require a hardware
// key.
package verification

floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: guarded: {
		paths: "src/**":     true
		requires: "c-build": true
	}
}

templates: plain: {}
type: "plain"

project: classes: guarded: paths: "cmd/**": true

// Approval case 3. The template requires a hardware key as a default, and
// the project turns the default off. The composed policy does not require a
// hardware key.
package verification

floor: checks: "c-build": {runner: "nix", attribute: "c-build"}

templates: service: {
	classes: guarded: {
		paths: "hooks/**":   bool | *true
		requires: "c-build": bool | *true
	}
	approval: hardware: bool | *true
}
type: "service"

project: approval: hardware: false

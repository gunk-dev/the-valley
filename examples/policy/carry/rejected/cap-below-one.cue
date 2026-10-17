// The project sets the restacker's cap to 0. The cap is at least 1, so vet
// names the field.
package verification

floor: checks: "c-build": {runner: "nix", attribute: "c-build"}

templates: plain: {}
type: "plain"

project: restack: maxAutomaticVersions: 0

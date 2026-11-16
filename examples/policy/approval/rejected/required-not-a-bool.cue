// Approval case 9. The project writes the approval requirement as a string.
// The requirement is a boolean, so vet names the field.
package verification

floor: checks: "c-build": {runner: "nix", attribute: "c-build"}

templates: plain: {}
type: "plain"

project: approval: required: "yes"

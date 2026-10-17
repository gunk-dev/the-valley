// The cap written at the top level rather than in the project layer. Only a
// layer's `restack` is composed and hashed with the policy, so the
// top-level sentinel rejects this one and vet names the field.
package verification

floor: checks: "c-build": {runner: "nix", attribute: "c-build"}

templates: plain: {}
type: "plain"

restack: maxAutomaticVersions: 16

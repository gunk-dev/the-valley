// Approval case 5. The project writes an approval field the schema does not
// have. `approval` is closed to `hardware`, so vet names the field rather
// than reading it as nothing.
package verification

floor: checks: "c-build": {runner: "nix", attribute: "c-build"}

templates: plain: {}
type: "plain"

project: approval: touch: true

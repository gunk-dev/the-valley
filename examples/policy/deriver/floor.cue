package verification
floor: {
  checks: {
    "c-docs": {runner:      "nix", attribute: "c-docs"}
    "c-notes": {runner:     "nix", attribute: "c-notes"}
    "c-flat": {runner:      "nix", attribute: "c-flat"}
    "c-deep": {runner:      "nix", attribute: "c-deep"}
    "c-nested": {runner:    "nix", attribute: "c-nested"}
    "c-uncovered": {runner: "nix", attribute: "c-uncovered"}
  }
  classes: {
    docs: {paths: "docs/**": true, requires: "c-docs": true}
    notes: {paths: "notes/**": true, requires: "c-notes": true}
    flat: {paths: "src/*.rs": true, requires: "c-flat": true}
    deep: {paths: "src/**/*.rs": true, requires: "c-deep": true}
    nested: {paths: "a/**/b": true, requires: "c-nested": true}
  }
  unclassified: "c-uncovered": true
}

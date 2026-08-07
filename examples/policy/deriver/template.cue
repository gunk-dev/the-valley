package verification
#tmpl: #Policy & {
  checks: "c-tmpl": {runner: "nix", attribute: "c-tmpl"}
  classes: docs: requires: "c-tmpl": bool | *true
}

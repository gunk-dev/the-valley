// Carry case 2. The template offers `same-class` as a default on two
// classes, and the project turns it off on one of them. The guarded class
// resolves to `same-paths`. The kept class, which the project leaves alone,
// keeps the template's default and resolves to `same-class`.
package verification

floor: checks: "c-build": {runner: "nix", attribute: "c-build"}

templates: service: classes: {
	guarded: {
		paths: "hooks/**":   bool | *true
		requires: "c-build": bool | *true
		carry: "same-class": bool | *true
	}
	kept: {
		paths: "gate/**":    bool | *true
		requires: "c-build": bool | *true
		carry: "same-class": bool | *true
	}
}
type: "service"

project: classes: guarded: carry: "same-class": false

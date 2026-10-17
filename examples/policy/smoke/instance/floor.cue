// The floor of the tiny policy `valley self-update` smoke-tests a build
// with (selfupdate/). The valley package ships this directory and its
// sibling project/ at share/valley/smoke-policy. A build passes only if its
// `valley checks --names` derives exactly smoke-floor and smoke-project
// from the two, for a change that edits README.md and adds src/main.go.
// Deriving them composes a policy under the schema the build carries.
// What these documents require is what the smoke test expects (smokeWant
// in selfupdate/selfupdate.go), so the two change together.
package verification

floor: {
	checks: "smoke-floor": {
		runner:    "nix"
		attribute: "smoke-floor"
	}
	classes: prose: {
		paths: "**/*.md":        true
		requires: "smoke-floor": true
	}
	unclassified: "smoke-floor": true
}

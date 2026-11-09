module the-valley/integrator

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/composed v0.0.0
	the-valley/derive v0.0.0
	the-valley/note v0.0.0
	the-valley/telemetry v0.0.0
)

// The change record is shared with the push hook (../valleyhook) and the
// valley command (../status), so the integrator reads a record exactly the
// way the hook admitted it and the command wrote it. The composed-policy
// reader (../composed) is shared with the restacker (../restack), so both
// read one composition of the policy, and the deriver (../derive) is the
// one reading of what that policy requires of a diff. The signed-note
// format (../note) is the one reading of a verifier key, which
// publish-signers holds the known-signers file to. A decision's timing is stored as a telemetry
// record (../telemetry) the collector serves. All five live in this
// repository and are never fetched.
replace (
	the-valley/change => ../change
	the-valley/composed => ../composed
	the-valley/derive => ../derive
	the-valley/note => ../note
	the-valley/telemetry => ../telemetry
)

// The deriver of required checks: which checks a change requires, from the
// composed policy and the paths the change's diff touches. The policy is
// read through the composed-policy reader (../composed) the integrator and
// the restacker share, so every reader reads one composition. Standard
// library only.
module the-valley/derive

go 1.24

require the-valley/composed v0.0.0

// The composed-policy reader lives in this repository and is never
// fetched.
replace the-valley/composed => ../composed

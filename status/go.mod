// Named for the program it builds rather than its directory: the verbs
// of the valley command that are written in Go. A binary called `status`
// on a PATH would not say whose status it reads, and this one writes
// change records too.
module the-valley/valley-go

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/composed v0.0.0
	the-valley/delta v0.0.0
	the-valley/derive v0.0.0
	the-valley/knot v0.0.0
	the-valley/rebase v0.0.0
	the-valley/selfupdate v0.0.0
)

// The change record is shared with the push hook (../valleyhook), so this
// program writes a record exactly the way the hook reads it, and so is the
// plan graph (../knot), so this program writes a graph exactly the way the
// hook validates it. valley-delta-v1 (../delta) is the one reading of a
// delta between two trees. The rebase engine (../rebase) rebases a family
// of stacked changes and reads how far each version has landed. Self-update
// (../selfupdate) installs the tip of a branch of the-valley. The deriver
// (../derive) says which checks a change requires, from the policy the
// composed-policy reader (../composed) reads, the reader the integrator and
// the restacker share. All seven live in this repository and are never
// fetched.
replace (
	the-valley/change => ../change
	the-valley/composed => ../composed
	the-valley/delta => ../delta
	the-valley/derive => ../derive
	the-valley/knot => ../knot
	the-valley/rebase => ../rebase
	the-valley/selfupdate => ../selfupdate
)

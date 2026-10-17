// Named for the program it builds rather than its directory: the verbs
// of the valley command that are written in Go. A binary called `status`
// on a PATH would not say whose status it reads, and this one writes
// change records too.
module the-valley/valley-go

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/delta v0.0.0
	the-valley/knot v0.0.0
	the-valley/rebase v0.0.0
)

// The change record is shared with the push hook (../valleyhook), so this
// program writes a record exactly the way the hook reads it, and so is the
// plan graph (../knot), so this program writes a graph exactly the way the
// hook validates it. valley-delta-v1 (../delta) is the one reading of a
// delta between two trees. The rebase engine (../rebase) rebases a family
// of stacked changes and reads how far each version has landed. All four
// live in this repository and are never fetched.
replace (
	the-valley/change => ../change
	the-valley/delta => ../delta
	the-valley/knot => ../knot
	the-valley/rebase => ../rebase
)

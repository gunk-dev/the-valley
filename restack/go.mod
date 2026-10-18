// The restacker: a host program that rebases ready changes onto main when
// main moves under them. This slice is its planner, a read-only pass. It
// reads origin through the rebase engine (../rebase), records with the one
// reading of a record the push hook shares (../change), and the cap on its
// automatic versions through the composed-policy reader it shares with
// the integrator (../composed). All three live in this repository and are
// never fetched.
module the-valley/restack

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/composed v0.0.0
	the-valley/rebase v0.0.0
)

replace (
	the-valley/change => ../change
	the-valley/composed => ../composed
	the-valley/rebase => ../rebase
)

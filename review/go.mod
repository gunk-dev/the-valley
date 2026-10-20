// Review records: the-valley/review/v1, a reviewer's judgment of one
// version of a change, kept append-only in project storage. It reads the
// change record a review is bound to with the one reading of a record the
// push hook shares (../change), and recomputes the delta a review is bound
// to with valley-delta-v1 (../delta), within a budget. Both live in this
// repository and are never fetched.
module the-valley/review

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/delta v0.0.0
)

replace (
	the-valley/change => ../change
	the-valley/delta => ../delta
)

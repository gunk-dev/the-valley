module the-valley/approval

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/note v0.0.0
)

// An approval travels in the note envelope every valley reader opens
// (../note), and names a change the way its record does (../change). Both
// live in this repository and are never fetched.
replace (
	the-valley/change => ../change
	the-valley/note => ../note
)

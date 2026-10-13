module the-valley/valleyhook

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/note v0.0.0
)

// The note envelope is shared with attest (../attest), so the hook reads a
// pushed note exactly the way a verifier does. The change record is shared
// with the valley command (../status), so the hook reads a pushed record
// exactly the way the command that wrote it does. Both live in this
// repository and are never fetched.
replace (
	the-valley/change => ../change
	the-valley/note => ../note
)

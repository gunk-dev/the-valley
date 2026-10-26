module the-valley/valleyhook

go 1.24

require (
	the-valley/change v0.0.0
	the-valley/exactjson v0.0.0
	the-valley/knot v0.0.0
	the-valley/note v0.0.0
)

// The note envelope is shared with attest (../attest), so the hook reads a
// pushed note exactly the way a verifier does. The change record is shared
// with the valley command (../status), so the hook reads a pushed record
// exactly the way the command that wrote it does. The plan graph is
// shared with the valley command too, so the hook validates a pushed graph
// by the rules the command wrote it by. The push policy is read with
// exactjson (../exactjson), which holds every key to the spelling of its
// field, as the identity compiler reads the registry. All four live in this
// repository and are never fetched.
replace (
	the-valley/change => ../change
	the-valley/exactjson => ../exactjson
	the-valley/knot => ../knot
	the-valley/note => ../note
)

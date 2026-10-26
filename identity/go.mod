module the-valley/identity

go 1.24

require (
	the-valley/exactjson v0.0.0
	the-valley/note v0.0.0
)

// The approval signers files are verifier keys, and the compiler reads
// them back with the note reader every verifier uses (../note). The
// registry's export is read with exactjson (../exactjson), which holds
// every key to the spelling of its field. Both live in this repository and
// are never fetched.
replace (
	the-valley/exactjson => ../exactjson
	the-valley/note => ../note
)

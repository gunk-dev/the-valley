module the-valley/identity

go 1.24

require (
	the-valley/approval v0.0.0
	the-valley/exactjson v0.0.0
	the-valley/note v0.0.0
)

require the-valley/change v0.0.0 // indirect

// The approval signers files are verifier keys, and the compiler reads
// them back with the note reader every verifier uses (../note). Each opens
// with the authority stamp the approval format defines (../approval),
// which names refs by the change record's branch rule (../change). The
// registry's export is read with exactjson (../exactjson), which holds
// every key to the spelling of its field. All four live in this repository
// and are never fetched.
replace (
	the-valley/approval => ../approval
	the-valley/change => ../change
	the-valley/exactjson => ../exactjson
	the-valley/note => ../note
)

module the-valley/identity

go 1.24

require the-valley/note v0.0.0

// The approval signers files are verifier keys, and the compiler reads
// them back with the note reader every verifier uses (../note). It lives
// in this repository and is never fetched.
replace the-valley/note => ../note

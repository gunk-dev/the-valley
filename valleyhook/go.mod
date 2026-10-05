module the-valley/valleyhook

go 1.24

require the-valley/note v0.0.0

// The note envelope is shared with attest (../attest), so the hook reads a
// pushed note exactly the way a verifier does. It lives in this repository
// and is never fetched.
replace the-valley/note => ../note

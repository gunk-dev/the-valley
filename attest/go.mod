module github.com/gunk-dev/the-valley/attest

go 1.23

require the-valley/note v0.0.0

// The note envelope is shared with the pre-receive hook (../valleyhook), so
// both read a note the same way. It lives in this repository and is never
// fetched.
replace the-valley/note => ../note

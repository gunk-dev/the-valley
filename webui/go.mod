// The web UI: what a valley host publishes to a web server, and what that
// server shows of it. Package export is the contract the publisher and the
// server share: the public export, its signed manifest, and the upload
// between them.
module the-valley/webui

go 1.24

require (
	the-valley/knot v0.0.0
	the-valley/note v0.0.0
)

// The plan graph's names and IDs are read by the rules the graph is
// written by (../knot), and the manifest is a signed note read the way
// every verifier reads one (../note). Both live in this repository and
// are never fetched.
replace (
	the-valley/knot => ../knot
	the-valley/note => ../note
)

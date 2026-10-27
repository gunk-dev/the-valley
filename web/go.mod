// The web UI: what a valley host publishes to a web server, and what that
// server shows of it. Package export is the contract the publisher and the
// server share: the public export, its signed manifest, and the upload
// between them. The publisher reads who may read a project through
// readaccess.
module the-valley/web

go 1.24

require (
	the-valley/knot v0.0.0
	the-valley/note v0.0.0
	the-valley/readaccess v0.0.0
)

require the-valley/exactjson v0.0.0 // indirect

// The plan graph's names and IDs are read by the rules the graph is
// written by (../knot), the manifest is a signed note read the way every
// verifier reads one (../note), and the two consents to read access are
// read and evaluated by ../readaccess. readaccess reads cue's exports with
// exactjson (../exactjson), and only the main module's replace applies,
// so this one names it too. All four live in this repository and are
// never fetched.
replace (
	the-valley/exactjson => ../exactjson
	the-valley/knot => ../knot
	the-valley/note => ../note
	the-valley/readaccess => ../readaccess
)

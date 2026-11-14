module the-valley/valleyhook

go 1.24

require (
	the-valley/approval v0.0.0
	the-valley/change v0.0.0
	the-valley/exactjson v0.0.0
	the-valley/knot v0.0.0
	the-valley/note v0.0.0
	the-valley/telemetry v0.0.0
)

// The note envelope is shared with attest (../attest), so the hook reads a
// pushed note exactly the way a verifier does. An approval's ref and note
// are read with the approval format (../approval), which the client that
// writes one and every reader of one share. The change record is shared
// with the valley command (../status), so the hook reads a pushed record
// exactly the way the command that wrote it does. The plan graph is
// shared with the valley command too, so the hook validates a pushed graph
// by the rules the command wrote it by. The push policy is read with
// exactjson (../exactjson), which holds every key to the spelling of its
// field, as the identity compiler reads the registry. The telemetry
// command's grammar (../telemetry) is shared with the collector, so the
// shell passes on exactly what the collector accepts. All six live in
// this repository and are never fetched.
replace (
	the-valley/approval => ../approval
	the-valley/change => ../change
	the-valley/exactjson => ../exactjson
	the-valley/knot => ../knot
	the-valley/note => ../note
	the-valley/telemetry => ../telemetry
)

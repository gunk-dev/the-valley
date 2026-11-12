module the-valley/attest

go 1.23

require (
	the-valley/note v0.0.0
	the-valley/telemetry v0.0.0
)

// The note envelope is shared with the pre-receive hook (../valleyhook), so
// both read a note the same way, and the written form of a statement and
// the valley-tree-v1 scheme with the rebase engine (../rebase), which
// verifies transfer statements with them (../note/statement). A run's progress is recorded in the
// telemetry streams (../telemetry) the collector reads. Both live in this
// repository and are never fetched.
replace (
	the-valley/note => ../note
	the-valley/telemetry => ../telemetry
)

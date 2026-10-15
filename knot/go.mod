// The plan graph: a project's plan kept as data at refs/the-valley/knots.
// The push hook (../valleyhook) and the valley command (../status) both
// read and validate the graph through this module, so the command writes
// exactly what the hook admits.
module the-valley/knot

go 1.24

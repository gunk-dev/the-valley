// Named for the program it builds rather than its directory: the verbs
// of the valley command that are written in Go. A binary called `status`
// on a PATH would not say whose status it reads, and this one writes
// change records too.
module the-valley/valley-go

go 1.24

require the-valley/change v0.0.0

// The change record is shared with the push hook (../valleyhook), so this
// program writes a record exactly the way the hook reads it. It lives in
// this repository and is never fetched.
replace the-valley/change => ../change

module the-valley/integrator

go 1.24

require the-valley/change v0.0.0

// The change record is shared with the push hook (../valleyhook) and the
// valley command (../status), so the integrator reads a record exactly the
// way the hook admitted it and the command wrote it. It lives in this
// repository and is never fetched.
replace the-valley/change => ../change

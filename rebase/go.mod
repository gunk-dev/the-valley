// The rebase engine: a family of stacked changes, read from one snapshot
// of origin, rebased onto main in a throwaway worktree, and published as
// one atomic branch push and one record append per change. valley-go
// (../status) runs it for valley rebase and review's [b]ase, and it reads
// how far each version has landed for valley-go's listings too.
module the-valley/rebase

go 1.24

require the-valley/change v0.0.0

// The change record is shared with the push hook (../valleyhook), so the
// engine writes a record exactly the way the hook reads it. It lives in
// this repository and is never fetched.
replace the-valley/change => ../change

// valley-delta-v1: the change between two git trees, as whole files. The
// valley command (../status) compares a version under review with its
// approved version through this module, and the review module (../review)
// recomputes the delta a review is bound to through it, within a budget.
// It lives in this repository and is never fetched.
module the-valley/delta

go 1.24

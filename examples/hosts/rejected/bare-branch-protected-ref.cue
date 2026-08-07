// A protected ref written as a branch name. Patterns are matched against
// the full refname, so this one would protect nothing.
package valley

projects: ok: protection: {
	refs: ["main"]
	writers: ["integrator"]
}

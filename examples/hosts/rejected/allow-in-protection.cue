// Grants are the project's, not its protection's. A protection block names
// protected refs and their writers and nothing else, so a grant written
// inside it is an unknown field.
package valley

projects: ok: protection: allow: [{
	refs: ["refs/tags/*"]
	writers: ["operator"]
}]

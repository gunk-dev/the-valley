// A grant naming a namespace closed to every push. Replacement refs take no
// push whatever a declaration says, and the hook refuses them before it
// reads any grant, so this declaration would read as a grant that never
// applies. The schema refuses it instead.
package valley

projects: ok: grants: replace: {
	refs: ["refs/replace/*"]
	writers: ["operator"]
}

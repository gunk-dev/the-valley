// An allow entry naming a namespace whose rule is fixed. Replacement refs
// are closed to every push, and the hook refuses them before it reads any
// allow entry, so this declaration would read as an opening that never
// applies. The schema refuses it instead.
package valley

projects: ok: protection: allow: [{
	refs: ["refs/replace/*"]
	writers: ["operator"]
}]

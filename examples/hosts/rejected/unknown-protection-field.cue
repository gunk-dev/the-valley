// Policy beyond the one structural invariant. Which checks a change must
// carry is the integrator's, and #Protection is closed so it has nowhere to
// land here.
package valley

projects: ok: protection: {
	writers: ["integrator"]
	requires: "attestation"
}

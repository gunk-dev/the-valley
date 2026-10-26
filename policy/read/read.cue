// Who may read the-valley (schema/read.cue): anyone, and the operator. A
// reader reads it at an edge only where the owning instance's identity
// registry grants that reader read of the-valley there too.
package read

project:  "the-valley"
identity: "d5a595d4685886620547e84e49aee730"
readers: {
	anyone:   true
	operator: true
}

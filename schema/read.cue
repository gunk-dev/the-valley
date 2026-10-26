// A project's consent to read access: who the project lets read it.
//
// Read access is a grant to a principal, and it takes two consents. A
// project names its readers here, in policy/read/ on its main. The
// instance's identity registry (schema/identity.cue) grants each reader read
// at an edge read boundary, for this project or for every project. A
// principal reads a project only where both say so. The reader `anyone` is
// the reader who presents no credential: a project is public where both
// consents let `anyone` read it.
//
// Every reader is named, the operator included. A project the operator
// reads names the operator here, as it names any other reader. Membership,
// push access and approval give no read access.
//
// The policy names the project it is for, and the project's publication
// identity. A policy read from another project grants nothing there. An
// instance grant that names this project names this identity too, so a new
// project that reuses the name does not inherit the old project's grants.
//
// The policy lives in policy/read/, beside and not inside policy/instance/
// and policy/project/. Those two directories are verification-policy
// layers, which schema/verification.cue closes to its own fields.
//
//   Validate a policy:
//     cue vet -c schema/read.cue policy/read/*.cue
package read

import "strings"

// project is the name of the project this policy is for: the name the
// instance lists it under.
project!: #ProjectName

// identity is the project's publication identity: 32 lowercase hex digits,
// chosen at random when the project's policy/read is first written, and
// never changed.
identity!: #Identity

// readers is the set of principals the project lets read it. An empty set
// lets no one read. close keeps out a key that does not match #Principal:
// a pattern constrains only the keys it matches, and a struct that is not
// closed takes any other key unconstrained.
readers!: close({[#Principal]: true})

// #ProjectName is a project's name, as schema/valley.cue names projects.
#ProjectName: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$" & strings.MaxRunes(64)

// #Identity is a publication identity.
#Identity: =~"^[0-9a-f]{32}$"

// #Principal is a principal's name, as schema/identity.cue names
// principals. `anyone` is the reader who presents no credential.
#Principal: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"

// A file's top level cannot be closed, so a stray top-level field, such as
// a `reader:` typo, conflicts with this sentinel and fails vet with an
// error naming the field.
[!="project" & !="identity" & !="readers"]: "INVALID: unknown top-level field; only \"project\", \"identity\" and \"readers\" are allowed"

// The web visibility of valley projects: who may read a project's plan
// graph and source on the web server a valley host publishes to.
//
// Two documents decide it, and both must say public. A project declares its
// own visibility in policy/web/ on its main. The instance repository's
// policy/web/ names the projects the instance lets the web read. A project
// is exported publicly only when its own declaration and the instance's
// both say public. A declaration that is missing, or that this schema
// refuses, means private.
//
// The instance repository is also a project, so its policy/web/ can carry
// both blocks: `instance` for what the instance allows, and `project` for
// the instance repository's own visibility. The publisher reads `instance`
// from the instance repository alone, and ignores it anywhere else.
//
// The declarations live in policy/web/, beside and not inside
// policy/instance/ and policy/project/. Those two directories are
// verification-policy layers, which schema/verification.cue closes to its
// own fields.
//
//   Validate a declaration:
//     cue vet -c schema/web.cue policy/web/*.cue
package web

import "strings"

// #Visibility is who may read a project on the web. public is anyone;
// private is no one.
#Visibility: "public" | "private"

// #Declaration is one project's visibility.
#Declaration: {
	visibility: #Visibility
}

// #Name is a project's name, as schema/valley.cue names projects.
#Name: =~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$" & strings.MaxRunes(64)

// project is what a repository declares of itself.
project?: #Declaration

// instance is what the instance repository allows.
instance?: #Instance

// #Instance names the projects the web may read, each with the visibility
// the instance allows it. A project it does not name is private.
#Instance: {
	projects: [#Name]: #Declaration
}

// A file's top level cannot be closed, so a stray top-level field, such as
// a `visibilty:` typo, conflicts with this sentinel and fails vet with an
// error naming the field.
[!="project" & !="instance"]: "INVALID: unknown top-level field; only \"project\" and \"instance\" are allowed"

// A host declaration with nothing but a project: no mirrors, no backup, no
// protection. Every optional field is absent, which is the compatibility
// case — a declaration written before a field existed must evaluate exactly
// as it did then. The flake's module-eval check holds the rendered host to
// that, and cue-vet holds this file to the schema.
package valley

projects: "the-valley": {}

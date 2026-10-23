# The deriver of required checks (derive/), and the model of the composed
# policy it derives from (composed/), held by the deriver's own tests.
{
  packages,
  ...
}:
{
  # Run with go vet: the glob language by example, a diff's paths read
  # from git diff -z and every cut of it refused, and the deriver's rules
  # over a policy written in the test.
  # Then the deriver held to bin/valley's: both run over the repository's
  # own policy, the example policies and paths drawn from a fixed seed,
  # with bin/valley under the C, C.UTF-8 and en_US.UTF-8 locales and the
  # layers in directories named like glob patterns. Every global of
  # bin/valley's derivation must equal the Go answer: the documents
  # composed, the changed paths, the classes, the carry mode, the
  # unclassified paths, and each required check's classes, pin, runner,
  # attribute, command and cost. Each glob is held to glob_ere and each diff
  # reading to diff_paths, run by bash out of bin/valley itself. Both refuse
  # what cue vet rejects, a schema older than either, and a layer with no
  # document that bash's glob names. Where bin/valley is wrong
  # (bd-11e0e4d), the test records what each answers: a path holding a
  # newline, a class added by refining policy that requires an undefined
  # check, and a class named unclassified.
  derive-unit = packages.derive.unit;
}

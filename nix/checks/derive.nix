# The deriver of required checks (derive/), and the model of the composed
# policy it derives from (composed/), held by the deriver's own tests.
{
  packages,
  ...
}:
{
  # Run in the package's checkPhase with go vet: the glob language by
  # example, a diff's paths read from git diff -z and every cut of it
  # refused, and the deriver's rules over a policy written in the test.
  # Then the deriver held to bin/valley's: both run over the repository's
  # own policy, the example policies and paths drawn from a fixed seed, and
  # each must require the same checks, for the same classes, with the same
  # pins and costs, in the same carry mode, with the same paths
  # unclassified. Each glob is held to glob_ere and each diff reading to
  # diff_paths, run by bash out of bin/valley itself. Both refuse what cue
  # vet rejects and a schema older than either. Where bin/valley is wrong
  # (bd-11e0e4d), the test records what each answers: a path holding a
  # newline, a class added by refining policy that requires an undefined
  # check, and a class named unclassified.
  derive-unit = packages.derive;
}

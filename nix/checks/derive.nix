# The deriver of required checks (derive/), and the composed-policy reader
# it derives from (composed/), each held by its own tests.
{
  packages,
  ...
}:
{
  # Run with go vet: the glob language by example, a diff's paths read
  # from git diff -z and every cut of it refused, and the deriver's rules
  # over a policy written in the test. Then the deriver over the
  # repository's own policy and the example policies, with layers in
  # directories named like glob patterns, and changes drawn from a fixed
  # seed: each answer is held to the paths whose entries the change
  # differs in, the classes their globs match, and the checks those
  # classes and the unclassified set require. Paths are bytes, a path
  # holding a newline is one path, a class named unclassified is kept apart
  # from the unclassified set, and the reader refuses what cue vet rejects,
  # a schema older than the deriver, a layer with no document, and a class
  # added at `policy` that requires an undefined check.
  derive-unit = packages.derive.unit;

  # Run with go vet: a layer read out of real repositories as present,
  # absent or refused, with directories named like patterns, and with the
  # objects of a tip, a layer's tree or a document removed.
  composed-unit = packages.composed.unit;
}

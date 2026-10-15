# The plan graph's module (knot/): its own tests, and its validator held to
# the CUE schema and the knowledge lint on every rule the two share. What
# the CLI does with it is valley-knot's, in cli.nix.
{
  pkgs,
  packages,
  ...
}:
{
  # The canonical form's parser, cycles, transitions and every limit at
  # its edge, run in the package's checkPhase.
  knot-unit = packages.knot;

  # The Go validator is the authority for refs/the-valley/knots, and
  # schema/node.cue with nix/knowledge-lint.py is the one documented schema
  # the archive shares. Both are run over the shared fixtures and over
  # graphs generated from a fixed seed, and any graph on which either
  # verdict differs from the case's fails the check. The lint and the
  # schema are the same store paths lib.knowledgeLint uses.
  knot-agree = pkgs.runCommand "valley-knot-agree" {
    nativeBuildInputs = [
      pkgs.cue
      pkgs.python3
      packages.knot
    ];
    lint = ../knowledge-lint.py;
    nodeSchema = ../../schema/node.cue;
  } (builtins.readFile ./knot-agree.sh);
}

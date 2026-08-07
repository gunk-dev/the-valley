# The knowledge graph and the prose: this repo's own graph, the lint in the
# reusable form a consuming project instantiates, the graphs it must reject,
# and the format every markdown file is held to.
{
  pkgs,
  self,
  system,
  packages,
  ...
}:
{
  # This repo's own graph, checked by the derivation the flake
  # exposes rather than by a second implementation of it. Whatever a
  # consuming project runs, this repo runs.
  knowledge-lint = self.lib.knowledgeLint {
    inherit system;
    src = self;
  };

  # The reusable form, exercised over a foreign tree: a project that
  # is a README, a docs directory and five nodes, and a project with no
  # graph at all. Both must pass, and both are checked by depending on
  # the instantiated derivations rather than by re-running the lint.
  knowledge-lint-consumer = pkgs.runCommand "valley-knowledge-lint-consumer" {
    consumer = self.lib.knowledgeLint {
      inherit system;
      src = ../../examples/graphs/consumer;
    };
    graphless = self.lib.knowledgeLint {
      inherit system;
      src = ../../examples/graphs/graphless;
    };
  } (builtins.readFile ./knowledge-lint-consumer.sh);

  # The lint must fail on a broken graph, and its failure must name
  # the file and the defect — a check that only says "no" leaves the
  # reader to find the breakage themselves. The script and the schema
  # here are the same store paths lib.knowledgeLint uses, so the
  # rejections cannot drift from what a consumer runs.
  knowledge-lint-rejects = pkgs.runCommand "valley-knowledge-lint-rejects" {
    nativeBuildInputs = [
      pkgs.cue
      pkgs.python3
    ];
    lint = ../knowledge-lint.py;
    nodeSchema = ../../schema/node.cue;
    graphs = ../../examples/graphs;
    rejectedCases = ./knowledge-lint-rejected.txt;
  } (builtins.readFile ./knowledge-lint-rejects.sh);

  # Markdown prose format (ida-1ec03b1): the check is formatter
  # idempotence — formatting the tree must change nothing. On failure,
  # fix with `nix run .#fmt`.
  prose-format = pkgs.runCommand "valley-prose-format" {
    nativeBuildInputs = [ pkgs.prettier ];
    tree = self;
    inherit (packages) proseFmtArgs;
  } (builtins.readFile ./prose-format.sh);
}

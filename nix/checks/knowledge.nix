# The knowledge lint and the prose: the lint in the reusable form a consuming
# project instantiates, the graphs it must reject, and the format every
# markdown file is held to. the-valley's own source holds no knowledge graph:
# its plan graph is project storage, at refs/the-valley/knots, and the push
# hook holds it to the knot module's rules. The nodes under
# archive/.the-valley/ are history, and nothing lints them.
{
  pkgs,
  self,
  system,
  packages,
  ...
}:
{
  # The lint over the consumer graph, under the name main's policy still
  # requires. A change's required checks come from its target's policy,
  # and the policy on main declares knowledge-lint, so a change to main
  # needs this attribute until that policy stops declaring it.
  knowledge-lint = self.lib.knowledgeLint {
    inherit system;
    src = ../../examples/graphs/consumer;
  };

  # The reusable form, exercised over a foreign tree: a project that
  # is a README, a docs directory and seven nodes, and a project with no
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

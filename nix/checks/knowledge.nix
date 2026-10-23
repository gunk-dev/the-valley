# The knowledge lint and the prose: the lint in the reusable form a consuming
# project instantiates, the graphs it must reject, and the format every
# markdown file is held to. the-valley's own source holds no knowledge graph:
# its plan graph is project storage, at refs/the-valley/knots, and the push
# hook holds it to the knot module's rules. The nodes under
# archive/.the-valley/ are history, and nothing lints them.
{
  pkgs,
  lib,
  self,
  system,
  packages,
  partOf,
  ...
}:
let
  root = ../..;

  # What prettier reads: every markdown file, and the files it takes its
  # configuration and its list of files to ignore from.
  prettierReads = lib.fileset.fileFilter (
    f:
    f.hasExt "md"
    || builtins.elem f.name [
      ".editorconfig"
      ".gitignore"
      ".prettierignore"
      "package.json"
      "package.yaml"
    ]
    || lib.hasPrefix ".prettierrc" f.name
    || lib.hasPrefix "prettier.config." f.name
  ) root;

  # The documents whose links prose-format.sh resolves, and their text.
  linking =
    lib.filter builtins.pathExists [
      (root + "/README.md")
      (root + "/AGENTS.md")
    ]
    ++ map (n: root + "/docs/${n}") (
      lib.filter (lib.hasSuffix ".md") (builtins.attrNames (builtins.readDir (root + "/docs")))
    );
  linkingText = lib.concatMapStrings builtins.readFile linking;

  # Every directory and file of the tree, as a path relative to its root,
  # with a slash after each directory. A checkout's .git is no part of the
  # source, and is skipped where a path flake shows it.
  entries =
    dir: prefix:
    lib.concatLists (
      lib.mapAttrsToList (
        name: type:
        if type == "directory" then
          [ "${prefix}${name}/" ] ++ entries (dir + "/${name}") "${prefix}${name}/"
        else
          [ "${prefix}${name}" ]
      ) (removeAttrs (builtins.readDir dir) [ ".git" ])
    );

  # The link check asks only whether a link's target exists, and a link
  # names its target's path, so the target's name is in the linking
  # documents' text. The tree's other files and directories whose names are
  # in that text are given by path alone, and stand empty in the tree the
  # check reads. Every link resolves as it does in the source, and an edit
  # to one of those files changes nothing here.
  linkTargets = lib.filter (e: !(lib.hasSuffix ".md" e) && lib.hasInfix (baseNameOf e) linkingText) (
    entries root ""
  );
in
{
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
    graphs = partOf ../../examples/graphs [ ../../examples/graphs/rejected ];
    rejectedCases = ./knowledge-lint-rejected.txt;
  } (builtins.readFile ./knowledge-lint-rejects.sh);

  # Markdown prose format (ida-1ec03b1): formatter idempotence —
  # formatting the tree must change nothing — and, across README.md,
  # AGENTS.md and docs/*.md, relative link and heading-anchor integrity
  # and the style rules of AGENTS.md that a pattern can catch.
  # On formatting failure, fix with `nix run .#fmt`.
  #
  # It reads every markdown file in the tree, archives and fixtures
  # included, and prettier's configuration. Other files are link targets
  # only, given by path.
  prose-format = pkgs.runCommand "valley-prose-format" {
    nativeBuildInputs = [
      pkgs.gawk
      pkgs.prettier
      pkgs.python3
    ];
    markdown = lib.fileset.toSource {
      inherit root;
      fileset = prettierReads;
    };
    linkTargets = lib.concatLines linkTargets;
    passAsFile = [ "linkTargets" ];
    inherit (packages) proseFmtArgs;
  } (builtins.readFile ./prose-format.sh);
}

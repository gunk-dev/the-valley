# The knowledge lint as a derivation over one project tree: frontmatter vetted
# against schema/node.cue, filename coherence, and reference integrity
# (ida-1ec03b1). The tree is an argument, so the same derivation checks this
# repo's graph and any other project's.
#
# The lint itself is a Python script rather than Nix: it walks markdown and
# resolves paths, and Nix is the wrong language for both. Nix supplies the tree
# and the toolchain and nothing else.
#
# Instantiate through the flake's `lib.knowledgeLint` rather than importing this
# file directly — that entry point is what a consuming project gets from a flake
# input, and it pins the toolchain and the schema for it.
{
  pkgs,
  src,
  root ? ".the-valley",
  name ? "knowledge-lint",
}:

pkgs.runCommand name
  {
    nativeBuildInputs = [
      pkgs.cue
      pkgs.python3
    ];
  }
  ''
    mkdir -p work
    python3 ${./knowledge-lint.py} \
      --tree ${src} \
      --root "${root}" \
      --schema ${../schema/node.cue} \
      --work work
    touch $out
  ''

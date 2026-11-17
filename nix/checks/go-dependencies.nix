# The Go dependency boundary (AGENTS.md, "Go dependencies"): every Go
# module imports only the standard library and this repository's modules,
# and only flow/ imports third-party modules.
{
  pkgs,
  lib,
  packages,
  ...
}:
let
  root = ../..;

  # Its tests run in its unit build, against fixture trees.
  checker = packages.goModule {
    pname = "valley-go-dependencies";
    dir = ./go-dependencies-checker;
    tests.postCheck = ''
      go vet ./...
    '';
  };

  # What the checker reads, and what it must see to refuse: every Go file,
  # go.mod and go.work, every file under a vendor directory, and every
  # symlink, whatever it names.
  goSource = lib.fileset.fromSource (
    lib.cleanSourceWith {
      src = root;
      filter =
        path: type:
        type == "directory"
        || type == "symlink"
        || lib.hasSuffix ".go" path
        || builtins.elem (baseNameOf path) [
          "go.mod"
          "go.work"
        ]
        || lib.hasInfix "/vendor/" path;
    }
  );
in
{
  # Scans the tree, without building or testing any module in it: each Go
  # file's imports, whatever its build constraints, and each go.mod's
  # module path, requires, replacements and tools, with the standard
  # library as the pinned toolchain lists it. A workspace, a vendor
  # directory, a go.mod or Go file that does not parse, and a symlink
  # leaving the tree or giving a Go file's name to another file are
  # refused. Every refusal names its file and its import or directive.
  #
  # It holds the committed tree: Go source or a go.mod that a build phase
  # generates or rewrites after the scan is outside it. It runs on changes to
  # Go source, module and workspace manifests, and anything named vendor
  # (policy/project/policy.cue); a change that adds or changes only another
  # symlink or non-Go path, retargeting a symlink included, is not judged
  # until a later change touches one of those. Crossing the boundary takes an
  # import, a module directive or a vendor entry, and all three are in paths
  # whose changes run it.
  go-dependencies = pkgs.runCommand "valley-go-dependencies" {
    nativeBuildInputs = [
      pkgs.go
      checker.unit
    ];
    tree = lib.fileset.toSource {
      inherit root;
      fileset = goSource;
    };
  } (builtins.readFile ./go-dependencies.sh);
}

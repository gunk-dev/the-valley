# flow (flow/): its shipping packages, its unit tests, go vet, and its
# end-to-end suite, which drives the real binary against real git and a
# private tmux server.
{
  pkgs,
  lib,
  self,
  system,
  packages,
  ...
}:
let
  inherit (packages) valley-flow version;

  # What the checks verify beside klaus's tests: the version the package
  # reports, and that no test reached tmux by default. Its own tests run in
  # flow-build, which builds its unit form.
  checker = packages.goModule {
    pname = "klaus-flow-checker";
    dir = ./flow-checker;
    tests.postCheck = ''
      go vet ./...
    '';
    meta.mainProgram = "flow-checker";
  };

  # The package rebuilt over the whole module, tests included, with the
  # package's own vendored modules, running script in place of the build.
  # It carries no version stamp: no test reads one. Test file paths stay
  # real, as in buildGoModule's own check phase, so a test finds what it
  # reads beside its source.
  flowCheck =
    name:
    {
      inputs ? [ ],
      script,
    }:
    valley-flow.overrideAttrs (old: {
      pname = "klaus-${name}";
      version = "0";
      ldflags = [ ];
      src = packages.flowComplete;
      inherit (valley-flow) goModules;
      nativeBuildInputs = old.nativeBuildInputs ++ inputs;
      buildPhase = ''
        runHook preBuild
        export GOFLAGS=''${GOFLAGS//-trimpath/}
        ${script}
        runHook postBuild
      '';
      doCheck = false;
      installPhase = "touch $out";
      dontFixup = true;
    });

  # tmux finds a server through $TMUX or a socket under $TMUX_TMPDIR. Both
  # stay inside the build's temporary directory, as do HOME and the XDG
  # directories, the runtime one included, so no test reads or reaches
  # anything of the builder's. Each test starts its own server on its own
  # socket; a socket left under TMUX_TMPDIR afterwards is a server some test
  # reached by default, and fails the check. A test that cannot find a tool
  # it needs fails rather than skipping.
  isolated = script: ''
    unset TMUX TMUX_PANE
    export TMUX_TMPDIR="$TMPDIR/tmux" HOME="$TMPDIR/home" XDG_RUNTIME_DIR="$TMPDIR/run"
    export XDG_CONFIG_HOME="$HOME/.config" XDG_STATE_HOME="$HOME/.local/state"
    export XDG_CACHE_HOME="$HOME/.cache" XDG_DATA_HOME="$HOME/.local/share"
    mkdir -p "$TMUX_TMPDIR" "$HOME"
    mkdir -m 700 "$XDG_RUNTIME_DIR"
    export KLAUS_TEST_TOOLS=required
    ${script}
    flow-checker sockets "$TMUX_TMPDIR"
  '';

  # The flake's packages of the client, and the systems it offers them for.
  flowPackages = [
    "valley-flow"
    "klaus"
  ];
  flowSystems = [
    system
    "aarch64-darwin"
  ];

  # What is wrong with the client's package attr on system s: a store path
  # not named <attr>-<version>, which nix profile would install as an
  # element of another name, or a mainProgram other than the executable
  # named attr. Reading names evaluates the Darwin packages and builds
  # nothing for Darwin.
  identityFaults =
    s: attr:
    let
      pkg = self.packages.${s}.${attr};
      main = pkg.meta.mainProgram or "none";
    in
    lib.optional (pkg.name != "${attr}-${version}" || main != attr)
      "packages.${s}.${attr} is ${pkg.name} with mainProgram ${main}, not ${attr}-${version} with ${attr}";
  identity = lib.concatMap (s: lib.concatMap (identityFaults s) flowPackages) flowSystems;
in
{
  # The packages as they ship, and the version they report: the CLI's,
  # stamped through the-valley/flow/internal/cmd.version. Each of
  # valley-flow and klaus holds the one program as bin/klaus, which prints
  # it as klaus, which an installed self-update's smoke test reads, and as
  # bin/valley-flow, which names itself valley flow, as does the valley
  # package's `valley flow`, which runs valley-flow from PATH. A package
  # away from its identity fails the check's evaluation.
  flow-build =
    if identity != [ ] then
      throw "flow-build: ${lib.concatStringsSep "; " identity}"
    else
      pkgs.runCommand "klaus-flow-build"
        {
          nativeBuildInputs = [
            self.packages.${system}.valley-flow
            packages.valley
            checker.unit
          ];
          packages = map (attr: self.packages.${system}.${attr}) flowPackages;
        }
        ''
          for p in $packages; do
            flow-checker version "klaus version ${version}" "$p/bin/klaus"
            flow-checker version "valley flow version ${version}" "$p/bin/valley-flow"
          done
          flow-checker version "valley flow version ${version}" valley flow
          touch $out
        '';

  # The unit tests. TestFlakeRefSurvivesNix has nix parse the flake
  # references self-update builds, which needs no store: the dummy store
  # serves an evaluation. It takes the newest nix, because nix 2.31, the
  # pinned default, refuses git+git:// references. ps and lsof are how
  # klaus reads processes where there is no /proc, as on macOS, and the
  # tests read real ones with them here.
  flow-unit = flowCheck "unit" {
    inputs = [
      checker
      pkgs.git
      pkgs.tmux
      pkgs.nixVersions.latest
      pkgs.procps
      pkgs.lsof
    ];
    script = isolated ''
      export NIX_CONFIG="store = dummy://"
      go test ./...
    '';
  };

  flow-vet = flowCheck "vet" {
    script = "go vet ./...";
  };

  # The end-to-end suite, with the valley command its restack and landing
  # scenarios run. It is the unstamped valley: those scenarios read no
  # version.
  flow-e2e = flowCheck "e2e" {
    inputs = [
      checker
      pkgs.git
      pkgs.tmux
      pkgs.bash
      packages.valley-unstamped
    ];
    script = isolated ''
      go test -tags e2e ./e2e/...
    '';
  };
}

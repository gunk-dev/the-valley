# klaus (flow/): its shipping package, its unit tests, go vet, and its
# end-to-end suite, which drives the real binary against real git and a
# private tmux server.
{
  pkgs,
  lib,
  packages,
  ...
}:
let
  inherit (packages) klaus version;

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
    klaus.overrideAttrs (old: {
      pname = "klaus-${name}";
      version = "0";
      ldflags = [ ];
      src = packages.flowComplete;
      inherit (klaus) goModules;
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
in
{
  # The package as it ships, and the version it reports: the CLI's,
  # stamped through the-valley/flow/internal/cmd.version. bin/klaus prints
  # it as klaus, which an installed self-update's smoke test reads; the
  # same binary run as bin/valley-flow names itself valley flow, and so
  # does the valley package's `valley flow`, which runs valley-flow from
  # PATH.
  flow-build =
    pkgs.runCommand "klaus-flow-build"
      {
        nativeBuildInputs = [
          klaus
          packages.valley
          checker.unit
        ];
      }
      ''
        flow-checker version "klaus version ${version}" ${klaus}/bin/klaus
        flow-checker version "valley flow version ${version}" ${klaus}/bin/valley-flow
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

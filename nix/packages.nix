# Everything this flake builds: the CLI, the formatter, the attestation
# helper, the integrator. The flake wires these into packages, apps, and the
# checks that drive them; nothing here knows about any of those outputs.
{ pkgs, lib }:
rec {
  # Markdown prose is filled paragraphs hard-wrapped at 100 columns
  # (ida-1ec03b1). One flag string backs both the formatter app and the
  # prose-format check, so the two cannot drift. Embedded-language
  # formatting is off so fenced code blocks pass through byte-for-byte.
  proseFmtArgs = "--prose-wrap always --print-width 100 --embedded-language-formatting off";

  # The integrator's CLI (bin/valley) wrapped for `nix run`. The script
  # itself must keep running bare from any checkout — the package is the
  # second of its two shipping modes, never a dependency of the first.
  valley-script = pkgs.writeShellApplication {
    name = "valley";
    runtimeInputs = [
      pkgs.git
      pkgs.natscli # tail and replay only; every other verb needs just git
      pkgs.cue # checks only — it composes the policy layers
      pkgs.less # review only — its v key is what takes a note
    ];
    text = builtins.readFile ../bin/valley;
  };

  # The installed package: the wrapped script plus the shell completions,
  # at the standard paths home-manager/NixOS auto-link.
  valley = pkgs.symlinkJoin {
    name = "valley";
    paths = [ valley-script ];
    nativeBuildInputs = [ pkgs.installShellFiles ];
    postBuild = ''
      installShellCompletion --bash --name valley ${../completions/valley.bash}
      installShellCompletion --zsh --name _valley ${../completions/_valley}
    '';
  };

  # `nix run .#fmt` — rewrap every tracked *.md in the repo to 100 columns.
  prose-fmt = pkgs.writeShellApplication {
    name = "valley-fmt";
    runtimeInputs = [
      pkgs.git
      pkgs.prettier
    ];
    text = ''
      cd "$(git rev-parse --show-toplevel)"
      git ls-files -z -- '*.md' | xargs -0 --no-run-if-empty \
        prettier ${proseFmtArgs} --write
    '';
  };

  # The Phase 2 attestation helper (dcr-0de694f). Go, standard library
  # only, note format included — hence vendorHash = null, and hence no
  # module fetch at build time. Its unit tests run in the checkPhase and
  # need git and ssh-keygen.
  attest-unwrapped = pkgs.buildGoModule {
    pname = "valley-attest";
    version = "0";
    src = ../attest;
    vendorHash = null;
    nativeCheckInputs = [
      pkgs.git
      pkgs.openssh
    ];
    meta.mainProgram = "attest";
  };

  # The shipping form. git and cue are pinned to this flake's nixpkgs,
  # so every host composes and vets a statement with the same tools.
  # Signing needs no tool at all: a note is signed on crypto/ed25519
  # inside the binary. `nix` is deliberately NOT pinned: the check being
  # attested to must be built by the nix the machine actually runs, and
  # a nix carried in here would be a second one.
  attest =
    pkgs.runCommand "valley-attest"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "attest";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe attest-unwrapped} $out/bin/attest \
          --prefix PATH : ${
            lib.makeBinPath [
              pkgs.git
              pkgs.cue
            ]
          } \
          --set-default VALLEY_ATTEST_SCHEMA ${../schema/attestation.cue}
      '';

  # The Phase 3 integrator (dcr-439b771). Go, standard library only,
  # same trade as attest — hence vendorHash = null and no module fetch.
  integrator-unwrapped = pkgs.buildGoModule {
    pname = "valley-integrator";
    version = "0";
    src = ../integrator;
    vendorHash = null;
    meta.mainProgram = "integrator";
  };

  # The shipping form. The integrator drives four programs rather than
  # reimplementing what they do: attest judges every statement and
  # every note, the valley deriver and cue compose the policy, and git
  # holds the requests and the refs. All four are pinned to this
  # flake's nixpkgs so an instance's integrators agree; `nix` is
  # deliberately absent for the same reason it is absent from attest —
  # a closure must be recomputed by the nix the machine actually runs.
  integrator =
    pkgs.runCommand "valley-integrator"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "integrator";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe integrator-unwrapped} $out/bin/integrator \
          --prefix PATH : ${
            lib.makeBinPath [
              pkgs.git
              pkgs.cue
              pkgs.natscli
              attest
              valley-script
            ]
          } \
          --set-default VALLEY_ATTEST_SCHEMA ${../schema/attestation.cue} \
          --set-default VALLEY_VERIFICATION_SCHEMA ${../schema/verification.cue} \
          --set-default VALLEY_EVENT_SCHEMA ${../schema/events.cue}
      '';
}

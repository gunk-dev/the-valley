# Everything this flake builds: the CLI, the formatter, the attestation
# helper, the integrator, the identity compiler, the pre-receive hook's
# policy, the security-key signature verifier. The flake wires these into packages, apps, and the checks that
# drive them; nothing here knows about any of those outputs.
{ pkgs, lib }:
rec {
  # The source of a Go program that reads signed notes: its own directory
  # and the note module (../note), which its go.mod replaces with that local
  # directory. One reading of the envelope is shared by the programs that
  # write, verify and admit attestations, and none of them fetches it.
  withNote =
    dir:
    lib.fileset.toSource {
      root = ../.;
      fileset = lib.fileset.unions [
        dir
        ../note
      ];
    };

  # vendorHash = null builds in vendor mode, which has no reading of a local
  # replace. The replaced module is a directory beside the program and
  # nothing is fetched either way, so these builds read go.mod as written.
  readsGoMod = ''
    export GOFLAGS="''${GOFLAGS//-mod=vendor/-mod=mod}"
  '';

  # Markdown prose is filled paragraphs hard-wrapped at 100 columns
  # (ida-1ec03b1). One flag string backs both the formatter app and the
  # prose-format check, so the two cannot drift. Embedded-language
  # formatting is off so fenced code blocks pass through byte-for-byte.
  proseFmtArgs = "--prose-wrap always --print-width 100 --embedded-language-formatting off";

  # The integrator's CLI (bin/valley) wrapped for `nix run`. The script
  # itself must keep running bare from any checkout — the package is the
  # second of its two shipping modes, never a dependency of the first.
  valley-script-unwrapped = pkgs.writeShellApplication {
    name = "valley";
    runtimeInputs = [
      pkgs.git
      pkgs.natscli # tail and replay only; every other verb needs just git
      pkgs.cue # checks and review's [a]sk — it composes the policy layers
      pkgs.less # review only — its v key is what takes a note
      attest # review's [a]sk only — it runs the checks and signs them
    ];
    text = builtins.readFile ../bin/valley;
  };

  # The shipping form. The verification schema travels with the script, as
  # it does with the integrator: only the-valley's own tree carries
  # schema/, so a project's checkout has none to read. --set-default leaves
  # an explicit VALLEY_VERIFICATION_SCHEMA in charge.
  valley-script =
    pkgs.runCommand "valley"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "valley";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe valley-script-unwrapped} $out/bin/valley \
          --set-default VALLEY_VERIFICATION_SCHEMA ${../schema/verification.cue}
      '';

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
    src = withNote ../attest;
    modRoot = "attest";
    vendorHash = null;
    preBuild = readsGoMod;
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

  # The identity registry compiler (dcr-b87f6e8). Go, standard library
  # only, same trade as attest and the integrator — hence vendorHash = null
  # and no module fetch. Its unit tests pin the note format's key hash
  # against the reference vector attest is held to.
  identity-unwrapped = pkgs.buildGoModule {
    pname = "valley-identity";
    version = "0";
    src = ../identity;
    vendorHash = null;
    meta.mainProgram = "identity";
  };

  # The shipping form. git reads the registry out of the instance
  # repository's tip and cue vets and exports it, both pinned to this
  # flake's nixpkgs so every host compiles a registry the same way. The
  # schema travels with the binary: a compiler judging a registry against a
  # schema the consumer supplied would be no floor at all.
  identity =
    pkgs.runCommand "valley-identity"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "identity";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe identity-unwrapped} $out/bin/identity \
          --prefix PATH : ${
            lib.makeBinPath [
              pkgs.git
              pkgs.cue
            ]
          } \
          --set-default VALLEY_IDENTITY_SCHEMA ${../schema/identity.cue}
      '';

  # The security-key signature verifier (sigverify/README.md). Go,
  # standard library only — hence vendorHash = null and no module fetch.
  #
  # The git its git-tag command runs is fixed at build time to this
  # flake's git, as an absolute store path compiled into the binary. A
  # wrapper that puts git on PATH would leave the binary inside it looking
  # git up on PATH, and a caller's environment could change which git that
  # is. Compiled in, there is no PATH lookup to change.
  #
  # Its tests run in the checkPhase against real ssh-keygen and git. The
  # security-key cases among them need OpenSSH's sk-dummy authenticator,
  # which only the sigverify-unit check supplies; here they skip, so a
  # consumer building this package never builds OpenSSH.
  sigverify = pkgs.buildGoModule {
    pname = "valley-sigverify";
    version = "0";
    src = ../sigverify;
    vendorHash = null;
    ldflags = [ "-X main.gitProgram=${lib.getExe pkgs.git}" ];
    nativeCheckInputs = [
      pkgs.git
      pkgs.openssh
    ];
    meta.mainProgram = "sigverify";
  };

  # The pre-receive hook's policy (valleyhook/): what a push may write to a
  # project a valley host serves. The same binary is the git user's login
  # shell, which derives the pushing principal and pauses pushes until the
  # host has converged. Go, standard library only — hence
  # vendorHash = null and no module fetch. Its unit tests run in the
  # checkPhase.
  #
  # The git it reads the repository with is fixed at build time, the way
  # sigverify's is: an absolute store path compiled into the binary, so no
  # PATH a hook runs under can change which git that is.
  valleyhook = pkgs.buildGoModule {
    pname = "valley-valleyhook";
    version = "0";
    src = withNote ../valleyhook;
    modRoot = "valleyhook";
    vendorHash = null;
    preBuild = readsGoMod;
    ldflags = [ "-X main.gitProgram=${lib.getExe pkgs.git}" ];
    # The walk's tests build hostile trees with real git.
    nativeCheckInputs = [ pkgs.git ];
    meta.mainProgram = "valleyhook";
  };

  # The note envelope's own tests, which hold it to refusing every line a
  # relayer could append that one of its readers would refuse.
  note = pkgs.buildGoModule {
    pname = "valley-note";
    version = "0";
    src = ../note;
    vendorHash = null;
  };

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

# Everything this flake builds: the CLI and its Go verbs, the
# formatter, the attestation helper, the integrator, the identity
# compiler, the pre-receive hook's policy, the security-key signature
# verifier. The flake wires these into packages, apps, and the checks that
# drive them; nothing here knows about any of those outputs.
{
  pkgs,
  lib,
  # The commit the flake is built from: its full SHA, the SHA with -dirty
  # after it for a tree with uncommitted changes, or null when nix knows no
  # commit. Only the installed CLI's version stamp reads it.
  rev ? null,
}:
rec {
  # The CLI's version, which `valley --version` prints and self-update
  # reads to tell which commit is installed (selfupdate/): cliVersion and
  # the short commit, as 0.1.0-97db597. A dirty tree's has -dirty after
  # it, and with no commit known it ends in -unknown, so neither names a
  # commit.
  cliVersion = "0.1.0";
  version =
    if rev == null then
      "${cliVersion}-unknown"
    else if lib.hasSuffix "-dirty" rev then
      "${cliVersion}-${builtins.substring 0 7 rev}-dirty"
    else
      "${cliVersion}-${builtins.substring 0 7 rev}";

  # What no Go build or test here reads: prose and .gitignore files. A Go
  # package's source leaves them out, so editing a module's README rebuilds
  # nothing.
  unread = lib.fileset.fileFilter (f: f.hasExt "md" || f.name == ".gitignore") ../.;

  # The source of a Go program that uses modules of this repository: its own
  # directory and those modules, which its go.mod replaces with their local
  # directories, each in its place in the tree. Nothing shared this way is
  # fetched. withLocalExcept also leaves out the paths in its last argument,
  # which are files of the directory that only a flake check reads, never
  # the package's build or tests.
  withLocalExcept =
    dir: modules: excluded:
    lib.fileset.toSource {
      root = ../.;
      fileset = lib.fileset.difference (lib.fileset.unions ([ dir ] ++ modules)) (
        lib.fileset.unions ([ unread ] ++ excluded)
      );
    };
  withLocal = dir: modules: withLocalExcept dir modules [ ];

  # The source of a Go module that uses no other module of this repository:
  # its own directory.
  ownSource =
    dir:
    lib.fileset.toSource {
      root = dir;
      fileset = lib.fileset.difference dir unread;
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
      pkgs.cue # checks and approving — it composes the policy layers
      pkgs.less # review only — its v key is what takes a note
      attest # approving only — it runs the checks and signs them
      valley-go # status, ready, unready, withdraw and rebase, naming a change by its ID, and pending's drafts
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

  # The installed package: the wrapped script stamped with its version, the
  # shell completions at the standard paths home-manager/NixOS auto-link,
  # and the tiny policy self-update's smoke test derives checks from
  # (examples/policy/smoke/). The stamp is set on this outer wrapper alone,
  # so the packages the checks and the integrator use do not change with
  # every commit. The name carries the version as well: nix profile names
  # the element valley from it, and self-update reads a hand-installed
  # valley's commit from it.
  valley =
    pkgs.runCommand "valley-${version}"
      {
        inherit version;
        nativeBuildInputs = [
          pkgs.makeWrapper
          pkgs.installShellFiles
        ];
        meta.mainProgram = "valley";
      }
      ''
        mkdir -p $out/bin $out/share/valley
        makeWrapper ${lib.getExe valley-script} $out/bin/valley \
          --set VALLEY_VERSION ${version}
        installShellCompletion --bash --name valley ${../completions/valley.bash}
        installShellCompletion --zsh --name _valley ${../completions/_valley}
        cp -r --no-preserve=mode ${../examples/policy/smoke} $out/share/valley/smoke-policy
      '';

  # The verbs of the CLI written in Go (status/). status reads one change's
  # request and outcome record from origin, and prints them as sentences or
  # as the valley-status/v1 JSON object front ends read. ready, unready and
  # withdraw write a change's record, with the one reading of a record the
  # push hook shares (../change). rebase rebases a change's whole stack and
  # adds each change's rebased head to its record, through the rebase engine
  # (../rebase), which also reads how far each version has landed for the
  # listings. knot reads and writes the plan graph, with the one reading of
  # a graph the push hook shares (../knot), and plan shows that graph as a
  # view. compare reads a version's file delta and its approved version's
  # with the one reading of valley-delta-v1 (../delta), for review.
  # self-update installs the tip of a branch of the-valley into the user's
  # nix profile (../selfupdate). checks prints which checks a change
  # requires, as the deriver (../derive) derives them from the policy the
  # composed-policy reader (../composed) reads. Go,
  # standard library only — hence vendorHash = null and no module fetch. It
  # runs whatever git and cue are on PATH, which inside the CLI's package
  # are the CLI's own. Its unit tests hold its shell quoting to the bash it
  # replaced, so they need bash, lay pending out over real repositories,
  # so they need git, and derive a change's checks under the verification
  # schema, so they need cue and the schema. The plan's golden files
  # (status/testdata/plan/) are valley-plan's, and no Go test reads them.
  valley-go = pkgs.buildGoModule {
    pname = "valley-go";
    version = "0";
    src =
      withLocalExcept ../status
        [
          ../change
          ../composed
          ../delta
          ../derive
          ../knot
          ../note
          ../rebase
          ../selfupdate
          ../schema/verification.cue
        ]
        [ ../status/testdata/plan ];
    modRoot = "status";
    vendorHash = null;
    preBuild = readsGoMod;
    nativeCheckInputs = [
      pkgs.bash
      pkgs.git
      pkgs.cue
    ];
    # A test that cannot find a tool it needs fails rather than skipping.
    preCheck = ''
      export VALLEY_TEST_TOOLS=required
    '';
    meta.mainProgram = "valley-go";
  };

  # The check worker: one pass of `valley-go work` (status/worker.go), which
  # builds the nix checks ready changes require and publishes the evidence
  # before anyone approves them. It runs this flake's git, cue and attest,
  # and the verification schema travels with it, as it does with the CLI.
  # nix is the machine's own, for the reason attest gives.
  valley-worker =
    pkgs.runCommand "valley-worker"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "valley-worker";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe valley-go} $out/bin/valley-worker \
          --add-flags work \
          --prefix PATH : ${
            lib.makeBinPath [
              pkgs.git
              pkgs.cue
              attest
            ]
          } \
          --set-default VALLEY_VERIFICATION_SCHEMA ${../schema/verification.cue}
      '';

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
  # need git and ssh-keygen. It reads signed notes with the note module
  # (../note), the one reading of the envelope shared by the programs that
  # write, verify and admit attestations. The conformance vectors
  # (attest/conformance/) are attest-conformance's, and no Go test reads
  # them.
  attest-unwrapped = pkgs.buildGoModule {
    pname = "valley-attest";
    version = "0";
    src = withLocalExcept ../attest [ ../note ] [ ../attest/conformance ];
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
  # only, note format included, same trade as attest and the integrator —
  # hence vendorHash = null and no module fetch. Its unit tests pin the
  # note format's key hash against the reference vector attest is held to,
  # and verify a real YubiKey attestation against the Yubico roots it
  # carries.
  identity-unwrapped = pkgs.buildGoModule {
    pname = "valley-identity";
    version = "0";
    src = withLocal ../identity [ ../note ];
    modRoot = "identity";
    vendorHash = null;
    preBuild = readsGoMod;
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
    src = ownSource ../sigverify;
    vendorHash = null;
    ldflags = [ "-X main.gitProgram=${lib.getExe pkgs.git}" ];
    nativeCheckInputs = [
      pkgs.git
      pkgs.openssh
    ];
    meta.mainProgram = "sigverify";
  };

  # The pre-receive hook's policy (valleyhook/): what a push may write to a
  # project a valley host serves. It validates a push to the plan graph
  # with the one reading of a graph the CLI shares (../knot). The same binary is the git user's login
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
    src = withLocal ../valleyhook [
      ../note
      ../change
      ../knot
    ];
    modRoot = "valleyhook";
    vendorHash = null;
    preBuild = readsGoMod;
    ldflags = [ "-X main.gitProgram=${lib.getExe pkgs.git}" ];
    # The walk's and the change records' tests build hostile objects with
    # real git.
    nativeCheckInputs = [ pkgs.git ];
    meta.mainProgram = "valleyhook";
  };

  # The note envelope's own tests, which hold it to refusing every line a
  # relayer could append that one of its readers would refuse.
  note = pkgs.buildGoModule {
    pname = "valley-note";
    version = "0";
    src = ownSource ../note;
    vendorHash = null;
  };

  # The plan graph (knot/): its canonical form, its layout, cycles and
  # transitions, held by its own tests. The package is knotcheck, which
  # knot-agree runs to hold the Go validator to the CUE schema and the
  # knowledge lint.
  knot = pkgs.buildGoModule {
    pname = "valley-knot";
    version = "0";
    src = ownSource ../knot;
    vendorHash = null;
    meta.mainProgram = "knotcheck";
  };

  # The rebase engine (rebase/): a family of stacked changes captured from
  # one snapshot of origin, rebased in a throwaway worktree, and published
  # as one atomic branch push and one record append per change. Building
  # valley-go does not run a module's tests that it only imports, so this
  # package runs the engine's: go test, against real repositories, real
  # pushes and real git, and go vet. Go, standard library only — hence
  # vendorHash = null and no module fetch.
  rebase = pkgs.buildGoModule {
    pname = "valley-rebase";
    version = "0";
    src = withLocal ../rebase [ ../change ];
    modRoot = "rebase";
    vendorHash = null;
    preBuild = readsGoMod;
    nativeCheckInputs = [ pkgs.git ];
    postCheck = ''
      go vet ./...
    '';
  };

  # Self-update (selfupdate/): the installed CLI brought up to the tip of a
  # branch, built with nix and installed into the user's profile. Building
  # valley-go does not run the tests of a module it only imports, so this
  # package runs them, and go vet: a real bare repository for ls-remote,
  # and a stand-in for nix whose builds are small scripts, so nothing is
  # built and no profile is touched. Go, standard library only — hence
  # vendorHash = null and no module fetch.
  selfupdate = pkgs.buildGoModule {
    pname = "valley-selfupdate";
    version = "0";
    src = ownSource ../selfupdate;
    vendorHash = null;
    nativeCheckInputs = [ pkgs.git ];
    postCheck = ''
      go vet ./...
    '';
  };

  # The deriver of required checks (derive/): a change's paths read from
  # git diff -z, globs matched byte by byte, and the required checks, pins,
  # carry mode and unclassified paths derived from the whole composed
  # policy (../composed). Building valley-go does not run the tests of a
  # module it only imports, so this package runs them, and go vet. They
  # hold it to bin/valley's deriver, run over the repository's own policy,
  # the example policies and paths drawn from a fixed seed, so the source
  # carries bin/valley, the schema and those policies, and the tests need
  # bash, git and cue. bin/valley is run under C.UTF-8 and en_US.UTF-8 as
  # well as C, so the tests read the UTF-8 locales' archive. Go, standard
  # library only — hence vendorHash = null and no module fetch.
  derive = pkgs.buildGoModule {
    pname = "valley-derive";
    version = "0";
    src = withLocal ../derive [
      ../composed
      ../bin/valley
      ../schema/verification.cue
      ../examples/policy
      ../policy/project
    ];
    modRoot = "derive";
    vendorHash = null;
    preBuild = readsGoMod;
    nativeCheckInputs = [
      pkgs.bash
      pkgs.git
      pkgs.cue
    ];
    # A test that cannot find a tool it needs, or a locale, fails rather
    # than skipping.
    preCheck = ''
      export VALLEY_TEST_TOOLS=required
      export LOCALE_ARCHIVE=${pkgs.glibcLocalesUtf8}/lib/locale/locale-archive
    '';
    postCheck = ''
      go vet ./...
    '';
  };

  # The change record's own tests: its strict reading, its fuzz target's
  # seed corpus, and its branch-name rule, which they hold to real git's
  # check-ref-format.
  change = pkgs.buildGoModule {
    pname = "valley-change";
    version = "0";
    src = ownSource ../change;
    vendorHash = null;
    nativeCheckInputs = [ pkgs.git ];
  };

  # valley-delta-v1 (delta/): the encoding's strict reading, and the delta
  # read from real trees. Its checkPhase runs go vet, the conformance
  # vectors' script in its --check mode, which holds the committed expected
  # results to what the script computes, and go test, which builds each
  # vector's trees with real git and holds the Go code to those results.
  delta = pkgs.buildGoModule {
    pname = "valley-delta";
    version = "0";
    src = ownSource ../delta;
    vendorHash = null;
    nativeCheckInputs = [
      pkgs.git
      pkgs.python3
    ];
    preCheck = ''
      go vet ./...
      python3 -I testdata/vectors.py --check testdata
    '';
  };

  # Review records (review/): the-valley/review/v1's strict reading, a
  # review's binding to the version it judged, recomputed from git within a
  # budget, and the append-only history a review is kept in, with the check
  # an append is held to. It reads change records with the one reading of a
  # record the push hook shares (../change), and the delta with
  # valley-delta-v1's bounded reading (../delta). Its checkPhase runs go
  # test, which builds every repository it reads with real git, its fuzz
  # target's seed corpus, and then go vet. Go, standard library only —
  # hence vendorHash = null and no module fetch.
  review = pkgs.buildGoModule {
    pname = "valley-review";
    version = "0";
    src = withLocal ../review [
      ../change
      ../delta
    ];
    modRoot = "review";
    vendorHash = null;
    preBuild = readsGoMod;
    nativeCheckInputs = [ pkgs.git ];
    postCheck = ''
      go vet ./...
    '';
  };

  # The restacker (restack/): `valley-restacker once --report-only` reads
  # one snapshot of origin through the rebase engine (../rebase), says
  # which families it would restack and which it would hold, and builds the
  # writes a pass would push, pushing none. It reads the cap on its
  # automatic versions through the composed-policy reader it shares with
  # the integrator (../composed), and a signer projects file through the
  # note module (../note). Its unit tests cover request association,
  # outcome currency, capacity and report bounds, and, against real git,
  # the journal of writes and recovery's decisions, its environment, its
  # scratch repository's identity, its state directory's lock and binding,
  # and the deadline on what it runs. go vet runs after them. Go, standard
  # library only — hence vendorHash = null and no module fetch.
  restack-unwrapped = pkgs.buildGoModule {
    pname = "valley-restacker";
    version = "0";
    # The program alone: e2e/transport is a module of restack-e2e's own,
    # which neither this build nor its tests read.
    src =
      withLocalExcept ../restack
        [
          ../change
          ../composed
          ../note
          ../rebase
        ]
        [ ../restack/e2e ];
    modRoot = "restack";
    subPackages = [ "." ];
    vendorHash = null;
    preBuild = readsGoMod;
    nativeCheckInputs = [ pkgs.git ];
    postCheck = ''
      go vet ./...
    '';
    # The module is named for its directory; the program, for what it is.
    postInstall = ''
      mv $out/bin/restack $out/bin/valley-restacker
    '';
    meta.mainProgram = "valley-restacker";
  };

  # The shipping form: git to read origin, cue to compose the policy, and
  # attest to check a transfer statement, all pinned to this flake's
  # nixpkgs, with the packaged verification schema as the default.
  valley-restacker =
    pkgs.runCommand "valley-restacker"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "valley-restacker";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe restack-unwrapped} $out/bin/valley-restacker \
          --prefix PATH : ${
            lib.makeBinPath [
              pkgs.git
              pkgs.cue
              attest
            ]
          } \
          --set-default VALLEY_VERIFICATION_SCHEMA ${../schema/verification.cue}
      '';

  # The Phase 3 integrator (dcr-439b771). Go, standard library only,
  # same trade as attest — hence vendorHash = null and no module fetch. It
  # reads change records with the one reading of a record the push hook
  # shares (../change), and verifier keys with the one reading of a key the
  # note format defines (../note).
  integrator-unwrapped = pkgs.buildGoModule {
    pname = "valley-integrator";
    version = "0";
    # The verification schema is in the source for the policy bridge's
    # tests, which compose a policy under it. The scenarios (integrator/e2e/)
    # are integrator-e2e's, and no Go test reads them.
    src =
      withLocalExcept ../integrator
        [
          ../change
          ../composed
          ../note
          ../schema/verification.cue
        ]
        [ ../integrator/e2e ];
    modRoot = "integrator";
    vendorHash = null;
    preBuild = readsGoMod;
    # The policy bridge's tests check a deriver's report against real git,
    # and compose a policy with real cue.
    nativeCheckInputs = [
      pkgs.git
      pkgs.cue
    ];
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

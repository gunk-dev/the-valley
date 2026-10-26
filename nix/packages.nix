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

  # Every Go test file in the tree. No package's build reads one.
  goTests = lib.fileset.fileFilter (f: lib.hasSuffix "_test.go" f.name) ../.;

  # A Go module of this repository, built as a package that runs no tests,
  # with its unit tests beside it as the package's unit attribute.
  #
  # The package's source is what its build reads: the module's directory
  # (dir) and the modules of this repository it uses (modules), which its
  # go.mod replaces with their local directories, each in its place in the
  # tree. Nothing shared this way is fetched. The source holds no test file
  # and none of the files only the tests read (tests.reads), and the
  # package's checkPhase is off. So an edit to a test, or to a file only the
  # tests read, changes neither the package nor any check built on it.
  #
  # unit is the same build over the same source with the module's own tests
  # and tests.reads added, and with its checkPhase on. The checkPhase runs
  # go test, with the rest of tests: the tools the tests need, and what runs
  # before and after them, such as go vet. Each module's unit is its unit
  # check (nix/checks/), so its tests run in that check and in no package
  # build.
  #
  # excluded names files of dir that neither the build nor the tests read:
  # files only a flake check reads, or the part of a shared module that
  # another package builds and tests.
  goModule =
    {
      dir,
      modules ? [ ],
      excluded ? [ ],
      tests ? { },
      ...
    }@args:
    let
      testReads = tests.reads or [ ];
      source =
        fileset:
        lib.fileset.toSource {
          root = ../.;
          inherit fileset;
        };
      build = lib.fileset.difference (lib.fileset.unions ([ dir ] ++ modules)) (
        lib.fileset.unions ([ unread goTests ] ++ excluded ++ testReads)
      );
      withTests = lib.fileset.difference (lib.fileset.unions (
        [
          build
          (lib.fileset.intersection goTests dir)
        ]
        ++ testReads
      )) (lib.fileset.unions ([ unread ] ++ excluded));
    in
    pkgs.buildGoModule (
      finalAttrs:
      removeAttrs args [
        "dir"
        "modules"
        "excluded"
        "tests"
      ]
      // {
        version = "0";
        src = source build;
        modRoot = lib.path.removePrefix ../. dir;
        # Standard library only, so no module is fetched.
        vendorHash = null;
        doCheck = false;
        passthru.unit = finalAttrs.finalPackage.overrideAttrs (
          prev:
          removeAttrs tests [ "reads" ]
          // {
            pname = "${prev.pname}-unit";
            src = source withTests;
            doCheck = true;
          }
        );
      }
    );

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
  # composed-policy reader (../composed) reads. Go, standard library only.
  # It runs whatever git and cue are on PATH, which inside the CLI's package
  # are the CLI's own. Its unit tests hold its shell quoting to the bash it
  # replaced, so they need bash, lay pending out over real repositories,
  # so they need git, and derive a change's checks under the verification
  # schema, so they need cue and the schema. The plan's golden files
  # (status/testdata/plan/) are valley-plan's, and no Go test reads them.
  valley-go = goModule {
    pname = "valley-go";
    dir = ../status;
    modules = [
      ../change
      ../composed
      ../delta
      ../derive
      ../knot
      ../note
      ../rebase
      ../selfupdate
    ];
    excluded = [ ../status/testdata/plan ];
    preBuild = readsGoMod;
    tests = {
      reads = [ ../schema/verification.cue ];
      nativeCheckInputs = [
        pkgs.bash
        pkgs.git
        pkgs.cue
      ];
      # A test that cannot find a tool it needs fails rather than skipping.
      preCheck = ''
        export VALLEY_TEST_TOOLS=required
      '';
    };
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
  # only, note format included, and hence no module fetch at build time.
  # Its unit tests need git and ssh-keygen. It reads signed notes with the
  # note module (../note), the one reading of the envelope shared by the
  # programs that write, verify and admit attestations. The conformance
  # vectors (attest/conformance/) are attest-conformance's, and no Go test
  # reads them.
  attest-unwrapped = goModule {
    pname = "valley-attest";
    dir = ../attest;
    modules = [ ../note ];
    excluded = [ ../attest/conformance ];
    preBuild = readsGoMod;
    tests.nativeCheckInputs = [
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
  # only, note format included, same trade as attest and the integrator, so
  # no module is fetched. It reads the registry's export with exactjson
  # (../exactjson). Its unit tests pin the note format's key hash
  # against the reference vector attest is held to, and verify a real
  # YubiKey attestation against the Yubico roots it carries. The test
  # vectors (identity/testdata/) are read by the tests and by identity-e2e,
  # never by the build.
  identity-unwrapped = goModule {
    pname = "valley-identity";
    dir = ../identity;
    modules = [
      ../note
      ../exactjson
    ];
    preBuild = readsGoMod;
    tests.reads = [ ../identity/testdata ];
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
  # standard library only, so no module is fetched.
  #
  # The git its git-tag command runs is fixed at build time to this
  # flake's git, as an absolute store path compiled into the binary. A
  # wrapper that puts git on PATH would leave the binary inside it looking
  # git up on PATH, and a caller's environment could change which git that
  # is. Compiled in, there is no PATH lookup to change.
  #
  # Its tests run against real ssh-keygen and git. The security-key cases
  # among them need OpenSSH's sk-dummy authenticator, which the
  # sigverify-unit check supplies. Building this package runs no test, so a
  # consumer building it never builds OpenSSH.
  sigverify = goModule {
    pname = "valley-sigverify";
    dir = ../sigverify;
    ldflags = [ "-X main.gitProgram=${lib.getExe pkgs.git}" ];
    tests.nativeCheckInputs = [
      pkgs.git
      pkgs.openssh
    ];
    meta.mainProgram = "sigverify";
  };

  # The pre-receive hook's policy (valleyhook/): what a push may write to a
  # project a valley host serves. It validates a push to the plan graph
  # with the one reading of a graph the CLI shares (../knot), and reads the
  # project's push policy with exactjson (../exactjson). The same
  # binary is the git user's login shell, which derives the pushing
  # principal and pauses pushes until the host has converged. Go, standard
  # library only, so no module is fetched.
  #
  # The git it reads the repository with is fixed at build time, the way
  # sigverify's is: an absolute store path compiled into the binary, so no
  # PATH a hook runs under can change which git that is.
  valleyhook = goModule {
    pname = "valley-valleyhook";
    dir = ../valleyhook;
    modules = [
      ../note
      ../change
      ../knot
      ../exactjson
    ];
    preBuild = readsGoMod;
    ldflags = [ "-X main.gitProgram=${lib.getExe pkgs.git}" ];
    # The walk's and the change records' tests build hostile objects with
    # real git.
    tests.nativeCheckInputs = [ pkgs.git ];
    meta.mainProgram = "valleyhook";
  };

  # The note envelope (note/). Its tests hold it to refusing every line a
  # relayer could append that one of its readers would refuse.
  note = goModule {
    pname = "valley-note";
    dir = ../note;
  };

  # The strict JSON reading (exactjson/) that the identity compiler, the
  # pre-receive hook and the web publisher read CUE's exports with: every
  # key spelled exactly as the field it is read into, and no key twice in
  # one object. Its tests hold it to each spelling encoding/json alone
  # would fold into a field.
  exactjson = goModule {
    pname = "valley-exactjson";
    dir = ../exactjson;
  };

  # The web UI (webui/). Package export is the contract between the publisher
  # on a valley host and the web server it pushes to: the public export and
  # the projection onto it, the signed manifest, the upload and the receiver's
  # state directory. It reads names and IDs by the plan graph's rules
  # (../knot) and the manifest with the note module (../note). The publisher
  # reads web visibility declarations with exactjson (../exactjson). The
  # program built from it is the publisher, cmd/valley-web-publish, which
  # valley-web-publish below wraps. The server and its command are built by
  # valley-web below. The export's tests accept bundles and build the shared
  # fixture's repositories with real git. The publisher's tests run it against
  # a valley built with real git and the CLI itself (bin/valley, run by bash,
  # with valley-go on PATH), read declarations with cue under schema/web.cue,
  # and upload to a receiver over a real store. One lands a change as the
  # integrator does, with a transfer statement attest signs under a key
  # ssh-keygen makes, which the CLI verifies with attest. go vet runs after
  # them. The fixture's expected export (export/exporttest/testdata/) is read
  # by the tests alone. Go, standard library only, so no module is fetched.
  webui = goModule {
    pname = "valley-webui";
    dir = ../webui;
    modules = [
      ../knot
      ../note
      ../exactjson
    ];
    excluded = [
      ../webui/server
      ../webui/cmd/valley-web
    ];
    preBuild = readsGoMod;
    tests = {
      reads = [
        ../webui/export/exporttest/testdata
        ../bin/valley
        ../schema/web.cue
      ];
      nativeCheckInputs = [
        pkgs.git
        pkgs.bash
        pkgs.cue
        pkgs.openssh
        valley-go
        attest
      ];
      # A test that cannot find a tool it needs fails rather than skipping.
      preCheck = ''
        export VALLEY_TEST_TOOLS=required
      '';
      postCheck = ''
        go vet ./...
      '';
    };
    meta.mainProgram = "valley-web-publish";
  };

  # Read access (readaccess/): the two consents a principal needs to read a
  # project, read out of git commits with cue under schema/read.cue and
  # schema/identity.cue, and the evaluator that intersects them. The web
  # publisher reads through it, and its command is the coverage report,
  # which valley-read-coverage below wraps. Its tests read real commits with
  # real git and the real cue under both schemas, and go vet runs after
  # them. Go, standard library only, so no module is fetched.
  readaccess = goModule {
    pname = "valley-readaccess";
    dir = ../readaccess;
    tests = {
      reads = [
        ../schema/read.cue
        ../schema/identity.cue
      ];
      nativeCheckInputs = [
        pkgs.git
        pkgs.cue
      ];
      # A test that cannot find a tool it needs fails rather than skipping.
      preCheck = ''
        export VALLEY_TEST_TOOLS=required
      '';
      postCheck = ''
        go vet ./...
      '';
    };
    meta.mainProgram = "valley-read-coverage";
  };

  # The coverage report: which projects a principal cannot read at a
  # destination, and which consent each lacks. It runs this flake's git and
  # cue, and both schemas travel with it.
  valley-read-coverage =
    pkgs.runCommand "valley-read-coverage"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "valley-read-coverage";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe readaccess} $out/bin/valley-read-coverage \
          --prefix PATH : ${
            lib.makeBinPath [
              pkgs.git
              pkgs.cue
            ]
          } \
          --set-default VALLEY_READ_SCHEMA ${../schema/read.cue} \
          --set-default VALLEY_IDENTITY_SCHEMA ${../schema/identity.cue}
      '';

  # The web UI's server (webui/server/) and its command, valley-web
  # (webui/cmd/valley-web/), which serves the active export of a receiver's
  # state directory to anonymous readers. It reads the export and the state
  # directory with package export, so its source is the webui module, less the
  # fixture's expected export, which only webui's tests read. It holds every
  # module webui's go.mod replaces, exactjson too, which only the publisher
  # imports. The git it reads repositories with is fixed at build time, the
  # way valleyhook's is. Its tests run the real program over the shared
  # fixture and real git, and go vet runs after them. Go, standard library
  # only, so no module is fetched.
  valley-web = goModule {
    pname = "valley-web";
    dir = ../webui;
    modules = [
      ../knot
      ../note
      ../exactjson
    ];
    excluded = [
      ../webui/export/exporttest/testdata
      ../webui/cmd/valley-web-publish
    ];
    subPackages = [ "cmd/valley-web" ];
    preBuild = readsGoMod;
    ldflags = [ "-X the-valley/webui/export.GitProgram=${lib.getExe pkgs.git}" ];
    tests = {
      subPackages = [
        "server"
        "cmd/valley-web"
      ];
      nativeCheckInputs = [ pkgs.git ];
      postCheck = ''
        go vet ./server/... ./cmd/valley-web/...
      '';
    };
    meta.mainProgram = "valley-web";
  };

  # The web publisher: one pass of valley-web-publish, which reads a valley
  # host's public projects through the CLI's JSON commands and uploads them
  # to a web receiver. It runs this flake's git, cue and CLI, and the web
  # visibility schema travels with it, as the verification schema does with
  # the CLI.
  valley-web-publish =
    pkgs.runCommand "valley-web-publish"
      {
        nativeBuildInputs = [ pkgs.makeWrapper ];
        meta.mainProgram = "valley-web-publish";
      }
      ''
        mkdir -p $out/bin
        makeWrapper ${lib.getExe webui} $out/bin/valley-web-publish \
          --prefix PATH : ${
            lib.makeBinPath [
              pkgs.git
              pkgs.cue
              valley-script
            ]
          } \
          --set-default VALLEY_WEB_SCHEMA ${../schema/web.cue}
      '';

  # The plan graph (knot/): its canonical form, its layout, cycles and
  # transitions, held by its own tests. The package is knotcheck, which
  # knot-agree runs to hold the Go validator to the CUE schema and the
  # knowledge lint.
  knot = goModule {
    pname = "valley-knot";
    dir = ../knot;
    meta.mainProgram = "knotcheck";
  };

  # The rebase engine (rebase/): a family of stacked changes captured from
  # one snapshot of origin, rebased in a throwaway worktree, and published
  # as one atomic branch push and one record append per change. Its tests
  # run against real repositories, real pushes and real git, with go vet
  # after them. Go, standard library only, so no module is fetched.
  rebase = goModule {
    pname = "valley-rebase";
    dir = ../rebase;
    modules = [ ../change ];
    preBuild = readsGoMod;
    tests = {
      nativeCheckInputs = [ pkgs.git ];
      postCheck = ''
        go vet ./...
      '';
    };
  };

  # Self-update (selfupdate/): the installed CLI brought up to the tip of a
  # branch, built with nix and installed into the user's profile. Its tests,
  # with go vet after them, use a real bare repository for ls-remote and a
  # stand-in for nix whose builds are small scripts, so nothing is built and
  # no profile is touched. Go, standard library only, so no module is
  # fetched.
  selfupdate = goModule {
    pname = "valley-selfupdate";
    dir = ../selfupdate;
    tests = {
      nativeCheckInputs = [ pkgs.git ];
      postCheck = ''
        go vet ./...
      '';
    };
  };

  # The deriver of required checks (derive/): a change's paths read from
  # git diff -z, globs matched byte by byte, and the required checks, pins,
  # carry mode and unclassified paths derived from the whole composed
  # policy (../composed). Its tests, with go vet after them, hold it to
  # bin/valley's deriver, run over the repository's own policy, the example
  # policies and paths drawn from a fixed seed. So the tests read
  # bin/valley, the schema and those policies, and need bash, git and cue.
  # bin/valley is run under C.UTF-8 and en_US.UTF-8 as well as C, so the
  # tests read the UTF-8 locales' archive. Go, standard library only, so no
  # module is fetched.
  derive = goModule {
    pname = "valley-derive";
    dir = ../derive;
    modules = [ ../composed ];
    preBuild = readsGoMod;
    tests = {
      reads = [
        ../bin/valley
        ../schema/verification.cue
        ../examples/policy
        ../policy/project
      ];
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
  };

  # The change record (change/). Its tests cover its strict reading, its
  # fuzz target's seed corpus, and its branch-name rule, which they hold to
  # real git's check-ref-format.
  change = goModule {
    pname = "valley-change";
    dir = ../change;
    tests.nativeCheckInputs = [ pkgs.git ];
  };

  # valley-delta-v1 (delta/): the encoding's strict reading, and the delta
  # read from real trees. Its unit tests run go vet, the conformance
  # vectors' script in its --check mode, which holds the committed expected
  # results to what the script computes, and go test, which builds each
  # vector's trees with real git and holds the Go code to those results.
  delta = goModule {
    pname = "valley-delta";
    dir = ../delta;
    tests = {
      reads = [ ../delta/testdata ];
      nativeCheckInputs = [
        pkgs.git
        pkgs.python3
      ];
      preCheck = ''
        go vet ./...
        python3 -I testdata/vectors.py --check testdata
      '';
    };
  };

  # Review records (review/): the-valley/review/v1's strict reading, a
  # review's binding to the version it judged, recomputed from git within a
  # budget, and the append-only history a review is kept in, with the check
  # an append is held to. It reads change records with the one reading of a
  # record the push hook shares (../change), and the delta with
  # valley-delta-v1's bounded reading (../delta). Its tests build every
  # repository they read with real git and run its fuzz target's seed
  # corpus, and go vet runs after them. Go, standard library only, so no
  # module is fetched.
  review = goModule {
    pname = "valley-review";
    dir = ../review;
    modules = [
      ../change
      ../delta
    ];
    preBuild = readsGoMod;
    tests = {
      nativeCheckInputs = [ pkgs.git ];
      postCheck = ''
        go vet ./...
      '';
    };
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
  # library only, so no module is fetched.
  restack-unwrapped = goModule {
    pname = "valley-restacker";
    dir = ../restack;
    modules = [
      ../change
      ../composed
      ../note
      ../rebase
    ];
    # The program alone: e2e/transport is a module of restack-e2e's own,
    # which neither this build nor its tests read.
    excluded = [ ../restack/e2e ];
    subPackages = [ "." ];
    preBuild = readsGoMod;
    tests = {
      nativeCheckInputs = [ pkgs.git ];
      postCheck = ''
        go vet ./...
      '';
    };
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
  # same trade as attest, so no module is fetched. It reads change records
  # with the one reading of a record the push hook shares (../change), and
  # verifier keys with the one reading of a key the note format defines
  # (../note).
  integrator-unwrapped = goModule {
    pname = "valley-integrator";
    dir = ../integrator;
    modules = [
      ../change
      ../composed
      ../note
    ];
    # The scenarios (integrator/e2e/) are integrator-e2e's, and no Go test
    # reads them.
    excluded = [ ../integrator/e2e ];
    preBuild = readsGoMod;
    tests = {
      # The policy bridge's tests compose a policy under the verification
      # schema with real cue, and check a deriver's report against real git.
      reads = [ ../schema/verification.cue ];
      nativeCheckInputs = [
        pkgs.git
        pkgs.cue
      ];
    };
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

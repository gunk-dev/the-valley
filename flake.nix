{
  description = "the-valley: the CUE domain schema for a valley host, and the NixOS module that installs one";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";

  outputs =
    { self, nixpkgs }:
    let
      lib = nixpkgs.lib;
      # Checks use import-from-derivation (the module's cue export), so they
      # are only defined for the system that can actually build them here.
      systems = [ "x86_64-linux" ];

      # The integrator's CLI (bin/valley) wrapped for `nix run`. The script
      # itself must keep running bare from any checkout — the package is the
      # second of its two shipping modes, never a dependency of the first.
      valleyScriptFor =
        pkgs:
        pkgs.writeShellApplication {
          name = "valley";
          runtimeInputs = [
            pkgs.git
            pkgs.natscli # tail and replay only; every other verb needs just git
            pkgs.cue # checks only — it composes the policy layers
          ];
          text = builtins.readFile ./bin/valley;
        };

      # The installed package: the wrapped script plus the shell completions,
      # at the standard paths home-manager/NixOS auto-link.
      valleyCliFor =
        pkgs:
        pkgs.symlinkJoin {
          name = "valley";
          paths = [ (valleyScriptFor pkgs) ];
          nativeBuildInputs = [ pkgs.installShellFiles ];
          postBuild = ''
            installShellCompletion --bash --name valley ${./completions/valley.bash}
            installShellCompletion --zsh --name _valley ${./completions/_valley}
          '';
        };

      # Markdown prose is filled paragraphs hard-wrapped at 100 columns
      # (ida-1ec03b1). One prettier invocation backs both the formatter app
      # and the prose-format check, so the two cannot drift.
      # Embedded-language formatting is off so fenced code blocks pass
      # through byte-for-byte.
      proseFmtArgs = "--prose-wrap always --print-width 100 --embedded-language-formatting off";

      # `nix run .#fmt` — rewrap every tracked *.md in the repo to 100 columns.
      proseFmtFor =
        pkgs:
        pkgs.writeShellApplication {
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
      # only — hence vendorHash = null, and hence no module fetch at build
      # time. Its unit tests run in the checkPhase and need git.
      attestUnwrappedFor =
        pkgs:
        pkgs.buildGoModule {
          pname = "valley-attest";
          version = "0";
          src = ./attest;
          vendorHash = null;
          nativeCheckInputs = [ pkgs.git ];
          meta.mainProgram = "attest";
        };

      # The shipping form. git, ssh-keygen and cue are pinned to this
      # flake's nixpkgs, so every host composes and vets a statement with
      # the same tools. `nix` is deliberately NOT pinned: the check being
      # attested to must be built by the nix the machine actually runs, and
      # a nix carried in here would be a second one.
      attestFor =
        pkgs:
        pkgs.runCommand "valley-attest"
          {
            nativeBuildInputs = [ pkgs.makeWrapper ];
            meta.mainProgram = "attest";
          }
          ''
            mkdir -p $out/bin
            makeWrapper ${lib.getExe (attestUnwrappedFor pkgs)} $out/bin/attest \
              --prefix PATH : ${
                lib.makeBinPath [
                  pkgs.git
                  pkgs.openssh
                  pkgs.cue
                ]
              } \
              --set-default VALLEY_ATTEST_SCHEMA ${./schema/attestation.cue}
          '';
    in
    {
      nixosModules.valley-host = ./nix/valley-host.nix;
      nixosModules.default = self.nixosModules.valley-host;

      # The knowledge lint, for any project that keeps a `.the-valley/` graph.
      # This is the check's primary form: a consuming project takes this flake
      # as an input and instantiates the derivation over its own source, so the
      # check a policy names is supplied by the instance rather than written
      # again by every project the policy covers (dcr-f41f718). A whole
      # consuming flake is:
      #
      #   {
      #     inputs.the-valley.url = "github:gunk-dev/the-valley";
      #     outputs =
      #       { self, the-valley }:
      #       {
      #         checks.x86_64-linux.knowledge-lint = the-valley.lib.knowledgeLint {
      #           system = "x86_64-linux";
      #           src = self;
      #         };
      #       };
      #   }
      #
      # There is no nixpkgs input in that flake on purpose: cue and python come
      # from this flake's pinned nixpkgs, so every project in a group runs the
      # same lint against the same toolchain. `root` names the graph directory
      # for a project that keeps its graph somewhere other than `.the-valley/`.
      lib.knowledgeLint =
        {
          system,
          src,
          root ? ".the-valley",
        }:
        import ./nix/knowledge-lint.nix {
          inherit src root;
          pkgs = nixpkgs.legacyPackages.${system};
        };

      apps = lib.genAttrs systems (system: {
        fmt = {
          type = "app";
          program = lib.getExe (proseFmtFor nixpkgs.legacyPackages.${system});
        };
        attest = {
          type = "app";
          program = lib.getExe (attestFor nixpkgs.legacyPackages.${system});
        };
      });

      packages = lib.genAttrs systems (
        system:
        let
          valley = valleyCliFor nixpkgs.legacyPackages.${system};
        in
        {
          inherit valley;
          default = valley;
          attest = attestFor nixpkgs.legacyPackages.${system};
        }
      );

      checks = lib.genAttrs systems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};

          # Eval-only instantiation of the module — catches option-surface
          # and cue-export regressions without building or booting anything.
          # Each check host shares the machine baseline and supplies its own
          # declaration (and, where the declaration asks for backup, the
          # secret paths).
          mkHost =
            module:
            lib.nixosSystem {
              inherit system;
              modules = [
                self.nixosModules.default
                {
                  fileSystems."/" = {
                    device = "/dev/disk/by-label/nixos";
                    fsType = "ext4";
                  };
                  boot.loader.grub.enable = false;
                  system.stateVersion = "25.11";
                  services.valley = {
                    enable = true;
                    authorizedKeys = [ "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlaceholderKeyForEvalOnlyCheck0 valley-check" ];
                  };
                }
                module
              ];
            };

          # The example declaration enables backup, so this host supplies
          # the four secret paths (eval-only placeholders).
          host = mkHost {
            services.valley = {
              config = ./examples/host.cue;
              backup = {
                repositoryFile = "/run/agenix/valley-restic-repo";
                passwordFile = "/run/agenix/valley-restic-password";
                sshKeyFile = "/run/agenix/valley-git-ssh-key";
                knownHostsFile = "/var/lib/valley-backup/known_hosts";
              };
            };
          };

          # A declaration without a backup block predates the field and must
          # keep evaluating exactly as before: zero restic machinery.
          noBackupHost = mkHost {
            services.valley.config = pkgs.writeText "no-backup-host.cue" ''
              package valley
              projects: "the-valley": {}
            '';
          };

          # The example declaration with the secret paths left unset: the
          # module must refuse with assertions naming the missing options.
          noSecretsHost = mkHost {
            services.valley.config = ./examples/host.cue;
          };

          # Bus enabled, minimal declaration. The bus-e2e check drives this
          # host's rendered hooks and stream-init against a real server.
          busHost = mkHost {
            services.valley = {
              config = pkgs.writeText "bus-host.cue" ''
                package valley
                projects: "events-pilot": {}
              '';
              bus.enable = true;
            };
          };

          # Two mirror URLs the sandbox can serve: git resolves a relative
          # URL against the pushing repo's directory, so the mirror-e2e
          # check drives the rendered script with no substitution at all —
          # one reachable sibling repo, one that does not exist.
          mirrorHost = mkHost {
            services.valley.config = pkgs.writeText "mirror-host.cue" ''
              package valley
              projects: "mirror-pilot": mirrors: ["../mirror.git"]
              projects: "dead-mirror": mirrors: ["../nope.git"]
            '';
          };

          failedAssertions = builtins.filter (a: !a.assertion) (
            host.config.assertions
            ++ noBackupHost.config.assertions
            ++ busHost.config.assertions
            ++ mirrorHost.config.assertions
          );

          missingSecretAssertions = builtins.filter (a: !a.assertion) noSecretsHost.config.assertions;

          resticRenderedWithoutDeclaration =
            noBackupHost.config.systemd.services ? "restic-backups-valley"
            || noBackupHost.config.systemd.timers ? "restic-backups-valley";

          # The bus defaults off; a host that never asked for one must
          # render zero bus machinery.
          busRenderedWithoutEnable =
            noBackupHost.config.systemd.services ? valley-bus
            || noBackupHost.config.systemd.services ? valley-bus-init;

          # A tree in the store, written from a path -> contents map. The
          # knowledge lint reads a whole graph rather than a file, so every
          # fixture below is small but complete.
          mkTree =
            name: files:
            pkgs.runCommand name { } (
              lib.concatStrings (
                lib.mapAttrsToList (path: text: ''
                  install -Dm444 ${pkgs.writeText "knowledge-lint-fixture" text} $out/${path}
                '') files
              )
            );

          # A project that is nearly nothing: a README, a docs directory, and
          # two nodes. This is the shape the first consuming project has, and
          # the knowledge-lint-consumer check instantiates lib.knowledgeLint
          # over it exactly as that project's own flake would.
          consumerProject = mkTree "knowledge-lint-consumer-project" {
            "README.md" = ''
              # A project

              It keeps its knowledge in [.the-valley](./.the-valley/).
            '';
            "docs/design.md" = ''
              # Design

              ## The shape
            '';
            ".the-valley/ideas/ida-0a1b2c3-the-shape.md" = ''
              ---
              type: idea
              id: ida-0a1b2c3
              status: raw
              title: The shape the design should take
              created: 2026-08-02
              ---

              # The shape the design should take

              As sketched in [design.md](../../docs/design.md#the-shape).
            '';
            ".the-valley/outcomes/oc-4d5e6f7-the-design-lands.md" = ''
              ---
              type: outcome
              id: oc-4d5e6f7
              status: open
              title: The design lands
              created: 2026-08-02
              blocked_by: [ida-0a1b2c3]
              ---

              # The design lands

              Waits on [[ida-0a1b2c3]].
            '';
          };

          # A project with no graph at all. Adding the check must not require
          # having written a node yet, or no project would ever adopt it
          # before its first one.
          graphlessProject = mkTree "knowledge-lint-graphless-project" {
            "README.md" = ''
              # A project with no knowledge graph yet
            '';
          };

          # What the lint must reject, and the words its failure must contain.
          # A lint that fails without naming the file and the defect costs the
          # reader the whole diagnosis, so both are pinned.
          rejectedGraphs = {
            unknown-status = {
              names = ''ida-0000001-a-node.md".status'';
              files.".the-valley/ideas/ida-0000001-a-node.md" = ''
                ---
                type: idea
                id: ida-0000001
                status: nonsense
                title: A node in a state ideas do not have
                created: 2026-08-02
                ---
              '';
            };

            edge-on-the-wrong-type = {
              names = "blocked_by: field not allowed";
              files.".the-valley/ideas/ida-0000002-a-node.md" = ''
                ---
                type: idea
                id: ida-0000002
                status: raw
                title: An idea carrying the one edge only outcomes carry
                created: 2026-08-02
                blocked_by: []
                ---
              '';
            };

            node-in-the-wrong-directory = {
              names = "a bug node belongs in .the-valley/bugs/";
              files.".the-valley/ideas/bd-0000003-a-bug.md" = ''
                ---
                type: bug
                id: bd-0000003
                status: open
                title: A bug filed among the ideas
                created: 2026-08-02
                ---
              '';
            };

            filename-id-mismatch = {
              names = "filename says id ida-0000004, frontmatter says ida-0000005";
              files.".the-valley/ideas/ida-0000004-a-node.md" = ''
                ---
                type: idea
                id: ida-0000005
                status: raw
                title: A node whose filename and frontmatter disagree
                created: 2026-08-02
                ---
              '';
            };

            duplicate-id = {
              names = "is already used by";
              files = {
                ".the-valley/ideas/ida-0000006-one.md" = ''
                  ---
                  type: idea
                  id: ida-0000006
                  status: raw
                  title: One node
                  created: 2026-08-02
                  ---
                '';
                ".the-valley/ideas/ida-0000006-two.md" = ''
                  ---
                  type: idea
                  id: ida-0000006
                  status: raw
                  title: Another node wearing the same id
                  created: 2026-08-02
                  ---
                '';
              };
            };

            missing-frontmatter = {
              names = "no YAML frontmatter";
              files.".the-valley/ideas/ida-0000007-a-node.md" = ''
                # A node that is only prose
              '';
            };

            dangling-wiki-link = {
              names = "[[ida-9999999]] is not a node id";
              files.".the-valley/ideas/ida-0000008-a-node.md" = ''
                ---
                type: idea
                id: ida-0000008
                status: raw
                title: A node pointing at nothing
                created: 2026-08-02
                ---

                It builds on [[ida-9999999]].
              '';
            };

            dangling-blocked-by = {
              names = "blocked_by names oc-9999999, which is not a node";
              files.".the-valley/outcomes/oc-0000009-an-outcome.md" = ''
                ---
                type: outcome
                id: oc-0000009
                status: open
                title: An outcome waiting on nothing that exists
                created: 2026-08-02
                blocked_by: [oc-9999999]
                ---
              '';
            };

            broken-link = {
              names = "./nowhere.md does not exist";
              files.".the-valley/ideas/ida-000000a-a-node.md" = ''
                ---
                type: idea
                id: ida-000000a
                status: raw
                title: A node linking to a file that is not there
                created: 2026-08-02
                ---

                See [nowhere](./nowhere.md).
              '';
            };

            broken-anchor = {
              names = "names no heading";
              files.".the-valley/ideas/ida-000000b-a-node.md" = ''
                ---
                type: idea
                id: ida-000000b
                status: raw
                title: A node linking to a heading that is not there
                created: 2026-08-02
                ---

                # A heading

                See [above](./ida-000000b-a-node.md#not-this-heading).
              '';
            };
          };
        in
        {
          # The CLI must stay a lint-clean script whose help verb answers
          # without a repo or a remote — the cheap end of its eviction clause
          # (dcr-74c3158); anything needing more graduates instead. The
          # completions are held to the same bar, and the installed package
          # must carry them at the auto-linked paths.
          valley-cli =
            let
              valley = valleyCliFor pkgs;
            in
            pkgs.runCommand "valley-cli"
              {
                nativeBuildInputs = [
                  pkgs.shellcheck
                  pkgs.zsh
                  valley
                ];
              }
              ''
                shellcheck ${./bin/valley}
                shellcheck ${./completions/valley.bash}
                # shellcheck cannot lint zsh; a bare parse is the cheap check.
                zsh -n ${./completions/_valley}
                valley help > help.txt
                grep -q '^usage: valley' help.txt
                test -f ${valley}/share/bash-completion/completions/valley
                test -f ${valley}/share/zsh/site-functions/_valley
                touch $out
              '';

          # Markdown prose format (ida-1ec03b1): the check is formatter
          # idempotence — formatting the tree must change nothing. On failure,
          # fix with `nix run .#fmt`.
          prose-format =
            pkgs.runCommand "valley-prose-format"
              {
                nativeBuildInputs = [ pkgs.prettier ];
              }
              ''
                cd ${self}
                find . -name '*.md' -print0 | xargs -0 --no-run-if-empty \
                  prettier ${proseFmtArgs} --check
                touch $out
              '';

          # This repo's own graph, checked by the derivation the flake
          # exposes rather than by a second implementation of it. Whatever a
          # consuming project runs, this repo runs.
          knowledge-lint = self.lib.knowledgeLint {
            inherit system;
            src = self;
          };

          # The reusable form, exercised over a foreign tree: a project that
          # is a README, a docs directory and two nodes, and a project with no
          # graph at all. Both must pass, and both are checked by depending on
          # the instantiated derivations rather than by re-running the lint.
          knowledge-lint-consumer =
            pkgs.runCommand "valley-knowledge-lint-consumer"
              {
                consumer = self.lib.knowledgeLint {
                  inherit system;
                  src = consumerProject;
                };
                graphless = self.lib.knowledgeLint {
                  inherit system;
                  src = graphlessProject;
                };
              }
              ''
                test -e "$consumer" && test -e "$graphless"
                touch $out
              '';

          # The lint must fail on a broken graph, and its failure must name
          # the file and the defect — a check that only says "no" leaves the
          # reader to find the breakage themselves. The script and the schema
          # here are the same store paths lib.knowledgeLint uses, so the
          # rejections cannot drift from what a consumer runs.
          knowledge-lint-rejects =
            pkgs.runCommand "valley-knowledge-lint-rejects"
              {
                nativeBuildInputs = [
                  pkgs.cue
                  pkgs.python3
                ];
              }
              ''
                ${lib.concatStrings (
                  lib.mapAttrsToList (case: spec: ''
                    mkdir -p work/${case}
                    if python3 ${./nix/knowledge-lint.py} \
                      --tree ${mkTree "knowledge-lint-${case}" spec.files} \
                      --schema ${./schema/node.cue} \
                      --work work/${case} 2> ${case}.err
                    then
                      echo "knowledge-lint: expected graph '${case}' to be rejected" >&2
                      exit 1
                    fi
                    if ! grep -qF -- ${lib.escapeShellArg spec.names} ${case}.err; then
                      echo "knowledge-lint: '${case}' was rejected without naming" \
                        ${lib.escapeShellArg spec.names} >&2
                      cat ${case}.err >&2
                      exit 1
                    fi
                  '') rejectedGraphs
                )}
                touch $out
              '';

          # The example host declaration must vet against the schema, and the
          # schema must reject what it claims to reject: unsafe project names,
          # unknown project fields, stray top-level fields (typos, deployment
          # concerns), and malformed backup policy (machine concerns, targets
          # without an implementation, non-positive retention). CUE closedness
          # is easy to regress silently — e.g. embedding #Host instead of
          # referencing it opens the schema — so the rejections are pinned
          # here.
          cue-vet =
            let
              invalidConfigs = {
                unsafe-project-name = ''
                  package valley
                  projects: ".hidden": {}
                '';
                unknown-project-field = ''
                  package valley
                  projects: ok: dataDir: "/srv/git"
                '';
                top-level-typo = ''
                  package valley
                  project: oops: {}
                '';
                deployment-concern-in-cue = ''
                  package valley
                  user: "git"
                  projects: ok: {}
                '';
                machine-concern-in-backup = ''
                  package valley
                  projects: ok: {}
                  backup: repositoryFile: "/run/agenix/valley-restic-repo"
                '';
                unimplemented-backup-target = ''
                  package valley
                  projects: ok: {}
                  backup: target: "restic-s3"
                '';
                negative-retention = ''
                  package valley
                  projects: ok: {}
                  backup: retention: daily: -1
                '';
              };
            in
            pkgs.runCommand "valley-cue-vet"
              {
                nativeBuildInputs = [ pkgs.cue ];
              }
              ''
                cue vet -c ${./schema/valley.cue} ${./examples/host.cue}
                cue export ${./schema/valley.cue} ${./examples/host.cue} > example.json
                grep -q 'gunk-dev/the-valley' example.json
                grep -q 'restic-sftp' example.json

                # A declaration without a backup block must stay valid —
                # consumers written before the field existed keep vetting.
                cue vet -c ${./schema/valley.cue} ${pkgs.writeText "no-backup.cue" ''
                  package valley
                  projects: "the-valley": {}
                ''}

                # The event schema accepts what the publisher hook emits …
                cue vet -d '#RefUpdated' ${./schema/events.cue} ${pkgs.writeText "ref-updated.json" ''
                  {"event":"ref-updated","repo":"the-valley","ref":"refs/heads/main","old":"0000000000000000000000000000000000000000","new":"a94a8fe5ccb19ba61c4c0873d391e987982fbbd3"}
                ''}
                # … and stays closed: a field outside the git-derivable
                # identity — wall-clock time, a hostname — must be rejected,
                # or replay determinism silently dies.
                if cue vet -d '#RefUpdated' ${./schema/events.cue} ${pkgs.writeText "ref-updated-timestamped.json" ''
                  {"event":"ref-updated","repo":"the-valley","ref":"refs/heads/main","old":"0000000000000000000000000000000000000000","new":"a94a8fe5ccb19ba61c4c0873d391e987982fbbd3","time":"2026-07-16T00:00:00Z"}
                ''}; then
                  echo "cue-vet: expected the timestamped ref-updated payload to be rejected" >&2
                  exit 1
                fi
                if cue vet -d '#RefUpdated' ${./schema/events.cue} ${pkgs.writeText "ref-updated-short-sha.json" ''
                  {"event":"ref-updated","repo":"the-valley","ref":"refs/heads/main","old":"0000000000000000000000000000000000000000","new":"a94a8fe"}
                ''}; then
                  echo "cue-vet: expected the abbreviated object id to be rejected" >&2
                  exit 1
                fi

                ${lib.concatStrings (
                  lib.mapAttrsToList (name: cfg: ''
                    if cue vet -c ${./schema/valley.cue} ${pkgs.writeText "${name}.cue" cfg}; then
                      echo "cue-vet: expected invalid config '${name}' to be rejected" >&2
                      exit 1
                    fi
                  '') invalidConfigs
                )}
                touch $out
              '';

          # The attestation statement schema (dcr-0de694f). A statement the
          # helper composes must vet, and the schema must reject what it
          # claims to reject. Two rejections carry the decision's whole
          # weight and are pinned by name below: a subject with no
          # content-addressed digest has no identity at all, and a
          # predicate that crosses the pure/effectful line would let a
          # notarization pass itself off as re-derivable.
          attest-schema =
            let
              treeDigest = "73847e0b723a444df8c677e5e67d2e7858327259323c76b24f8f11b650b66d69";
              subject = ''
                "subject": {
                  "primary": "valley-tree-v1",
                  "digest": {
                    "valley-tree-v1": "${treeDigest}",
                    "git-sha1": "99b5f2f7971e2c8e37a7a579cb761d80272a9a3d"
                  }
                }
              '';
              pureStatement = ''
                {
                  "statementType": "the-valley/attestation/v1",
                  ${subject},
                  "predicateType": "the-valley/check/pure/v1",
                  "predicate": {
                    "check": {"name": "prose-format", "runner": "nix", "attribute": "prose-format"},
                    "result": "passed",
                    "inputs": {"valley-inputs-v1": "8df8c199decb43eb715790d6fc3a9dd96adaaf3a75073a50acfa51d73aaf3253"},
                    "derivation": {"sha256": "ef69ed0ccc0546a38a40eebbfcac610cedd7cf4f81036cd21ddff483f08d3ad7"},
                    "output": {"valley-tree-v1": "cf1f5a9c96bbdf1d69da451c94168fa3a760f91f94827ba88bdb72de8dfa8af9"}
                  },
                  "provenance": {
                    "harness": "a harness",
                    "model": "a model",
                    "prompt": {"sha256": "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"},
                    "delegation": [{"principal": "human:integrator", "grant": "land changes"}]
                  }
                }
              '';
              effectfulStatement = ''
                {
                  "statementType": "the-valley/attestation/v1",
                  ${subject},
                  "predicateType": "the-valley/check/effectful/v1",
                  "predicate": {
                    "check": {"name": "live-api", "runner": "command", "command": "curl -sf https://example.invalid"},
                    "result": "passed",
                    "environment": "a sealed environment",
                    "observedAt": "2026-08-02T13:02:00Z"
                  }
                }
              '';
              # Each case is the pure statement with one thing wrong.
              invalidStatements = {
                # Carriage is not statement: a signature inside the signed
                # bytes is a category error, and the sentinel names it.
                signature-in-statement = builtins.replaceStrings [ "\"statementType\"" ] [
                  "\"signature\": \"-----BEGIN SSH SIGNATURE-----\", \"statementType\""
                ] pureStatement;
                # A subject with only advisory members.
                no-content-addressed-subject =
                  builtins.replaceStrings [ "\"valley-tree-v1\": \"${treeDigest}\"," ] [ "" ]
                    pureStatement;
                # A notarization wearing a re-derivable predicate type.
                effectful-claiming-pure = builtins.replaceStrings [ "the-valley/check/effectful/v1" ] [
                  "the-valley/check/pure/v1"
                ] effectfulStatement;
                # A re-derivable claim that drops what it is re-derived
                # against.
                pure-without-output = builtins.replaceStrings [ "\"output\"" ] [ "\"outputs\"" ] pureStatement;
                # A predicate type nobody has published a shape for.
                unversioned-predicate-type =
                  builtins.replaceStrings [ "the-valley/check/pure/v1" ] [ "the-valley/check/pure" ]
                    pureStatement;
                # A digest under a scheme no verifier can check.
                unknown-digest-scheme = builtins.replaceStrings [ "\"git-sha1\"" ] [ "\"md5\"" ] pureStatement;
                # An abbreviated digest.
                short-digest = builtins.replaceStrings [ treeDigest ] [ "73847e0b" ] pureStatement;
                # A check name outside the shape schema/verification.cue
                # holds check names to.
                unsafe-check-name = builtins.replaceStrings [ "\"prose-format\", \"runner\"" ] [
                  "\"Prose Format\", \"runner\""
                ] pureStatement;
                # A nix check carrying a command line.
                nix-check-with-a-command = builtins.replaceStrings [ "\"runner\": \"nix\"" ] [
                  "\"runner\": \"nix\", \"command\": \"make\""
                ] pureStatement;
              };
            in
            pkgs.runCommand "valley-attest-schema"
              {
                nativeBuildInputs = [ pkgs.cue ];
              }
              ''
                cue vet -c ${./schema/attestation.cue} ${pkgs.writeText "pure.json" pureStatement}
                cue vet -c ${./schema/attestation.cue} ${pkgs.writeText "effectful.json" effectfulStatement}

                ${lib.concatStrings (
                  lib.mapAttrsToList (name: text: ''
                    if cue vet -c ${./schema/attestation.cue} ${pkgs.writeText "${name}.json" text} \
                      > ${name}.err 2>&1; then
                      echo "attest-schema: expected invalid statement '${name}' to be rejected" >&2
                      exit 1
                    fi
                  '') invalidStatements
                )}

                # A rejection that does not name the field leaves the reader
                # to find the breakage themselves.
                grep -q 'signature: conflicting values' signature-in-statement.err
                grep -q 'subject.digest."valley-tree-v1"' no-content-addressed-subject.err
                grep -q 'predicate.environment: field not allowed' effectful-claiming-pure.err
                touch $out
              '';

          # The helper itself, driven end to end over scratch repositories
          # and a throwaway key: what it publishes, what it refuses to
          # publish, and what a verifier makes of both.
          #
          # Every check here uses the command runner. The nix runner cannot
          # be exercised from inside a nix build — there is no daemon in the
          # sandbox — so what a pure statement looks like is pinned by
          # attest-schema above and by the unit tests over the digest
          # scheme, and the runner itself is exercised by running the helper
          # outside the sandbox.
          attest-e2e =
            pkgs.runCommand "valley-attest-e2e"
              {
                nativeBuildInputs = [
                  pkgs.git
                  pkgs.openssh
                  (attestFor pkgs)
                ];
              }
              ''
                export HOME="$TMPDIR"
                export GIT_CONFIG_NOSYSTEM=1
                git config --global user.name valley-check
                git config --global user.email valley-check@localhost
                git config --global init.defaultBranch main

                # A throwaway key in a scratch directory. The signer is a
                # parameter: provisioning a host identity is deployment.
                ssh-keygen -q -t ed25519 -N "" -C valley-check -f "$TMPDIR/host"
                ssh-keygen -q -t ed25519 -N "" -C stranger -f "$TMPDIR/stranger"
                printf 'host@valley %s' "$(cat "$TMPDIR/host.pub")" > "$TMPDIR/allowed_signers"
                printf 'stranger@elsewhere %s' "$(cat "$TMPDIR/stranger.pub")" > "$TMPDIR/other_signers"

                repo="$TMPDIR/tree"
                git init --quiet "$repo"
                cd "$repo"
                echo hello > hello.txt
                mkdir -p sub
                echo nested > sub/nested.txt
                ln -s hello.txt link
                printf '#!/bin/sh\ntrue\n' > run.sh
                chmod 755 run.sh
                git add -A
                git commit --quiet -m base

                digest="$(attest digest | sed 's/^valley-tree-v1://')"
                case "$digest" in
                  [0-9a-f][0-9a-f]*) ;;
                  *) echo "attest-e2e: no tree digest: $digest" >&2; exit 1 ;;
                esac

                refs() { git for-each-ref --format='%(refname)' refs/the-valley; }
                nothing_published() {
                  if [ -n "$(refs)" ]; then
                    echo "attest-e2e: $1" >&2
                    git for-each-ref refs/the-valley >&2
                    exit 1
                  fi
                }

                # Without a key, nothing is emitted — an unsigned statement
                # is not an attestation.
                if attest run --command 'ok=true' 2> nokey.err; then
                  echo "attest-e2e: a run with no signing key must fail" >&2
                  exit 1
                fi
                grep -q 'no signing key' nokey.err
                nothing_published "a run with no signing key stored a ref"

                # A failing check publishes nothing, and says which check.
                if attest run --key "$TMPDIR/host" \
                  --command 'ok=true' --command 'nope=false' > failing.out 2> failing.err; then
                  echo "attest-e2e: a failing check must fail the run" >&2
                  exit 1
                fi
                grep -qE '^check   nope +command  failed$' failing.out
                grep -q 'nothing published' failing.err
                nothing_published "a failing check left a ref behind"

                # A statement the schema rejects is stopped before it is
                # signed or stored. Provenance is the caller's, so a
                # malformed one is how a malformed statement gets composed
                # at all.
                echo '{"harness":"valley-check","authority":"root"}' > bad-provenance.json
                if attest run --key "$TMPDIR/host" --command 'ok=true' \
                  --provenance bad-provenance.json > malformed.out 2> malformed.err; then
                  echo "attest-e2e: a malformed statement must fail the run" >&2
                  exit 1
                fi
                grep -q 'provenance.authority: field not allowed' malformed.err
                grep -q 'nothing published' malformed.err
                nothing_published "a malformed statement left a ref behind"

                # The successful run.
                echo '{"harness":"valley-check","model":"none","delegation":[{"principal":"human:integrator","grant":"land changes"}]}' > provenance.json
                attest run --key "$TMPDIR/host" --command 'ok=true' \
                  --provenance provenance.json > run.out
                grep -q "^subject valley-tree-v1:$digest\$" run.out
                grep -q '^check   ok  *command  passed$' run.out

                # Stored at a ref keyed by the subject digest.
                ref="$(refs)"
                case "$ref" in
                  refs/the-valley/attestations/"$digest"/*) ;;
                  *) echo "attest-e2e: ref $ref is not keyed by the subject digest" >&2; exit 1 ;;
                esac
                git cat-file -p "$ref:ok/statement.json" > statement.json
                git cat-file -p "$ref:ok/statement.sig" > statement.sig
                grep -q 'BEGIN SSH SIGNATURE' statement.sig
                grep -q "\"valley-tree-v1\":\"$digest\"" statement.json
                grep -q '"harness":"valley-check"' statement.json

                # No git object is signed: the signature is over the
                # statement and nothing else.
                if git cat-file commit HEAD | grep -q '^gpgsig'; then
                  echo "attest-e2e: attest signed a git object" >&2
                  exit 1
                fi

                verify() {
                  attest verify --statement "$1" --signature statement.sig \
                    --allowed-signers "$2" --repo "$repo"
                }

                verify statement.json "$TMPDIR/allowed_signers" > verify.out
                grep -q 'Good "the-valley.attestation.v1" signature for host@valley' verify.out
                grep -q 'matches the recorded subject digest' verify.out

                # One atomic native-git push carries the branch and the
                # attestation ref together.
                git init --quiet --bare "$TMPDIR/host.git"
                git remote add valley "$TMPDIR/host.git"
                attest run --key "$TMPDIR/host" --command 'ok=true' --push valley > push.out
                grep -q -- '--atomic' push.out
                git -C "$TMPDIR/host.git" for-each-ref --format='%(refname)' > published.txt
                grep -qx 'refs/heads/main' published.txt
                grep -qx "$ref" published.txt

                # A signature by a key the allowed-signers file does not
                # list is not a bad signature — it is not a signer.
                if verify statement.json "$TMPDIR/other_signers" > stranger.out 2>&1; then
                  echo "attest-e2e: a statement signed by nobody we allow must not verify" >&2
                  exit 1
                fi
                grep -q 'no principal' stranger.out

                # Tampering with the signed bytes.
                sed "s/$digest/00000000000000000000000000000000000000000000000000000000000000ff/" \
                  statement.json > tampered.json
                if verify tampered.json "$TMPDIR/allowed_signers" > tampered.out 2>&1; then
                  echo "attest-e2e: a tampered statement must not verify" >&2
                  exit 1
                fi
                grep -q 'Signature verification failed' tampered.out

                # A statement rendered some other way than canonically.
                # It would verify here and fail wherever it was recomposed,
                # so it is refused.
                sed 's/^{/{ /' statement.json > loose.json
                if verify loose.json "$TMPDIR/allowed_signers" > loose.out 2>&1; then
                  echo "attest-e2e: a non-canonical statement must not verify" >&2
                  exit 1
                fi
                grep -q 'not in canonical form' loose.out

                # The subject is the tree. Change the tree and the same
                # signed statement stops being about it …
                echo goodbye > hello.txt
                git commit --quiet -am "change the tree"
                if verify statement.json "$TMPDIR/allowed_signers" > moved.out 2>&1; then
                  echo "attest-e2e: a statement about another tree must not verify" >&2
                  exit 1
                fi
                grep -q 'tree digest mismatch' moved.out

                # … while rewriting the commit over the same tree leaves the
                # attestation standing, which is what a subject that is a
                # tree digest buys.
                git commit --quiet --amend -m "same tree, new commit"
                rewritten="$(attest digest | sed 's/^valley-tree-v1://')"
                git revert --quiet --no-edit HEAD
                restored="$(attest digest | sed 's/^valley-tree-v1://')"
                if [ "$restored" != "$digest" ]; then
                  echo "attest-e2e: restoring the tree did not restore its digest" >&2
                  echo "  original $digest" >&2
                  echo "  restored $restored" >&2
                  exit 1
                fi
                if [ "$rewritten" = "$digest" ]; then
                  echo "attest-e2e: a changed tree kept its digest" >&2
                  exit 1
                fi
                verify statement.json "$TMPDIR/allowed_signers" > rebased.out
                grep -q 'matches the recorded subject digest' rebased.out
                touch $out
              '';

          # The verification-policy deriver (dcr-f41f718). Composing the two
          # layers is CUE's job and is pinned by cue-vet above; matching
          # paths is the script's own, and a path matcher is wrong exactly at
          # the glob boundaries — `*` inside one segment against `**` across
          # segments, `**` matching no segment at all — and at the sides of a
          # diff a naive reader drops: a rename's old path and a deletion.
          # Each is pinned by driving the real script over a scratch
          # repository and a scratch policy, never by reading the code.
          policy-deriver =
            let
              # A floor whose classes are one glob boundary each, and whose
              # check names say which class matched.
              deriverFloor = pkgs.writeText "deriver-floor.cue" ''
                package verification
                floor: {
                  checks: {
                    "c-docs": {runner:      "nix", attribute: "c-docs"}
                    "c-notes": {runner:     "nix", attribute: "c-notes"}
                    "c-flat": {runner:      "nix", attribute: "c-flat"}
                    "c-deep": {runner:      "nix", attribute: "c-deep"}
                    "c-nested": {runner:    "nix", attribute: "c-nested"}
                    "c-uncovered": {runner: "nix", attribute: "c-uncovered"}
                  }
                  classes: {
                    docs: {paths: "docs/**": true, requires: "c-docs": true}
                    notes: {paths: "notes/**": true, requires: "c-notes": true}
                    flat: {paths: "src/*.rs": true, requires: "c-flat": true}
                    deep: {paths: "src/**/*.rs": true, requires: "c-deep": true}
                    nested: {paths: "a/**/b": true, requires: "c-nested": true}
                  }
                  unclassified: "c-uncovered": true
                }
              '';
              # One requirement that is a template default rather than a
              # rule, so the two forms are told apart on real output.
              deriverTemplate = pkgs.writeText "deriver-template.cue" ''
                package verification
                #tmpl: #Policy & {
                  checks: "c-tmpl": {runner: "nix", attribute: "c-tmpl"}
                  classes: docs: requires: "c-tmpl": bool | *true
                }
              '';
              deriverProject = pkgs.writeText "deriver-project.cue" ''
                package verification
                project: {
                  #tmpl
                }
              '';
            in
            pkgs.runCommand "valley-policy-deriver"
              {
                nativeBuildInputs = [
                  pkgs.git
                  pkgs.cue
                  (valleyScriptFor pkgs)
                ];
              }
              ''
                export HOME="$TMPDIR"
                export GIT_CONFIG_NOSYSTEM=1
                git config --global user.name valley-check
                git config --global user.email valley-check@localhost
                git config --global init.defaultBranch main

                policy="$TMPDIR/policy"
                mkdir -p "$policy/instance" "$policy/project"
                cp ${deriverFloor} "$policy/instance/floor.cue"
                cp ${deriverTemplate} "$policy/instance/template.cue"
                cp ${deriverProject} "$policy/project/policy.cue"

                repo="$TMPDIR/tree"
                git init --quiet "$repo"
                cd "$repo"
                mkdir -p docs notes src/deep a/x/y
                echo moved > docs/moved.md
                for f in docs/kept.md docs/gone.md src/one.rs src/deep/two.rs \
                  a/b a/x/y/b Makefile; do
                  echo seed > "$f"
                done
                git add -A
                git commit --quiet -m base
                base="$(git rev-parse HEAD)"

                derive() {
                  valley checks --schema ${./schema/verification.cue} \
                    --instance "$policy/instance" --project "$policy/project" \
                    "$base" HEAD
                }

                # The check names in the deriver's table, sorted, on one line.
                named() {
                  awk '/^required checks:$/ {f = 1; next}
                       /^$/ {f = 0}
                       f && /^  / {print $1}' <<<"$report" | sort | paste -sd' ' -
                }

                # Start a case from the base tree; assert its check table.
                case_start() { git checkout --quiet -B "$1" "$base"; }
                expect() {
                  local got
                  got="$(named)"
                  if [ "$got" != "$1" ]; then
                    echo "policy-deriver: $2" >&2
                    echo "  expected: $1" >&2
                    echo "  got:      $got" >&2
                    echo "$report" >&2
                    exit 1
                  fi
                }

                # `**` crosses segments, `*` does not. One file under src/
                # matches both patterns; one a segment deeper matches only
                # the `**` one.
                case_start flat
                echo edit > src/one.rs
                git commit --quiet -am flat
                report="$(derive)"
                expect "c-deep c-flat" "src/*.rs must match a file directly under src/"

                case_start deep
                echo edit > src/deep/two.rs
                git commit --quiet -am deep
                report="$(derive)"
                expect "c-deep" "src/*.rs must not match across a path separator"

                # A `**` between two segments matches no segment at all …
                case_start nested-none
                echo edit > a/b
                git commit --quiet -am nested-none
                report="$(derive)"
                expect "c-nested" "a/**/b must match a/b"

                # … and any number of them.
                case_start nested-many
                echo edit > a/x/y/b
                git commit --quiet -am nested-many
                report="$(derive)"
                expect "c-nested" "a/**/b must match a/x/y/b"

                # A rename counts on both sides: the class covering the old
                # path is required as much as the one covering the new.
                case_start renamed
                git mv docs/moved.md notes/moved.md
                git commit --quiet -m renamed
                git diff --name-status -M "$base" HEAD | grep -q '^R' \
                  || { echo "policy-deriver: the rename case is not a rename" >&2; exit 1; }
                report="$(derive)"
                expect "c-docs c-notes c-tmpl" "a rename must count on both sides"

                # A deleted path is still a changed path.
                case_start deleted
                git rm --quiet docs/gone.md
                git commit --quiet -m deleted
                report="$(derive)"
                expect "c-docs c-tmpl" "a deletion must count"

                # The form of the value is the mandatory bit: the floor's
                # concrete `true` against the template's `bool | *true`.
                grep -qE '^  c-docs +mandatory +docs$' <<<"$report"
                grep -qE '^  c-tmpl +default +docs$' <<<"$report"

                # A path no class covers takes the unclassified set, and says
                # so by name.
                case_start uncovered
                echo edit > Makefile
                git commit --quiet -am uncovered
                report="$(derive)"
                expect "c-uncovered" "an uncovered path must take the unclassified set"
                grep -q '^classes matched: unclassified$' <<<"$report"
                grep -q '^unclassified: 1 path(s) matched no class$' <<<"$report"
                grep -q '^  Makefile$' <<<"$report"

                touch $out
              '';

          module-eval =
            if failedAssertions != [ ] then
              throw "valley module-eval: failed assertions: ${lib.concatMapStringsSep "; " (a: a.message) failedAssertions}"
            else if resticRenderedWithoutDeclaration then
              throw "valley module-eval: restic machinery rendered for a declaration without a backup block"
            else if busRenderedWithoutEnable then
              throw "valley module-eval: bus machinery rendered without services.valley.bus.enable"
            else if
              !(lib.any (a: lib.hasInfix "services.valley.backup." a.message) missingSecretAssertions)
            then
              throw "valley module-eval: enabling backup without the secret-path options must fail an assertion naming them"
            else
              pkgs.runCommand "valley-module-eval"
                {
                  initScript = host.config.systemd.services.valley-init.script;
                  sshdConfig = host.config.services.openssh.extraConfig;
                  resticService = host.config.systemd.units."restic-backups-valley.service".text;
                  resticTimer = host.config.systemd.units."restic-backups-valley.timer".text;
                  passAsFile = [
                    "initScript"
                    "sshdConfig"
                    "resticService"
                    "resticTimer"
                  ];
                }
                ''
                  # The repo init list and mirror hook must be generated from
                  # the CUE export, and the git user's sshd Match block must
                  # be terminated so it cannot scope later config.
                  grep -q "the-valley" "$initScriptPath"
                  grep -q "valley-mirrors" "$initScriptPath"
                  grep -q "Match All" "$sshdConfigPath"

                  # The mirror publishes main and tags only — a heads glob
                  # would publish every topic branch awaiting review — with
                  # --prune for tag deletions and a sweep that unpublishes
                  # any other head (--prune cannot: it ignores non-glob
                  # refspecs). Never --mirror: it also deletes remote-only
                  # refs, and GitHub's read-only refs/pull/* fails every such
                  # push. Follow the hook chain from the init script to the
                  # rendered push script and pin the invocation there.
                  mirrorHook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-[^ ]*' "$initScriptPath" | head -n1)"
                  mirrorPush="$(grep -o '/nix/store/[^ ]*-valley-mirror-push-[^ ]*' "$mirrorHook" | head -n1)"
                  grep -q -- 'push --prune' "$mirrorPush"
                  grep -qF -- '+refs/heads/main:refs/heads/main' "$mirrorPush"
                  grep -qF -- '+refs/tags/*:refs/tags/*' "$mirrorPush"
                  grep -qF -- 'ls-remote --heads' "$mirrorPush"
                  grep -qF -- 'push "$url" --delete' "$mirrorPush"
                  if grep -qF -- '+refs/heads/*:refs/heads/*' "$mirrorPush"; then
                    echo "module-eval: mirror push regressed to publishing every head" >&2
                    exit 1
                  fi
                  if grep -q -- '--mirror' "$mirrorPush"; then
                    echo "module-eval: mirror push regressed to --mirror" >&2
                    exit 1
                  fi
                  # Deletions must never reach a namespace the mirror owns.
                  if grep -q 'refs/pull' "$mirrorPush"; then
                    echo "module-eval: mirror push must not name refs/pull/*" >&2
                    exit 1
                  fi

                  # The rendered restic units must back up the data directory
                  # to the consumer-supplied repository with the declared
                  # retention over a pinned host key, on the declared cadence.
                  grep -q "RESTIC_REPOSITORY_FILE=/run/agenix/valley-restic-repo" "$resticServicePath"
                  grep -q -- "--keep-daily 7 --keep-weekly 4 --keep-monthly 6" "$resticServicePath"
                  grep -q "UserKnownHostsFile=/var/lib/valley-backup/known_hosts" "$resticServicePath"
                  grep -q "BatchMode=yes" "$resticServicePath"
                  grep -q "OnCalendar=03:30" "$resticTimerPath"

                  # The backup paths render as a --files-from list: follow
                  # ExecStartPre to that list and pin the data directory.
                  preStart="$(sed -n 's/^ExecStartPre=//p' "$resticServicePath" | head -n1)"
                  staticPaths="$(grep -o '/nix/store/[^ ]*-staticPaths' "$preStart" | head -n1)"
                  grep -qx "/srv/git" "$staticPaths"
                  touch $out
                '';

          # What a mirror ends up holding, end to end and unmocked: a bare
          # repo wired with the real rendered hooks, pushed to for real, and
          # a second bare repo standing in for the mirror. The refspec is
          # easy to get subtly wrong — --prune ignores the non-glob heads
          # refspec, so pruning heads is the sweep's job — and only running
          # git can tell. Relative mirror URLs (mirrorHost) resolve against
          # the pushing repo, so the shipped scripts run with no
          # substitution: this is exactly what a host executes.
          mirror-e2e =
            pkgs.runCommand "valley-mirror-e2e"
              {
                nativeBuildInputs = [ pkgs.git ];
                initScript = mirrorHost.config.systemd.services.valley-init.script;
                passAsFile = [ "initScript" ];
              }
              ''
                export HOME="$TMPDIR"
                export GIT_CONFIG_NOSYSTEM=1
                git config --global user.name valley-check
                git config --global user.email valley-check@localhost
                git config --global init.defaultBranch main
                cd "$TMPDIR"

                wait_for() {
                  for _ in $(seq 1 150); do
                    "$@" >/dev/null 2>&1 && return 0
                    sleep 0.2
                  done
                  echo "mirror-e2e: timed out waiting for: $*" >&2
                  return 1
                }
                heads() { git -C mirror.git for-each-ref --format='%(refname)' refs/heads; }
                tags() { git -C mirror.git for-each-ref --format='%(refname)' refs/tags; }
                mirror_main_is() { [ "$(git -C mirror.git rev-parse main)" = "$tip" ]; }
                only_main() { [ "$(heads)" = refs/heads/main ]; }

                # A primary repo wired exactly as valley-init wires it, with
                # the real store paths followed from the rendered init script.
                dispatch="$(grep -o '/nix/store/[^ ]*-valley-post-receive' "$initScriptPath" | head -n1)"
                mhook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-mirror-pilot' "$initScriptPath" | head -n1)"
                deadhook="$(grep -o '/nix/store/[^ ]*-valley-mirrors-dead-mirror' "$initScriptPath" | head -n1)"
                test -x "$dispatch" && test -x "$mhook" && test -x "$deadhook"
                wire() {
                  mkdir -p "$1/hooks/post-receive.d"
                  ln -s "$dispatch" "$1/hooks/post-receive"
                  ln -s "$2" "$1/hooks/post-receive.d/valley-mirrors"
                }

                # The mirror as it stands today: every head and tag of the
                # primary, topic branches included. Seeded before the hook is
                # wired, so nothing sweeps it out from under the setup.
                git init --quiet --bare mirror-pilot.git
                git init --quiet --bare mirror.git
                git clone --quiet "$PWD/mirror-pilot.git" work
                git -C work commit --quiet --allow-empty -m one
                git -C work tag v1
                git -C work tag doomed
                for b in idea/one idea/two; do git -C work branch "$b"; done
                git -C work push --quiet origin \
                  '+refs/heads/*:refs/heads/*' '+refs/tags/*:refs/tags/*'
                git -C mirror-pilot.git push --quiet ../mirror.git \
                  '+refs/heads/*:refs/heads/*' '+refs/tags/*:refs/tags/*'
                [ "$(heads | wc -l)" -eq 3 ]
                wire mirror-pilot.git "$mhook"

                # A real push to the primary — a topic branch alongside main,
                # and a tag deleted. The hook replicates asynchronously, so
                # wait on main's new tip before judging the rest.
                git -C work commit --quiet --allow-empty -m two
                git -C work tag -d doomed >/dev/null
                git -C work branch idea/three
                git -C work push --quiet --follow-tags origin main idea/three
                git -C work push --quiet --delete origin doomed
                tip="$(git -C work rev-parse HEAD)"
                wait_for mirror_main_is

                # main and the tags are published; nothing else is, and the
                # topic branches that were on the mirror are gone. The sweep
                # is a second push, so it converges just after main lands.
                wait_for only_main
                [ "$(tags)" = refs/tags/v1 ]

                # A branch that appears on the mirror by any other route is
                # unpublished on the next push.
                git -C mirror.git branch sneaky main
                git -C work commit --quiet --allow-empty -m three
                git -C work push --quiet origin main
                tip="$(git -C work rev-parse HEAD)"
                wait_for mirror_main_is
                wait_for only_main

                # An unreachable mirror costs a log line, never the push: the
                # hook must return promptly and the push must succeed.
                git init --quiet --bare dead-mirror.git
                wire dead-mirror.git "$deadhook"
                git clone --quiet "$PWD/dead-mirror.git" deadwork
                git -C deadwork commit --quiet --allow-empty -m one
                timeout 60 git -C deadwork push --quiet origin main
                [ "$(git -C dead-mirror.git rev-parse main)" = "$(git -C deadwork rev-parse HEAD)" ]

                touch $out
              '';

          # Phase 1's exit criteria, end to end and unmocked: a push to a
          # bare repo wired with the real rendered hooks produces a
          # ref-updated event on a real JetStream server within seconds,
          # visible in `valley tail`; replaying the repo's refs is
          # deterministic; and a dead bus never fails a push. The hook and
          # stream-init scripts are followed from the rendered unit scripts,
          # so the check exercises exactly what a host would run — only the
          # server invocation differs (the sandbox cannot write /srv), same
          # binary and flags, relocated storage.
          bus-e2e =
            pkgs.runCommand "valley-bus-e2e"
              {
                nativeBuildInputs = [
                  pkgs.git
                  pkgs.natscli
                  pkgs.nats-server
                  pkgs.jq
                  pkgs.cue
                  (valleyScriptFor pkgs)
                ];
                initScript = busHost.config.systemd.services.valley-init.script;
                busInitScript = busHost.config.systemd.services.valley-bus-init.script;
                passAsFile = [
                  "initScript"
                  "busInitScript"
                ];
              }
              ''
                export HOME="$TMPDIR"
                export NATS_URL=nats://127.0.0.1:4222
                export GIT_CONFIG_NOSYSTEM=1
                git config --global user.name valley-check
                git config --global user.email valley-check@localhost
                git config --global init.defaultBranch main

                wait_for() {
                  for _ in $(seq 1 150); do
                    "$@" >/dev/null 2>&1 && return 0
                    sleep 0.2
                  done
                  echo "bus-e2e: timed out waiting for: $*" >&2
                  return 1
                }

                nats-server --addr 127.0.0.1 --port 4222 --jetstream \
                  --store_dir "$TMPDIR/js" &
                server_pid=$!

                # The real stream-init script the module renders.
                bash -eu "$busInitScriptPath"
                nats stream info valley >/dev/null

                # A bare repo wired exactly as valley-init wires it, with the
                # real store paths followed from the rendered init script.
                dispatch="$(grep -o '/nix/store/[^ ]*-valley-post-receive' "$initScriptPath" | head -n1)"
                bushook="$(grep -o '/nix/store/[^ ]*-valley-bus-events' "$initScriptPath" | head -n1)"
                test -x "$dispatch"
                test -x "$bushook"
                repo="$TMPDIR/events-pilot.git"
                git init --quiet --bare "$repo"
                mkdir -p "$repo/hooks/post-receive.d"
                ln -s "$dispatch" "$repo/hooks/post-receive"
                ln -s "$bushook" "$repo/hooks/post-receive.d/valley-bus"

                git clone --quiet "$repo" "$TMPDIR/work"
                cd "$TMPDIR/work"
                echo one > file
                git add file
                git commit --quiet -m one
                git push --quiet origin main
                first="$(git rev-parse HEAD)"
                zeros="$(printf '%040d' 0)"

                # Exit criterion 1: the event arrives within seconds, with a
                # payload that is exactly the git facts of the push …
                msgs_is() { [ "$(nats stream info valley --json | jq .state.messages)" -eq "$1" ]; }
                wait_for msgs_is 1
                got="$(nats stream get valley 1 --json)"
                [ "$(jq -r .subject <<<"$got")" = valley.git.events-pilot.ref-updated ]
                payload="$(jq -r .data <<<"$got" | base64 -d)"
                expected='{"event":"ref-updated","repo":"events-pilot","ref":"refs/heads/main","old":"'"$zeros"'","new":"'"$first"'"}'
                [ "$payload" = "$expected" ]

                # … valid against the shipped event schema.
                echo "$payload" > "$TMPDIR/payload.json"
                cue vet -d '#RefUpdated' ${./schema/events.cue} "$TMPDIR/payload.json"

                # … and visible in `valley tail`. Subscribe first, then push.
                valley tail > "$TMPDIR/tail.out" 2>&1 &
                tail_pid=$!
                subscribed() { grep -q 'valley.>' "$TMPDIR/tail.out"; }
                wait_for subscribed
                echo two > file
                git commit --quiet -am two
                git push --quiet origin main
                second="$(git rev-parse HEAD)"
                wait_for msgs_is 2
                payload2="$(nats stream get valley 2 --json | jq -r .data | base64 -d)"
                expected2='{"event":"ref-updated","repo":"events-pilot","ref":"refs/heads/main","old":"'"$first"'","new":"'"$second"'"}'
                [ "$payload2" = "$expected2" ]
                tail_saw() { grep -qF "$expected2" "$TMPDIR/tail.out"; }
                wait_for tail_saw
                kill "$tail_pid" 2>/dev/null || true

                # Exit criterion 2: rebuilding the stream from the repo's
                # refs is deterministic — two replays from scratch produce
                # identical events, landing on the pushed tip with the
                # all-zero id as old.
                dump_stream() {
                  local info first_seq last_seq i
                  info="$(nats stream info valley --json)"
                  first_seq="$(jq .state.first_seq <<<"$info")"
                  last_seq="$(jq .state.last_seq <<<"$info")"
                  for i in $(seq "$first_seq" "$last_seq"); do
                    nats stream get valley "$i" --json | jq -c '{subject: .subject, data: .data}'
                  done
                }
                nats stream purge valley --force >/dev/null
                valley replay "$repo"
                wait_for msgs_is 1
                run1="$(dump_stream)"
                nats stream purge valley --force >/dev/null
                valley replay "$repo"
                wait_for msgs_is 1
                run2="$(dump_stream)"
                [ "$run1" = "$run2" ]
                replayed="$(jq -r .data <<<"$run1" | base64 -d)"
                [ "$replayed" = '{"event":"ref-updated","repo":"events-pilot","ref":"refs/heads/main","old":"'"$zeros"'","new":"'"$second"'"}' ]

                # A dead bus never blocks a push: the event is simply lost
                # (and rebuildable by replay).
                kill "$server_pid"
                wait "$server_pid" || true
                echo three > file
                git commit --quiet -am three
                git push --quiet origin main

                touch $out
              '';
        }
      );
    };
}

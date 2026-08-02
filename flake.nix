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
    in
    {
      nixosModules.valley-host = ./nix/valley-host.nix;
      nixosModules.default = self.nixosModules.valley-host;

      apps = lib.genAttrs systems (system: {
        fmt = {
          type = "app";
          program = lib.getExe (proseFmtFor nixpkgs.legacyPackages.${system});
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

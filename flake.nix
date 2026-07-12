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
    in
    {
      nixosModules.valley-host = ./nix/valley-host.nix;
      nixosModules.default = self.nixosModules.valley-host;

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

          failedAssertions = builtins.filter (a: !a.assertion) (
            host.config.assertions ++ noBackupHost.config.assertions
          );

          missingSecretAssertions = builtins.filter (a: !a.assertion) noSecretsHost.config.assertions;

          resticRenderedWithoutDeclaration =
            noBackupHost.config.systemd.services ? "restic-backups-valley"
            || noBackupHost.config.systemd.timers ? "restic-backups-valley";
        in
        {
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

          module-eval =
            if failedAssertions != [ ] then
              throw "valley module-eval: failed assertions: ${lib.concatMapStringsSep "; " (a: a.message) failedAssertions}"
            else if resticRenderedWithoutDeclaration then
              throw "valley module-eval: restic machinery rendered for a declaration without a backup block"
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

                  # The rendered restic units must back up the data directory
                  # to the consumer-supplied repository with the declared
                  # retention over a pinned host key, on the declared cadence.
                  grep -q "RESTIC_REPOSITORY_FILE=/run/agenix/valley-restic-repo" "$resticServicePath"
                  grep -q -- "--keep-daily 7 --keep-weekly 4 --keep-monthly 6" "$resticServicePath"
                  grep -q "UserKnownHostsFile=/var/lib/valley-backup/known_hosts" "$resticServicePath"
                  grep -q "OnCalendar=03:30" "$resticTimerPath"

                  # The backup paths render as a --files-from list: follow
                  # ExecStartPre to that list and pin the data directory.
                  preStart="$(sed -n 's/^ExecStartPre=//p' "$resticServicePath" | head -n1)"
                  staticPaths="$(grep -o '/nix/store/[^ ]*-staticPaths' "$preStart" | head -n1)"
                  grep -qx "/srv/git" "$staticPaths"
                  touch $out
                '';
        }
      );
    };
}

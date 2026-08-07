# The hosts the module checks are about: eval-only instantiations of the
# NixOS module, one per declaration worth holding it to. Evaluating one
# catches option-surface and cue-export regressions without building or
# booting anything, and the hook checks read the scripts these render.
{
  pkgs,
  lib,
  self,
  system,
}:
let
  # Each check host shares the machine baseline and supplies its own
  # declaration (and, where the declaration asks for backup, the secret
  # paths).
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
            authorizedKeys = [
              "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlaceholderKeyForEvalOnlyCheck0 valley-check"
            ];
          };
        }
        module
      ];
    };
in
{
  # The example declaration enables backup, so this host supplies
  # the four secret paths (eval-only placeholders).
  host = mkHost {
    services.valley = {
      config = ../../examples/host.cue;
      backup = {
        repositoryFile = "/run/agenix/valley-restic-repo";
        passwordFile = "/run/agenix/valley-restic-password";
        sshKeyFile = "/run/agenix/valley-git-ssh-key";
        knownHostsFile = "/var/lib/valley-backup/known_hosts";
      };
    };
  };

  # A declaration with no optional field set at all — no backup, no
  # protection — must keep evaluating exactly as it did before those
  # fields existed: zero restic machinery, no hook.
  noBackupHost = mkHost {
    services.valley.config = ../../examples/hosts/no-backup.cue;
  };

  # The example declaration with the secret paths left unset: the
  # module must refuse with assertions naming the missing options.
  noSecretsHost = mkHost {
    services.valley.config = ../../examples/host.cue;
  };

  # Bus enabled, minimal declaration. The bus-e2e check drives this
  # host's rendered hooks and stream-init against a real server.
  busHost = mkHost {
    services.valley = {
      config = ../../examples/hosts/bus.cue;
      bus.enable = true;
    };
  };

  # Two mirror URLs the sandbox can serve: git resolves a relative
  # URL against the pushing repo's directory, so the mirror-e2e
  # check drives the rendered script with no substitution at all —
  # one reachable sibling repo, one that does not exist.
  mirrorHost = mkHost {
    services.valley.config = ../../examples/hosts/mirrors.cue;
  };

  # The protection declaration, plus the machine half it needs: keys
  # bound to the principal names it uses. The baseline key above is
  # anonymous and stays in the list, so this host also renders the
  # two forms of key side by side.
  protectedHost = mkHost {
    services.valley = {
      config = ../../examples/hosts/protected.cue;
      authorizedKeys = [
        {
          principal = "integrator";
          key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlaceholderKeyForEvalOnlyCheck1 integrator";
        }
        {
          principal = "contributor";
          key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlaceholderKeyForEvalOnlyCheck2 contributor";
        }
      ];
    };
  };
}

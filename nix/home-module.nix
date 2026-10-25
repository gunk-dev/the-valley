# valley self-update on a timer, for a home-manager user, so the valley CLI
# follows a branch of the-valley without a system rebuild (selfupdate/).
# The flake offers it as homeManagerModules.valley. Enabling it adds the
# valley-self-update user service, which runs `valley self-update`, and a
# timer that starts the service two minutes after boot and every five
# minutes after. Each run installs the branch's tip into the user's nix
# profile once that exact commit builds and passes the smoke test; its
# history is in `journalctl --user -u valley-self-update`.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.valley.selfUpdate;
in
{
  options.programs.valley.selfUpdate = {
    enable = lib.mkEnableOption "the valley-self-update user timer, which installs the tip of a branch of the-valley into the user's nix profile";

    repo = lib.mkOption {
      type = lib.types.str;
      example = "git@valley-host:the-valley";
      description = ''
        The repository of the-valley to follow, as `valley self-update
        --repo` takes it: an https://, ssh://, git:// or file:// URL, an
        absolute path, or user@host:path. user@host:path reads a valley
        host's project from the git user's home, as git does.
      '';
    };

    branch = lib.mkOption {
      type = lib.types.str;
      default = "main";
      description = "The branch of the repository to follow.";
    };
  };

  config = lib.mkIf cfg.enable {
    # The first valley on PATH runs it, so the newest one installed does:
    # the one self-update put in the nix profile, else the one this
    # profile installs. HOME is the user's, so git and ssh read the user's
    # own config and keys, as they do in a shell. The repository and branch
    # are passed as flags, so the timer needs no git config.
    systemd.user.services.valley-self-update = {
      Unit.Description = "Install the tip of the-valley's ${cfg.branch} into the nix profile";
      Service = {
        Type = "oneshot";
        Environment = [
          "PATH=${
            lib.concatStringsSep ":" [
              "${config.home.homeDirectory}/.nix-profile/bin"
              "/etc/profiles/per-user/${config.home.username}/bin"
              (lib.makeBinPath [
                pkgs.nix
                pkgs.git
                pkgs.openssh # git's ssh, for a repository on a valley host
                pkgs.coreutils
              ])
            ]
          }"
        ];
        ExecStart = toString (
          pkgs.writeShellScript "valley-self-update" ''
            exec valley self-update --repo ${lib.escapeShellArg cfg.repo} --branch ${lib.escapeShellArg cfg.branch}
          ''
        );
      };
    };
    systemd.user.timers.valley-self-update = {
      Unit.Description = "Follow the-valley's ${cfg.branch}";
      Timer = {
        OnBootSec = "2min";
        OnUnitActiveSec = "5min";
      };
      Install.WantedBy = [ "timers.target" ];
    };
  };
}

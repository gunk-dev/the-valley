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

          # Eval-only instantiation of the module with the example config —
          # catches option-surface and cue-export regressions without
          # building or booting anything.
          host = lib.nixosSystem {
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
                  config = ./examples/host.cue;
                };
              }
            ];
          };
          failedAssertions = builtins.filter (a: !a.assertion) host.config.assertions;
        in
        {
          # The example host declaration must vet against the schema.
          cue-vet =
            pkgs.runCommand "valley-cue-vet"
              {
                nativeBuildInputs = [ pkgs.cue ];
              }
              ''
                cue vet -c ${./schema/valley.cue} ${./examples/host.cue}
                cue export ${./schema/valley.cue} ${./examples/host.cue} > example.json
                grep -q 'gunk-dev/the-valley' example.json
                touch $out
              '';

          module-eval =
            if failedAssertions != [ ] then
              throw "valley module-eval: failed assertions: ${lib.concatMapStringsSep "; " (a: a.message) failedAssertions}"
            else
              pkgs.runCommand "valley-module-eval"
                {
                  initScript = host.config.systemd.services.valley-init.script;
                  sshdConfig = host.config.services.openssh.extraConfig;
                  passAsFile = [
                    "initScript"
                    "sshdConfig"
                  ];
                }
                ''
                  # The repo init list and mirror hook must be generated from
                  # the CUE export, and the git user's sshd Match block must
                  # be terminated so it cannot scope later config.
                  grep -q "the-valley" "$initScriptPath"
                  grep -q "valley-mirrors" "$initScriptPath"
                  grep -q "Match All" "$sshdConfigPath"
                  touch $out
                '';
        }
      );
    };
}

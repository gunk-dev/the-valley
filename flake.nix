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
      # sigverify is also built and tested for aarch64-linux, because cosmo's
      # Raspberry Pis verify their releases with it. Its checks need no
      # import-from-derivation, so they are defined there too, and an
      # aarch64 builder (or binfmt emulation) runs them. Nothing else here
      # is offered on aarch64-linux, because nothing else is tested there.
      sigverifySystems = [ "aarch64-linux" ];
      # The valley CLI is also offered for aarch64-darwin, so it can be
      # installed on a Mac. It is evaluated here but not built or tested
      # here, because no Darwin machine runs this flake's checks. The
      # valley-darwin-eval check evaluates it from x86_64-linux without
      # building anything for Darwin. Nothing else is offered on Darwin.
      cliSystems = [ "aarch64-darwin" ];

      pkgsFor = system: nixpkgs.legacyPackages.${system};

      # What this flake builds (nix/packages.nix) and what it tests
      # (nix/checks.nix). A flake says which outputs exist and what they are
      # built from; what a package is and what a check does live in files.
      packagesFor =
        system:
        import ./nix/packages.nix {
          inherit lib;
          pkgs = pkgsFor system;
        };
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
          pkgs = pkgsFor system;
        };

      apps =
        lib.genAttrs systems (
          system:
          let
            packages = packagesFor system;
          in
          {
            fmt = {
              type = "app";
              program = lib.getExe packages.prose-fmt;
            };
            attest = {
              type = "app";
              program = lib.getExe packages.attest;
            };
            integrator = {
              type = "app";
              program = lib.getExe packages.integrator;
            };
            identity = {
              type = "app";
              program = lib.getExe packages.identity;
            };
            sigverify = {
              type = "app";
              program = lib.getExe packages.sigverify;
            };
          }
        )
        // lib.genAttrs sigverifySystems (system: {
          sigverify = {
            type = "app";
            program = lib.getExe (packagesFor system).sigverify;
          };
        });

      packages =
        lib.genAttrs systems (
          system:
          let
            packages = packagesFor system;
          in
          {
            inherit (packages)
              valley
              attest
              integrator
              identity
              valleyhook
              sigverify
              ;
            default = packages.valley;
          }
        )
        // lib.genAttrs sigverifySystems (system: {
          inherit (packagesFor system) sigverify;
        })
        // lib.genAttrs cliSystems (system: {
          inherit (packagesFor system) valley;
        });

      checks =
        lib.genAttrs systems (
          system:
          import ./nix/checks.nix {
            inherit lib self system;
            pkgs = pkgsFor system;
            packages = packagesFor system;
          }
        )
        // lib.genAttrs sigverifySystems (
          system:
          import ./nix/checks/sigverify.nix {
            pkgs = pkgsFor system;
            packages = packagesFor system;
          }
        );
    };
}

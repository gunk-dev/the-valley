# What the flake offers on aarch64-darwin (flake.nix): the client programs
# and the formatter. No Darwin machine runs these checks, so they prove
# what x86_64-linux can: that each client program's Go compiles, links and
# vets for darwin/arm64, and that every Darwin output evaluates. Nothing is
# run on Darwin, and no Darwin derivation is built.
{
  pkgs,
  lib,
  self,
  packages,
  ...
}:
let
  darwin = "aarch64-darwin";

  # One client program's Go build (clientPrograms, nix/packages.nix)
  # compiled for darwin/arm64 here, by valley-darwin-build.sh in place of its
  # build: the package's own source, modules and vendored modules, with
  # Darwin's GOOS and GOARCH and no cgo, as the Darwin package builds. The
  # version stamp and the store paths compiled into the program are left
  # out: they change no compile, and the stamp would change the check with
  # every commit.
  darwinBuild =
    build:
    build.overrideAttrs (old: {
      pname = "${old.pname}-darwin";
      version = "0";
      ldflags = [ ];
      env = old.env // {
        GOOS = "darwin";
        GOARCH = "arm64";
        CGO_ENABLED = 0;
      };
      # The package's build targets, or all of its module when it names none.
      targets = if old.subPackages or [ ] == [ ] then [ "./..." ] else map (p: "./${p}") old.subPackages;
      buildPhase = ''
        runHook preBuild
        ${builtins.readFile ./valley-darwin-build.sh}
        runHook postBuild
      '';
      doCheck = false;
      installPhase = ''
        { echo "$pname:"; sed 's/^/  /' "$TMPDIR/vetted"; } > $out
      '';
      dontFixup = true;
    });

  # Every derivation the flake offers on Darwin, with the output that
  # offers it.
  outputs =
    lib.mapAttrsToList (n: p: {
      name = "packages.${darwin}.${n}";
      drv = p;
    }) self.packages.${darwin}
    ++ [
      {
        name = "formatter.${darwin}";
        drv = self.formatter.${darwin};
      }
    ];
  discard = builtins.unsafeDiscardStringContext;

  # What is wrong with the Darwin outputs: a derivation built for another
  # system, and an app whose program no Darwin derivation holds.
  holds = app: lib.any (o: lib.hasPrefix "${discard o.drv.outPath}/" (discard app.program)) outputs;
  wrongSystem = lib.filter (o: o.drv.system != darwin) outputs;
  strayApps = lib.filterAttrs (_: app: !holds app) self.apps.${darwin};
  faults =
    map (o: "${o.name} is for ${o.drv.system}") wrongSystem
    ++ lib.mapAttrsToList (
      n: app: "apps.${darwin}.${n} runs ${discard app.program}, which no Darwin output holds"
    ) strayApps;
in
{
  # Each client program's Go build for darwin/arm64: its package's build
  # targets linked, and go vet over every package of this repository they
  # are built from. Each program is a derivation of its own, so a change to
  # one module compiles again only the programs built from it. The check
  # lists, per program, the packages it vetted.
  valley-darwin-build = pkgs.runCommand "valley-darwin-build" {
    builds = map darwinBuild (lib.attrValues packages.clientPrograms);
  } "cat $builds > $out";

  # Every Darwin package, app and formatter, held to evaluating: an input
  # with no Darwin build fails here rather than on a Mac. Each derivation
  # path is forced while this check is evaluated, and its string context is
  # discarded, so the Darwin derivations are written to the store but are
  # not inputs of this one. Building this check builds and substitutes
  # nothing for Darwin. A derivation for another system, or an app running
  # a program no Darwin derivation holds, fails its evaluation. The report
  # is one line per output: the derivation it is, or the program it runs.
  valley-darwin-eval =
    if faults != [ ] then
      throw "valley-darwin-eval: ${lib.concatStringsSep "; " faults}"
    else
      pkgs.runCommand "valley-darwin-eval" {
        report =
          lib.concatMapStrings (o: "${o.name} ${discard o.drv.drvPath}\n") outputs
          + lib.concatStrings (
            lib.mapAttrsToList (n: a: "apps.${darwin}.${n} ${discard a.program}\n") self.apps.${darwin}
          );
        passAsFile = [ "report" ];
      } (builtins.readFile ./valley-darwin-eval.sh);
}

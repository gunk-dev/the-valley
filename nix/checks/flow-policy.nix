# The project policy's classification of the paths around klaus (flow/):
# what a change to klaus's Go or prose, to its check definition, to the
# packaging it shares, to the valley command its end-to-end suite runs, and
# to an unrelated module requires.
{
  pkgs,
  packages,
  ...
}:
let
  checker = packages.goModule {
    pname = "valley-flow-policy";
    dir = ./flow-policy-checker;
    meta.mainProgram = "flow-policy";
  };
in
{
  # The real floor is in the owning valley's repository, which a build
  # cannot fetch; the example floor stands in for it, as in own-policy.
  flow-policy =
    pkgs.runCommand "valley-flow-policy"
      {
        nativeBuildInputs = [
          checker
          pkgs.git
          packages.valley-unstamped
        ];
      }
      ''
        flow-policy \
          --schema ${../../schema/verification.cue} \
          --floor ${../../examples/policy/instance/floor-format.cue} \
          --template ${../../examples/policy/instance/template-docs.cue} \
          --project ${../../policy/project}
        touch $out
      '';
}

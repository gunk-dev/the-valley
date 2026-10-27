# The push path end to end, over the shipped binaries: valley-web-publish,
# valley-web-receive and valley-web as nix/packages.nix builds them, with
# their wrappers, pinned tools and schemas, the valley CLI as the package
# ships it (valley-script, unstamped, so no commit alone rebuilds it), and
# real git. A valley instance grants anyone read of two projects in one
# group. The publisher uploads them to the receiver, and valley-web serves
# them at their group URLs, with an alias and a former name redirecting.
# A pass with a wrong token is refused as unauthenticated, and the edge
# keeps serving. Withdrawing a project's grant takes it off the edge on
# the next pass. Both servers restart onto the same content. With the
# receiver stopped, valley-web keeps serving, a withdrawal's pass fails
# because its upload cannot connect and changes nothing, and once the
# receiver is back the next pass takes the project off the edge.
#
# It runs in the build sandbox as one user. The split between the
# receiver's user and valley-web's is held by the receiver module's
# checks in module-eval and by web-image.
{
  pkgs,
  packages,
  ...
}:
{
  web-push-e2e = pkgs.runCommand "valley-web-push-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.cue
      pkgs.curl
      pkgs.jq
      packages.valley-script
      packages.valley-web-publish
      packages.valley-web-receive
      packages.valley-web
    ];
    schema = ../../schema/valley.cue;
  } (builtins.readFile ./web-push-e2e.sh);
}

# End-to-end check for the web push path using the packaged binaries:
# valley-web-publish, valley-web-receive, and valley-web as built and
# wrapped by nix/packages.nix (with their pinned tools and schemas),
# the packaged valley CLI (valley-script, unstamped so commits alone do
# not trigger a rebuild), and git.
#
# The test creates a valley instance that grants anyone read access to
# two projects in one group. It verifies that:
#   1. valley-web-publish uploads both projects to valley-web-receive,
#      and valley-web serves them at their group URLs and redirects a
#      short alias and a former name with 308.
#   2. An upload with a wrong token fails as unauthenticated while
#      valley-web continues serving the previous export.
#   3. Withdrawing a project's read grant removes the project on the
#      next pass.
#   4. Both servers preserve the active export across a restart.
#   5. When the receiver is stopped, valley-web continues serving, a
#      pass fails because the upload cannot connect, and the next pass
#      after the receiver restarts applies the withdrawal.
#
# The check runs inside the Nix build sandbox as a single user. The
# separation between the receiver's user and valley-web's user is
# checked by module-eval and web-image.
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

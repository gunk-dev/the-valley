# End-to-end check for the web push path using the packaged binaries:
# valley-web-publish, valley-web-receive, and valley-web as built and
# wrapped by nix/packages.nix (with their pinned tools and schemas),
# the packaged valley CLI (valley-script, unstamped so commits alone do
# not trigger a rebuild), and git.
#
# The test creates a valley instance that grants anyone read access to
# two projects in one group. It verifies that:
#   1. valley-web-publish sends an authorization and uploads both
#      projects to valley-web-receive, which makes the store's descriptor
#      and its v2 payload directory, and valley-web serves them at their
#      group URLs and redirects a short alias and a former name with 308.
#      The pages say when the policy was last checked, and a pass with
#      nothing changed sends no content but a newly signed authorization,
#      whose later check the pages show, while export.json keeps its
#      bytes. A replayed authorization, a forged one, and a manifest of the
#      first protocol are refused, and valley-web refuses the directory the
#      first store layout took as its state.
#   2. An upload with a wrong token fails as unauthenticated while
#      valley-web continues serving the previous export.
#   3. Withdrawing a project's read grant removes the project on the
#      next pass. A --policy-only pass withdraws a project while a
#      content upload is stalled on the receiver's activation lock: the
#      next request to valley-web, not restarted, no longer serves it; the
#      stalled upload, whose export still holds the project, activates
#      later and restores nothing; the receiver deletes the project's
#      repository, and the active export.json keeps its bytes. Removing
#      the stored authorization serves no project. Granting the project
#      again sends its bundle again.
#   4. Both servers preserve the active export across a restart.
#   5. Read links: issued only to a principal who may read a project,
#      carried to the edge as bindings, and revoked by the next pass. A
#      revoke does not wait for a pass checking the policy. It waits for
#      a pass paused holding the links lock, and the pass after it signs
#      no binding for the revoked link. A host
#      restore restores the links as they were at the backup, and
#      revoke --all then cuts every link off.
#   6. When the receiver is stopped, valley-web continues serving, a
#      pass fails because it cannot send its authorization, and the next
#      pass after the receiver restarts signs a new authorization, with a
#      later sequence, that applies the withdrawal.
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
      pkgs.util-linux
      packages.valley-script
      packages.valley-web-publish
      packages.valley-web-receive
      packages.valley-web
    ];
    schema = ../../schema/valley.cue;
    readSchema = ../../schema/read.cue;
  } (builtins.readFile ./web-push-e2e.sh);
}

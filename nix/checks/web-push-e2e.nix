# End-to-end check for the web push path using the packaged binaries:
# valley-web-publish, valley-web-receive, and valley-web as built and
# wrapped by nix/packages.nix (with their pinned tools and schemas),
# the packaged valley CLI (valley-script, unstamped so commits alone do
# not trigger a rebuild), and git.
#
# The test creates a valley instance with four projects in one group: two
# that anyone may read, app and site; docs, which two named readers may
# read, alice and the operator; and notes, which has no effective reader.
# It verifies that:
#   1. valley-web-publish sends an authorization and uploads app, site and
#      docs to valley-web-receive, which makes the store's descriptor
#      and its v2 payload directory, and valley-web serves them at their
#      group URLs and redirects a short alias and a former name with 308.
#      A project's Markdown README renders with an id on its heading and
#      its relative link rewritten to the target's page at the same
#      commit. Every response, a page, a redirect, an archive and a 404,
#      for GET and HEAD, is Cache-Control: private, no-store with Vary:
#      Cookie and no ETag, is never answered 304, and carries its route's
#      policy: the knots page's admits its script by its hash, every
#      other route's admits no script, and none lets a script connect.
#      The pages say when the policy was last checked, and a pass with
#      nothing changed sends no content but a newly signed authorization,
#      whose later check the pages show, while export.json keeps its
#      bytes. A replayed authorization, a forged one, and a manifest of the
#      first protocol are refused, and valley-web refuses the directory the
#      first store layout took as its state. docs reaches the store, its
#      authorization naming both its readers; nothing of notes does; and
#      an anonymous request gets 404 for both on every route.
#   2. An upload with a wrong token fails as unauthenticated while
#      valley-web continues serving the previous export.
#   3. A policy check that cannot read docs keeps only the readers a
#      grant still covers, on the basis of the stored authorization, and
#      fails as incomplete. Removing one of docs' two readers keeps docs;
#      removing the last withdraws it and deletes its repository while
#      export.json keeps its bytes. Withdrawing anyone's grant removes a
#      project from anonymous view on the next pass, and keeps it on the
#      edge for its named readers. A withdrawal of site that coincides
#      with growth past the 256 KiB a receiver reads still reaches the
#      edge, by the bounded fallback, and the pass fails naming the
#      overflow. A --policy-only pass withdraws site
#      and docs while a content upload is stalled on the receiver's
#      activation lock: the next request to valley-web, not restarted, no
#      longer serves site; the stalled upload, whose export still holds
#      both, activates later and restores neither's authorization; the
#      receiver deletes both repositories, and the active export.json
#      keeps its bytes. Removing the stored authorization serves no
#      project. Granting a project again sends its bundle again.
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
#   7. Readers sign in at the origin valley-web is given, with its browser
#      state in a directory the test makes for it. valley-web started
#      before the receiver made its store exits, and one started under
#      an authorization that binds no link makes an empty state. A link
#      the host issues redeems once, into a session renewed on a
#      redirect, a 404, a page and an archive, whose bytes match
#      anyone's download; of eight concurrent redemptions of one link one
#      wins; the session survives both servers' restarts, ends at logout,
#      and ends at the pass that delivers its link's revocation; a second
#      valley-web over the same browser state refuses to start. A lost
#      state is not made again while a link is bound, with redemption and
#      cookies answered 503 and anyone served, until every link is
#      revoked and a pass delivers that; an unreadable state is kept as it
#      is until the operator removes it. No log, no browser state and no
#      host state holds a link's secret or a session's token.
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

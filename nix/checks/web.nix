# The web UI (web/): the export contract the publisher, the receiver
# and the server share, the server, and the receiver. Their tests run
# here, in the packages' unit builds (nix/packages.nix).
{
  pkgs,
  packages,
  ...
}:
{
  # The projection over the shared fixture, which holds every disclosure
  # rule: what reaches export.json, hidden projects' edges, a knot that
  # waits on a hidden knot marked with waiting_on_hidden alone, the field
  # gone once its one hidden blocker is done, the display mapping. The
  # content digest; strict reading of export.json, with
  # nothing after its document; each of VerifyManifest's steps, in order;
  # the manifest's grammar and its agreement with export.json; the
  # streaming body check; bundle acceptance against real git, refusing
  # wrong tips, prerequisites, extra refs, corrupt objects and a header
  # past its bound, writing a commit-graph git verifies, and serving no
  # commit main does not reach, nor one named in the other object
  # format's length, alone or among several; git's
  # output read within its limit, with git stopped past it or when its
  # reader stops early; and the receiver's state directory. For the state
  # directory: uploads private to the receiver and generations readable
  # by every user, whatever its umask; readers holding
  # a generation through activations, every file of a generation synced
  # before high-water names it, a repository a crash interrupted built
  # again, recovery checking a generation before it activates it and
  # keeping one whose check git did not finish, and injected sweep and
  # swap failures, reported apart from the commit. The publisher's pass
  # uploading to the real receiver with its token, the real valley-web
  # serving what it activated, a pass with another token refused as
  # unauthenticated while the edge keeps serving, and a pass after
  # anyone's grant is withdrawn taking the project off the edge. go vet
  # runs after them.
  web-unit = packages.web.unit;

  # Read access, which the publisher reads its consents through: both
  # consents read out of real commits under the real schemas, an explicit
  # empty reader set and an empty scope letting no one read, anyone's
  # restrictions, no principal name special, a policy for another project
  # and a grant for another identity covering nothing, other destinations'
  # grants and expired principals' left out, policy/web never read, tree
  # entries with a directory part or named twice refused before anything
  # is copied out, a directory named twice on the way to a document
  # refused, redeclared definitions widening nothing, a field spelled in
  # another case than the schema's refused, and the coverage report over a
  # real valley, with the registry read at its ref. go vet runs after them.
  readaccess-unit = packages.readaccess.unit;

  # The server and valley-web over the shared fixture, uploaded to a real
  # store, and real git. The plan page's reading order, its free to start
  # legend, its one card of ready changes, and the export's age, marked
  # stale after five minutes; a knot's body and comments, with the edited
  # one marked; a hidden project answering every route exactly as a
  # missing one, though its repository is in the generation; a blocked
  # knot's chip naming its published blockers and, once, unpublished
  # work; projects
  # named source, knots and web-export served on every route, and their
  # roots redirected to their trees, while the server's own paths under
  # /-/ are never a project's; a commit
  # main's tip does not reach, and every other object ID or name, refused
  # with the one 404; hostile README, file, file name, commit subject and
  # knot text rendered inert, and large and binary files not shown; a
  # reference into the hidden project naming nothing of it; a directory
  # past MaxRead listed in part, and a 5 MiB commit message ending its
  # page of history early; the directories along a path sharing one
  # MaxRead; the older links of history showing every commit once through
  # a merge whose lines run across pages, and every link of a page past
  # MaxFrontier lines, or one ended before a merge past MaxLeftToShow,
  # reaching every commit; a merge whose parents pass a record of git log
  # shown, first on a page and further down, with its first MaxParents
  # followed; a source request's git slot held for a bound of its own from
  # admission, through a slow export read and its git, answering 503 and
  # giving back its slot, and given back before it renders; a SHA-256
  # project served by full commit IDs alone; a file of MaxFile newlines shown to MaxLines;
  # a chain of knots near the graph's limit shown to MaxPlanDepth, with
  # the plan's structure built once; the policy on every response,
  # admitting the page's inline stylesheet and script by their hashes; a
  # request keeping its generation while another activates; an export
  # read once per generation, however many requests wait for it, across
  # activations; a load started only by a request taking a loader slot,
  # with requests that wait for one ending at their deadline or when
  # their reader goes away, leaving no load behind and holding no
  # generation; a load reading export.json from a slow pipe stopped part
  # way once its one request's deadline passes, caching nothing and
  # giving back its generation; a flood during a load keeping
  # MaxLoadWaiting requests waiting and answering the rest 503, with the
  # server's goroutines and open files bounded; source requests past
  # MaxGit and MaxWaiting answering 503, with an activation while they
  # wait and a deadline that covers the wait; a request held past its
  # deadline before it renders answering 503; a page past MaxResponse on
  # a knot, and past a smaller bound on a file, a directory, a page of
  # history and the plan, answering the one short page that says it is
  # too large; readers that never read holding no git slot and no
  # generation, and, asking for more than MaxBuffered in large pages of
  # three sizes, holding no more of the heap than it and no more than
  # MaxLarge, their pages' starts counted, with a large page it has no
  # room for answering 503 while the plan, a knot and a directory still
  # render; and valley-web run as a process of its own, serving and
  # stopping on SIGTERM, and serving every page from a state directory
  # the receiver wrote under umask 0077 and that reads as another
  # user's, with nothing in it writable. go vet runs after them.
  web-server-unit = packages.valley-web.unit;

  # The receiver, valley-web-receive, run as a process of its own over the
  # shared fixture's export, signed now, and real git. Each step of the
  # upload activating the export, which the real valley-web, a process of
  # its own over the same state directory, serves; the manifest posted
  # again and the commit made again refused as replayed. A request without
  # the token, with another, in another scheme or twice refused as
  # unauthenticated, whatever it asks, staging nothing. Stale, early,
  # foreign-key and foreign-destination manifests refused, an older
  # manifest than the one staged superseded, with its bodies, and a commit
  # of a sequence never staged refused as no-manifest. Bodies with a hash
  # mismatch, a size mismatch with and without a length, a
  # Content-Encoding, or an undeclared hash refused; a body cut off half
  # way leaving nothing in staging, its commit refused as incomplete with
  # the active export as it was, and the body sent whole then activating.
  # A body past --max-body and bodies past --max-upload refused as
  # too-large before anything is staged, and an upload at both caps
  # accepted. Every request slot held by a stalled upload, the next
  # request answered 503 and one without the token still unauthenticated,
  # while valley-web serves every page; the receiver taking uploads again
  # once the stalled ones go away. A clean stop on SIGTERM, and a usage
  # error, a token file it cannot use and verifiers it cannot read refused
  # without printing the token. go vet runs after them.
  web-receiver-unit = packages.valley-web-receive.unit;

  # The edge's container image, unpacked as a container runtime would:
  # valley-web and valley-web-receive at /bin, each pointing at a program
  # the image holds, with the git each runs held too; both users and their
  # group in /etc/passwd and /etc/group; /var/lib/valley-web the image's
  # one volume, owned by the receiver's user and group with mode 0750; and
  # the image running as valley-web unless told otherwise, starting no
  # program of its own.
  web-image = pkgs.runCommand "valley-web-image" {
    nativeBuildInputs = [ pkgs.jq ];
    image = packages.valley-web-image;
  } (builtins.readFile ./web-image.sh);
}

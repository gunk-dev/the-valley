# The web UI (web/): the shared export contract, the publisher, the
# receiver, and the web server. Each check but web-image runs the unit
# test derivation defined in nix/packages.nix. web-image is a
# runCommand that unpacks the container image.
{
  pkgs,
  packages,
  ...
}:
{
  # The projection over the shared fixture, which holds every disclosure
  # rule: what reaches export.json, hidden projects' edges, a knot that
  # waits on a hidden knot marked with waiting_on_hidden alone, the field
  # gone once its one hidden blocker is done, the display mapping, each
  # public project's group, web name, former names and short aliases with
  # only public projects' groups, and names that collide refused, each
  # case for the reason it names. The content digest; strict reading of
  # export.json, with keys spelled exactly, none twice, and nothing after
  # its document; the names checked, and the groups validated, stopping
  # at each cancellation check; each of VerifyManifest's steps, in order;
  # the manifest's grammar and its agreement with export.json; the
  # streaming body check; bundle acceptance against real git, refusing
  # wrong tips, prerequisites, extra refs, corrupt objects and a header
  # past its bound, writing a commit-graph git verifies, and serving no
  # commit main does not reach, nor one named in the other object
  # format's length, alone or among several; git's
  # output read within its limit, with git stopped past it or when its
  # reader stops early; and the receiver's state directory. For the state
  # directory: staging/ stays private to the receiver and exports/ stays
  # group- and world-readable under any umask; readers hold
  # a generation through activations, every file of a generation is synced
  # before high-water names it, a repository interrupted by a crash is
  # rebuilt, recovery verifies a generation before activating it and keeps
  # one whose git check was interrupted, and injected sweep and swap
  # failures are reported separately from the commit; Stage and Activate
  # wait for a held store lock only until their context expires, leaving
  # staging and the active generation unchanged on cancellation; a
  # manifest's capacity sweep stops once its context is done, leaving the
  # rest of the trash and releasing the lock; and an activation that
  # exceeds MaxStorage is refused, with the repository whose build passed
  # the cap removed, while repositories built before it remain. The
  # publisher refuses a host declaration that is malformed, null where cue
  # exports an object, or names a project by something that is not a name,
  # before a pass. An end-to-end pass uploads to a real valley-web-receive
  # with its bearer token, a real valley-web serves the activated export, a
  # pass with a wrong token fails as unauthenticated while the edge keeps
  # serving, and a pass after withdrawing anyone's grant removes the project
  # from the edge. go vet runs after the tests.
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
  # one marked; a hidden project answering every route under its group,
  # under no group and under its short alias exactly as a missing one,
  # though its repository is in the generation; a blocked knot's chip
  # naming its published blockers and, once, unpublished work; groups and
  # projects named source, knots and web-export served on every route
  # under /<group>/<project>, and their roots redirected to their trees,
  # while the server's own paths under /-/ are never a group's; two groups
  # each serving a project named app; a group's alias, a project's former
  # name and a project's short alias answering with a permanent redirect
  # that keeps the path and query as sent, a bare "?" too, and leads a
  # browser that resolves its dot segments to a path on the same origin;
  # the index of groups, aliases and former names stopping at each
  # cancellation check; every 404 the one page made at
  # start, the box of text with no site name; a commit
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
  # stopping on SIGTERM, and serving every page from a read-only state
  # directory that the receiver wrote under umask 0077, with the test
  # simulating another owner, since it cannot change users. go vet runs
  # after the tests.
  web-server-unit = packages.valley-web.unit;

  # The receiver binary, valley-web-receive, run as a subprocess over the
  # shared fixture's signed export and real git. A three-step upload
  # activates an export that a separate valley-web process serves from the
  # same state directory, and re-posting the manifest or commit is refused
  # as replayed. Manifests with no token, a wrong token, a non-Bearer
  # scheme, or two Authorization headers, and a body, a commit and an
  # unused path with no token, are refused as unauthenticated without
  # staging anything. Stale, future-dated, foreign-key, and
  # foreign-destination manifests are refused; a manifest older than the
  # staged one is refused as superseded while newer manifests replace staged
  # bodies; and committing an unstaged sequence fails as no-manifest. Bodies
  # with a hash mismatch, a size mismatch (with or without Content-Length),
  # a Content-Encoding header, or an undeclared hash are refused; a
  # concurrent upload of the same body receives 503; and an interrupted body
  # upload leaves no partial file in staging, fails commit as incomplete
  # while keeping the active export intact, and activates once re-sent in
  # full. Manifests and bodies past --max-body or --max-upload fail as
  # too-large before staging, while uploads at both caps succeed; a manifest
  # reusing one bundle across eight projects fails when it exceeds
  # --max-storage, and uploads within the cap succeed. When stalled
  # manifests fill all 4 request slots, the next authenticated request gets
  # 503 and an unauthenticated request still gets 401 while valley-web keeps
  # serving, and uploads resume once the stalled clients disconnect. When 32
  # connections stall in headers, valley-web keeps serving and a subsequent
  # upload succeeds once the header timeout closes them. OPTIONS * with a
  # withheld body on all 32 connections returns immediately so a following
  # upload succeeds. A manifest request canceled while waiting for the store
  # lock stages nothing. A commit whose client disconnects, and a SIGTERM
  # during a commit, kill the process group of the commit's git: a shell
  # that stalls in commit-graph and its forked child holding git's output
  # open both die, the bundle stays staged, and the receiver exits only once
  # the commit has ended. On SIGTERM the receiver drains and exits within its
  # timeout even with all connections held, and CLI usage, token-file, and
  # verifier-file errors exit without printing the token. Every test request,
  # dial, and wait for a subprocess is bounded. go vet runs after the tests.
  web-receiver-unit = packages.valley-web-receive.unit;

  # The edge container image, unpacked layer by layer: /bin/valley-web and
  # /bin/valley-web-receive resolve to store paths inside the image, and
  # the image holds the git each program runs at its pinned store path; /etc/passwd and /etc/group define
  # valley-web-receive, valley-web, and their shared valley-web group;
  # /var/lib/valley-web is the sole declared volume and is owned by
  # valley-web-receive:valley-web with mode 0750; and the image defaults to
  # user valley-web with no default entrypoint or command.
  web-image = pkgs.runCommand "valley-web-image" {
    nativeBuildInputs = [ pkgs.jq ];
    image = packages.valley-web-image;
  } (builtins.readFile ./web-image.sh);
}

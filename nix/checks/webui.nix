# The web UI (webui/): the export contract the publisher and the server
# share, and the server. Their tests run here, in the packages' unit
# builds (nix/packages.nix).
{
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
  # swap failures, reported apart from the commit. go vet runs after them.
  webui-unit = packages.webui.unit;

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
  webui-server-unit = packages.valley-web.unit;
}

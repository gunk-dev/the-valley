# The web UI (webui/): the export contract the publisher and the server
# share, and the server. Their tests run here, in the packages' unit
# builds (nix/packages.nix).
{
  packages,
  ...
}:
{
  # The projection over the shared fixture, which holds every disclosure
  # rule: what reaches export.json, hidden projects' edges, the display
  # mapping. The content digest; strict reading of export.json, with
  # nothing after its document; each of VerifyManifest's steps, in order;
  # the manifest's grammar and its agreement with export.json; the
  # streaming body check; bundle acceptance against real git, refusing
  # wrong tips, prerequisites, extra refs, corrupt objects and a header
  # past its bound, and serving no commit main does not reach; git's
  # output read within its limit, with git stopped past it or when its
  # reader stops early; and the receiver's state directory. For the state directory: readers holding
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
  # missing one, though its repository is in the generation; a commit
  # main's tip does not reach, and every other object ID or name, refused
  # with the one 404; hostile README, file, file name, commit subject and
  # knot text rendered inert, and large and binary files not shown; a
  # reference into the hidden project naming nothing of it; a directory
  # past MaxRead listed in part, and a 5 MiB commit message ending its
  # page of history early; a file of MaxFile newlines shown to MaxLines;
  # a chain of knots near the graph's limit shown to MaxPlanDepth, with
  # the plan's structure built once; the policy on every response,
  # admitting the page's inline stylesheet and script by their hashes; a
  # request keeping its generation while another activates; an export
  # read once per generation, however many requests open it, across
  # activations; a read of an export started only by a request holding a
  # loader slot, with requests that wait for one ending at their deadline
  # or when their reader goes away, leaving no read behind and holding no
  # generation; source requests past MaxGit and MaxWaiting answering 503,
  # with an activation while they wait and a deadline that covers the
  # wait; a request held past its deadline before it renders answering
  # 503; a page past MaxResponse on a knot, and past a smaller bound on a
  # file, a directory, a page of history and the plan, answering the one
  # short page that says it is too large; readers that never read holding
  # no git slot and no generation, and, asking for twice MaxBuffered,
  # holding no more of the heap than it, with a page it has no room for
  # answering 503; and valley-web run as a process of its own, serving and
  # stopping on SIGTERM. go vet runs after them.
  webui-server-unit = packages.valley-web.unit;
}

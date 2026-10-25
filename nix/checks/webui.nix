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
  # past its bound, and serving no commit main does not reach; and the
  # receiver's state directory. For the state directory: readers holding
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
  # knot text rendered inert, and large and binary files not shown; the
  # policy on every response, admitting the page's inline stylesheet and
  # script by their hashes; a request keeping its generation while
  # another activates; and valley-web run as a process of its own,
  # serving and stopping on SIGTERM. go vet runs after them.
  webui-server-unit = packages.valley-web.unit;
}

# The web UI (webui/): the export contract the publisher and the server
# share. Its tests run here, in the package's unit build
# (nix/packages.nix).
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
  # again, recovery checking a generation before it activates it, and
  # injected sweep and swap failures, reported apart from the commit. go
  # vet runs after them.
  webui-unit = packages.webui.unit;
}

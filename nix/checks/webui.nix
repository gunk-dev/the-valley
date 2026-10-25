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
  # mapping. The content digest; each of VerifyManifest's steps, in order;
  # the manifest's grammar and its agreement with export.json; the
  # streaming body check; bundle acceptance against real git, refusing
  # wrong tips, prerequisites, extra refs, corrupt objects, and serving no
  # commit main does not reach; and the receiver's state directory, with
  # readers holding a generation through activations and recovery after a
  # crash. go vet runs after them.
  webui-unit = packages.webui.unit;
}

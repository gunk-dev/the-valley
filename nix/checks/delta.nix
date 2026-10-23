# valley-delta-v1 (delta/): the module the valley command compares a
# version with its approved version through. Its tests run here, in the
# package's unit build (nix/packages.nix).
{
  packages,
  ...
}:
{
  # The encoding's strict reading, the conformance vectors held to the
  # independent script that computed them, the delta read from trees real
  # git built, and the trees it must refuse, run with go vet.
  delta-unit = packages.delta.unit;
}

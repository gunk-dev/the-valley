# valley-delta-v1 (delta/): the module the valley command compares a
# version with its approved version through. valley-go does not run a
# module's tests when it builds with it, so they run here.
{
  packages,
  ...
}:
{
  # The encoding's strict reading, the conformance vectors held to the
  # independent script that computed them, the delta read from trees real
  # git built, and the trees it must refuse, run in the package's
  # checkPhase with go vet.
  delta-unit = packages.delta;
}

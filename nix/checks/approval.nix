# Approval statements: their written form held to pinned vectors, every
# field under the signature, the notes a reader must refuse, the authority
# a reader holds deciding which keys count, and the PIV session against a
# stand-in helper and against the packaged helper with no device to reach.
{ packages, ... }:
{
  approval-unit = packages.approval.unit;
}

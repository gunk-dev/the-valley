# Review records (review/): the record, its binding and its history.
# No program builds with the module, so its tests run here.
{
  packages,
  ...
}:
{
  # The record's strict reading and its bounds, with its fuzz target's
  # seed corpus; a binding made and recomputed from real git, refused when
  # any part of it does not reproduce, when the base is missing or
  # ambiguous, and when a hostile tree crosses the budget; and the history:
  # its parents, the records it keeps once the change's record ref is gone,
  # unique review IDs, withdrawals, every append the hook must refuse, and
  # a compare-and-swap publish between two clones. go vet runs after them.
  review-unit = packages.review;
}

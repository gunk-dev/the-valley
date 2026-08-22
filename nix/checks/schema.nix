# The schemas against the examples they must accept and the ones they must
# reject. CUE closedness is easy to regress silently — embedding a
# definition instead of referencing it opens the schema — so every rejection
# is a checked-in example, and the ones whose field carries the weight are
# held to naming it.
{ pkgs, ... }:
{
  # A host declaration: the pilot's own, the compatibility case, the
  # protection block the installer renders a hook from, and the nine
  # declarations the schema must refuse. The event payloads live here too —
  # the publisher hook's vocabulary is a schema like any other.
  cue-vet = pkgs.runCommand "valley-cue-vet" {
    nativeBuildInputs = [ pkgs.cue ];
    schema = ../../schema/valley.cue;
    example = ../../examples/host.cue;
    hosts = ../../examples/hosts;
    eventSchema = ../../schema/events.cue;
    events = ../../examples/events;
    rejectedCases = ./cue-vet-rejected.txt;
  } (builtins.readFile ./cue-vet.sh);

  # The attestation statement schema (dcr-0de694f). A statement the
  # helper composes must vet, and the schema must reject what it
  # claims to reject. Two rejections carry the decision's whole
  # weight and are pinned by name in the script: a subject with no
  # content-addressed digest has no identity at all, and a
  # predicate that crosses the pure/effectful line would let a
  # notarization pass itself off as re-derivable.
  attest-schema = pkgs.runCommand "valley-attest-schema" {
    nativeBuildInputs = [ pkgs.cue ];
    schema = ../../schema/attestation.cue;
    statements = ../../examples/attestations;
  } (builtins.readFile ./attest-schema.sh);

  # The identity registry schema (dcr-b87f6e8). The worked registry vets,
  # and the ten registries the schema must refuse are refused. Four of those
  # are the substrate's floor over every instance registry — mandatory
  # expiry where a raw key is held, an external principal carrying one, no
  # external principal governing the registry, a genesis entry that governs
  # it — and a floor that stopped being enforced would be silent, so each is
  # held to naming its field.
  identity-schema = pkgs.runCommand "valley-identity-schema" {
    nativeBuildInputs = [ pkgs.cue ];
    schema = ../../schema/identity.cue;
    registry = ../../examples/identity/registry;
    rejected = ../../examples/identity/rejected;
    rejectedCases = ./identity-rejected.txt;
  } (builtins.readFile ./identity-schema.sh);
}

# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
cue vet -c "$schema" "$statements/pure.json"
cue vet -c "$schema" "$statements/effectful.json"

# Each rejected statement is the pure or effectful one with a single thing
# wrong; the file name says which.
for bad in "$statements"/rejected/*.json; do
  name="$(basename "$bad" .json)"
  if cue vet -c "$schema" "$bad" > "$name.err" 2>&1; then
    echo "attest-schema: expected invalid statement '$name' to be rejected" >&2
    exit 1
  fi
done

# A map whose keys come from a caller is written as
# #SegmentKeyed. A field's name is written into the key that
# names its line, so a name holding the dot that separates
# segments, the space that separates key from value, or
# anything a line cannot carry is a statement with no written
# form. It fails vet rather than reaching a signer.
cue vet -d '#SegmentKeyed' "$schema" "$statements/segment-keys.json"
for bad in "$statements"/rejected-keys/*.json; do
  name="$(basename "$bad" .json)"
  if cue vet -d '#SegmentKeyed' "$schema" "$bad" > "$name.err" 2>&1; then
    echo "attest-schema: expected the key in '$name' to be rejected" >&2
    exit 1
  fi
  grep -q 'field not allowed' "$name.err"
done

# A rejection that does not name the field leaves the reader
# to find the breakage themselves.
grep -q 'signature: conflicting values' signature-in-statement.err
grep -q 'subject.digest."valley-tree-v1"' no-content-addressed-subject.err
grep -q 'predicate.environment: field not allowed' effectful-claiming-pure.err
grep -q 'predicate.check.command' command-over-two-lines.err
touch "$out"

# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
vectors="$vectorDir"

# One vector written out and compared, quietly, so the
# perturbed run at the end can expect it to fail without
# saying so.
writes() { attest render "$1/input.json" | cmp -s - "$1/statement.txt"; }

found=0
signed=0
for dir in "$vectors"/vectors/*/; do
  name="$(basename "$dir")"
  found=$((found + 1))

  if ! writes "$dir"; then
    echo "attest-conformance: $name is not written as its recorded bytes" >&2
    diff -u "$dir/statement.txt" <(attest render "$dir/input.json") >&2 || true
    exit 1
  fi

  # The written form is a fixed point, or a statement written
  # out again anywhere downstream stops matching what was
  # signed.
  if ! attest render "$dir/statement.txt" | cmp -s - "$dir/statement.txt"; then
    echo "attest-conformance: $name is not a fixed point of the written form" >&2
    exit 1
  fi

  # Every vector is a whole statement — order is defined per
  # predicate type, so a fragment has no written form — and is
  # held to the schema too, so the set cannot drift into
  # documents no verifier would accept.
  cue vet -c "$schema" "$dir/input.json"

  # The signed vector pins the whole path from a statement to
  # a note something else can check, not just the written
  # form. No --repo: the subject tree is not what this check
  # is about.
  if [ -e "$dir/statement.note" ]; then
    signed=$((signed + 1))
    attest verify --note "$dir/statement.note" \
      --known-keys "$vectors/known_keys" > "$name.verify"
    grep -q '^signature ok' "$name.verify"
    grep -q '^text      ok' "$name.verify"

    # The text a verifier reads back is the text on disk: a
    # note is its statement, and reading one gives the other.
    sed '/^$/q' "$dir/statement.note" | sed '$d' | cmp - "$dir/statement.txt"
  fi
done

# Two signers over one statement is the property the note
# format is here for, so the set must actually hold one.
siblings="$(grep -c '^— ' "$vectors"/vectors/04-two-signers/statement.note)"
if [ "$siblings" -ne 2 ]; then
  echo "attest-conformance: the two-signer vector carries $siblings signatures" >&2
  exit 1
fi
attest verify --note "$vectors"/vectors/04-two-signers/statement.note \
  --known-keys "$vectors/known_keys" \
  --signer conformance.the-valley.invalid/attestations \
  --signer witness.the-valley.invalid/attestations > siblings.verify
if [ "$(grep -c '^signature ok' siblings.verify)" -ne 2 ]; then
  echo "attest-conformance: both sibling signatures must verify" >&2
  cat siblings.verify >&2
  exit 1
fi

if [ "$found" -lt 4 ] || [ "$signed" -lt 2 ]; then
  echo "attest-conformance: $found vectors, $signed signed — the set has been emptied" >&2
  exit 1
fi

# The other half of the contract: what must not be written or
# read at all. A document with no written form, and text that
# is not the written form of anything.
refused=0
for file in "$vectors"/refused/*; do
  refused=$((refused + 1))
  if attest render "$file" > /dev/null 2> "$(basename "$file").err"; then
    echo "attest-conformance: $(basename "$file") was accepted" >&2
    exit 1
  fi
done
if [ "$refused" -lt 30 ]; then
  echo "attest-conformance: only $refused refusals — the set has been emptied" >&2
  exit 1
fi

# The comparison must be able to fail. One recorded byte
# sequence with a single value changed is what a divergent
# implementation looks like, and it must not pass.
mkdir -p perturbed
cp "$vectors/vectors/01-statement-minimal/input.json" perturbed/
sed 's/^predicate.result passed$/predicate.result failed/' \
  "$vectors/vectors/01-statement-minimal/statement.txt" > perturbed/statement.txt
cmp -s perturbed/statement.txt "$vectors/vectors/01-statement-minimal/statement.txt" \
  && { echo "attest-conformance: the perturbation changed nothing" >&2; exit 1; }
if writes perturbed; then
  echo "attest-conformance: a perturbed vector passed; the comparison proves nothing" >&2
  exit 1
fi

# And so must the signature check. One byte of the signed
# text changed is a signature that no longer covers it.
sed 's/a sealed environment/a sealed environmenT/' \
  "$vectors"/vectors/04-two-signers/statement.note > perturbed/statement.note
if attest verify --note perturbed/statement.note \
  --known-keys "$vectors/known_keys" > perturbed.verify 2>&1; then
  echo "attest-conformance: a note whose text was edited still verified" >&2
  exit 1
fi
grep -q 'does not check out over this text' perturbed.verify

echo "attest-conformance: $found vectors written byte for byte, $signed signed, $refused refused"
touch "$out"

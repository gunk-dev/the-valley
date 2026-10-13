# shellcheck shell=bash
# The Darwin package was evaluated when this derivation was; all that is
# left is to confirm it is the Darwin one and record which derivation it is.
# The values come from the derivation environment.
# shellcheck disable=SC2154
if [ "$darwinSystem" != aarch64-darwin ]; then
  echo "valley-darwin-eval: packages.aarch64-darwin.valley is for $darwinSystem" >&2
  exit 1
fi
echo "$darwinDrv" > "$out"

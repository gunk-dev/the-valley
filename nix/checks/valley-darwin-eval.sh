# shellcheck shell=bash
# Every Darwin output was evaluated, and held to Darwin, when this
# derivation was (darwin.nix); all that is left is to record the report.
# The values come from the derivation environment.
# shellcheck disable=SC2154
cp "$reportPath" "$out"

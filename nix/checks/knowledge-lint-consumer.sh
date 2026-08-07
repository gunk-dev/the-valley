# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
test -e "$consumer" && test -e "$graphless"
touch "$out"

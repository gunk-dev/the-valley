# shellcheck shell=bash
# The tree comes from the derivation environment.
# shellcheck disable=SC2154
go-dependencies "$tree"
touch "$out"

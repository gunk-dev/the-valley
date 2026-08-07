# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
shellcheck "$cliScript"
shellcheck "$bashCompletion"
# The linter cannot read zsh; a bare parse is the cheap check.
zsh -n "$zshCompletion"
valley help > help.txt
grep -q '^usage: valley' help.txt
test -f "$valley/share/bash-completion/completions/valley"
test -f "$valley/share/zsh/site-functions/_valley"
touch "$out"

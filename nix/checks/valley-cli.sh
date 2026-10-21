# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
shellcheck "$cliScript"
shellcheck "$bashCompletion"
# The linter cannot read zsh; a bare parse is the cheap check.
zsh -n "$zshCompletion"
# The completion turns the handle cache into candidates, ready changes
# first, with the title as the description, and then the branches. _describe
# and compadd are stubbed to print what they are given.
repo="$PWD/completion-repo"
git init --quiet "$repo"
mkdir "$repo/.git/valley"
printf 'mxrr\tready\tbump-the-valley-8770343e\tflake: bump the-valley\nhttp:abcd\tdraft\t\tno branch\n' > "$repo/.git/valley/handles"
git -C "$repo" update-ref refs/remotes/origin/topic HEAD 2>/dev/null || true
cat > complete.zsh <<'ZSH'
_describe() { print -r -- "describe $*"; print -rl -- "${(P@)${@[-1]}}" | sed 's/^/  /'; }
compadd() { print -r -- "compadd $*"; }
words=(valley review '')
CURRENT=3
source "$1"
ZSH
(cd "$repo" && zsh "$OLDPWD/complete.zsh" "$zshCompletion") > complete.out
grep -q '^  mxrr:flake: bump the-valley$' complete.out
grep -q '^  http\\:abcd:no branch$' complete.out
# With no cache, the branches alone.
rm "$repo/.git/valley/handles"
(cd "$repo" && zsh "$OLDPWD/complete.zsh" "$zshCompletion") > complete.out
if grep -q describe complete.out; then exit 1; fi
valley help > help.txt
grep -q '^usage: valley' help.txt
test "$(wc -l < help.txt)" -le 50
expected_verbs="ready
unready
withdraw
rebase
checks
pending
review
approve
status
changes
plan
knot
tail
replay
self-update"
verbs="$(awk '/^  [a-z]/ { print $1 }' help.txt)"
test "$verbs" = "$expected_verbs"
for v in $verbs; do
  valley help "$v" > "help-$v.txt"
  grep -q "^usage: valley $v" "help-$v.txt"
  valley "$v" --help > "help-$v-flag.txt"
  cmp -s "help-$v.txt" "help-$v-flag.txt"
  valley "$v" -h > "help-$v-short.txt"
  cmp -s "help-$v.txt" "help-$v-short.txt"
done
if valley help unknown-verb > /dev/null 2> err.txt; then exit 1; fi
grep -q 'unknown help topic: unknown-verb' err.txt
# Both zsh and bash completions complete all 15 verbs after `valley help`.
cat > complete-help.zsh <<'ZSH'
_describe() { print -r -- "describe $*"; print -rl -- "${(P@)${@[-1]}}" | sed 's/^/  /'; }
compadd() { print -r -- "compadd $*"; }
words=(valley help '')
CURRENT=3
source "$1"
ZSH
zsh "$PWD/complete-help.zsh" "$zshCompletion" > complete-help.out
for v in $verbs; do
  grep -q "^  $v:" complete-help.out
done
complete() { :; }
# shellcheck disable=SC2086
compgen() { printf '%s\n' $2; }
# shellcheck disable=SC1090
source "$bashCompletion"
COMP_WORDS=(valley help '')
COMP_CWORD=2
_valley
for v in $verbs; do
  printf '%s\n' "${COMPREPLY[@]}" | grep -qx "$v"
done
test -f "$valley/share/bash-completion/completions/valley"
test -f "$valley/share/zsh/site-functions/_valley"
touch "$out"

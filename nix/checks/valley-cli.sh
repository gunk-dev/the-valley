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
test -f "$valley/share/bash-completion/completions/valley"
test -f "$valley/share/zsh/site-functions/_valley"
touch "$out"

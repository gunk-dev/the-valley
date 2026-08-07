# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
export HOME="$TMPDIR" XDG_STATE_HOME="$TMPDIR/state" TERM=xterm
export GIT_CONFIG_NOSYSTEM=1
unset VISUAL || true
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main

# A project with an origin and one branch to review: two
# files, changed twice and once, far enough apart that the
# diff has two hunks in the first file and one in the second.
git init --quiet --bare "$TMPDIR/origin.git"
git init --quiet "$TMPDIR/work"
cd "$TMPDIR/work" || exit 1
git remote add origin "$TMPDIR/origin.git"
seq 1 20 | sed 's/^/alpha /' > alpha.md
seq 1 10 | sed 's/^/beta /' > beta.md
git add -A
git commit --quiet -m seed
git push --quiet -u origin main
git checkout --quiet -b scratch
sed -i -e '3s/.*/alpha 3 changed/' -e '15s/.*/alpha 15 changed/' alpha.md
sed -i -e '5s/.*/beta 5 changed/' beta.md
git commit --quiet -am "touch both files"
git push --quiet origin scratch
git checkout --quiet main
git fetch --quiet origin

# A pager filter that restructures the diff, as delta's
# rendering does: if v were handed what less displays, every
# anchor below would be wrong.
git config --global pager.diff "sed -e 's/^/| /'"
export EDITOR="$reviewer"
mkdir -p "$XDG_STATE_HOME"

# v opens the editor, RETURN dismisses less's LESSOPEN
# warning, q leaves the pager, s is the verdict.
{ printf 'v\nq'; sleep 2; printf 's\n'; sleep 1; } \
  | script -qec "valley review scratch" /dev/null > session.log 2>&1
sed -e 's/\x1b\[[0-9;?]*[a-zA-Z]//g' -e 's/\r//g' session.log > plain.log

# The anchors, exactly: above every file, on a file header
# above the first hunk, mid-hunk, in a second hunk whose
# numbering starts elsewhere, and in a second file.
diff -u - "$XDG_STATE_HOME/valley/notes/scratch.md" <<EOF
## scratch at $(git rev-parse --short origin/scratch)

(unanchored): a note above every file
alpha.md: alpha reads like two files
alpha.md:3: why is 3 changed at all?
alpha.md:13: and this hunk is fine
beta.md:5: beta wants the same treatment

EOF

# The editor was given the buffer valley wrote, not the
# rendering less displayed, and given a line to open at.
grep -qE 'editor got: \+[0-9]+ .*/review\.diff$' \
  "$XDG_STATE_HOME/editor.log"
# The filter did run: what the pager showed was its output.
grep -q '^| @@ ' plain.log
# The line the reviewer edited instead of inserting is gone,
# and said so.
grep -q 'changed or deleted diff line(s) ignored' plain.log
! grep -q 'alpha SEVENTEEN' "$XDG_STATE_HOME/valley/notes/scratch.md"

# The verdict is unchanged, and the notes come after it.
grep -q 'skipped scratch; nothing done' plain.log
verdict="$(grep -n 'skipped scratch' plain.log | cut -d: -f1)"
notes="$(grep -n '5 note(s) on scratch' plain.log | cut -d: -f1)"
[ "$verdict" -lt "$notes" ]

# Piped, review is what it always was: the diff on stdout,
# the verdict from stdin, and no notes to take.
printf 's\n' | valley review scratch > piped.log
grep -q '^diff --git a/alpha.md b/alpha.md$' piped.log
grep -q 'skipped scratch; nothing done' piped.log
! grep -q 'note(s) on scratch' piped.log

touch "$out"

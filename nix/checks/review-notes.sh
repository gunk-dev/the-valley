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

# The pending list titles each branch by the subject of its first own
# commit, on the line under its name, and cuts a long one short with … to
# fit 80 columns when stdout is no terminal. Nothing it prints there is an
# escape code.
valley pending > pending.log
grep -A1 '^scratch  ' pending.log | grep -q '^  touch both files$'
git checkout --quiet -b longsubject origin/main
git commit --quiet --allow-empty -m "$(printf 'x%.0s' $(seq 1 80))"
git push --quiet origin longsubject
git checkout --quiet main
valley pending > pending.log
grep -A1 '^longsubject  ' pending.log | grep -qE '^  x{77}…$'
if grep -q $'\x1b' pending.log; then exit 1; fi

# On a terminal, pending fits the terminal's width and sets each name in
# bold. With NO_COLOR set, it prints no escape code.
script -qec "stty cols 100; valley pending" /dev/null < /dev/null > tty.log 2>&1
grep -q $'^\x1b\\[1mlongsubject\x1b\\[0m ' tty.log
grep -qE $'^  x{80}\r?$' tty.log
NO_COLOR=1 script -qec "stty cols 100; valley pending" /dev/null < /dev/null > tty.log 2>&1
grep -q '^longsubject ' tty.log
if grep -q $'\x1b' tty.log; then exit 1; fi

# ----------------------------------------------------------------------
# A stack: a branch, then two changes with records, each on the one
# below, in a project whose floor requires nothing, so approving files a
# request with no evidence. Its origin logs every push it receives, which
# is how "publishes nothing" is read.
stack="$TMPDIR/stack.git"
pushlog="$TMPDIR/pushlog"
git init --quiet --bare "$stack"
cat > "$stack/hooks/pre-receive" << EOF
#!/bin/sh
echo '--- push' >> $pushlog
cat >> $pushlog
EOF
chmod +x "$stack/hooks/pre-receive"
git init --quiet "$TMPDIR/stackwork"
cd "$TMPDIR/stackwork" || exit 1
git remote add origin "$stack"
git config valley.principal operator
mkdir -p policy/instance
cat > policy/instance/floor.cue << 'EOF'
package verification

floor: {
	classes: everything: {
		paths: "**": true
		requires: {}
	}
	unclassified: {}
}
EOF
git add -A
git commit --quiet -m seed
git push --quiet -u origin main
from=main
for layer in one two three; do
  git checkout --quiet -b "$layer" "$from"
  echo "layer $layer" > "$layer.md"
  git add -A
  git commit --quiet -m "layer $layer"
  git push --quiet origin "$layer"
  from="$layer"
done
two="$(git rev-parse two)" three="$(git rev-parse three)"
q=qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq p=pppppppppppppppppppppppppppppppp
valley ready --change "$q" two > /dev/null
valley ready --change "$p" three > /dev/null
git checkout --quiet main
requests="refs/the-valley/integration-requests/main"

# s at the first layer, on a terminal, with a note written in its diff:
# the note is kept under the first layer's branch and printed at the end,
# and nothing is published.
cat > "$TMPDIR/stack-editor" << 'EOF'
#!/bin/sh
f="$1"
case "$1" in +*) f="$2" ;; esac
sed -i '/^+layer one$/a one wants a second line' "$f"
EOF
chmod +x "$TMPDIR/stack-editor"
: > "$pushlog"
{ printf 'v\nq'; sleep 2; printf 's\n'; sleep 1; } \
  | EDITOR="$TMPDIR/stack-editor" script -qec "valley review pppp" /dev/null > stack-tty.log 2>&1
sed -e 's/\x1b\[[0-9;?]*[a-zA-Z]//g' -e 's/\r//g' stack-tty.log > stack-plain.log
grep -q 'pppp is a stack of 3 changes; reviewing from the bottom, each against the one below it:$' stack-plain.log
grep -qxF 'read each layer and go on with [n]; approving pppp at the top lands all 3. [a] on a lower layer lands it and the layers under it, and ends the review.' stack-plain.log
grep -q '| change 1 of 3: one on main$' stack-plain.log
grep -qF 'one, 1 of 3: [n]ext layer, [a]pprove one alone, [v]iew full diff, [s]top? ' stack-plain.log
grep -q 'stopped reviewing pppp at change 1 of 3, one; nothing done' stack-plain.log
if grep -q 'change 2 of 3' stack-plain.log; then exit 1; fi
grep -qx 'one.md:1: one wants a second line' "$XDG_STATE_HOME/valley/notes/one.md"
grep -q '1 note(s) on one, appended to .*/valley/notes/one.md:' stack-plain.log
grep -q '^  one.md:1: one wants a second line$' stack-plain.log
[ ! -e "$XDG_STATE_HOME/valley/notes/two.md" ]
[ ! -s "$pushlog" ]

# n, n, a: the layers are paged bottom up, each diff holds only its own
# layer's file, and the top's request is filed at the top's head. Each
# prompt names its layer and place, and what approving there lands.
: > "$pushlog"
printf 'n\nn\na\n' | valley review pppp > stack.out 2> stack.err
grep -q '^  1\. one (branch): layer one$' stack.out
grep -q '^  2\. qqqq (ready): layer two$' stack.out
grep -q '^  3\. pppp (ready): layer three$' stack.out
grep -qxF 'read each layer and go on with [n]; approving pppp at the top lands all 3. [a] on a lower layer lands it and the layers under it, and ends the review.' stack.out
grep -qF 'one, 1 of 3: [n]ext layer, [a]pprove one alone, [v]iew full diff, [s]top? ' stack.out
grep -qF 'qqqq, 2 of 3: [n]ext layer, [a]pprove one and qqqq, [v]iew full diff, [s]top? ' stack.out
grep -qF 'pppp, 3 of 3: [a]pprove all 3, [v]iew full diff, [r]eject pppp, [s]top? ' stack.out
# Piped, each page's first line follows the prompt before it.
[ "$(grep -o 'change [0-9] of 3: [a-z]* on [a-z]*$' stack.out)" = "$(printf '%s\n' \
  'change 1 of 3: one on main' 'change 2 of 3: qqqq on one' 'change 3 of 3: pppp on qqqq')" ]
for n in 1 2 3; do
  awk -v n="$n" '/change [0-9] of 3: /{ on = index($0, "change " n " of 3: ") > 0 } on' stack.out > "page$n"
done
[ "$(grep '^diff --git' page1)" = 'diff --git a/one.md b/one.md' ]
[ "$(grep '^diff --git' page2)" = 'diff --git a/two.md b/two.md' ]
[ "$(grep '^diff --git' page3)" = 'diff --git a/three.md b/three.md' ]
[ "$(git -C "$stack" rev-parse "$requests/c-$p")" = "$three" ]
if git -C "$stack" rev-parse --verify --quiet "$requests/c-$q" > /dev/null; then exit 1; fi
git -C "$stack" update-ref -d "$requests/c-$p"

# a at the second layer files the second layer's request at its head, and
# nothing for the top, whose review never starts: neither its page nor its
# prompt is shown.
: > "$pushlog"
printf 'n\na\n' | valley review pppp > stack-two.out 2> stack-two.err
grep -qF 'qqqq, 2 of 3: [n]ext layer, [a]pprove one and qqqq, [v]iew full diff, [s]top? ' stack-two.out
[ "$(git -C "$stack" rev-parse "$requests/c-$q")" = "$two" ]
if git -C "$stack" rev-parse --verify --quiet "$requests/c-$p" > /dev/null; then exit 1; fi
grep -q "c-$q" "$pushlog"
if grep -q "c-$p" "$pushlog"; then exit 1; fi
if grep -q 'change 3 of 3' stack-two.out; then exit 1; fi
if grep -q '3 of 3: ' stack-two.out; then exit 1; fi

# A draft in the middle is named in the warning before anything is paged.
valley unready qqqq > /dev/null 2>&1
printf 's\n' | valley review pppp > stack-draft.out 2>&1
grep -q '^warning: qqqq, change 2 of 3, is a draft\. The integrator refuses to land it or anything above it (refused-not-ready)\.$' stack-draft.out

# A change with no stack under it pages and prompts as it always has.
printf 's\n' | valley review one > stack-one.out 2>&1
grep -q '^branch one, log against origin/main:$' stack-one.out
grep -q '^one: \[a\]pprove, \[v\]iew full diff, \[r\]eject, \[s\]kip? skipped one; nothing done$' stack-one.out
if grep -q 'stack of\|change 1 of' stack-one.out; then exit 1; fi

# answer_after <log> <prompt> <command> <answers>: wait until the review
# writing to <log> has asked <prompt>, run <command>, then answer. A page
# is shown before its prompt, so the command runs after the page is shown
# and before the answer.
answer_after() {
  local i=0
  until grep -q -- "$2" "$1" 2> /dev/null || [ "$i" -ge 600 ]; do
    sleep 0.1
    i=$((i + 1))
  done
  eval "$3"
  printf '%b' "$4"
}
seed="$(git rev-parse main)" onehead="$(git rev-parse one)"

# [a]pprove at the bottom, on a plain branch under the top, files that
# branch's head under its own request name, and nothing for the top.
: > "$pushlog"
printf 'a\n' | valley review pppp > stack-a-one.out 2>&1
[ "$(git -C "$stack" rev-parse "$requests/one")" = "$onehead" ]
grep -q "$requests/one" "$pushlog"
if grep -q "c-$p" "$pushlog"; then exit 1; fi

# Once a page is shown, the head approved is the head shown. When the
# branch moves on origin, and is fetched, after the page is shown and
# before the answer, approving refuses, names both heads, and publishes
# nothing: for a plain branch under the top, for a plain branch at the top
# of a stack, and for a plain branch alone. The request each would file is
# absent first, so a filing would push.
git -C "$stack" update-ref -d "$requests/one"
: > "$pushlog"
# shellcheck disable=SC2094 # the answers wait on the review's own output
answer_after moved-one.out 'one, 1 of 3: \[n\]ext' "git -C '$stack' update-ref refs/heads/one $seed; git fetch -q origin" 'a\ns\n' \
  | valley review pppp > moved-one.out 2>&1
grep -q "origin/one is at $seed, and is no longer at $onehead, the head shown; nothing published" moved-one.out
[ ! -s "$pushlog" ]
if git -C "$stack" rev-parse --verify --quiet "$requests/one" > /dev/null; then exit 1; fi
git -C "$stack" update-ref refs/heads/one "$onehead"
git fetch -q origin

git checkout --quiet -b four three
echo "layer four" > four.md
git add four.md
git commit --quiet -m "layer four"
git push --quiet origin four
fourhead="$(git rev-parse four)"
git checkout --quiet -b solo main
echo "solo" > solo.md
git add solo.md
git commit --quiet -m "solo"
git push --quiet origin solo
solohead="$(git rev-parse solo)"
git checkout --quiet main

: > "$pushlog"
# shellcheck disable=SC2094 # the answers wait on the review's own output
{
  printf 'n\nn\nn\n'
  answer_after moved-four.out 'four, 4 of 4: \[a\]pprove all 4, \[v\]iew full diff, \[r\]eject four, \[s\]top?' "git -C '$stack' update-ref refs/heads/four $seed; git fetch -q origin" 'a\ns\n'
} | valley review four > moved-four.out 2>&1
grep -q '^four is a stack of 4 changes' moved-four.out
# From the third layer up, a lower layer's prompt counts what approving
# there lands. The top's prompt names the top, and says approving it
# lands all 4.
grep -qF 'pppp, 3 of 4: [n]ext layer, [a]pprove the 3 up to pppp, [v]iew full diff, [s]top? ' moved-four.out
grep -qF 'four, 4 of 4: [a]pprove all 4, [v]iew full diff, [r]eject four, [s]top? ' moved-four.out
grep -q "origin/four is at $seed, and is no longer at $fourhead, the head shown; nothing published" moved-four.out
grep -q 'stopped reviewing four at change 4 of 4; nothing done' moved-four.out
[ ! -s "$pushlog" ]
if git -C "$stack" rev-parse --verify --quiet "$requests/four" > /dev/null; then exit 1; fi

: > "$pushlog"
# shellcheck disable=SC2094 # the answers wait on the review's own output
answer_after moved-solo.out 'solo: \[a\]pprove' "git -C '$stack' update-ref refs/heads/solo $seed; git fetch -q origin" 'a\ns\n' \
  | valley review solo > moved-solo.out 2>&1
grep -q '^branch solo, log against origin/main:$' moved-solo.out
grep -q "origin/solo is at $seed, and is no longer at $solohead, the head shown; nothing published" moved-solo.out
[ ! -s "$pushlog" ]
if git -C "$stack" rev-parse --verify --quiet "$requests/solo" > /dev/null; then exit 1; fi

# ----------------------------------------------------------------------
# A stack whose bottom does not fast-forward from main. The draft in the
# middle is readied again, the top gains a second version, and main moves
# on under the stack.
valley ready --change "$q" two > /dev/null
git checkout --quiet three
echo "layer three, again" >> three.md
git commit --quiet -am "layer three again"
git push --quiet origin three
threeagain="$(git rev-parse three)"
valley ready --change "$p" three > /dev/null
git checkout --quiet main
echo "main moves on" > moved.md
git add moved.md
git commit --quiet -m "main moves on"
git push --quiet origin main

# Approving the top is refused before any check runs, at its active head
# and at an earlier version's head, and the refusal names the rebase that
# fixes it. Nothing is published.
: > "$pushlog"
for head in "$threeagain" "$three"; do
  if valley approve --head "$head" pppp > stale-approve.out 2>&1; then exit 1; fi
  grep -qF 'does not fast-forward from main, and the commits it would bring hold a version of qqqq, not landed.' stale-approve.out
  grep -qF 'nothing published. Rebase the stack first: valley rebase pppp' stale-approve.out
  if grep -q 'requires no check' stale-approve.out; then exit 1; fi
done
[ ! -s "$pushlog" ]
# The bottom layer has no change under it, so it is approved as it is.
valley approve one > stale-bottom.out 2>&1
grep -q 'requires no check' stale-bottom.out
[ "$(git -C "$stack" rev-parse "$requests/one")" = "$onehead" ]
git -C "$stack" update-ref -d "$requests/one"

# review asks, before any layer is paged, whether to rebase the stack.
# [s]top ends it there, and nothing is done.
: > "$pushlog"
printf 's\n' | valley review pppp > stale-stop.out 2>&1
grep -qF 'one, at the bottom, does not fast-forward from main: [b]ase all 3 onto main, [s]top? stopped reviewing pppp before its first layer; nothing done' stale-stop.out
if grep -q 'change 1 of 3' stale-stop.out; then exit 1; fi
[ ! -s "$pushlog" ]

# [b]ase rebases the whole stack. The review then lists the stack again
# and pages the rebased layers, and [a]pprove at the top files the
# request at the top's rebased head.
printf 'b\nn\nn\na\n' | valley review pppp > stale-review.out 2>&1
grep -qF 'one, at the bottom, does not fast-forward from main: [b]ase all 3 onto main, [s]top? ' stale-review.out
grep -qF 'the stack under pppp, bottom first:' stale-review.out
git fetch --quiet origin
newone="$(git rev-parse origin/one)" newthree="$(git rev-parse origin/three)"
[ "$newone" != "$onehead" ]
git merge-base --is-ancestor origin/main "$newone"
awk '/change 1 of 3: /{ on = 1 } /change 2 of 3: /{ on = 0 } on' stale-review.out > stale-page1
grep -q "^$(git rev-parse --short "$newone") layer one$" stale-page1
if grep -q "$(git rev-parse --short "$onehead")" stale-page1; then exit 1; fi
grep -qF 'pppp, 3 of 3: [a]pprove all 3, [v]iew full diff, [r]eject pppp, [s]top? ' stale-review.out
[ "$(git -C "$stack" rev-parse "$requests/c-$p")" = "$newthree" ]
# qqqq's v1 was approved earlier, and the rebase gave it v2 with the same
# file delta, so its layer is printed as a clean restack of v1, and not
# paged. pppp has no approved version, so its layer is paged in full.
grep -qx "qqqq v2 at [0-9a-f]*: clean restack of approved v1 at $(git rev-parse --short "$two")\." stale-review.out
awk '/change 2 of 3: /{ on = 1 } /change 3 of 3: /{ on = 0 } on' stale-review.out > stale-page2
if grep -q '^diff --git' stale-page2; then exit 1; fi
awk '/change 3 of 3: /{ on = 1 } on' stale-review.out > stale-page3
[ "$(grep '^diff --git' stale-page3)" = 'diff --git a/three.md b/three.md' ]

touch "$out"

f="$1"
case "$1" in +*) f="$2" ;; esac
printf 'editor got: %s\n' "$*" >> "$XDG_STATE_HOME/editor.log"
sed -i \
  -e '/^branch scratch, log against/a a note above every file' \
  -e '/^diff --git a\/alpha.md/a alpha reads like two files' \
  -e '/^+alpha 3 changed$/a why is 3 changed at all?' \
  -e '/^ alpha 13$/a and this hunk is fine' \
  -e '/^+beta 5 changed$/a beta wants the same treatment' \
  -e 's/^ alpha 17$/ alpha SEVENTEEN/' \
  "$f"

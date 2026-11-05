# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
# The flags are one string so the formatter app and this check cannot
# drift; split it back into argv.
read -ra fmtArgs <<< "$proseFmtArgs"

# The tree the check reads: the markdown files and prettier's configuration,
# and the link targets that are neither, each an empty file or directory at
# its path.
tree="$TMPDIR/tree"
cp -r "$markdown" "$tree"
chmod -R u+w "$tree"
cd "$tree" || exit 1
while IFS= read -r path; do
  case "$path" in
    */) mkdir -p "$path" ;;
    *)
      mkdir -p "$(dirname "$path")"
      touch "$path"
      ;;
  esac
done < "$linkTargetsPath"
find . -name '*.md' -print0 | xargs -0 --no-run-if-empty \
  prettier "${fmtArgs[@]}" --check

python3 - <<'PY'
import glob
import os
import re
import sys

LINK = re.compile(r"\]\(\s*([^)\s]+)(?:\s+\"[^\"]*\")?\s*\)")
INLINE_CODE = re.compile(r"(`+)[^`\n]*?\1")
FENCE_OPEN = re.compile(r"^\s*(`{3,}|~{3,})")
HEADING = re.compile(r"^#{1,6}\s+(.*?)\s*#*?\s*$")
MD_LINK_IN_HEADING = re.compile(r"\[([^\]]*)\]\([^)]*\)")
NOT_IN_SLUG = re.compile(r"[^\w\s-]")
EXTERNAL = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.-]*:")


def outside_fences(lines):
    fence = None
    for number, line in enumerate(lines, start=1):
        if fence is None:
            m = FENCE_OPEN.match(line)
            if m:
                fence = m.group(1)
            else:
                yield number, line
        else:
            if re.match(rf"^\s*{fence[0]}{{{len(fence)},}}\s*$", line):
                fence = None


def anchors(path, cache):
    if path in cache:
        return cache[path]
    found, counts = set(), {}
    with open(path, encoding="utf-8") as handle:
        for _, line in outside_fences(handle.read().splitlines()):
            heading = HEADING.match(line)
            if not heading:
                continue
            title = MD_LINK_IN_HEADING.sub(r"\1", heading.group(1))
            title = title.replace("`", "").lower()
            slug = NOT_IN_SLUG.sub("", title).strip().replace(" ", "-")
            counts[slug] = counts.get(slug, 0) + 1
            found.add(slug if counts[slug] == 1 else f"{slug}-{counts[slug] - 1}")
    cache[path] = found
    return found


findings = []
cache = {}
documents = [p for p in ["README.md", "AGENTS.md"] if os.path.exists(p)] + sorted(
    glob.glob("docs/*.md")
)

for path in documents:
    with open(path, encoding="utf-8") as handle:
        lines = handle.read().splitlines()
    for number, line in outside_fences(lines):
        prose = INLINE_CODE.sub(" ", line)
        for target in LINK.findall(prose):
            if EXTERNAL.match(target):
                continue
            relative, has_hash, anchor = target.partition("#")
            if not relative:
                resolved = path
            else:
                resolved = os.path.normpath(
                    os.path.join(os.path.dirname(path), relative)
                )
            if resolved == ".." or resolved.startswith("../"):
                findings.append(
                    f"{path}:{number}: {target} points outside the repository"
                )
                continue
            if not os.path.exists(resolved):
                findings.append(f"{path}:{number}: {target} does not exist")
                continue
            if has_hash:
                if not anchor:
                    findings.append(
                        f"{path}:{number}: {target} has an empty heading anchor"
                    )
                elif not os.path.isfile(resolved) or not resolved.endswith(".md"):
                    findings.append(
                        f"{path}:{number}: {target} names a heading in non-markdown target {resolved}"
                    )
                elif anchor not in anchors(resolved, cache):
                    findings.append(
                        f"{path}:{number}: {target} names no heading in {resolved}"
                    )

if findings:
    print(
        f"prose-format: {len(findings)} broken link(s) or anchor(s):",
        file=sys.stderr,
    )
    for item in findings:
        print(f"  {item}", file=sys.stderr)
    sys.exit(1)
PY

# The style lint: the rules of AGENTS.md's style section that a pattern can
# catch. It prints a finding for each pattern a line breaks, as
# file:line: rule: what it found, and exits 1 if it printed any. A line
# allows one rule with the marker <!-- prose-allow: rule --> outside inline
# code, and a marker that allows nothing is itself a finding.
cat > "$TMPDIR/style.awk" <<'AWK'
BEGIN {
	split("line-number narration provenance vocabulary", names, " ")
	for (i in names) rules[names[i]]
	marker = "<!--[[:space:]]*prose-allow:[^>]*-->"
	# A source path is one with a directory, or a file name with an
	# extension of a source file in this tree. A host and port or a
	# timestamp is neither.
	seg = "[[:alnum:]_.-]+"
	path = seg "(/" seg ")+|" seg "\\.(go|sh|bash|nix|cue|py|md|txt|json|ya?ml|toml)"
	cite = "(^|[^[:alnum:]_./-])(" path "):[0-9]+(-[0-9]+)?([^0-9]|$)"
	# Where a passage came from: a date, a knot, a change or a commit
	# given as its reason, or a person's view given as the project's. A
	# date may carry a time.
	date = "\\<[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9:.]+Z?)?\\>"
	knot = "\\<(oc|bd|ida|qn|dcr)-[0-9a-f]{7}\\>"
	change = "\\<[k-z]{32}\\>"
	short = "[^[:alnum:]_./:-][0-9a-f]{7,12}([^[:alnum:]_/-]|$)"
	who = "(operator|user|maintainer|reviewer|author|planner|owner)s?"
	some = "(the |an? )?([[:alnum:]'’]+ )?"
	opinion = "\\<the ([[:alnum:]'’]+ )?(human )?operators? (said|says|wants|wanted|asked|decided|prefers|preferred|insisted|chose)\\>"
	quote = "(\"|“|”)"
	speech = "\\<(" who " (says|asks|writes)|said|asked|wrote|put it)[,:]?[[:space:]]*" quote "|" quote "[,.]?[[:space:]]+(said|says|asked|wrote|writes) " some who "\\>|\\<in " some who "'s words\\>"
}

function finding(rule, message) {
	if (rule in allowed) {
		used[rule]
		return
	}
	printf "%s:%d: %s: %s\n", FILENAME, FNR, rule, message
	failed = 1
}

# The start of the first match of re in s that ends after its first skip
# characters, which are the end of the line before, or 0. RLENGTH is the
# match's length.
function find(s, re, skip,    start) {
	for (start = 0; match(substr(s, start + 1), re); start += RSTART)
		if (start + RSTART + RLENGTH - 1 > skip)
			return start + RSTART
	return 0
}

# A word rule, matched case-insensitively when fold is set.
function word(rule, re, why, fold,    at) {
	if (at = find(fold ? low : s, re, skip))
		finding(rule, "\"" substr(s, at, RLENGTH) "\": " why)
}

# A short commit hash: seven to twelve hex digits, a digit and a letter
# among them, which "defaced" and "1234567" lack. A curve name such as
# ed25519 is not one. The space before s stands for the start of a line.
function hash(rule, why,    t, start, h) {
	t = " " s
	for (start = 0; match(substr(t, start + 1), short); start += RSTART) {
		h = substr(t, start + RSTART + 1, RLENGTH - 1)
		sub(/[^0-9a-f]$/, "", h)
		if (start + RSTART + RLENGTH - 2 > skip && h ~ /[0-9]/ && h ~ /[a-f]/ && h !~ /^ed[0-9]+$/) {
			finding(rule, "\"" h "\": " why)
			return
		}
	}
}

# The line t as the word rules read it, after prev, the last two words of
# the line before it.
function reading(t, prev) {
	s = t
	sub(/^[[:space:]]+/, "", s)
	s = prev " " s
	skip = length(prev) + 1
	low = tolower(s)
}

# The last two words of t.
function last(t,    w) {
	if (!match(t, /([^[:space:]]+[[:space:]]+)?[^[:space:]]+[[:space:]]*$/))
		return ""
	w = substr(t, RSTART, RLENGTH)
	sub(/[[:space:]]+$/, "", w)
	return w
}

FNR == 1 { fence = ""; tail = ""; bareTail = "" }

{
	delete allowed
	delete used
	code = $0
	gsub(/`[^`]*`/, "", code)
	while (match(code, marker)) {
		rule = substr(code, RSTART, RLENGTH)
		code = substr(code, RSTART + RLENGTH)
		sub(/^<!--[[:space:]]*prose-allow:[[:space:]]*/, "", rule)
		sub(/[[:space:]]*-->$/, "", rule)
		if (rule in rules)
			allowed[rule]
		else
			finding("allowance", "prose-allow names no rule \"" rule "\"; the rules are line-number, narration, provenance and vocabulary")
	}
	if (match($0, "^[[:space:]]*(([-*+>]|[0-9]+[.)])[[:space:]]+)?" marker))
		finding("allowance", "a marker that begins a line begins an HTML block; put it after a word")
	text = $0
	gsub(marker, " ", text)
	# Inline code quotes a value, such as a schema's cutoff date, an
	# example knot ID or a digest, so the provenance rules read the line
	# without it.
	bare = text
	gsub(/`[^`]*`/, " ", bare)

	infence = fence != ""
	if (!infence && match($0, /^[[:space:]]*(```+|~~~+)/)) {
		fence = substr($0, RSTART, RLENGTH)
		sub(/^[[:space:]]*/, "", fence)
		infence = 1
	} else if (infence && $0 ~ ("^[[:space:]]*" substr(fence, 1, 1) "{" length(fence) ",}[[:space:]]*$"))
		fence = ""

	if (match(text, cite)) {
		found = substr(text, RSTART, RLENGTH)
		sub(/^[^[:alnum:]_.\/-]/, "", found)
		sub(/[^0-9]$/, "", found)
		finding("line-number", "\"" found "\": cite the file or a name in it, not a line")
	}

	# Fenced code is exempt from the word rules. A phrase may wrap, so each
	# line is read after the last two words of the line before it.
	if (!infence) {
		reading(text, tail)
		word("vocabulary", "\\<ow(e|es|ed)\\>", "a change requires checks", 1)
		word("vocabulary", "\\<pull requests?\\>", "a valley change is a change", 1)
		word("vocabulary", "\\<PRs?\\>", "a valley change is a change", 0)
		word("vocabulary", "\\<knowledge[ -]?bases?\\>", "a plan item is a knot in the plan graph", 1)
		word("vocabulary", "\\<tickets?\\>", "a plan item is a knot in the plan graph", 1)
		word("narration", "\\<(no longer|previously|was changed to|has been replaced|is now)\\>", "describe what HEAD does today", 1)
		word("narration", "\\<((it|this|that|these|those|they|we|which|who) used to|used to be)\\>", "describe what HEAD does today", 1)
		word("narration", "\\<((was|were|has been|have been) (added|introduced)|we (added|introduced))\\>", "describe what HEAD does today", 1)
		word("narration", "\\<(anymore|now that)\\>", "describe what HEAD does today", 1)
		word("narration", "\\<since (commit|release|v[0-9])", "describe what HEAD does today", 1)
		reading(bare, bareTail)
		word("provenance", date, "a date belongs in a commit message or a knot", 0)
		word("provenance", knot, "state the reason itself; the knot belongs in a commit message", 0)
		word("provenance", change, "state the reason itself; the change belongs in a commit message", 0)
		hash("provenance", "state the reason itself; the commit belongs in a commit message")
		word("provenance", opinion, "state the position as the project's own", 1)
		word("provenance", speech, "state the position as the project's own, not as a quote", 1)
	}
	tail = infence ? "" : last(text)
	bareTail = infence ? "" : last(bare)

	for (i = 1; i in names; i++)
		if (names[i] in allowed && !(names[i] in used))
			finding("allowance", "allows " names[i] ", but nothing on this line breaks it")
}

END { exit failed }
AWK
style() { gawk -f "$TMPDIR/style.awk" "$@"; }

# The lint's own fixtures. A doc that breaks each rule fails with exactly
# these findings, and the same doc with an allowance on each line passes.
fixtures="$TMPDIR/style"
mkdir -p "$fixtures"
cd "$fixtures" || exit 1
cat > broken.md <<'EOF'
A change never owes a check, and nothing is owed.
Open a PR, or a Pull Request, against main.
Track it in the knowledgebase as a ticket.
The rule is in status/ready.go:146-165.
The verb is at `bin/valley:57`.
See [the hook](../valleyhook/policy.go:12) for the rest.
The hook no longer reads the tree.
It previously read it, and it is now a script.
The verb was changed to print JSON.
The flag has been replaced.
The check used to be slow, and it used to time out.
- A phrase can wrap: the hook is no
  longer a script.
The flag was added for speed.
The verb was introduced with it.
We added a check, so it is not slow anymore.
Now that the hook reads refs, the cap holds since v2.
The cap is 64 since commit 4ae651e.
It landed on 2026-11-05 (knot oc-03b6f8c).
It fixes [the bug](../archive/.the-valley/bugs/bd-75c8721-replace-refs.md).
Change kqxmrvtzkqxmrvtzkqxmrvtzkqxmrvtz was the fix.
The operator wants one worker, and the operator
decided to keep it.
As the reviewer wrote, "keep it small".
"Keep it small," said the author.

```sh
valley checks  # see status/ready.go:146
```
EOF
cat > broken.want <<'EOF'
broken.md:1: vocabulary: "owes": a change requires checks
broken.md:2: vocabulary: "Pull Request": a valley change is a change
broken.md:2: vocabulary: "PR": a valley change is a change
broken.md:3: vocabulary: "knowledgebase": a plan item is a knot in the plan graph
broken.md:3: vocabulary: "ticket": a plan item is a knot in the plan graph
broken.md:4: line-number: "status/ready.go:146-165": cite the file or a name in it, not a line
broken.md:5: line-number: "bin/valley:57": cite the file or a name in it, not a line
broken.md:6: line-number: "../valleyhook/policy.go:12": cite the file or a name in it, not a line
broken.md:7: narration: "no longer": describe what HEAD does today
broken.md:8: narration: "previously": describe what HEAD does today
broken.md:9: narration: "was changed to": describe what HEAD does today
broken.md:10: narration: "has been replaced": describe what HEAD does today
broken.md:11: narration: "used to be": describe what HEAD does today
broken.md:13: narration: "no longer": describe what HEAD does today
broken.md:14: narration: "was added": describe what HEAD does today
broken.md:15: narration: "was introduced": describe what HEAD does today
broken.md:16: narration: "We added": describe what HEAD does today
broken.md:16: narration: "anymore": describe what HEAD does today
broken.md:17: narration: "Now that": describe what HEAD does today
broken.md:17: narration: "since v2": describe what HEAD does today
broken.md:18: narration: "since commit": describe what HEAD does today
broken.md:18: provenance: "4ae651e": state the reason itself; the commit belongs in a commit message
broken.md:19: provenance: "2026-11-05": a date belongs in a commit message or a knot
broken.md:19: provenance: "oc-03b6f8c": state the reason itself; the knot belongs in a commit message
broken.md:20: provenance: "bd-75c8721": state the reason itself; the knot belongs in a commit message
broken.md:21: provenance: "kqxmrvtzkqxmrvtzkqxmrvtzkqxmrvtz": state the reason itself; the change belongs in a commit message
broken.md:22: provenance: "The operator wants": state the position as the project's own
broken.md:23: provenance: "the operator decided": state the position as the project's own
broken.md:24: provenance: "wrote, "": state the position as the project's own, not as a quote
broken.md:25: provenance: "" said the author": state the position as the project's own, not as a quote
broken.md:28: line-number: "status/ready.go:146": cite the file or a name in it, not a line
EOF
if style broken.md > broken.got; then
  echo "prose-format: the style lint passed a doc that breaks every rule" >&2
  exit 1
fi
diff -u broken.want broken.got >&2 || {
  echo "prose-format: the style lint's findings on broken.md are not the expected ones" >&2
  exit 1
}
# allowed.md is broken.md with a marker for each rule a line breaks.
gawk -F': ' '
  NR == FNR {
    split($1, at, ":")
    if (!((at[2], $2) in seen))
      allow[at[2]] = allow[at[2]] " <!-- prose-allow: " $2 " -->"
    seen[at[2], $2]
    next
  }
  { print $0 allow[FNR] }' broken.want broken.md > allowed.md
style allowed.md >&2 || {
  echo "prose-format: the style lint failed broken.md with an allowance on each line" >&2
  exit 1
}

# What the rules must pass: the words and the narration in a fenced code
# block, a marker quoted in inline code, an address, a timestamp, a hash,
# "used to" that is not narration, the operator named as the role that
# acts, a curve name, values quoted in inline code, and a quote that is
# what a page shows.
cat > clean.md <<'EOF'
Allow a word with `<!-- prose-allow: vocabulary -->` on its line.
The bus listens on nats://127.0.0.1:4222, and the run began at `2026-10-14T21:30`.
A version is git-sha1:2222222222222222222222222222222222222222.
The verifier keys used to verify a landing are the integrator's.
The operator approves a change, and the operator's key signs it.
An ed25519 key signs a note created after `2026-08-05`.
A knot ID looks like `oc-1234567`, and a digest like `88a4e1f0`.
The page says "The file is longer" when nothing has moved since the last read.

~~~
A PR owes a ticket, and the hook no longer reads the tree.
~~~
EOF
style clean.md >&2 || {
  echo "prose-format: the style lint failed clean.md, which breaks no rule" >&2
  exit 1
}

# A marker that allows a rule the line does not break, names no rule, or
# begins a line is a finding of its own.
cat > markers.md <<'EOF'
A change requires checks. <!-- prose-allow: vocabulary -->
A change has a version. <!-- prose-allow: words -->
<!-- prose-allow: vocabulary --> A PR is a change.
EOF
cat > markers.want <<'EOF'
markers.md:1: allowance: allows vocabulary, but nothing on this line breaks it
markers.md:2: allowance: prose-allow names no rule "words"; the rules are line-number, narration, provenance and vocabulary
markers.md:3: allowance: a marker that begins a line begins an HTML block; put it after a word
EOF
if style markers.md > markers.got; then
  echo "prose-format: the style lint passed a marker that allows nothing" >&2
  exit 1
fi
diff -u markers.want markers.got >&2 || {
  echo "prose-format: the style lint's findings on markers.md are not the expected ones" >&2
  exit 1
}

# The tree's own docs: the same documents the link check reads.
cd "$tree" || exit 1
style README.md AGENTS.md docs/*.md > "$TMPDIR/style.got" || {
  echo "prose-format: $(wc -l < "$TMPDIR/style.got") style finding(s):" >&2
  sed 's/^/  /' "$TMPDIR/style.got" >&2
  exit 1
}

touch "$out"

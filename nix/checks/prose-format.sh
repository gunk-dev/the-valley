# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
# The flags are one string so the formatter app and this check cannot
# drift; split it back into argv.
read -ra fmtArgs <<< "$proseFmtArgs"
cd "$tree" || exit 1
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
# catch. It prints one finding per rule broken on a line, as
# file:line: rule: what it found, and exits 1 if it printed any. A line
# allows one rule with the marker <!-- prose-allow: rule --> outside inline
# code, and a marker that allows nothing is itself a finding.
cat > "$TMPDIR/style.awk" <<'AWK'
BEGIN {
	split("line-number narration vocabulary", names, " ")
	for (i in names) rules[names[i]]
	marker = "<!--[[:space:]]*prose-allow:[^>]*-->"
	# A source path is one with a directory, or a file name with an
	# extension of a source file in this tree. A host and port or a
	# timestamp is neither.
	seg = "[[:alnum:]_.-]+"
	path = seg "(/" seg ")+|" seg "\\.(go|sh|bash|nix|cue|py|md|txt|json|ya?ml|toml)"
	cite = "(^|[^[:alnum:]_./-])(" path "):[0-9]+(-[0-9]+)?([^0-9]|$)"
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

FNR == 1 { fence = ""; tail = "" }

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
			finding("allowance", "prose-allow names no rule \"" rule "\"; the rules are line-number, narration and vocabulary")
	}
	if (match($0, "^[[:space:]]*(([-*+>]|[0-9]+[.)])[[:space:]]+)?" marker))
		finding("allowance", "a marker that begins a line begins an HTML block; put it after a word")
	text = $0
	gsub(marker, " ", text)

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
		s = text
		sub(/^[[:space:]]+/, "", s)
		s = tail " " s
		skip = length(tail) + 1
		low = tolower(s)
		word("vocabulary", "\\<ow(e|es|ed)\\>", "a change requires checks", 1)
		word("vocabulary", "\\<pull requests?\\>", "a valley change is a change", 1)
		word("vocabulary", "\\<PRs?\\>", "a valley change is a change", 0)
		word("vocabulary", "\\<knowledge[ -]?bases?\\>", "a plan item is a knot in the plan graph", 1)
		word("vocabulary", "\\<tickets?\\>", "a plan item is a knot in the plan graph", 1)
		word("narration", "\\<(no longer|previously|was changed to|has been replaced|is now)\\>", "describe what HEAD does today", 1)
		word("narration", "\\<((it|this|that|these|those|they|we|which|who) used to|used to be)\\>", "describe what HEAD does today", 1)
	}
	tail = ""
	if (!infence && match(text, /([^[:space:]]+[[:space:]]+)?[^[:space:]]+[[:space:]]*$/)) {
		tail = substr(text, RSTART, RLENGTH)
		sub(/[[:space:]]+$/, "", tail)
	}

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
broken.md:16: line-number: "status/ready.go:146": cite the file or a name in it, not a line
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
# and "used to" that is not narration.
cat > clean.md <<'EOF'
Allow a word with `<!-- prose-allow: vocabulary -->` on its line.
The bus listens on nats://127.0.0.1:4222, and the run began at 2026-10-14T21:30.
A version is git-sha1:2222222222222222222222222222222222222222.
The verifier keys used to verify a landing are the integrator's.

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
markers.md:2: allowance: prose-allow names no rule "words"; the rules are line-number, narration and vocabulary
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

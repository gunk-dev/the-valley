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

touch "$out"

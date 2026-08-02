#!/usr/bin/env python3
"""The knowledge lint: check a tree's .the-valley graph against the convention.

Three classes of finding, all reported in one run (ida-1ec03b1):

  frontmatter          every node's YAML frontmatter vets against #Node in
                       schema/node.cue, in one `cue vet` pass over the whole graph
  filename coherence   a node lives at <type directory>/<id>-<slug>.md, its filename
                       id is the id its frontmatter declares, and no id repeats
  reference integrity  [[wiki-links]], `blocked_by` ids, and relative markdown links
                       all resolve

The lint reads only the tree it is given. It never runs git, so it holds for a
worktree, a store path, or an unpacked archive alike.

A node id's hash is deliberately not re-derived: the convention names an example
algorithm rather than pinning one, so only the id's shape is checkable.
"""

import argparse
import json
import os
import re
import subprocess
import sys

# A markdown link target: ](target) or ](target "title").
LINK = re.compile(r"\]\(\s*([^)\s]+)(?:\s+\"[^\"]*\")?\s*\)")
WIKI_LINK = re.compile(r"\[\[([^\]|]+)\]\]")
INLINE_CODE = re.compile(r"`[^`\n]*`")
FENCE = re.compile(r"^\s*(```|~~~)")
NODE_FILENAME = re.compile(r"^([a-z]+-[0-9a-f]+)-([a-z0-9-]+)\.md$")
HEADING = re.compile(r"^#{1,6}\s+(.*?)\s*$")
NOT_IN_SLUG = re.compile(r"[^\w\s-]")
# A link target the tree cannot answer for: another host, or a mail client.
EXTERNAL = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.-]*:")


class Findings:
    """Every finding the run produces, in the order the files were read."""

    def __init__(self):
        self.items = []

    def add(self, path, message, line=None):
        where = f"{path}:{line}" if line else path
        self.items.append(f"{where}: {message}")

    def raw(self, line):
        """One line of cue's own error output, which already says where it is.

        cue states a message and then indents the source locations behind it, so an
        indented line continues the finding above rather than counting as another.
        """
        if self.items and line[:1].isspace():
            self.items[-1] += "\n" + line
        else:
            self.items.append(line)

    def __len__(self):
        return len(self.items)


def read_directories(schema):
    """The type -> directory table, read from the schema rather than restated."""
    out = subprocess.run(
        ["cue", "export", "-e", "#Directories", schema, "--out", "json"],
        capture_output=True,
        text=True,
    )
    if out.returncode != 0:
        sys.exit(f"knowledge-lint: cannot read #Directories from {schema}:\n{out.stderr}")
    return json.loads(out.stdout)


def outside_fences(lines):
    """Yield (line number, line) for every line that is not inside a fenced code block."""
    fenced = False
    for number, line in enumerate(lines, start=1):
        if FENCE.match(line):
            fenced = not fenced
        elif not fenced:
            yield number, line


def strip_code(lines):
    """Yield (line number, prose) with fenced blocks and inline code spans removed.

    A link inside code formatting is a link being talked about, not a link — the
    convention's own documentation writes `[[wiki-links]]` that way.
    """
    for number, line in outside_fences(lines):
        yield number, INLINE_CODE.sub(" ", line)


def anchors(path, cache):
    """The anchors a markdown file offers: one per heading, slugged as forges slug them.

    Lowercase, drop everything that is neither word character, whitespace nor hyphen,
    then turn each remaining space into a hyphen. A heading that repeats an earlier
    slug gets a numeric suffix, in the order the headings appear.
    """
    if path in cache:
        return cache[path]
    found, counts = set(), {}
    with open(path, encoding="utf-8") as handle:
        for _, line in outside_fences(handle.read().splitlines()):
            heading = HEADING.match(line)
            if not heading:
                continue
            title = heading.group(1).replace("`", "").lower()
            slug = NOT_IN_SLUG.sub("", title).strip().replace(" ", "-")
            counts[slug] = counts.get(slug, 0) + 1
            found.add(slug if counts[slug] == 1 else f"{slug}-{counts[slug] - 1}")
    cache[path] = found
    return found


def read_frontmatter(text):
    """Return the file's frontmatter lines, or None if it opens with no --- block."""
    lines = text.splitlines()
    if not lines or lines[0].rstrip() != "---":
        return None
    for index in range(1, len(lines)):
        if lines[index].rstrip() == "---":
            return lines[1:index]
    return None


def collect_documents(tree, root, directories, findings):
    """Find every markdown file in the graph, and say which of them are nodes.

    A file directly under the graph root is a graph document (the README), not a
    node. A file in a type directory is a node. Anything else is markdown filed
    somewhere the convention has no reading for, which is itself a finding.
    """
    nodes, documents = [], []
    node_directories = set(directories.values())
    for current, subdirectories, filenames in os.walk(os.path.join(tree, root)):
        subdirectories.sort()
        for filename in sorted(filenames):
            if not filename.endswith(".md"):
                continue
            path = os.path.relpath(os.path.join(current, filename), tree)
            documents.append(path)
            parent = os.path.dirname(path)
            if parent == root:
                continue
            if os.path.relpath(parent, root) in node_directories:
                nodes.append(path)
            else:
                findings.add(path, f"markdown outside {root}'s type directories")
    return nodes, documents


def build_graph_document(tree, nodes, findings):
    """Fold every node's frontmatter into one YAML document keyed by file path.

    One document means one `cue vet`, which means one run reports every node's
    schema errors and names the file each belongs to.
    """
    chunks = []
    for path in nodes:
        with open(os.path.join(tree, path), encoding="utf-8") as handle:
            frontmatter = read_frontmatter(handle.read())
        if frontmatter is None:
            findings.add(path, "no YAML frontmatter: the file must open with a --- block")
            continue
        chunks.append(json.dumps(path) + ":\n")
        chunks.extend("  " + line + "\n" if line.strip() else "\n" for line in frontmatter)
    return "".join(chunks)


def vet_frontmatter(schema, graph_yaml, work, findings):
    """Vet the whole graph against #Node, and return it parsed for the later checks."""
    if not graph_yaml.strip():
        # Every node failed to yield frontmatter, or there are no nodes. Either
        # way there is nothing to vet and nothing to hand the later checks.
        return {}

    document = os.path.join(work, "graph.yaml")
    with open(document, "w", encoding="utf-8") as handle:
        handle.write(graph_yaml)

    vet = subprocess.run(
        ["cue", "vet", "-c", "-d", "#Graph", schema, document],
        capture_output=True,
        text=True,
    )
    if vet.returncode != 0:
        for line in vet.stderr.rstrip().splitlines():
            findings.raw(line)

    export = subprocess.run(
        ["cue", "export", document, "--out", "json"], capture_output=True, text=True
    )
    if export.returncode != 0:
        # Frontmatter that is not YAML at all: vet has already said so, and
        # there is nothing to hand the reference checks.
        return {}
    return json.loads(export.stdout) or {}


def check_filenames(nodes, frontmatter, directories, root, findings):
    """A node's path must agree with its frontmatter, and its id must be its own."""
    seen = {}
    for path in nodes:
        match = NODE_FILENAME.match(os.path.basename(path))
        if not match:
            findings.add(path, "filename is not <id>-<slug>.md with a lowercase slug")
            continue
        filename_id = match.group(1)

        declared = frontmatter.get(path, {})
        node_id = declared.get("id")
        if node_id and node_id != filename_id:
            findings.add(path, f"filename says id {filename_id}, frontmatter says {node_id}")

        node_type = declared.get("type")
        if node_type in directories:
            expected = os.path.join(root, directories[node_type])
            if os.path.dirname(path) != expected:
                findings.add(path, f"a {node_type} node belongs in {expected}/")

        if filename_id in seen:
            findings.add(path, f"id {filename_id} is already used by {seen[filename_id]}")
        else:
            seen[filename_id] = path


def check_references(tree, documents, frontmatter, findings):
    """Every [[id]], blocked_by id, and relative link must resolve inside the tree."""
    ids = {node.get("id") for node in frontmatter.values()}
    ids.discard(None)
    headings = {}

    for path, node in sorted(frontmatter.items()):
        for blocked_by in node.get("blocked_by", []):
            if blocked_by not in ids:
                findings.add(path, f"blocked_by names {blocked_by}, which is not a node")

    for path in documents:
        with open(os.path.join(tree, path), encoding="utf-8") as handle:
            lines = handle.read().splitlines()
        for number, prose in strip_code(lines):
            for target in WIKI_LINK.findall(prose):
                if target not in ids:
                    findings.add(path, f"[[{target}]] is not a node id", line=number)
            for target in LINK.findall(prose):
                check_link(tree, path, target, number, headings, findings)


def check_link(tree, path, target, number, headings, findings):
    """One markdown link: the file it names must exist, and its anchor must be there."""
    if EXTERNAL.match(target):
        return
    relative, _, anchor = target.partition("#")
    if not relative:
        # A link within the same file; its anchor is checked against this file.
        resolved = path
    else:
        resolved = os.path.normpath(os.path.join(os.path.dirname(path), relative))
        if resolved.startswith(".."):
            findings.add(path, f"{target} points outside the tree", line=number)
            return
        if not os.path.exists(os.path.join(tree, resolved)):
            findings.add(path, f"{target} does not exist", line=number)
            return
    if anchor and resolved.endswith(".md"):
        if anchor not in anchors(os.path.join(tree, resolved), headings):
            findings.add(path, f"{target} names no heading in {resolved}", line=number)


def main():
    parser = argparse.ArgumentParser(description="Lint a tree's knowledge graph.")
    parser.add_argument("--tree", required=True, help="the project tree to lint")
    parser.add_argument("--root", default=".the-valley", help="the graph root inside the tree")
    parser.add_argument("--schema", required=True, help="path to schema/node.cue")
    parser.add_argument("--work", required=True, help="a writable directory for intermediates")
    arguments = parser.parse_args()

    graph_root = os.path.join(arguments.tree, arguments.root)
    if not os.path.isdir(graph_root):
        print(f"knowledge-lint: no graph at {arguments.root}/ — nothing to check")
        return 0

    findings = Findings()
    directories = read_directories(arguments.schema)
    nodes, documents = collect_documents(arguments.tree, arguments.root, directories, findings)
    graph_yaml = build_graph_document(arguments.tree, nodes, findings)
    frontmatter = vet_frontmatter(arguments.schema, graph_yaml, arguments.work, findings)
    check_filenames(nodes, frontmatter, directories, arguments.root, findings)
    check_references(arguments.tree, documents, frontmatter, findings)

    if findings:
        print(f"knowledge-lint: {len(findings)} findings in {arguments.root}/", file=sys.stderr)
        for item in findings.items:
            print(item, file=sys.stderr)
        return 1
    print(f"knowledge-lint: {len(nodes)} nodes in {arguments.root}/, no findings")
    return 0


if __name__ == "__main__":
    sys.exit(main())

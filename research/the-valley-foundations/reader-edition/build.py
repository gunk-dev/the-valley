#!/usr/bin/env python3
"""Build the report as a reflowable EPUB, with offline citation navigation.

Requires Python 3, Pandoc 3, and rsvg-convert. EPUBCheck can validate the output.
The Markdown report remains the source; presentation changes happen in the AST.
"""

import argparse
import copy
import json
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.parse import unquote, urlsplit
from xml.etree import ElementTree
from zipfile import ZipFile

HERE = Path(__file__).resolve().parent
REPORT = HERE.parent
REPO = REPORT.parent.parent
FILES = [
    "README.md",
    "01-project-and-problem-map.md",
    "02-existing-systems.md",
    "03-verification-and-integration.md",
    "04-events-trust-and-durability.md",
    "05-knowledge-agents-and-outcomes.md",
    "06-synthesis-and-research-agenda.md",
    "07-reading-guide-and-glossary.md",
    "sources-systems.md",
    "sources-verification.md",
    "sources-distributed.md",
    "sources-agents.md",
]
TITLES = {
    "README.md": "Overview and principal findings",
    "03-verification-and-integration.md": "3. Verification that can travel with a change",
    "04-events-trust-and-durability.md": "4. Events, trust, and durability",
    "05-knowledge-agents-and-outcomes.md": "5. Knowledge, agents, and outcomes",
    "sources-systems.md": "Sources S: architecture and existing systems",
    "sources-verification.md": "Sources V: verification and integration",
    "sources-distributed.md": "Sources D: events, trust, and durability",
    "sources-agents.md": "Sources A: knowledge, agents, and outcomes",
}
INTRO = """# About this reading edition

This edition contains the complete research report and its annotated sources. It is
prepared for a Kindle Scribe, with adjustable text and a linked table of contents.
The research is dated September 20, 2026.

Start with the overview and chapter 6 for the main judgments. Read the chapters in
order for the full argument. Chapter 7 offers a route through the original literature.

Citations bearing an S, V, D, or A source number link to the annotation included in this
book. The annotation retains the link to the original publication, which needs an
internet connection. Use the reader's Back command to return after following a link.

Compact comparison tables remain tables. Dense tables are presented as a sequence of
labeled entries, and the architecture diagram is rendered as a list of connections.
Repository references appear as notes containing the original file paths. Those source
files are not included; they refer to the report's inspected repository snapshot,
`5bb3a7736fe49b0cc4914299b142c0c9bafb6870`.
"""


def node(kind, content):
    return {"t": kind, "c": content}


def words(text):
    result = []
    for word in text.split():
        if result:
            result.append({"t": "Space"})
        result.append(node("Str", word))
    return result


def plain(value):
    if isinstance(value, list):
        return "".join(map(plain, value))
    if not isinstance(value, dict):
        return ""
    kind = value["t"]
    if kind == "Str":
        return value["c"]
    if kind in ("Space", "SoftBreak", "LineBreak"):
        return " "
    if kind == "Code":
        return value["c"][1]
    if kind in ("Link", "Image", "Span"):
        return plain(value["c"][1])
    return plain(value.get("c", []))


def div(blocks, css_class):
    return node("Div", [["", [css_class], []], blocks])


def parse(pandoc, text):
    return json.loads(subprocess.check_output(
        [pandoc, "--from=gfm", "--to=json"], input=text.encode()
    ))


def cell_inlines(cell):
    # The report's pipe-table cells contain only inline paragraphs. Fail if a
    # future table uses richer content rather than silently dropping it.
    result = []
    if cell[2:4] != [1, 1]:
        raise ValueError("Spanning table cells require an explicit reading layout")
    for block in cell[4]:
        if block["t"] not in ("Plain", "Para"):
            raise ValueError("A table cell contains unsupported block content")
        if result:
            result.append({"t": "Space"})
        result.extend(block["c"])
    return result


def table_layout(table, stats, compatibility=False):
    attr, caption, specs, head, bodies, foot = table["c"]
    if caption != [None, []] or foot[1] or any(body[2] for body in bodies):
        raise ValueError("A table has captions or row groups requiring explicit handling")
    headers = [cell_inlines(cell) for cell in head[1][0][1]]
    rows = [[cell_inlines(cell) for cell in row[1]]
            for body in bodies for row in body[3]]
    lengths = [[len(plain(cell)) for cell in row] for row in [headers] + rows]
    compact = (not compatibility and len(headers) <= 3 and max(map(max, lengths)) <= 95
               and max(map(sum, lengths)) <= 180)
    if compact:
        # Give prose more space while reserving enough width in every column
        # for enlarged type on the Scribe.
        weights = [max(12, max(row[i] for row in lengths)) ** 0.7
                   for i in range(len(headers))]
        total = sum(weights)
        floor = 0.24 if len(headers) == 3 else 0.28
        remainder = 1 - floor * len(headers)
        table["c"][2] = [[spec[0], node("ColWidth", floor + remainder * weight / total)]
                         for spec, weight in zip(specs, weights)]
        stats["compact_tables"] += 1
        return table
    records = []
    for row in rows:
        if len(row) != len(headers):
            raise ValueError("Table row width does not match its header")
        title = div([node("Para", [node("Strong", row[0])])], "record-title")
        fields = [title]
        for header, cell in zip(headers[1:], row[1:]):
            label = node("Strong", header + [node("Str", ":")])
            fields.append(div([node("Para", [label, {"t": "Space"}] + cell)],
                              "record-field"))
        records.append(div(fields, "table-record"))
    stats["stacked_tables"] += 1
    stats["stacked_rows"] += len(rows)
    return div(records, "stacked-table")


def diagram_as_list(code, stats):
    labels = dict(re.findall(r'(\w+)\["([^"]+)"\]', code))
    edges = []
    for line in code.splitlines()[1:]:
        match = re.fullmatch(
            r'\s*(\w+)(?:\["[^"]+"\])? --> (\w+)(?:\["[^"]+"\])?\s*', line
        )
        if not match:
            raise ValueError(f"Unrecognized diagram connection: {line}")
        left, right = match.groups()
        edges.append([node("Plain", words(labels[left] + " → " + labels[right]))])
    stats["diagram_connections"] += len(edges)
    return div([
        node("Para", [node("Strong", words("Information flow"))]),
        node("Para", words("Each arrow means that the receiving stage needs information "
                           "from the preceding stage. The connections are:")),
        node("BulletList", edges),
    ], "diagram-reading")


def kindle_cover(path):
    # Pandoc wraps the raster cover in SVG. Use a plain image so the delivered
    # book does not depend on Amazon's handling of embedded SVG.
    with ZipFile(path) as archive:
        contents = [(entry, archive.read(entry.filename)) for entry in archive.infolist()]
    with ZipFile(path, "w") as archive:
        for entry, data in contents:
            if entry.filename.endswith("/text/cover.xhtml"):
                text = data.decode()
                image = re.search(r'xlink:href="([^"]+)"', text)
                if not image:
                    raise ValueError("Pandoc's cover structure has changed")
                text = re.sub(r'<svg\b.*?</svg>',
                              '<img src="' + image.group(1) + '" '
                              'alt="the-valley: Foundations and Research Opportunities" />',
                              text, flags=re.S)
                data = text.encode()
            elif entry.filename.endswith("/content.opf"):
                data = data.replace(b' properties="svg"', b'')
            archive.writestr(entry, data)


def normalize_navigation(path):
    # Keep the legacy NCX's declared depth consistent with its actual tree.
    # Its optional, nonstandard cover hint is unnecessary: the OPF owns that
    # information, and Pandoc's hint can name an image ID that does not exist.
    namespace = "{http://www.daisy.org/z3986/2005/ncx/}"

    def depth(element):
        children = element.findall(namespace + "navPoint")
        return 1 + max(map(depth, children), default=0) if children else 0

    with ZipFile(path) as archive:
        contents = [(entry, archive.read(entry.filename)) for entry in archive.infolist()]
    with ZipFile(path, "w") as archive:
        for entry, data in contents:
            if entry.filename.endswith(".ncx"):
                text = data.decode()
                nav = ElementTree.fromstring(text).find(namespace + "navMap")
                text = re.sub(r'(<meta name="dtb:depth" content=")\d+("\s*/>)',
                              lambda match: match[1] + str(depth(nav)) + match[2], text)
                text = re.sub(r'\s*<meta name="cover"[^>]*/>', "", text)
                data = text.encode()
            archive.writestr(entry, data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pandoc", default="pandoc")
    parser.add_argument("--rsvg-convert", default="rsvg-convert")
    parser.add_argument("--epubcheck", help="Optional EPUBCheck executable")
    parser.add_argument("--compatibility", action="store_true",
                        help="Build EPUB 2 with plain layouts and ordinary reference links")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--title", help="Optional title for a distinct revised edition")
    parser.add_argument("--identifier", help="Optional EPUB identifier for a distinct revised edition")
    parser.add_argument("--preview", type=Path, help="Optional standalone HTML preview")
    args = parser.parse_args()
    if args.output is None:
        filename = "the-valley-research-compatibility.epub" if args.compatibility else "the-valley-research.epub"
        args.output = REPORT / filename
    docs = {name: parse(args.pandoc, (REPORT / name).read_text()) for name in FILES}
    ids = {}
    starts = {}
    sources = {}
    for index, (name, doc) in enumerate(docs.items()):
        prefix = f"report-{index:02d}-"
        for block in doc["blocks"]:
            if block["t"] == "Header":
                level, attr, title = block["c"]
                old = attr[0]
                attr[0] = f"h{len(ids) + 1:04d}" if args.compatibility else prefix + old
                ids[(name, old)] = attr[0]
                if level == 1:
                    starts[name] = attr[0]
                    if name in TITLES:
                        block["c"][2] = words(TITLES[name])
                match = re.match(r"([SVDA]\d{2})\b", plain(title))
                if name.startswith("sources-") and match:
                    sources[match.group(1)] = attr[0]
                if name.startswith("sources-") and level > 1:
                    # Direct citation links retain all entry anchors without
                    # turning the contents into a second 106-entry bibliography.
                    attr[1].append("unlisted")
                # Local numbering in these chapters becomes book numbering.
                if name[:2] in ("03", "04", "05") and level == 2:
                    text = plain(title)
                    if re.match(r"\d+\. ", text):
                        block["c"][2] = words(f"{int(name[:2])}." + text)
    stats = {key: 0 for key in ("compact_tables", "stacked_tables", "stacked_rows",
                               "diagram_connections", "offline_citations",
                               "repository_notes")}
    repository_refs = {}

    def transform(value, name):
        if isinstance(value, list):
            return [transform(item, name) for item in value]
        if not isinstance(value, dict):
            return value
        kind = value.get("t")
        if kind == "Link":
            attr, label, target = copy.deepcopy(value["c"])
            url, title = target
            source = re.search(r"\b([SVDA]\d{2})\b", plain(label))
            if source and source.group(1) in sources and not name.startswith("sources-"):
                stats["offline_citations"] += 1
                return node("Link", [attr, label, ["#" + sources[source.group(1)], title]])
            parts = urlsplit(url)
            if parts.scheme or parts.netloc:
                return value
            dest = ((REPORT / name).parent / unquote(parts.path)).resolve()
            if not parts.path:
                dest = REPORT / name
            if dest.parent == REPORT and dest.name in docs:
                anchor = unquote(parts.fragment)
                key = ids[(dest.name, anchor)] if anchor else starts[dest.name]
                return node("Link", [attr, label, ["#" + key, title]])
            if not dest.exists() or not dest.is_relative_to(REPO):
                raise ValueError(f"Unresolved local reference in {name}: {url}")
            path = dest.relative_to(REPO).as_posix()
            if dest.is_dir():
                path += "/"
            if parts.fragment:
                path += "#" + unquote(parts.fragment)
            if args.compatibility:
                ref_id = repository_refs.setdefault(path, f"r{len(repository_refs) + 1:03d}")
                stats["repository_notes"] += 1
                reference = node("Link", [["", [], []], words(f"[{ref_id.upper()}]"),
                                          ["#" + ref_id, "Repository reference"]])
                return node("Span", [["", [], []], label + [{"t": "Space"}, reference]])
            note = node("Note", [node("Para", words("Repository source:") + [
                {"t": "Space"}, node("Code", [["", ["repository-path"], []], path])
            ])])
            stats["repository_notes"] += 1
            return node("Span", [["", [], []], label + [note]])
        if kind == "CodeBlock" and "mermaid" in value["c"][0][1]:
            return diagram_as_list(value["c"][1], stats)
        mapped = {key: transform(item, name) for key, item in value.items()}
        if kind == "Table":
            return table_layout(mapped, stats, args.compatibility)
        return mapped

    intro = INTRO
    if args.compatibility:
        intro = intro.replace(
            "Compact comparison tables remain tables. Dense tables are presented as a sequence of\n"
            "labeled entries, and the architecture diagram is rendered as a list of connections.\n"
            "Repository references appear as notes containing the original file paths.",
            "This compatibility edition presents every table as a sequence of labeled entries.\n"
            "The architecture diagram is rendered as a list of connections. Repository references\n"
            "link to an appendix containing the original file paths."
        )
    book = parse(args.pandoc, intro)
    for name, doc in docs.items():
        book["blocks"].extend(transform(doc["blocks"], name))
    if args.compatibility:
        book["blocks"].append(node("Header", [1, ["repository-references", [], []],
                                               words("Repository references")]))
        book["blocks"].append(node("Para", words(
            "These paths refer to the repository snapshot inspected for this report. "
            "The source files are not embedded in the book. Use the reader's Back command "
            "to return to the passage that cited a path."
        )))
        for path, ref_id in repository_refs.items():
            book["blocks"].append(node("Div", [[ref_id, [], []], [node("Para", [
                node("Strong", words(ref_id.upper() + ":")), {"t": "Space"},
                node("Code", [["", [], []], path])
            ])]]))
    metadata = {
        "title": "the-valley: Foundations & Research Opportunities",
        "subtitle": "A critical literature and systems review",
        "author": "the-valley",
        "lang": "en-US",
        "date": "2026-09-20",
        "identifier": "urn:uuid:fe8306ef-12c2-4975-bc95-c019a83816e1",
        "toc-title": "Contents",
    }
    if args.compatibility:
        metadata["title"] = "the-valley Research - Compatibility Edition"
        metadata["identifier"] = "urn:uuid:3e1f6cc5-8a16-472c-bba1-a9a66f8a0e9d"
    if args.title:
        metadata["title"] = args.title
    if args.identifier:
        metadata["identifier"] = args.identifier
    book["meta"] = {key: node("MetaString", value) for key, value in metadata.items()}
    args.output = args.output.resolve()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="valley-reader-") as temp:
        cover = Path(temp) / "cover.png"
        if not args.compatibility:
            subprocess.run([args.rsvg_convert, "--output", str(cover), str(HERE / "cover.svg")],
                           check=True)
        data = json.dumps(book).encode()
        stylesheet = "compatibility.css" if args.compatibility else "reader.css"
        common = [args.pandoc, "--from=json", "--standalone", "--toc", "--toc-depth=2",
                  "--css", str(HERE / stylesheet), "--wrap=none"]
        epub_options = (["--to=epub2"] if args.compatibility else
                        ["--to=epub3", "--epub-cover-image", str(cover)])
        subprocess.run(common + epub_options + ["--split-level=1", "--output", str(args.output)],
                       input=data, check=True)
        if args.preview:
            subprocess.run(common + ["--to=html5", "--embed-resources",
                                     "--output", str(args.preview)], input=data, check=True)
    if not args.compatibility:
        kindle_cover(args.output)
    normalize_navigation(args.output)
    if args.epubcheck:
        subprocess.run([args.epubcheck, str(args.output)], check=True)
    print(json.dumps({"output": str(args.output), "bytes": args.output.stat().st_size,
                      "source_documents": len(docs), "annotated_sources": len(sources),
                      **stats}, indent=2))


if __name__ == "__main__":
    main()

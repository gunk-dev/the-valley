#!/usr/bin/env python3
"""Render the reader HTML as a fixed-page PDF for the Kindle Scribe."""

from __future__ import annotations

import argparse
import base64
from pathlib import Path
import re
import subprocess
import tempfile


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--html", type=Path, required=True, help="Reader HTML from build.py")
    parser.add_argument("--weasyprint", default="weasyprint", help="WeasyPrint executable")
    parser.add_argument("--output", type=Path, help="PDF destination")
    args = parser.parse_args()

    edition = Path(__file__).resolve().parent
    output = args.output or edition.parent / "the-valley-research-scribe.pdf"
    source = args.html.resolve().read_text(encoding="utf-8")
    if 'id="report-11-' not in source or 'id="TOC"' not in source:
        parser.error("Expected the complete reader HTML with its table of contents")

    # The PDF has its own page layout. Reflowable EPUB styles do not apply here.
    source = re.sub(r"<style\b[^>]*>.*?</style>", "", source, flags=re.DOTALL)
    css = (edition / "pdf.css").read_text(encoding="utf-8")
    source = source.replace("</head>", f"<style>\n{css}\n</style>\n</head>", 1)
    cover = base64.b64encode((edition / "cover.svg").read_bytes()).decode("ascii")
    source = re.sub(
        r'<header id="title-block-header">.*?</header>',
        '<div class="pdf-cover"><img alt="the-valley: Foundations and Research '
        f'Opportunities" src="data:image/svg+xml;base64,{cover}" /></div>',
        source,
        count=1,
        flags=re.DOTALL,
    )
    source = source.replace(
        "with adjustable text and a linked table of contents",
        "with pages sized for its screen, embedded fonts, and a linked table of contents",
        1,
    )
    source = source.replace(
        '<section id="footnotes" class="footnotes footnotes-end-of-document" '
        'role="doc-endnotes">\n<hr />',
        '<section id="footnotes" class="footnotes footnotes-end-of-document" '
        'role="doc-endnotes">\n<h1 id="repository-source-notes">Repository source notes</h1>',
        1,
    )
    nav_end = source.index("</nav>")
    nav_close = source.rfind("</ul>", 0, nav_end)
    source = (
        source[:nav_close]
        + '<li><a href="#repository-source-notes">Repository source notes</a></li>\n'
        + source[nav_close:]
    )

    output = output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="the-valley-pdf-") as temporary:
        html = Path(temporary) / "report.html"
        html.write_text(source, encoding="utf-8")
        subprocess.run(
            [args.weasyprint, "--base-url", str(args.html.resolve().parent), str(html), str(output)],
            check=True,
        )
    print(f"Wrote {output}")


if __name__ == "__main__":
    main()

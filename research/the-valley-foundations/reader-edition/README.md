# Kindle Scribe reading editions

The current revision centers clear requirements, established solutions, and constraints across the
complete workflow. It adds a requirements clarification checklist and replaces the novelty
discussion with an adoption rule and a test of the complete system.

- [Revised EPUB](../the-valley-research-revised.epub), titled **the-valley Research - Requirements
  and Composition**. It uses the compatibility layout with adjustable text.
- [Revised Scribe PDF](../the-valley-research-scribe-revised.pdf), with fixed pages and linked
  contents.

Each contains its full report revision and all 106 annotated source entries. Email the chosen file
to the Scribe's Send to Kindle address. See Amazon's
[delivery instructions](https://digprjsurvey.amazon.com/csad/help/node/G5WYD9SAF7PGXRNA).

The EPUB uses EPUB 2, presents tables as stacked entries, and links to an ordinary repository
reference appendix. The PDF uses pages sized for the Scribe, embedded fonts, and page numbers.
Earlier exports are not included in this submission.

## Test locally

Calibre includes an [ebook viewer](https://manual.calibre-ebook.com/generated/en/ebook-viewer.html).
From the repository root on Nix:

```sh
nix shell nixpkgs#calibre --command ebook-viewer \
  research/the-valley-foundations/the-valley-research-revised.epub
```

A successful local test verifies that reader's handling of the file. Amazon performs a separate
conversion for Kindle delivery.

## Rebuild the EPUBs

The Markdown report is the source. With Python 3, Pandoc 3, and EPUBCheck on the path, build and
validate the compatibility edition:

```sh
python3 research/the-valley-foundations/reader-edition/build.py --compatibility \
  --output research/the-valley-foundations/the-valley-research-revised.epub \
  --title 'the-valley Research - Requirements and Composition' \
  --identifier urn:uuid:63de45ec-5215-451c-851f-173f083bf142 --epubcheck epubcheck
```

To use this repository's pinned Nix packages:

```sh
nix shell --impure --expr '
  let pkgs = (builtins.getFlake (toString ./.)).inputs.nixpkgs.legacyPackages.x86_64-linux;
  in [ pkgs.python3 pkgs.pandoc pkgs.librsvg pkgs.epubcheck pkgs.dejavu_fonts ]
' --command python3 research/the-valley-foundations/reader-edition/build.py --compatibility \
  --output research/the-valley-foundations/the-valley-research-revised.epub \
  --title 'the-valley Research - Requirements and Composition' \
  --identifier urn:uuid:63de45ec-5215-451c-851f-173f083bf142 --epubcheck epubcheck
```

Omit `--compatibility` to build the original layout. That mode also requires `rsvg-convert` for its
cover. The cover uses DejaVu fonts when installed, with generic font fallbacks. The EPUB body leaves
fonts to the reader.

## Rebuild the PDF

With the preceding tools and WeasyPrint available, generate the standard reader HTML and render it:

```sh
python3 research/the-valley-foundations/reader-edition/build.py \
  --output /tmp/the-valley-reading.epub --preview /tmp/the-valley-reading.html \
  --title 'the-valley Research - Requirements and Composition'
python3 research/the-valley-foundations/reader-edition/build_pdf.py \
  --html /tmp/the-valley-reading.html \
  --output research/the-valley-foundations/the-valley-research-scribe-revised.pdf
```

WeasyPrint is available as `python3Packages.weasyprint` in the pinned Nix package set. The PDF uses
DejaVu fonts. The default output is
`research/the-valley-foundations/the-valley-research-scribe.pdf`.

## Validation and remaining uncertainty

Both EPUB layouts pass EPUBCheck 5.3.0. Internal references and source passages are checked against
the Markdown. An earlier EPUB downloaded to a Scribe but would not open. The compatibility edition
removes images, tables, and EPUB 3 footnote semantics. It also uses short anchor identifiers and a
consistent navigation depth. None of those features has been established as the cause of the
original failure. Passing EPUBCheck does not establish that Amazon's converted document will open on
a particular device.

The PDF is rendered locally and checked for readable pages and preserved content. A replacement
edition was confirmed to open on the Scribe, although the confirmation did not identify which format
was used. The latest content revision has not been tested on the device.

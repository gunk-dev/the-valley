#!/usr/bin/env python3
"""The valley-delta-v1 conformance vectors, computed from the specification.

This script is the source of every expected result under this directory. It
reads nothing but the vector specs, and uses nothing but Python's standard
library: no git, and none of the Go code it checks. See README.md.

    python3 -I vectors.py <testdata dir>           write the expected results
    python3 -I vectors.py --check <testdata dir>   fail if any differs

For each valid/<name>.json, which lists the files of a pre tree and a post
tree, it writes valid/<name>.delta, the encoding, and valid/<name>.sha256, its
digest. It writes each malformed vector below to malformed/<name>.delta.
"""

import hashlib
import json
import pathlib
import sys

HEADER = b"valley-delta-v1\n"


def files(listing):
    """A tree's files: path bytes to (mode, SHA-256 of the content)."""
    tree = {}
    for f in listing:
        path = bytes.fromhex(f["path_hex"]) if "path_hex" in f else f["path"].encode("utf-8")
        content = bytes.fromhex(f["content_hex"]) if "content_hex" in f else f["content"].encode("utf-8")
        if path in tree:
            raise SystemExit(f"{path!r} is listed twice")
        tree[path] = (f["mode"], hashlib.sha256(content).hexdigest())
    return tree


def image(entry):
    return b"-" if entry is None else f"{entry[0]} {entry[1]}".encode()


def encode(pre, post):
    """The header, then a line for each path whose entries differ, bytewise."""
    out = bytearray(HEADER)
    for path in sorted(set(pre) | set(post)):
        before, after = pre.get(path), post.get(path)
        if before == after:
            continue
        out += b"%d %s %s %s\n" % (len(path), path, image(before), image(after))
    return bytes(out)


H1 = hashlib.sha256(b"one\n").hexdigest().encode()
H2 = hashlib.sha256(b"two\n").hexdigest().encode()


def line(path, pre, post):
    return b"%d %s %s %s\n" % (len(path), path, pre, post)


# Each malformed vector: its name, why it is refused, and its bytes.
MALFORMED = [
    ("empty-file", "no header", b""),
    ("other-header", "the header names another scheme",
     b"valley-delta-v2\n" + line(b"a", b"-", b"100644 " + H1)),
    ("header-unterminated", "the header has no newline", b"valley-delta-v1"),
    ("header-crlf", "the header ends in CRLF", b"valley-delta-v1\r\n"),
    ("duplicate-path", "a path is given twice",
     HEADER + line(b"a", b"-", b"100644 " + H1) + line(b"a", b"-", b"100644 " + H2)),
    ("unsorted", "paths are out of bytewise order",
     HEADER + line(b"b", b"-", b"100644 " + H1) + line(b"a", b"-", b"100644 " + H2)),
    ("length-leading-zero", "the length has a leading zero",
     HEADER + b"01 a - 100644 " + H1 + b"\n"),
    ("length-plus", "the length has a sign", HEADER + b"+1 a - 100644 " + H1 + b"\n"),
    ("length-zero", "the path is empty", HEADER + b"0  - 100644 " + H1 + b"\n"),
    ("length-short", "the length stops inside the path",
     HEADER + b"1 ab - 100644 " + H1 + b"\n"),
    ("length-long", "the length runs into the images",
     HEADER + b"3 a - 100644 " + H1 + b"\n"),
    ("length-past-end", "the length runs past the end", HEADER + b"9999 a\n"),
    ("length-huge", "the length has more digits than any path could need",
     HEADER + b"99999999999999999999 a - 100644 " + H1 + b"\n"),
    ("length-missing", "the line opens with the path", HEADER + b"a - 100644 " + H1 + b"\n"),
    ("mode-invalid", "the mode is no mode git writes for a file",
     HEADER + line(b"a", b"-", b"100664 " + H1)),
    ("mode-short", "the mode is a prefix of a valid one", HEADER + line(b"a", b"-", b"10064 " + H1)),
    ("mode-directory", "a directory is no entry", HEADER + line(b"a", b"-", b"40000 " + H1)),
    ("mode-directory-padded", "a directory is no entry, however written",
     HEADER + line(b"a", b"-", b"040000 " + H1)),
    ("mode-submodule", "a submodule is an unsupported entry",
     HEADER + line(b"a", b"-", b"160000 " + H1)),
    ("hash-uppercase", "the hash is upper case",
     HEADER + line(b"a", b"-", b"100644 " + H1.upper())),
    ("hash-short", "the hash has 63 digits", HEADER + line(b"a", b"-", b"100644 " + H1[:63])),
    ("hash-long", "the hash has 65 digits", HEADER + line(b"a", b"-", b"100644 " + H1 + b"0")),
    ("hash-not-hex", "the hash holds a letter past f",
     HEADER + line(b"a", b"-", b"100644 " + H1[:63] + b"g")),
    ("hash-sha1", "the hash is a git object ID, not SHA-256",
     HEADER + line(b"a", b"-", b"100644 " + b"e" * 40)),
    ("image-missing", "the line has one image", HEADER + b"1 a -\n"),
    ("both-absent", "a path absent on both sides is no change", HEADER + line(b"a", b"-", b"-")),
    ("unchanged", "a path with equal images is no change",
     HEADER + line(b"a", b"100644 " + H1, b"100644 " + H1)),
    ("double-space", "two spaces between the images",
     HEADER + b"1 a -  100644 " + H1 + b"\n"),
    ("line-crlf", "a line ends in CRLF", HEADER + b"1 a - 100644 " + H1 + b"\r\n"),
    ("line-unterminated", "the last line has no newline", HEADER + b"1 a - 100644 " + H1),
    ("trailing-bytes", "something follows the last line",
     HEADER + line(b"a", b"-", b"100644 " + H1) + b"\n"),
    ("path-dot-dot", "a path climbs out of the tree", HEADER + line(b"../a", b"-", b"100644 " + H1)),
    ("path-dot", "a path has the component .", HEADER + line(b"./a", b"-", b"100644 " + H1)),
    ("path-dotgit", "a path is inside .git", HEADER + line(b".git/config", b"-", b"100644 " + H1)),
    ("path-dotgit-case", "a path is inside .git, in another case",
     HEADER + line(b"sub/.GiT/hooks", b"-", b"100644 " + H1)),
    ("path-leading-slash", "a path is absolute", HEADER + line(b"/a", b"-", b"100644 " + H1)),
    ("path-trailing-slash", "a path ends in a slash", HEADER + line(b"a/", b"-", b"100644 " + H1)),
    ("path-double-slash", "a path has an empty component",
     HEADER + line(b"a//b", b"-", b"100644 " + H1)),
    ("path-nul", "a path holds a NUL", HEADER + line(b"a\x00b", b"-", b"100644 " + H1)),
    ("alias-post", "the post side has a file at a path and another under it",
     HEADER + line(b"a", b"-", b"100644 " + H1) + line(b"a/b", b"-", b"100644 " + H2)),
    ("alias-pre", "the pre side has a symlink at a path and a file under it",
     HEADER + line(b"d", b"120000 " + H1, b"-") + line(b"d/e/f", b"100644 " + H2, b"-")),
]


def main(argv):
    check = argv[:1] == ["--check"]
    if check:
        argv = argv[1:]
    if len(argv) != 1:
        raise SystemExit(__doc__)
    root = pathlib.Path(argv[0])
    want = {}
    for spec in sorted((root / "valid").glob("*.json")):
        vector = json.loads(spec.read_text(encoding="utf-8"))
        encoding = encode(files(vector["pre"]), files(vector["post"]))
        want[spec.with_suffix(".delta")] = encoding
        want[spec.with_suffix(".sha256")] = hashlib.sha256(encoding).hexdigest().encode() + b"\n"
    for name, _, data in MALFORMED:
        want[root / "malformed" / f"{name}.delta"] = data
    stale = []
    for path, data in sorted(want.items()):
        if check:
            if not path.exists() or path.read_bytes() != data:
                stale.append(str(path))
        else:
            path.write_bytes(data)
    for directory, pattern in (("valid", "*.delta"), ("valid", "*.sha256"), ("malformed", "*.delta")):
        for path in (root / directory).glob(pattern):
            if path not in want:
                stale.append(f"{path} (no vector makes it)")
    if stale:
        raise SystemExit("not what vectors.py writes:\n  " + "\n  ".join(stale))


if __name__ == "__main__":
    main(sys.argv[1:])

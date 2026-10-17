# Conformance vectors for valley-delta-v1

These files fix the valley-delta-v1 encoding and digest. Any implementation of the delta, this
module or another, must produce exactly these bytes from these trees, and must refuse every
malformed encoding here. The encoding itself is specified in [`../delta.go`](../delta.go).

## What is here

- `valid/<name>.json` lists the files of two trees, `pre` and `post`. Each file has a path, a mode
  and its content. A path or content that JSON cannot carry as text is given in hex instead, as
  `path_hex` or `content_hex`. A symlink's content is its target. `about` says what the vector
  covers.
- `valid/<name>.delta` is the exact encoding of the delta from `pre` to `post`.
- `valid/<name>.sha256` is the digest of that encoding, in lowercase hex.
- `malformed/<name>.delta` is an encoding a reader must refuse. The table in
  [`vectors.py`](./vectors.py) says why each one is malformed.

The valid vectors cover the empty delta, binary content, renames, mode-only changes, symlinks,
deletions, CRLF, a missing final newline, and paths holding spaces and newlines. They also cover a
file that becomes a directory, bytewise path order, and paths that are not UTF-8. The malformed
vectors cover duplicate and unsorted paths, non-canonical lengths, invalid modes and hashes,
unsupported entries such as a submodule or a directory, paths no tree can hold, and broken line
structure.

## Where the expected results come from

Every `.delta` and `.sha256` file is written by [`vectors.py`](./vectors.py), a short Python script
that uses only Python's standard library. It reads the JSON specs and nothing else. It computes each
encoding straight from the specification: it hashes each file's content with SHA-256, keeps the
paths whose mode or hash differ, sorts them bytewise, and writes the lines. It does not run git, and
it shares no code with the Go module. So a result here is never what the Go code happened to
produce.

The script was run once, and its output is committed:

```sh
python3 -I delta/testdata/vectors.py delta/testdata
```

Run it the same way after adding or changing a vector, and commit what it writes.

## How they are checked

The `delta-unit` check runs three things:

- `vectors.py --check`, which fails if any committed file differs from what the script writes, or if
  a file is here that no vector makes;
- `go test`, which builds each vector's two trees as real git objects, computes the delta with the
  Go code from the trees and from commits of them, and requires the committed encoding and digest.
  It then parses the encoding and requires it to write back byte for byte, and requires every
  malformed vector to be refused;
- `go vet`.

Trees that no checkout can hold faithfully, such as a file and a directory at one path, cannot be
written as JSON specs. `../tree_test.go` builds them with git directly and requires them to be
refused.

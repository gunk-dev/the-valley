# Prose experiments

This directory keeps two prose explorations of the original design corpus. Each one rewrites the
corpus in plainer language. They are kept as material for a later prose pass, not as current
documentation.

Each subdirectory holds one branch's version of every file that branch changed, byte for byte, at
the file's original path. The branches predate the move of `design/` and `.the-valley/` under
`archive/`, so `design/architecture.md` here is the rewrite of `archive/design/architecture.md`.
Neither branch deletes a file, so nothing is skipped.

- `plain-language-corpus/` is branch `docs/plain-language-corpus` at commit
  `b45f281382cb3538d3c69ad695a8584e1afe53c7`. It rewrites the corpus in plain, direct engineering
  English with a subtle Daoist flavor. It holds 98 files.
- `plain-language-knowledge-graph/` is branch `docs/plain-language-knowledge-graph` at commit
  `7f4e39ba7e1dc58d2cd7e40fdd76e2a9991c5a7f`. It rewrites the knowledge graph and the design
  documents in plain language. Its second commit preserves the corpus's original Taoist voice. It
  holds 98 files.

Both branches start from commit `5bb3a7736fe49b0cc4914299b142c0c9bafb6870`. Their relative links
point at the paths of that time, so many do not resolve from here. The copies repeat node IDs from
`archive/.the-valley/`, but they are not part of that graph: the knowledge lint reads only
`archive/.the-valley/`.

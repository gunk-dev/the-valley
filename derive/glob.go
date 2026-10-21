package derive

import "strings"

// Match reports whether a changed path matches a class's glob.
//
// A glob is read segment by segment, and matched byte by byte, because a
// path is bytes and need not be text. In a segment, `*` matches any bytes
// but `/`, `?` matches one byte that is not `/`, and every other byte
// matches itself. A segment that is exactly `**` matches any number of
// segments, none included, so `a/**/b` matches `a/b` as well as `a/x/y/b`,
// and a last `**` matches the rest of the path, whatever it is. Every
// other segment matches exactly one segment.
//
// This is bin/valley's reading (glob_ere) as the C locale gives it, down
// to its edges. One trailing `/` is dropped, as `read -a` drops the empty
// field after it, so `docs/` matches the path `docs`. `**` is special only
// as a whole segment, so `a**b` is two `*` in one segment.
func Match(glob, path string) bool {
	return compile(glob).match(path)
}

// glob is a compiled glob: a sequence of tokens, each matching some bytes.
type glob []token

type token struct {
	kind tokenKind
	b    byte // the byte a literal matches
}

type tokenKind uint8

const (
	literal  tokenKind = iota // one byte, itself
	one                       // `?`: one byte that is not `/`
	within                    // `*`: any bytes, none of them `/`
	rest                      // a last `**` segment: any bytes at all
	segments                  // any other `**` segment: nothing, or any bytes ending in `/`
)

func compile(pattern string) glob {
	segs := strings.Split(pattern, "/")
	if segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	var g glob
	for i, seg := range segs {
		last := i == len(segs)-1
		if seg == "**" {
			if last {
				g = append(g, token{kind: rest})
			} else {
				// It takes the `/` that would follow it, so that matching
				// no segment leaves no `/` behind.
				g = append(g, token{kind: segments})
			}
			continue
		}
		for j := 0; j < len(seg); j++ {
			switch seg[j] {
			case '*':
				g = append(g, token{kind: within})
			case '?':
				g = append(g, token{kind: one})
			default:
				g = append(g, token{kind: literal, b: seg[j]})
			}
		}
		if !last {
			g = append(g, token{kind: literal, b: '/'})
		}
	}
	return g
}

// match reports whether the whole of path matches the whole of g. Each
// pair of token and position is decided once, so a glob with many `*` and
// `**` costs no more than the product of its length and the path's.
func (g glob) match(path string) bool {
	n := len(path)
	const unknown, no, yes = 0, 1, 2
	seen := make([]uint8, (len(g)+1)*(n+1))
	var at func(t, p int) bool
	at = func(t, p int) bool {
		if t == len(g) {
			return p == n
		}
		k := t*(n+1) + p
		if seen[k] != unknown {
			return seen[k] == yes
		}
		ok := false
		switch tok := g[t]; tok.kind {
		case literal:
			ok = p < n && path[p] == tok.b && at(t+1, p+1)
		case one:
			ok = p < n && path[p] != '/' && at(t+1, p+1)
		case within:
			for q := p; !ok; q++ {
				ok = at(t+1, q)
				if q == n || path[q] == '/' {
					break
				}
			}
		case rest:
			for q := p; q <= n && !ok; q++ {
				ok = at(t+1, q)
			}
		case segments:
			ok = at(t+1, p)
			for q := p; q < n && !ok; q++ {
				if path[q] == '/' {
					ok = at(t+1, q+1)
				}
			}
		}
		seen[k] = no
		if ok {
			seen[k] = yes
		}
		return ok
	}
	return at(0, 0)
}

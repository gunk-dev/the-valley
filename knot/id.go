package knot

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// DeriveID is the ID a knot of type t with slug gets: the type's prefix
// and the first 7 hex digits of the SHA-256 of the slug, the rule the
// archived graph follows (archive/.the-valley/README.md).
func DeriveID(t Type, slug string) string {
	sum := sha256.Sum256([]byte(slug))
	return t.Prefix + "-" + hex.EncodeToString(sum[:])[:7]
}

// Slug is the slug a title gives: its letters and digits in lowercase,
// every other run of characters one hyphen, cut to MaxSlug bytes at a
// hyphen where one is near. A title with no ASCII letter or digit gives
// "knot".
func Slug(title string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
		default:
			hyphen = true
		}
	}
	return cut(b.String(), MaxSlug)
}

// cut is a slug cut to at most n bytes, at its last hyphen within them
// when that keeps more than half, and with no hyphen at its end.
func cut(slug string, n int) string {
	if len(slug) > n {
		slug = slug[:n]
		if i := strings.LastIndexByte(slug, '-'); i > n/2 {
			slug = slug[:i]
		}
		slug = strings.TrimRight(slug, "-")
	}
	if slug == "" {
		return "knot"
	}
	return slug
}

// NewID is the ID and slug a new knot of type t titled title gets in a
// graph where taken reports the IDs already used. When the title's slug
// gives an ID that is taken, the slug gains a suffix, -2, -3 and on, until
// the ID it gives is free.
func NewID(t Type, title string, taken func(id string) bool) (id, slug string) {
	base := Slug(title)
	slug = base
	for n := 2; ; n++ {
		id = DeriveID(t, slug)
		if !taken(id) {
			return id, slug
		}
		suffix := "-" + strconv.Itoa(n)
		slug = cut(base, MaxSlug-len(suffix)) + suffix
	}
}

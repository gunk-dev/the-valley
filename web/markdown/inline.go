package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Inline content is parsed into a tree of nodes held in linked lists, so
// that wrapping a run of nodes in emphasis or a link takes constant time.

type ikind uint8

const (
	itext ikind = iota
	icode
	isoft
	ihard
	iemph
	istrong
	idel
	ilink
	iimage
	iautolink
)

type node struct {
	// text is text, code, an autolink's text, or the delimiters of
	// emphasis, strong emphasis or strikethrough.
	text string
	// link is a link's or an image's destination and title: a reference's
	// definition, or one of its own.
	link        *linkRef
	first, last *node
	prev, next  *node
	kind        ikind
	// brk is the line break, isoft or ihard, that follows a text node,
	// so that a break after text takes no node of its own.
	brk               ikind
	delimiter, opener bool // a text node that holds a delimiter run or a bracket
	mail              bool // an autolink to an email address
}

// dest is where an autolink goes.
func (n *node) dest() string {
	if n.mail {
		return "mailto:" + n.text
	}
	return n.text
}

// delim is a run of *, _ or ~ that may open or close emphasis.
type delim struct {
	n                 *node
	c                 byte
	count, orig       int
	canOpen, canClose bool
	prev, next        *delim
}

// bracket is a [ or ![ that may open a link or an image.
type bracket struct {
	n     *node
	image bool
	pos   int // where its text starts
	delim *delim
}

type inliner struct {
	r        *renderer
	s        string
	root     node
	delims   *delim // the last delimiter
	brackets []bracket
	floor    int // brackets below floor may open no link
	// The maximal runs of backticks, by length, and how far each length's
	// list has been searched.
	ticks  map[int][]int
	cursor map[int]int
}

var special = func() (t [256]bool) {
	for _, c := range []byte("\n\\`*_~[]!<&h") {
		t[c] = true
	}
	return t
}()

// parseInline parses s, or gives nil when the document's inline content
// takes more than MaxInlineNodes nodes with s's, or rendering stops.
func (r *renderer) parseInline(s string) *node {
	if r.full {
		return nil
	}
	in := &inliner{r: r, s: s}
	in.run()
	if r.stopped || r.full {
		return nil
	}
	in.emphasis(nil)
	if r.full {
		return nil
	}
	return &in.root
}

// count counts a new node against MaxInlineNodes.
func (in *inliner) count() {
	if in.r.nodes++; in.r.nodes > MaxInlineNodes {
		in.r.full = true
	}
}

func (in *inliner) append(n *node) *node {
	in.count()
	if in.root.last == nil {
		in.root.first = n
	} else {
		in.root.last.next = n
		n.prev = in.root.last
	}
	in.root.last = n
	return n
}

func (in *inliner) remove(n *node) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		in.root.first = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		in.root.last = n.prev
	}
	n.prev, n.next = nil, nil
}

// plainLast is the last node when it is text that no delimiter, bracket
// or break ends, and nil when it is not.
func (in *inliner) plainLast() *node {
	if l := in.root.last; l != nil && l.kind == itext && !l.delimiter && !l.opener && l.brk == 0 {
		return l
	}
	return nil
}

// addBreak adds a line break: to the last node, when that is plain text,
// or as a node of its own.
func (in *inliner) addBreak(brk ikind) {
	if l := in.plainLast(); l != nil {
		l.brk = brk
		return
	}
	in.append(&node{kind: brk})
}

func (in *inliner) addText(t string) {
	if t == "" {
		return
	}
	in.append(&node{kind: itext, text: t}) // never joined to the last, which would copy it again

}

func (in *inliner) run() {
	s := in.s
	for i := 0; i < len(s); {
		if in.r.done() || in.r.full {
			return
		}
		c := s[i]
		if !special[c] {
			j := i + 1
			for j < len(s) && !special[s[j]] {
				j++
			}
			in.addText(s[i:j])
			i = j
			continue
		}
		switch c {
		case '\n':
			brk := isoft
			if l := in.plainLast(); l != nil {
				t := strings.TrimRight(l.text, " ")
				if len(l.text)-len(t) >= 2 {
					brk = ihard
				}
				l.text = t
			}
			in.addBreak(brk)
			i++
			for i < len(s) && s[i] == ' ' {
				i++
			}
		case '\\':
			switch {
			case i+1 < len(s) && s[i+1] == '\n':
				in.addBreak(ihard)
				i += 2
				for i < len(s) && s[i] == ' ' {
					i++
				}
			case i+1 < len(s) && isPunct(s[i+1]):
				in.addText(s[i+1 : i+2])
				i += 2
			default:
				in.addText("\\")
				i++
			}
		case '`':
			i = in.code(i)
		case '*', '_', '~':
			i = in.delimiter(i)
		case '[':
			// A [ past maxBrackets open is text.
			if len(in.brackets) < maxBrackets {
				in.open(bracket{n: in.append(&node{kind: itext, text: "[", opener: true}), pos: i + 1, delim: in.delims})
			} else {
				in.addText("[")
			}
			i++
		case '!':
			if i+1 < len(s) && s[i+1] == '[' && len(in.brackets) < maxBrackets {
				in.open(bracket{n: in.append(&node{kind: itext, text: "![", opener: true}), image: true, pos: i + 2, delim: in.delims})
				i += 2
			} else {
				in.addText("!")
				i++
			}
		case ']':
			i = in.closeBracket(i)
		case '<':
			if n, end := autolink(s, i); end > 0 {
				in.append(n)
				i = end
			} else {
				in.addText("<")
				i++
			}
		case '&':
			if t, n := entity(s[i:]); n > 0 {
				in.addText(t)
				i += n
			} else {
				in.addText("&")
				i++
			}
		case 'h':
			if n, end := bareLink(s, i); end > 0 {
				in.append(n)
				i = end
			} else {
				in.addText("h")
				i++
			}
		}
	}
}

// open adds a bracket that waits for its ].
func (in *inliner) open(b bracket) {
	in.brackets = append(in.brackets, b)
	in.r.brackets = max(in.r.brackets, len(in.brackets))
}

// code reads a code span, or a run of backticks that opens none, at i.
func (in *inliner) code(i int) int {
	s := in.s
	n := run(s[i:], '`')
	end := i + n
	if in.ticks == nil {
		in.ticks, in.cursor = map[int][]int{}, map[int]int{}
		for j := 0; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			k := run(s[j:], '`')
			in.ticks[k] = append(in.ticks[k], j)
			j += k
		}
	}
	list := in.ticks[n]
	k := in.cursor[n]
	for k < len(list) && list[k] < end {
		k++
	}
	in.cursor[n] = k
	if k == len(list) {
		in.addText(s[i:end])
		return end
	}
	at := list[k]
	text := strings.ReplaceAll(s[end:at], "\n", " ")
	if len(text) >= 2 && text[0] == ' ' && text[len(text)-1] == ' ' && strings.Trim(text, " ") != "" {
		text = text[1 : len(text)-1]
	}
	in.append(&node{kind: icode, text: text})
	return at + n
}

func (in *inliner) delimiter(i int) int {
	s := in.s
	c := s[i]
	n := run(s[i:], c)
	if c == '~' && n > 2 {
		in.addText(s[i : i+n])
		return i + n
	}
	before, after := '\n', '\n'
	if i > 0 {
		before, _ = utf8.DecodeLastRuneInString(s[:i])
	}
	if i+n < len(s) {
		after, _ = utf8.DecodeRuneInString(s[i+n:])
	}
	left := !isSpace(after) && (!isPunctRune(after) || isSpace(before) || isPunctRune(before))
	right := !isSpace(before) && (!isPunctRune(before) || isSpace(after) || isPunctRune(after))
	canOpen, canClose := left, right
	if c == '_' {
		canOpen = left && (!right || isPunctRune(before))
		canClose = right && (!left || isPunctRune(after))
	}
	nd := in.append(&node{kind: itext, text: s[i : i+n], delimiter: true})
	if canOpen || canClose {
		d := &delim{n: nd, c: c, count: n, orig: n, canOpen: canOpen, canClose: canClose, prev: in.delims}
		if in.delims != nil {
			in.delims.next = d
		}
		in.delims = d
	}
	return i + n
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || unicode.Is(unicode.Zs, r)
}

func isPunctRune(r rune) bool {
	if r < 0x80 {
		return isPunct(byte(r))
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

func (in *inliner) removeDelim(d *delim) {
	if d.prev != nil {
		d.prev.next = d.next
	}
	if d.next != nil {
		d.next.prev = d.prev
	} else {
		in.delims = d.prev
	}
}

// emphasis matches the delimiters above bottom, as CommonMark's process
// emphasis does. Each search for an opener starts no lower than the
// last search for the same kind of closer ended, so the whole takes time
// linear in the delimiters.
func (in *inliner) emphasis(bottom *delim) {
	var openersBottom [3][2][3]*delim
	slot := func(c byte) int {
		switch c {
		case '*':
			return 0
		case '_':
			return 1
		}
		return 2
	}
	for a := range openersBottom {
		for b := range openersBottom[a] {
			for k := range openersBottom[a][b] {
				openersBottom[a][b][k] = bottom
			}
		}
	}
	// The first delimiter above bottom.
	var closer *delim
	for d := in.delims; d != nil && d != bottom; d = d.prev {
		closer = d
	}
	for closer != nil && !in.r.full {
		if !closer.canClose {
			closer = closer.next
			continue
		}
		open := 0
		if closer.canOpen {
			open = 1
		}
		mod := closer.orig % 3
		if closer.c == '~' {
			mod = closer.orig
		}
		ob := &openersBottom[slot(closer.c)][open][mod]
		var opener *delim
		for o := closer.prev; o != nil && o != bottom && o != *ob; o = o.prev {
			if o.c != closer.c || !o.canOpen {
				continue
			}
			if closer.c == '~' {
				if o.count == closer.count {
					opener = o
					break
				}
				continue
			}
			odd := (closer.canOpen || o.canClose) && closer.orig%3 != 0 && (o.orig+closer.orig)%3 == 0
			if !odd {
				opener = o
				break
			}
		}
		if opener == nil {
			*ob = closer.prev
			next := closer.next
			if !closer.canOpen {
				in.removeDelim(closer)
			}
			closer = next
			continue
		}
		use, kind := 1, iemph
		switch {
		case closer.c == '~':
			use, kind = closer.count, idel
		case closer.count >= 2 && opener.count >= 2:
			use, kind = 2, istrong
		}
		opener.count -= use
		closer.count -= use
		on, cn := opener.n, closer.n
		in.count()
		e := &node{kind: kind, text: on.text[len(on.text)-use:]}
		on.text = on.text[:len(on.text)-use]
		cn.text = cn.text[:len(cn.text)-use]
		if f := on.next; f != cn {
			e.first, e.last = f, cn.prev
			f.prev, cn.prev.next = nil, nil
		}
		on.next, e.prev, e.next, cn.prev = e, on, cn, e
		// The delimiters between opener and closer can match nothing now.
		for d := closer.prev; d != nil && d != opener; d = d.prev {
			in.removeDelim(d)
		}
		if opener.count == 0 {
			in.remove(on)
			in.removeDelim(opener)
		}
		if closer.count == 0 {
			next := closer.next
			in.remove(cn)
			in.removeDelim(closer)
			closer = next
		}
	}
	for in.delims != nil && in.delims != bottom {
		in.removeDelim(in.delims)
	}
}

// closeBracket handles a ] at i: it closes a link or an image when the
// last open bracket and what follows make one, and is text otherwise.
func (in *inliner) closeBracket(i int) int {
	s := in.s
	if len(in.brackets) == 0 {
		in.addText("]")
		return i + 1
	}
	br := in.brackets[len(in.brackets)-1]
	in.brackets = in.brackets[:len(in.brackets)-1]
	// A [ below the floor is inside a link already, and opens none.
	inactive := !br.image && len(in.brackets) < in.floor
	in.floor = min(in.floor, len(in.brackets))
	var ref *linkRef
	end, found := 0, false
	if !inactive {
		var dest, title string
		if dest, title, end, found = in.inlineLink(i + 1); found {
			ref = &linkRef{dest: dest, title: title}
		}
	}
	if !inactive && !found && len(in.r.refs) > 0 {
		// A full reference, [text][label]; a collapsed one, [label][];
		// or a shortcut, [label].
		label, after := s[br.pos:i], i+1
		if l, e, ok := scanLabel(s, i+1); ok {
			label, after = l, e
		} else if strings.HasPrefix(s[i+1:], "[]") {
			after = i + 3
		}
		if r, ok := in.r.lookup(label); ok {
			ref, end, found = r, after, true
		}
	}
	if !found {
		in.addText("]")
		return i + 1
	}
	in.emphasis(br.delim)
	in.count()
	l := &node{kind: ilink, link: ref}
	if br.image {
		l.kind = iimage
	}
	if f := br.n.next; f != nil {
		l.first, l.last = f, in.root.last
		f.prev = nil
	}
	l.prev = br.n.prev
	if l.prev != nil {
		l.prev.next = l
	} else {
		in.root.first = l
	}
	in.root.last = l
	if !br.image {
		in.floor = len(in.brackets) // no [ open now opens a link
	}
	return end
}

// lookup finds the definition that label names. A label of more than
// maxLabel characters names none. Every label looked up counts its bytes
// against maxLabelBytes, before it is measured or normalized, and once
// they are spent no lookup finds anything.
func (r *renderer) lookup(label string) (*linkRef, bool) {
	if len(label) > 4*maxLabel {
		return nil, false // more than maxLabel characters
	}
	if r.labels+len(label) > maxLabelBytes {
		r.labels = maxLabelBytes
		return nil, false
	}
	r.labels += len(label)
	if utf8.RuneCountInString(label) > maxLabel {
		return nil, false
	}
	ref, ok := r.refs[normalizeLabel(label)]
	return ref, ok
}

// inlineLink reads (destination "title") at i.
func (in *inliner) inlineLink(i int) (dest, title string, end int, ok bool) {
	s := in.s
	if i >= len(s) || s[i] != '(' {
		return "", "", 0, false
	}
	j := skipSpace(s, i+1, true)
	dest, k, ok := scanDest(s, j)
	if !ok {
		return "", "", 0, false
	}
	m := skipSpace(s, k, true)
	if m > k {
		if t, e, ok := scanTitle(s, m); ok {
			title, m = t, skipSpace(s, e, true)
		}
	}
	if m >= len(s) || s[m] != ')' {
		return "", "", 0, false
	}
	return dest, title, m + 1, true
}

// autolink reads <scheme:…> or <address@domain> at i.
func autolink(s string, i int) (*node, int) {
	j := i + 1
	k := j
	for k < len(s) && k-j < 33 && (isAlpha(s[k]) || k > j && (isDigit(s[k]) || s[k] == '+' || s[k] == '.' || s[k] == '-')) {
		k++
	}
	if k-j >= 2 && k < len(s) && s[k] == ':' {
		for e := k + 1; e < len(s); e++ {
			c := s[e]
			if c == '>' {
				u := s[j:e]
				return &node{kind: iautolink, text: u}, e + 1
			}
			if c <= ' ' || c == '<' || c == 0x7f {
				break
			}
		}
	}
	// An email address.
	at := -1
	for e := j; e < len(s); e++ {
		c := s[e]
		switch {
		case c == '@' && at < 0 && e > j:
			at = e
		case c == '>' && at > 0 && e > at+1:
			if validDomain(s[at+1 : e]) {
				a := s[j:e]
				return &node{kind: iautolink, text: a, mail: true}, e + 1
			}
			return nil, 0
		case at < 0 && (isAlpha(c) || isDigit(c) || strings.IndexByte(".!#$%&'*+/=?^_`{|}~-", c) >= 0):
		case at > 0 && (isAlpha(c) || isDigit(c) || c == '-' || c == '.'):
		default:
			return nil, 0
		}
	}
	return nil, 0
}

func validDomain(d string) bool {
	for _, part := range strings.Split(d, ".") {
		if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
	}
	return true
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// bareLink reads a bare http:// or https:// autolink at i, as GitHub
// does: after the line's start, a space or one of *, _, ~ and (, up to
// the next space or <, less trailing punctuation.
func bareLink(s string, i int) (*node, int) {
	if i > 0 && !(isSpace(rune(s[i-1])) || strings.IndexByte("*_~(", s[i-1]) >= 0) {
		return nil, 0
	}
	rest := s[i:]
	var n int
	switch {
	case strings.HasPrefix(rest, "http://"):
		n = 7
	case strings.HasPrefix(rest, "https://"):
		n = 8
	default:
		return nil, 0
	}
	if n >= len(rest) || !(isAlpha(rest[n]) || isDigit(rest[n])) {
		return nil, 0
	}
	e := n
	for e < len(rest) && rest[e] > ' ' && rest[e] != '<' && rest[e] != 0x7f {
		e++
	}
	// A ) at the end goes while the link holds more ) than (. The two
	// are counted once, and the count of ) kept as each goes.
	opens, closes := strings.Count(rest[:e], "("), strings.Count(rest[:e], ")")
	for e > n {
		c := rest[e-1]
		switch {
		case strings.IndexByte("?!.,:*_~'\"", c) >= 0:
			e--
			continue
		case c == ')' && closes > opens:
			e--
			closes--
			continue
		case c == ';':
			k := e - 2
			for k > n && (isAlpha(rest[k]) || isDigit(rest[k])) {
				k--
			}
			if k < e-2 && rest[k] == '&' {
				e = k
				continue
			}
		}
		break
	}
	u := rest[:e]
	return &node{kind: iautolink, text: u}, i + e
}

// inline renders s, and writes its text to plain when plain is not nil.
// Emphasis, strong emphasis, strikethrough and links nest at most
// MaxDepth deep: deeper, emphasis shows its delimiters as text, and a
// link, an image or an autolink its text alone.
func (r *renderer) inline(s string, plain *strings.Builder) {
	if r.stopped {
		return
	}
	t := strings.Trim(s, " \n")
	var root *node
	if !plainText(t) {
		root = r.parseInline(t)
	}
	if root == nil {
		// Text alone, such as most table cells, takes no parse, and
		// neither does text past MaxInlineNodes.
		r.text(t)
		if plain != nil {
			plain.WriteString(t)
		}
		return
	}
	type frame struct {
		n     *node
		close string
		// element is set when the frame opened an element.
		element bool
	}
	var stack []frame
	inLink, depth := 0, 0
	push := func(n *node, open, close string, element bool) {
		r.raw(open)
		stack = append(stack, frame{n, close, element})
		if element {
			depth++
		}
	}
	write := func(s string) {
		if plain != nil {
			plain.WriteString(s)
		}
	}
	n := root.first
	for n != nil || len(stack) > 0 {
		if r.done() {
			return
		}
		if n == nil {
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			r.raw(f.close)
			if f.element {
				depth--
				if f.n.kind == ilink {
					inLink--
				}
			}
			if !f.element && f.n.kind != ilink {
				write(f.n.text)
			}
			n = f.n.next
			continue
		}
		switch n.kind {
		case itext, icode:
			if n.kind == icode {
				r.raw("<code>")
			}
			r.text(n.text)
			if n.kind == icode {
				r.raw("</code>")
			}
			write(n.text)
			switch n.brk {
			case isoft:
				r.raw("\n")
				write(" ")
			case ihard:
				r.raw("<br>\n")
				write(" ")
			}
		case isoft:
			r.raw("\n")
			write(" ")
		case ihard:
			r.raw("<br>\n")
			write(" ")
		case iemph, istrong, idel:
			if depth < MaxDepth {
				tag := elements[n.kind]
				push(n, "<"+tag+">", "</"+tag+">", true)
			} else {
				d := escape(n.text)
				push(n, d, d, false)
				write(n.text)
			}
		case ilink:
			if inLink == 0 && depth < MaxDepth {
				if tag, ok := r.linkTag(n); ok {
					inLink++
					push(n, tag, "</a>", true)
					break
				}
			}
			push(n, "", "", false)
		case iimage:
			alt := altText(n)
			write(alt)
			tag, ok := "", false
			if inLink == 0 && depth < MaxDepth {
				tag, ok = r.linkTag(n)
			}
			if !ok {
				r.text(alt)
				break
			}
			if alt == "" {
				alt = n.link.dest
			}
			r.raw(tag)
			r.text(alt)
			r.raw("</a>")
		case iautolink:
			write(n.text)
			href, ok := "", false
			if inLink == 0 && depth < MaxDepth {
				href, ok = r.href(n.dest())
			}
			if ok {
				r.raw(`<a href="` + escape(href) + `">`)
				r.text(n.text)
				r.raw("</a>")
			} else {
				r.text(n.text)
			}
		}
		if len(stack) > 0 && stack[len(stack)-1].n == n {
			n = n.first
			continue
		}
		n = n.next
	}
}

// elements names the element of each kind that holds others.
var elements = [...]string{iemph: "em", istrong: "strong", idel: "del"}

// linkTag is the start tag of a link or an image to n's destination, or
// ok false for a destination that renders as text. It is made once for
// each definition, however many links use it.
func (r *renderer) linkTag(n *node) (string, bool) {
	l := n.link
	if !l.made {
		l.made = true
		href, ok := r.href(l.dest)
		if !ok {
			return "", false
		}
		l.tag, l.ok = `<a href="`+escape(href)+`"`, true
		if l.title != "" {
			l.tag += ` title="` + escape(l.title) + `"`
		}
		l.tag += ">"
	}
	return l.tag, l.ok
}

// plainText reports whether s holds no byte that may start markup.
func plainText(s string) bool {
	for i := 0; i < len(s); i++ {
		if special[s[i]] {
			return false
		}
	}
	return true
}

// altText is the text of n's children, as an image's alt text.
func altText(n *node) string {
	var b strings.Builder
	var stack []*node
	c := n.first
	for c != nil || len(stack) > 0 {
		if c == nil {
			c = stack[len(stack)-1].next
			stack = stack[:len(stack)-1]
			continue
		}
		switch c.kind {
		case itext, icode, iautolink:
			b.WriteString(c.text)
		case isoft, ihard:
			b.WriteByte(' ')
		}
		if c.brk != 0 {
			b.WriteByte(' ')
		}
		if c.first != nil {
			stack = append(stack, c)
			c = c.first
			continue
		}
		c = c.next
	}
	return b.String()
}

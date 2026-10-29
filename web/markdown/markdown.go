// Package markdown renders Markdown as HTML that is safe to show on the
// edge's origin. Render is a pure function from bytes to HTML: it knows
// nothing of the server, of git or of URLs beyond the resolver it is
// given.
//
// # Safety
//
// The renderer writes only its own fixed set of elements and attributes:
// h1 to h6 with id, p, br, em, strong, del, code, pre, blockquote, ul, ol
// with start, li, hr, table, thead, tbody, tr, th and td with an
// alignment class, and a with href, title and class. It escapes every
// byte of text and every attribute value it takes from the input. Raw
// HTML in the input, inline or block, is shown as escaped text. So no
// input byte reaches the output unescaped, and no input can add an
// element, an attribute, a script, a style or an event handler.
//
// A link's href is one of three kinds. An http or https destination that
// names a host, or a mailto destination, is written as given,
// percent-encoded where a URL needs it. A relative or fragment-only
// destination is what the resolver makes of it. Any other destination,
// such as javascript:, data:, http:x, https:// or one that holds a
// backslash anywhere, written as one or decoded from an escape or an
// entity, or one the resolver refuses, renders as the link's text with no
// a element. An image renders as a link to its destination, with its alt
// text, and never as an img element.
//
// # What it reads
//
// Render follows CommonMark's rules for the constructs it supports: ATX
// and setext headings, paragraphs, hard line breaks, emphasis, strong
// emphasis, inline code, fenced and indented code blocks, block quotes,
// ordered and unordered lists, thematic breaks, inline and reference
// links, autolinks, images, backslash escapes and entity references. It
// supports GitHub's strikethrough, pipe tables, task list items and bare
// http(s) autolinks too. Anything else, such as footnotes, math or HTML,
// renders as text.
//
// # Bounds
//
// The input is at most MaxInput bytes. Rendering takes time near-linear
// in the input, whatever it holds: no regular expression is used, and
// every scan that fails is remembered or bounded. Block quotes and list
// items nest at most MaxDepth deep, beyond which their markers render as
// text. Emphasis, strong emphasis, strikethrough, links, images and
// autolinks nest at most MaxDepth deep within a block, beyond which
// emphasis's delimiters render as text, and a link, an image or an
// autolink as its text alone. A table has at most MaxTableColumns
// columns, and a document's tables at most MaxTableCells cells between
// them; a table's rows past that render as a paragraph.
//
// The structures Render builds are bounded too, so that one render of a
// MaxInput document allocates less than 128 MiB in all, when
// Options.MaxOutput is set, as the server always sets it. A document holds
// at most MaxBlocks blocks, quotes, lists and items among them. A line
// that would add a block past that, less one kept for the rest of the
// input, adds none, and it and the input after it render as one block of
// preformatted text. Its inline content takes at most MaxInlineNodes
// nodes between all its blocks: the block that passes MaxInlineNodes, and
// every block after it, renders its text alone, escaped. Within a block
// at most 4,096 [ or ![ wait for their ], and a [ past that is text.
//
// A link label holds at most 999 characters. A render looks up at most
// 4 MiB of labels as references, so that the copies made to match them
// are bounded too, and a reference past that renders as text. A document
// with no reference definitions looks up none.
//
// The output is at most Options.MaxOutput bytes. Render stops all work
// once the output would pass it, and returns the document up to the last
// block that fit, with the blocks that hold it closed. A link reference
// makes its destination's href once, however often it is used, so each
// use costs only the output it writes. Render stops too once its context
// is done.
package markdown

import (
	"bytes"
	"context"
	"errors"
	"html"
	"html/template"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxInput is the largest input Render reads.
	MaxInput = 1 << 20
	// MaxDepth is how deep block quotes and list items nest, and how
	// deep emphasis, strikethrough and links nest within a block.
	MaxDepth = 32
	// MaxTableColumns is the most columns a table has. A header with
	// more is no table.
	MaxTableColumns = 64
	// MaxTableCells is the most cells a document's tables hold between
	// them, each row counted at its table's columns.
	MaxTableCells = 100_000
	// maxLabel is the most characters a link label holds, as CommonMark
	// has it. A longer one is no label.
	maxLabel = 999
	// maxLabelBytes is the most bytes of labels a render looks up as
	// references. Past it, a reference renders as text.
	maxLabelBytes = 4 << 20
	// maxParens is how deeply parentheses nest in a link destination.
	maxParens = 32
	// MaxBlocks is the most blocks a document holds: headings,
	// paragraphs, quotes, lists, items, code blocks, rules and tables.
	MaxBlocks = 1 << 16
	// MaxInlineNodes is the most nodes the inline content of a
	// document's blocks takes between them: one for each run of text,
	// code span, line break, delimiter run, bracket and autolink, and
	// one for each emphasis or link they make.
	MaxInlineNodes = 1 << 18
	// maxBrackets is the most [ and ![ that wait for their ] at once
	// within a block.
	maxBrackets = 4096
)

// ErrTooLong is Render's error for an input longer than MaxInput.
var ErrTooLong = errors.New("markdown: the input is longer than MaxInput")

// Options are what Render is given besides its input.
type Options struct {
	// Resolve gives the href of a relative or fragment-only destination,
	// or ok false to refuse it. A nil Resolve refuses every one.
	Resolve func(dest string) (href string, ok bool)
	// MaxOutput bounds the HTML Render writes. Zero means no bound.
	MaxOutput int
}

// Heading is one heading of a document.
type Heading struct {
	Level int
	Text  string
	// ID is the heading's id, empty for a heading whose text gives none.
	ID string
}

// Doc is a rendered document.
type Doc struct {
	HTML     template.HTML
	Headings []Heading
	// Truncated is set when the document is larger than MaxOutput, and
	// HTML and Headings hold its start: the blocks that fit, whole.
	Truncated bool
}

// Render renders src. Invalid UTF-8 and NUL bytes read as U+FFFD. It
// fails with ctx's error once ctx is done.
func Render(ctx context.Context, src []byte, opt Options) (Doc, error) {
	d, _, err := renderDoc(ctx, src, opt)
	return d, err
}

// usage is how much of each bounded structure a render took: blocks,
// inline nodes, the most brackets open at once in a block, and the bytes
// of labels looked up.
type usage struct{ blocks, nodes, brackets, labels int }

func renderDoc(ctx context.Context, src []byte, opt Options) (Doc, usage, error) {
	if len(src) > MaxInput {
		return Doc{}, usage{}, ErrTooLong
	}
	text := strings.ToValidUTF8(string(src), "�")
	text = strings.ReplaceAll(text, "\x00", "�")
	p := &parser{ctx: ctx, refs: map[string]*linkRef{}}
	root := p.parse(text)
	if err := ctx.Err(); err != nil {
		return Doc{}, usage{}, err
	}
	r := &renderer{ctx: ctx, opt: opt, refs: p.refs, slugs: map[string]int{}}
	r.block(root, false)
	if r.err != nil {
		return Doc{}, usage{}, r.err
	}
	u := usage{blocks: p.blocks, nodes: r.nodes, brackets: r.brackets, labels: r.labels}
	return Doc{HTML: template.HTML(r.out.String()), Headings: r.headings, Truncated: r.truncated}, u, nil
}

// Slug is GitHub's id for a heading's text: the text in lower case, less
// every character that is not a letter, a digit, a space, a hyphen or an
// underscore, with each space made a hyphen. A document numbers a slug it
// has given before.
func Slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Block structure.

type kind int

const (
	document kind = iota
	quote
	list
	item
	paragraph
	heading
	fenced
	indented
	rule
	table
)

type block struct {
	kind     kind
	parent   *block
	children []*block
	open     bool
	lines    []string
	// lastBlank is set when the block's last line was blank.
	lastBlank, checked bool

	level int // a heading's level

	// A list's or item's marker: its character, an ordered list's start,
	// and an item's content column.
	ordered bool
	marker  byte
	start   int
	indent  int
	tight   bool
	// emptyStart is set on an item whose first line held its marker alone.
	emptyStart bool
	startLine  int

	// A fenced block's fence and info word, and the indent it strips.
	fence     string
	info      string
	fenceCols int

	// A table's alignment by column, and its rows of cells.
	align []byte
	rows  [][]string
}

// linkRef is a link reference definition: its destination and title,
// and the start tag of a link to it once a use has made it, or ok false
// once one has found it renders as text.
type linkRef struct {
	dest, title string
	tag         string
	made, ok    bool
}

type parser struct {
	ctx    context.Context
	refs   map[string]*linkRef
	root   *block
	tip    *block
	lineNo int
	// The line being read, the byte the parser is at, and whether the
	// rest of the line is blank.
	line   string
	pos    int
	blank  bool
	indent int
	next   int // the first non-space byte at or after pos
	// cells is how many cells the document's tables hold so far, and
	// blocks how many blocks it holds. full is set once a line would add
	// a block past MaxBlocks less one, the one kept for the input from
	// that line on, and added the blocks the line has added so far.
	cells, blocks int
	full          bool
	added         []*block
}

func (p *parser) parse(text string) *block {
	p.root = &block{kind: document, open: true}
	p.tip = p.root
	rest := ""
	for len(text) > 0 {
		at := text
		end, skip := len(text), 0
		if i := strings.IndexAny(text, "\r\n"); i >= 0 {
			end, skip = i, 1
			if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
				skip = 2
			}
		}
		line := text[:end]
		text = text[end+skip:]
		p.lineNo++
		if p.lineNo%1024 == 0 && p.ctx.Err() != nil {
			return p.root
		}
		p.added = p.added[:0]
		p.addLine(expandTabs(line))
		if p.full {
			// The line takes back the blocks it added, and it and the
			// input after it are preformatted text.
			for i := len(p.added) - 1; i >= 0; i-- {
				b := p.added[i]
				if ch := b.parent.children; len(ch) > 0 && ch[len(ch)-1] == b {
					b.parent.children = ch[:len(ch)-1]
					p.blocks--
				}
			}
			rest = strings.TrimRight(at, "\r\n")
			break
		}
	}
	for p.tip != nil {
		p.close(p.tip)
	}
	if rest != "" {
		p.root.children = append(p.root.children, &block{kind: indented, parent: p.root, lines: []string{rest}})
		p.blocks++
	}
	return p.root
}

// expandTabs is line with each tab made the spaces to the next tab stop
// of four columns.
func expandTabs(line string) string {
	if strings.IndexByte(line, '\t') < 0 {
		return line
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			n := 4 - col%4
			b.WriteString("    "[:n])
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

func (p *parser) findNext() {
	i := p.pos
	for i < len(p.line) && p.line[i] == ' ' {
		i++
	}
	p.next, p.indent, p.blank = i, i-p.pos, i == len(p.line)
}

func (p *parser) advance(n int) { p.pos += n; p.findNext() }

// consume ends the line: a leaf took the rest of it.
func (p *parser) consume() { p.pos = len(p.line); p.findNext() }

// depth is how many quotes and items hold b.
func depth(b *block) int {
	n := 0
	for ; b != nil; b = b.parent {
		if b.kind == quote || b.kind == item {
			n++
		}
	}
	return n
}

func (p *parser) addLine(line string) {
	p.line, p.pos = line, 0
	p.findNext()
	// Match the open containers.
	c := p.root
	allMatched := true
	for {
		last := lastOpen(c)
		if last == nil {
			break
		}
		ok, done := p.continues(last)
		if done {
			return // a closing fence: the line is consumed
		}
		if !ok {
			allMatched = false
			break
		}
		c = last
	}
	unmatchedFrom := c // the deepest container matched

	// Open new blocks, unless c takes lines as they are. Every leaf a
	// line opens consumes the line.
	startedNew := false
	for c.kind != fenced && c.kind != indented && !p.blank {
		if p.indent >= 4 {
			if p.tip.kind == paragraph || p.tip.kind == table {
				break // indented code interrupts no paragraph
			}
			p.advance(4)
			p.closeUnmatched(unmatchedFrom, &allMatched)
			c = p.add(c, &block{kind: indented})
			if p.full {
				return
			}
			startedNew = true
			break
		}
		nb, ok := p.start(c, unmatchedFrom, &allMatched)
		if p.full {
			return
		}
		if !ok {
			break
		}
		startedNew, c = true, nb
		if c.kind != quote && c.kind != item {
			for b := c; b != nil; b = b.parent {
				b.lastBlank = false
			}
			return
		}
	}

	// The rest of the line is text.
	if !startedNew && !allMatched && p.tip.kind == paragraph && !p.blank {
		p.tip.lines = appendLine(p.tip.lines, p.line[p.next:]) // a lazy continuation
		return
	}
	p.closeUnmatched(unmatchedFrom, &allMatched)
	if p.blank && len(c.children) > 0 {
		c.children[len(c.children)-1].lastBlank = true
	}
	lb := p.blank && !(c.kind == quote || c.kind == fenced ||
		c.kind == item && len(c.children) == 0 && c.startLine == p.lineNo)
	for b := c; b != nil; b = b.parent {
		b.lastBlank = lb
	}
	switch c.kind {
	case fenced:
		n := 0
		for n < c.fenceCols && p.pos+n < len(p.line) && p.line[p.pos+n] == ' ' {
			n++
		}
		c.lines = appendLine(c.lines, p.line[p.pos+n:])
	case indented:
		if p.blank {
			if p.pos+4 <= len(p.line) {
				c.lines = appendLine(c.lines, p.line[p.pos+4:])
			} else {
				c.lines = appendLine(c.lines, "")
			}
		} else {
			c.lines = appendLine(c.lines, p.line[p.pos:])
		}
	case paragraph:
		c.lines = appendLine(c.lines, p.line[p.next:])
	case table:
		if p.cells+len(c.align) > MaxTableCells {
			// The document's tables are full: the rest of this one is a
			// paragraph.
			p.close(c)
			np := p.add(c.parent, &block{kind: paragraph})
			np.lines = append(np.lines, p.line[p.next:])
			break
		}
		p.cells += len(c.align)
		c.rows = append(c.rows, splitRow(p.line[p.next:], len(c.align)))
	default:
		if !p.blank {
			np := p.add(c, &block{kind: paragraph})
			np.lines = append(np.lines, p.line[p.next:])
		}
	}
}

// appendLine appends l to a block's lines, doubling their capacity as
// they grow, so a block of n lines allocates room for at most 4n.
func appendLine(lines []string, l string) []string {
	if len(lines) == cap(lines) {
		lines = append(make([]string, 0, max(4, 2*cap(lines))), lines...)
	}
	return append(lines, l)
}

// lastOpen is c's last child when it is open.
func lastOpen(c *block) *block {
	if n := len(c.children); n > 0 && c.children[n-1].open {
		return c.children[n-1]
	}
	return nil
}

// continues matches the start of the line to b, an open block, and
// consumes what b's continuation takes. done is set when the line closed
// b and holds nothing else.
func (p *parser) continues(b *block) (ok, done bool) {
	switch b.kind {
	case quote:
		if !p.blank && p.indent < 4 && p.line[p.next] == '>' {
			p.advance(p.next - p.pos + 1)
			if p.pos < len(p.line) && p.line[p.pos] == ' ' {
				p.advance(1)
			}
			return true, false
		}
		return false, false
	case item:
		if p.blank {
			if len(b.children) == 0 {
				return false, false
			}
			p.advance(p.next - p.pos)
			return true, false
		}
		if p.indent >= b.indent {
			p.advance(b.indent)
			return true, false
		}
		return false, false
	case list:
		return true, false
	case fenced:
		if p.indent < 4 && !p.blank {
			rest := p.line[p.next:]
			n := run(rest, b.fence[0])
			if n >= len(b.fence) && strings.TrimRight(rest[n:], " ") == "" {
				p.close(b)
				return false, true
			}
		}
		return true, false
	case indented:
		if p.indent >= 4 {
			p.advance(4)
			return true, false
		}
		if p.blank {
			return true, false
		}
		return false, false
	case paragraph, table:
		return !p.blank, false
	}
	return false, false
}

// closeUnmatched closes the open blocks below from, once a line has
// opened something new or ended the lazy continuation.
func (p *parser) closeUnmatched(from *block, allMatched *bool) {
	if *allMatched {
		return
	}
	for p.tip != from {
		p.close(p.tip)
	}
	*allMatched = true
}

// room reports whether the document has room for another block, with
// one kept for the input past MaxBlocks. When it has none, it sets full.
func (p *parser) room() bool {
	if p.blocks >= MaxBlocks-1 {
		p.full = true
	}
	return !p.full
}

// add adds b as the last child of c, closing what c cannot hold. When the
// document has no room, it adds nothing and gives b, in no tree.
func (p *parser) add(c *block, b *block) *block {
	if !p.room() {
		return b
	}
	for !canHold(c.kind, b.kind) {
		p.close(c)
		c = c.parent
	}
	b.parent, b.open, b.startLine = c, true, p.lineNo
	c.children = append(c.children, b)
	p.blocks++
	p.added = append(p.added, b)
	p.tip = b
	return b
}

func canHold(parent, child kind) bool {
	switch parent {
	case document, quote, item:
		return child != item
	case list:
		return child == item
	}
	return false
}

func (p *parser) close(b *block) {
	if !b.open {
		return
	}
	// Close the blocks b holds first.
	for p.tip != b && p.tip != nil {
		p.close(p.tip)
	}
	b.open = false
	p.tip = b.parent
	switch b.kind {
	case paragraph:
		p.definitions(b)
	case indented:
		for len(b.lines) > 0 && strings.TrimLeft(b.lines[len(b.lines)-1], " ") == "" {
			b.lines = b.lines[:len(b.lines)-1]
		}
	case list:
		b.tight = true
		for i, it := range b.children {
			last := i == len(b.children)-1
			if endsBlank(it) && !last {
				b.tight = false
				break
			}
			for j, sub := range it.children {
				if endsBlank(sub) && (!last || j < len(it.children)-1) {
					b.tight = false
					break
				}
			}
			if !b.tight {
				break
			}
		}
	}
	if b.kind == paragraph && len(b.lines) == 0 && b.parent != nil {
		ch := b.parent.children
		if ch[len(ch)-1] == b {
			b.parent.children = ch[:len(ch)-1]
		}
	}
}

func endsBlank(b *block) bool {
	for b != nil {
		if b.lastBlank {
			return true
		}
		if !b.checked && (b.kind == list || b.kind == item) && len(b.children) > 0 {
			b.checked = true
			b = b.children[len(b.children)-1]
			continue
		}
		b.checked = true
		break
	}
	return false
}

// start tries to open a block at the parser's position, in container c.
func (p *parser) start(c, unmatchedFrom *block, allMatched *bool) (*block, bool) {
	if p.blank || p.indent >= 4 {
		return nil, false
	}
	rest := p.line[p.next:]
	ch := rest[0]
	nested := depth(c) < MaxDepth
	switch {
	case ch == '>' && nested:
		p.advance(p.next - p.pos + 1)
		if p.pos < len(p.line) && p.line[p.pos] == ' ' {
			p.advance(1)
		}
		p.closeUnmatched(unmatchedFrom, allMatched)
		return p.add(c, &block{kind: quote}), true
	case ch == '#':
		if level, text, ok := atx(rest); ok {
			p.closeUnmatched(unmatchedFrom, allMatched)
			b := p.add(c, &block{kind: heading, level: level})
			b.lines = []string{text}
			p.consume()
			p.close(b)
			return b, true
		}
	case ch == '`' || ch == '~':
		n := run(rest, ch)
		info := rest[n:]
		if n >= 3 && !(ch == '`' && strings.IndexByte(info, '`') >= 0) {
			p.closeUnmatched(unmatchedFrom, allMatched)
			b := p.add(c, &block{kind: fenced, fence: rest[:n], fenceCols: p.indent})
			if f := strings.Fields(info); len(f) > 0 {
				b.info = unescapeText(f[0])
			}
			p.consume()
			return b, true
		}
	}
	// A paragraph's last line may turn out to be a setext heading or a
	// table's header. A table has at most MaxTableColumns columns, and
	// starts only while the document's tables have room for its header.
	if c.kind == paragraph {
		if align, ok := delimiterRow(rest); ok && len(c.lines) > 0 &&
			len(align) <= MaxTableColumns && p.cells+len(align) <= MaxTableCells {
			header := c.lines[len(c.lines)-1]
			if cells := splitRow(header, -1); len(cells) == len(align) {
				if !p.room() {
					return nil, false // before the header leaves the paragraph
				}
				p.closeUnmatched(unmatchedFrom, allMatched)
				c.lines = c.lines[:len(c.lines)-1]
				parent := c.parent
				p.close(c)
				t := p.add(parent, &block{kind: table, align: align})
				t.rows = [][]string{cells}
				p.cells += len(align)
				p.consume()
				return t, true
			}
		}
		if ch == '=' || ch == '-' {
			n := run(rest, ch)
			if strings.TrimRight(rest[n:], " ") == "" {
				p.closeUnmatched(unmatchedFrom, allMatched)
				level := 1
				if ch == '-' {
					level = 2
				}
				p.definitions(c)
				if len(c.lines) > 0 {
					c.kind, c.level = heading, level
					c.lines = []string{strings.Join(c.lines, "\n")}
					p.close(c)
					p.consume()
					return c, true
				}
			}
		}
	}
	if thematic(rest) {
		p.closeUnmatched(unmatchedFrom, allMatched)
		b := p.add(c, &block{kind: rule})
		p.consume()
		p.close(b)
		return b, true
	}
	if !nested {
		return nil, false
	}
	return p.listItem(c, unmatchedFrom, allMatched)
}

func (p *parser) listItem(c, unmatchedFrom *block, allMatched *bool) (*block, bool) {
	rest := p.line[p.next:]
	var marker byte
	ordered, start, n := false, 0, 0
	switch rest[0] {
	case '-', '+', '*':
		marker, n = rest[0], 1
	default:
		for n < len(rest) && n < 9 && rest[n] >= '0' && rest[n] <= '9' {
			n++
		}
		if n == 0 || n >= len(rest) || rest[n] != '.' && rest[n] != ')' {
			return nil, false
		}
		start, _ = strconv.Atoi(rest[:n])
		ordered, marker = true, rest[n]
		n++
	}
	if n < len(rest) && rest[n] != ' ' {
		return nil, false
	}
	after := rest[n:]
	empty := strings.TrimLeft(after, " ") == ""
	if c.kind == paragraph && (empty || ordered && start != 1) {
		return nil, false // neither interrupts a paragraph
	}
	spaces := len(after) - len(strings.TrimLeft(after, " "))
	width := n + spaces
	if empty || spaces >= 5 {
		width = n + 1
	}
	indent := p.indent + width
	p.closeUnmatched(unmatchedFrom, allMatched)
	p.advance(p.next - p.pos + min(width, len(rest)))
	if c.kind != list || c.ordered != ordered || c.marker != marker {
		c = p.add(c, &block{kind: list, ordered: ordered, marker: marker, start: start})
	}
	it := p.add(c, &block{kind: item, indent: indent, emptyStart: empty})
	return it, true
}

// run is how many bytes of c s starts with.
func run(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// atx reads an ATX heading: one to six #, then a space or the line's end.
func atx(s string) (level int, text string, ok bool) {
	level = run(s, '#')
	if level > 6 || level < len(s) && s[level] != ' ' {
		return 0, "", false
	}
	text = strings.Trim(s[level:], " ")
	// A closing sequence of # goes, when a space precedes it.
	if t := strings.TrimRight(text, "#"); len(t) < len(text) {
		if t == "" {
			text = ""
		} else if strings.HasSuffix(t, " ") {
			text = strings.TrimRight(t, " ")
		}
	}
	return level, text, true
}

// thematic reads a thematic break: three or more of *, - or _, alone with
// spaces.
func thematic(s string) bool {
	c, n := s[0], 0
	if c != '*' && c != '-' && c != '_' {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case c:
			n++
		case ' ':
		default:
			return false
		}
	}
	return n >= 3
}

// delimiterRow reads a table's delimiter row: cells of hyphens, each with
// an optional colon either side, between pipes. It holds a pipe, so a
// line of hyphens alone stays a setext underline or a break. A row of
// more than MaxTableColumns cells is none, and is not split.
func delimiterRow(s string) (align []byte, ok bool) {
	if n := strings.Count(s, "|"); n == 0 || n > MaxTableColumns+1 {
		return nil, false
	}
	for _, cell := range splitRow(s, -1) {
		cell = strings.TrimSpace(cell)
		left, right := strings.HasPrefix(cell, ":"), strings.HasSuffix(cell, ":")
		dashes := strings.TrimSuffix(strings.TrimPrefix(cell, ":"), ":")
		if dashes == "" || strings.Trim(dashes, "-") != "" {
			return nil, false
		}
		a := byte(0)
		switch {
		case left && right:
			a = 'c'
		case left:
			a = 'l'
		case right:
			a = 'r'
		}
		align = append(align, a)
	}
	return align, len(align) > 0
}

// splitRow is a table row's cells, split at each pipe no backslash
// escapes, less the pipes that open and close the row. A row of a table
// with n columns has n cells, the missing ones empty. n < 0 keeps them
// all.
func splitRow(s string, n int) []string {
	s = strings.Trim(s, " ")
	if strings.HasPrefix(s, "|") {
		s = s[1:]
	}
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '|':
			cells = append(cells, s[start:i])
			start = i + 1
		}
		if n >= 0 && len(cells) > n {
			break
		}
	}
	cells = append(cells, s[start:])
	for i := range cells {
		cells[i] = strings.ReplaceAll(strings.TrimSpace(cells[i]), `\|`, "|")
	}
	if n >= 0 {
		for len(cells) < n {
			cells = append(cells, "")
		}
		cells = cells[:n]
	}
	return cells
}

// definitions takes the link reference definitions that open a
// paragraph out of it.
func (p *parser) definitions(b *block) {
	text := strings.Join(b.lines, "\n")
	took := false
	for strings.HasPrefix(text, "[") {
		label, dest, title, n, ok := definition(text)
		if !ok {
			break
		}
		if key := normalizeLabel(label); key != "" {
			if _, seen := p.refs[key]; !seen {
				p.refs[key] = &linkRef{dest: dest, title: title}
			}
		}
		text, took = text[n:], true
	}
	if took {
		if strings.TrimSpace(text) == "" {
			b.lines = nil
		} else {
			b.lines = strings.Split(text, "\n")
		}
	}
}

// definition reads one link reference definition from the start of s.
func definition(s string) (label, dest, title string, n int, ok bool) {
	label, i, ok := scanLabel(s, 0)
	if !ok || i >= len(s) || s[i] != ':' || strings.HasPrefix(label, "^") {
		return "", "", "", 0, false
	}
	i = skipSpace(s, i+1, true)
	dest, j, ok := scanDest(s, i)
	if !ok || j == i && !strings.HasPrefix(s[i:], "<>") {
		return "", "", "", 0, false
	}
	// A title, separated from the destination by whitespace, and then
	// nothing else on its line.
	k := skipSpace(s, j, true)
	if k > j {
		if t, e, ok := scanTitle(s, k); ok {
			if end := lineEnd(s, e); end >= 0 {
				return label, dest, t, end, true
			}
		}
	}
	if end := lineEnd(s, j); end >= 0 {
		return label, dest, "", end, true
	}
	return "", "", "", 0, false
}

// lineEnd is where the line after i ends, past its line ending, when the
// rest of it is blank, and -1 when it is not.
func lineEnd(s string, i int) int {
	for i < len(s) && s[i] == ' ' {
		i++
	}
	switch {
	case i == len(s):
		return i
	case s[i] == '\n':
		return i + 1
	}
	return -1
}

// skipSpace skips spaces, and at most one line ending when newline is set.
func skipSpace(s string, i int, newline bool) int {
	for i < len(s) && s[i] == ' ' {
		i++
	}
	if newline && i < len(s) && s[i] == '\n' {
		i++
		for i < len(s) && s[i] == ' ' {
			i++
		}
	}
	return i
}

// scanLabel reads a link label, [ to ], from s[i]: at most maxLabel
// characters, with no unescaped bracket inside and something not blank.
func scanLabel(s string, i int) (label string, end int, ok bool) {
	if i >= len(s) || s[i] != '[' {
		return "", 0, false
	}
	// A character takes at most 4 bytes.
	for j := i + 1; j < len(s) && j-i-1 <= 4*maxLabel; j++ {
		switch s[j] {
		case '\\':
			j++
		case '[':
			return "", 0, false
		case ']':
			label = s[i+1 : j]
			return label, j + 1, strings.TrimSpace(label) != "" && utf8.RuneCountInString(label) <= maxLabel
		}
	}
	return "", 0, false
}

// normalizeLabel is the key a label matches by: case folded, with its
// whitespace collapsed. It copies s at most three times, whatever s holds.
func normalizeLabel(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, c := range s {
		if unicode.IsSpace(c) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(c)
	}
	return strings.ToLower(strings.ToUpper(b.String()))
}

// scanDest reads a link destination from s[i]: <…> on one line, or text
// with no space or control character and balanced parentheses.
func scanDest(s string, i int) (dest string, end int, ok bool) {
	if i < len(s) && s[i] == '<' {
		for j := i + 1; j < len(s); j++ {
			switch s[j] {
			case '\\':
				j++
			case '\n', '<':
				return "", 0, false
			case '>':
				return unescapeText(s[i+1 : j]), j + 1, true
			}
		}
		return "", 0, false
	}
	depth, j := 0, i
	for ; j < len(s); j++ {
		c := s[j]
		if c == '\\' && j+1 < len(s) && isPunct(s[j+1]) {
			j++
			continue
		}
		if c <= ' ' || c == 0x7f {
			break
		}
		if c == '(' {
			if depth++; depth > maxParens {
				return "", 0, false
			}
		}
		if c == ')' {
			if depth == 0 {
				break
			}
			depth--
		}
	}
	if depth != 0 {
		return "", 0, false
	}
	return unescapeText(s[i:j]), j, true
}

// scanTitle reads a link title from s[i]: in double or single quotes, or
// in parentheses, with no blank line inside.
func scanTitle(s string, i int) (title string, end int, ok bool) {
	if i >= len(s) {
		return "", 0, false
	}
	open := s[i]
	closer := open
	switch open {
	case '"', '\'':
	case '(':
		closer = ')'
	default:
		return "", 0, false
	}
	for j := i + 1; j < len(s); j++ {
		switch c := s[j]; {
		case c == '\\':
			j++
		case c == closer:
			return unescapeText(s[i+1 : j]), j + 1, true
		case c == '(' && open == '(':
			return "", 0, false
		case c == '\n' && j+1 < len(s) && strings.TrimLeft(s[j+1:lineEndAt(s, j+1)], " ") == "":
			return "", 0, false
		}
	}
	return "", 0, false
}

func lineEndAt(s string, i int) int {
	if k := strings.IndexByte(s[i:], '\n'); k >= 0 {
		return i + k
	}
	return len(s)
}

func isPunct(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

// unescapeText decodes s's backslash escapes and entity references.
func unescapeText(s string) string {
	if strings.IndexAny(s, `\&`) < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s) && isPunct(s[i+1]):
			b.WriteByte(s[i+1])
			i++
		case c == '&':
			if text, n := entity(s[i:]); n > 0 {
				b.WriteString(text)
				i += n - 1
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// entity reads an entity reference at the start of s, and gives what it
// stands for and its length. n is 0 when s starts with none.
func entity(s string) (text string, n int) {
	if len(s) < 3 || s[0] != '&' {
		return "", 0
	}
	end := strings.IndexByte(s[:min(len(s), 40)], ';')
	if end < 2 {
		return "", 0
	}
	name := s[1:end]
	if name[0] == '#' {
		digits, base := name[1:], 10
		if len(digits) > 0 && (digits[0] == 'x' || digits[0] == 'X') {
			digits, base = digits[1:], 16
		}
		if len(digits) == 0 || base == 10 && len(digits) > 7 || base == 16 && len(digits) > 6 {
			return "", 0
		}
		v, err := strconv.ParseUint(digits, base, 32)
		if err != nil {
			return "", 0
		}
		r := rune(v)
		if r == 0 || !utf8.ValidRune(r) {
			r = utf8.RuneError
		}
		return string(r), end + 1
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return "", 0
		}
	}
	// html knows every named entity. A name it decodes only in part,
	// as a legacy name followed by more, leaves at least two more
	// characters, and a whole one stands for one or two.
	ref := s[:end+1]
	if d := html.UnescapeString(ref); d != ref && utf8.RuneCountInString(d) <= 2 {
		return d, end + 1
	}
	return "", 0
}

// Rendering.

type renderer struct {
	ctx      context.Context
	opt      Options
	refs     map[string]*linkRef
	out      bytes.Buffer
	headings []Heading
	slugs    map[string]int
	// open closes the blocks open now, innermost first. mark is where the
	// output ended when the last block ended, and marked and headings
	// what was open and how many headings there were then.
	open     *closer
	mark     int
	marked   *closer
	headingN int
	// stopped is set once the output would pass MaxOutput, when
	// truncated is set too, or once ctx is done, when err is its error.
	// Nothing more is parsed or written once it is set.
	stopped, truncated bool
	err                error
	steps              int
	// nodes is how many inline nodes the document's blocks have taken,
	// and brackets the most that waited for their ] at once. full is set
	// once nodes passes MaxInlineNodes: from then on, a block's inline
	// content renders as its text alone.
	nodes, brackets int
	full            bool
	// labels is how many bytes of labels the document's references have
	// looked up, at most maxLabelBytes.
	labels int
}

// closer is the end tag of an open block, and n the bytes of it and of
// every closer after it.
type closer struct {
	tags string
	n    int
	next *closer
}

func (c *closer) size() int {
	if c == nil {
		return 0
	}
	return c.n
}

// fits reports whether n more bytes of output fit within MaxOutput, with
// room left to close the blocks open now. When they do not, it truncates
// the output.
func (r *renderer) fits(n int) bool {
	if r.stopped {
		return false
	}
	if r.opt.MaxOutput > 0 && r.out.Len()+n+r.open.size() > r.opt.MaxOutput {
		r.truncate()
		return false
	}
	return true
}

// truncate cuts the output back to the last block that ended, closes
// the blocks open then, and stops rendering.
func (r *renderer) truncate() {
	r.stopped, r.truncated = true, true
	r.out.Truncate(r.mark)
	for c := r.marked; c != nil; c = c.next {
		r.out.WriteString(c.tags)
	}
	r.headings = r.headings[:r.headingN]
}

// done stops rendering, and is true, once ctx is done. It looks at ctx
// once in every 256 calls.
func (r *renderer) done() bool {
	if r.stopped {
		return true
	}
	if r.steps++; r.steps%256 == 0 && r.ctx.Err() != nil {
		r.stopped, r.err = true, r.ctx.Err()
	}
	return r.stopped
}

func (r *renderer) raw(s string) {
	if r.fits(len(s)) {
		r.out.WriteString(s)
	}
}

// text writes s escaped. Escaping makes nothing shorter, so s that does
// not fit is never escaped.
func (r *renderer) text(s string) {
	if r.fits(len(s)) {
		r.raw(escape(s))
	}
}

func escape(s string) string { return html.EscapeString(s) }

// enter writes a container's start tag, and holds its end tag until
// leave writes it.
func (r *renderer) enter(start, end string) {
	if r.fits(len(start) + len(end)) {
		r.out.WriteString(start)
		r.open = &closer{tags: end, n: len(end) + r.open.size(), next: r.open}
	}
}

func (r *renderer) leave() {
	if r.stopped {
		return
	}
	c := r.open
	r.open = c.next
	r.out.WriteString(c.tags) // counted since enter
	r.ended()
}

// ended marks a block's end: the output is whole up to here, once the
// blocks open now are closed.
func (r *renderer) ended() {
	if !r.stopped {
		r.mark, r.marked, r.headingN = r.out.Len(), r.open, len(r.headings)
	}
}

func (r *renderer) block(b *block, tight bool) {
	if r.done() {
		return
	}
	switch b.kind {
	case document:
		for _, c := range b.children {
			r.block(c, false)
		}
		return
	case quote:
		r.enter("<blockquote>\n", "</blockquote>\n")
		for _, c := range b.children {
			r.block(c, false)
		}
		r.leave()
		return
	case list:
		switch {
		case !b.ordered:
			r.enter("<ul>\n", "</ul>\n")
		case b.start != 1:
			r.enter(`<ol start="`+strconv.Itoa(b.start)+`">`+"\n", "</ol>\n")
		default:
			r.enter("<ol>\n", "</ol>\n")
		}
		for _, c := range b.children {
			r.block(c, b.tight)
		}
		r.leave()
		return
	case item:
		r.enter("<li>", "</li>\n")
		for i, c := range b.children {
			if i == 0 && c.kind == paragraph {
				c.lines = taskBox(c.lines)
			}
			if tight && c.kind == paragraph {
				r.inline(strings.Join(c.lines, "\n"), nil)
			} else {
				if i == 0 {
					r.raw("\n")
				}
				r.block(c, tight)
			}
		}
		r.leave()
		return
	case paragraph:
		r.raw("<p>")
		r.inline(strings.Join(b.lines, "\n"), nil)
		r.raw("</p>\n")
	case heading:
		var plain strings.Builder
		at := r.out.Len()
		r.inline(strings.Join(b.lines, "\n"), &plain)
		if r.stopped {
			return
		}
		body := string(r.out.Bytes()[at:])
		r.out.Truncate(at)
		// A heading whose text gives no slug, such as # !!!, has no id
		// and no self-link.
		h := Heading{Level: b.level, Text: plain.String(), ID: r.slug(plain.String())}
		tag := "h" + strconv.Itoa(b.level)
		if h.ID == "" {
			r.raw("<" + tag + ">" + body + "</" + tag + ">\n")
		} else {
			r.raw("<" + tag + ` id="` + escape(h.ID) + `">` + body + ` <a class="anchor" href="#` +
				escape(fragment(h.ID)) + `">#</a></` + tag + ">\n")
		}
		if !r.stopped {
			r.headings = append(r.headings, h)
		}
	case fenced, indented:
		if b.info != "" {
			r.raw(`<pre><code class="language-` + escape(b.info) + `">`)
		} else {
			r.raw("<pre><code>")
		}
		for _, l := range b.lines {
			r.text(l)
			r.raw("\n")
		}
		r.raw("</code></pre>\n")
	case rule:
		r.raw("<hr>\n")
	case table:
		r.raw("<table>\n<thead>\n")
		for i, row := range b.rows {
			if i == 1 {
				r.raw("<tbody>\n")
			}
			cell := "td"
			if i == 0 {
				cell = "th"
			}
			r.raw("<tr>")
			for j, c := range row {
				switch b.align[j] {
				case 'l':
					r.raw("<" + cell + ` class="left">`)
				case 'c':
					r.raw("<" + cell + ` class="center">`)
				case 'r':
					r.raw("<" + cell + ` class="right">`)
				default:
					r.raw("<" + cell + ">")
				}
				r.inline(c, nil)
				r.raw("</" + cell + ">")
			}
			r.raw("</tr>\n")
			if i == 0 {
				r.raw("</thead>\n")
			}
			if r.stopped {
				return
			}
		}
		if len(b.rows) > 1 {
			r.raw("</tbody>\n")
		}
		r.raw("</table>\n")
	}
	r.ended()
}

// taskBox shows a task list item's box as text: ☐ for [ ] and ☑ for [x].
func taskBox(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	l := lines[0]
	if len(l) >= 3 && l[0] == '[' && l[2] == ']' && (len(l) == 3 || l[3] == ' ') {
		box := ""
		switch l[1] {
		case ' ':
			box = "☐"
		case 'x', 'X':
			box = "☑"
		}
		if box != "" {
			out := append([]string{box + l[3:]}, lines[1:]...)
			return out
		}
	}
	return lines
}

// slug is a heading's id: its Slug, numbered when the document gave it
// before.
func (r *renderer) slug(text string) string {
	base := Slug(text)
	if base == "" {
		return ""
	}
	id := base
	for {
		if _, seen := r.slugs[id]; !seen {
			break
		}
		r.slugs[base]++
		id = base + "-" + strconv.Itoa(r.slugs[base])
	}
	r.slugs[id] = 0
	return id
}

// fragment is a heading's id as a URL fragment.
func fragment(id string) string { return normalizeURL(id) }

// normalizeURL percent-encodes what a URL may not hold as it is, and
// keeps a percent-encoding already there.
func normalizeURL(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte("-._~:/?#@!$&'()*+,;=", c) >= 0:
			b.WriteByte(c)
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// href is where a link's destination goes, or ok false for one that
// renders as text. dest is decoded already, so a control character or a
// backslash in it was one in the input or was written as an escape or an
// entity. A browser may read a backslash as a slash, so no destination
// with one is safe to resolve or to write.
func (r *renderer) href(dest string) (string, bool) {
	for i := 0; i < len(dest); i++ {
		if c := dest[i]; c < ' ' || c == 0x7f || c == '\\' {
			return "", false
		}
	}
	switch scheme, rest, kind := schemeOf(dest); kind {
	case relative:
		if dest == "" || r.opt.Resolve == nil {
			return "", false
		}
		return r.opt.Resolve(dest)
	case absolute:
		switch strings.ToLower(scheme) {
		case "http", "https":
			// Only with a host: http:x is relative to the page, and
			// https:// names none. An IPv6 address keeps its brackets.
			if before, host, after, ok := splitHost(rest); ok {
				if host[0] == '[' {
					return normalizeURL(scheme+":"+before) + host + normalizeURL(after), true
				}
				return normalizeURL(dest), true
			}
		case "mailto":
			return normalizeURL(dest), true
		}
	}
	return "", false
}

// The kinds of destination schemeOf tells apart.
const (
	relative = iota
	absolute
	refused
)

// schemeOf reads the start of dest, up to its first /, ? or #. When that
// is a scheme, a letter then letters, digits, +, - or ., followed by a
// colon, dest is absolute, and schemeOf gives the scheme and what
// follows its colon. When it holds no colon, dest is relative. Any other
// dest is refused: a colon there that follows no scheme makes no
// relative reference.
func schemeOf(dest string) (scheme, rest string, kind int) {
	for i := 0; i < len(dest); i++ {
		switch c := dest[i]; {
		case c == ':':
			if !isScheme(dest[:i]) {
				return "", "", refused
			}
			return dest[:i], dest[i+1:], absolute
		case c == '/' || c == '?' || c == '#':
			return "", "", relative
		}
	}
	return "", "", relative
}

// splitHost splits rest, what follows an http or https scheme's colon,
// at the host its authority names: what comes before the host, the host,
// and what follows it. ok is false unless rest starts with // and an
// authority whose host is not empty once its user and its port are taken
// off. A host in brackets is an IPv6 address, and ok is false unless
// netip parses it as one, and only a port of one to five digits, or none,
// follows it.
func splitHost(rest string) (before, host, after string, ok bool) {
	if !strings.HasPrefix(rest, "//") {
		return "", "", "", false
	}
	end := len(rest)
	if i := strings.IndexAny(rest[2:], "/?#"); i >= 0 {
		end = 2 + i
	}
	start := 2
	if i := strings.LastIndexByte(rest[:end], '@'); i >= 0 {
		start = i + 1
	}
	hostPort := rest[start:end]
	if strings.HasPrefix(hostPort, "[") {
		i := strings.IndexByte(hostPort, ']')
		if i < 2 {
			return "", "", "", false
		}
		if a, err := netip.ParseAddr(hostPort[1:i]); err != nil || !a.Is6() {
			return "", "", "", false
		}
		if port := hostPort[i+1:]; port != "" && !isPort(port) {
			return "", "", "", false
		}
		host = hostPort[:i+1]
	} else if host, _, _ = strings.Cut(hostPort, ":"); host == "" {
		return "", "", "", false
	}
	return rest[:start], host, rest[start+len(host):], true
}

// isPort reports whether s is a colon and then one to five digits.
func isPort(s string) bool {
	if len(s) < 2 || len(s) > 6 || s[0] != ':' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func isScheme(s string) bool {
	if s == "" || !isAlpha(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; !(isAlpha(c) || isDigit(c) || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

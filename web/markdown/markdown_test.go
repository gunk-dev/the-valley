package markdown

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// resolve is a resolver that takes every relative path under /r/.
func resolve(dest string) (string, bool) {
	if strings.HasPrefix(dest, "#") {
		return dest, true
	}
	return "/r/" + strings.TrimPrefix(dest, "/"), true
}

func render(t *testing.T, src string) string {
	t.Helper()
	d, err := Render(context.Background(), []byte(src), Options{Resolve: resolve})
	if err != nil {
		t.Fatalf("Render(%q): %v", src, err)
	}
	return string(d.HTML)
}

// Examples from the CommonMark and GFM specifications, for the constructs
// the renderer supports, with the output in the renderer's own spacing.
func TestConstructs(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"# foo\n## foo\n###### foo", `<h1 id="foo">foo <a class="anchor" href="#foo">#</a></h1>` + "\n" +
			`<h2 id="foo-1">foo <a class="anchor" href="#foo-1">#</a></h2>` + "\n" +
			`<h6 id="foo-2">foo <a class="anchor" href="#foo-2">#</a></h6>` + "\n"},
		{"####### foo", "<p>####### foo</p>\n"},
		{"#5 bolt\n\n#hashtag", "<p>#5 bolt</p>\n<p>#hashtag</p>\n"},
		{"## foo ##\n# foo#", `<h2 id="foo">foo <a class="anchor" href="#foo">#</a></h2>` + "\n" + `<h1 id="foo-1">foo# <a class="anchor" href="#foo-1">#</a></h1>` + "\n"},
		{"Foo *bar*\n=========", `<h1 id="foo-bar">Foo <em>bar</em> <a class="anchor" href="#foo-bar">#</a></h1>` + "\n"},
		{"Foo\nbar\n---", `<h2 id="foo-bar">Foo` + "\n" + `bar <a class="anchor" href="#foo-bar">#</a></h2>` + "\n"},
		{"aaa\n\nbbb", "<p>aaa</p>\n<p>bbb</p>\n"},
		{"foo  \nbar\\\nbaz", "<p>foo<br>\nbar<br>\nbaz</p>\n"},
		{"*foo bar*", "<p><em>foo bar</em></p>\n"},
		{"a * foo bar*", "<p>a * foo bar*</p>\n"},
		{"foo*bar*", "<p>foo<em>bar</em></p>\n"},
		{"_foo_bar", "<p>_foo_bar</p>\n"},
		{"**foo*bar**", "<p><strong>foo*bar</strong></p>\n"},
		{"*foo**bar**baz*", "<p><em>foo<strong>bar</strong>baz</em></p>\n"},
		{"***strong emph***", "<p><em><strong>strong emph</strong></em></p>\n"},
		{"*foo**bar*", "<p><em>foo**bar</em></p>\n"},
		{"~~Hi~~ Hello, ~there~ world!", "<p><del>Hi</del> Hello, <del>there</del> world!</p>\n"},
		{"This ~~~is no~~~ strikethrough", "<p>This ~~~is no~~~ strikethrough</p>\n"},
		{"`` foo ` bar ``", "<p><code>foo ` bar</code></p>\n"},
		{"`foo\\`bar`", "<p><code>foo\\</code>bar`</p>\n"},
		{"```foo``", "<p>```foo``</p>\n"},
		{"```\n<\n >\n```", "<pre><code>&lt;\n &gt;\n</code></pre>\n"},
		{"~~~ruby startline=3\ndef\n~~~~", `<pre><code class="language-ruby">def` + "\n</code></pre>\n"},
		{"```\naaa\n~~~\n```", "<pre><code>aaa\n~~~\n</code></pre>\n"},
		{"    a simple\n      indented code block", "<pre><code>a simple\n  indented code block\n</code></pre>\n"},
		{"> # Foo\n> bar\nbaz", "<blockquote>\n" + `<h1 id="foo">Foo <a class="anchor" href="#foo">#</a></h1>` + "\n<p>bar\nbaz</p>\n</blockquote>\n"},
		{"> > > foo\nbar", "<blockquote>\n<blockquote>\n<blockquote>\n<p>foo\nbar</p>\n</blockquote>\n</blockquote>\n</blockquote>\n"},
		{"- foo\n- bar\n+ baz", "<ul>\n<li>foo</li>\n<li>bar</li>\n</ul>\n<ul>\n<li>baz</li>\n</ul>\n"},
		{"1. foo\n2. bar\n3) baz", "<ol>\n<li>foo</li>\n<li>bar</li>\n</ol>\n<ol start=\"3\">\n<li>baz</li>\n</ol>\n"},
		{"- foo\n\n- bar", "<ul>\n<li>\n<p>foo</p>\n</li>\n<li>\n<p>bar</p>\n</li>\n</ul>\n"},
		{"- foo\n  - bar\n    - baz", "<ul>\n<li>foo<ul>\n<li>bar<ul>\n<li>baz</li>\n</ul>\n</li>\n</ul>\n</li>\n</ul>\n"},
		{"The number of windows in my house is\n14.  The number of doors is 6.", "<p>The number of windows in my house is\n14.  The number of doors is 6.</p>\n"},
		{"- [ ] foo\n- [x] bar", "<ul>\n<li>☐ foo</li>\n<li>☑ bar</li>\n</ul>\n"},
		{"| foo | bar |\n| --- | :-: |\n| baz | bim |", "<table>\n<thead>\n<tr><th>foo</th><th class=\"center\">bar</th></tr>\n</thead>\n<tbody>\n<tr><td>baz</td><td class=\"center\">bim</td></tr>\n</tbody>\n</table>\n"},
		{"| f\\|oo |\n| ------ |\n| `b\\|az` |", "<table>\n<thead>\n<tr><th>f|oo</th></tr>\n</thead>\n<tbody>\n<tr><td><code>b|az</code></td></tr>\n</tbody>\n</table>\n"},
		{"***\n---\n___", "<hr>\n<hr>\n<hr>\n"},
		{"[link](/uri \"title\")", `<p><a href="/r/uri" title="title">link</a></p>` + "\n"},
		{"[link](<foo bar>)", `<p><a href="/r/foo bar">link</a></p>` + "\n"},
		{"[foo]: /url \"title\"\n\n[foo] [Foo][] [x][FOO]", `<p><a href="/r/url" title="title">foo</a> <a href="/r/url" title="title">Foo</a> <a href="/r/url" title="title">x</a></p>` + "\n"},
		{"[link [foo [bar]]](/uri)", `<p><a href="/r/uri">link [foo [bar]]</a></p>` + "\n"},
		{"[foo [bar](/uri)](/uri)", `<p>[foo <a href="/r/uri">bar</a>](/uri)</p>` + "\n"},
		{"<https://foo.bar.baz/test?q=hello&id=22&boolean>", `<p><a href="https://foo.bar.baz/test?q=hello&amp;id=22&amp;boolean">https://foo.bar.baz/test?q=hello&amp;id=22&amp;boolean</a></p>` + "\n"},
		{"<foo@bar.example.com>", `<p><a href="mailto:foo@bar.example.com">foo@bar.example.com</a></p>` + "\n"},
		{"Visit https://www.example.org/a_(b)) now.", `<p>Visit <a href="https://www.example.org/a_(b)">https://www.example.org/a_(b)</a>) now.</p>` + "\n"},
		{"![foo *bar*](train.jpg \"t\")", `<p><a href="/r/train.jpg" title="t">foo bar</a></p>` + "\n"},
		{"\\*not emphasized* \\[not a link](/foo)", "<p>*not emphasized* [not a link](/foo)</p>\n"},
		{"&nbsp; &copy; &#35; &#X22; &#0; &ThisIsNotDefined; &ampx;", "<p>  © # &#34; � &amp;ThisIsNotDefined; &amp;ampx;</p>\n"},
		{"[a](#frag)", `<p><a href="#frag">a</a></p>` + "\n"},
		{"# !!!\n\n## a", "<h1>!!!</h1>\n" + `<h2 id="a">a <a class="anchor" href="#a">#</a></h2>` + "\n"},
		{"| a |\n| - |\n| b | c |\n|", "<table>\n<thead>\n<tr><th>a</th></tr>\n</thead>\n<tbody>\n<tr><td>b</td></tr>\n<tr><td></td></tr>\n</tbody>\n</table>\n"},
	} {
		if got := render(t, c.in); got != c.want {
			t.Errorf("Render(%q)\n got: %q\nwant: %q", c.in, got, c.want)
		}
	}
}

// Raw HTML, hostile schemes and attribute values arrive as text.
func TestHostileInputIsInert(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<script>alert(1)</script>", "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>\n"},
		{"<div onclick=\"x()\">\n\n*hi*\n\n</div>", "<p>&lt;div onclick=&#34;x()&#34;&gt;</p>\n<p><em>hi</em></p>\n<p>&lt;/div&gt;</p>\n"},
		{"a <img src=x onerror=alert(1)> b", "<p>a &lt;img src=x onerror=alert(1)&gt; b</p>\n"},
		{"<svg><script>x</script></svg>", "<p>&lt;svg&gt;&lt;script&gt;x&lt;/script&gt;&lt;/svg&gt;</p>\n"},
		{"[x](javascript:alert(1))", "<p>x</p>\n"},
		{"[x](JaVaScRiPt:alert(1))", "<p>x</p>\n"},
		{"[x](&#106;avascript:alert(1))", "<p>x</p>\n"},
		{"[x](java&#9;script:alert(1))", "<p>x</p>\n"},
		{"[x](data:text/html;base64,PHNjcmlwdD4=)", "<p>x</p>\n"},
		{"[x](vbscript:msgbox)", "<p>x</p>\n"},
		{"<javascript:alert(1)>", "<p>javascript:alert(1)</p>\n"},
		{"![x](data:image/png;base64,AAAA)", "<p>x</p>\n"},
		{"[x](\\javascript:alert(1))", "<p>x</p>\n"},
		{`[x](http\z/../../target)`, "<p>x</p>\n"},
		{`[x](https:\\evil.org/)`, "<p>x</p>\n"},
		{"[x](http:../../target) [y](HTTPS:x)", "<p>x y</p>\n"},
		{"[x](a\\b/c) [y](1a:b) [z](a b:c)", "<p>x y [z](a b:c)</p>\n"},
		{`[x](http://a.org/b\c) [y](/a\b) [z](#a\b) [w](mailto:a\b@c.org)`, "<p>x y z w</p>\n"},
		{`[x](http://a.org/\\b) [y](a&#92;b) [z](<a\b>)`, "<p>x y z</p>\n"},
		{"[x](https://) [y](http://) [z](https:///a) [w](http://:80/) [v](https://a@/) <https://>", "<p>x y z w v https://</p>\n"},
		{"[x](https://a.org) [y](http://[::1]:80/)", `<p><a href="https://a.org">x</a> <a href="http://[::1]:80/">y</a></p>` + "\n"},
		{"[x](http://alice@[2001:db8::1]:8080/a[b]?c) <https://[::ffff:192.0.2.1]>", `<p><a href="http://alice@[2001:db8::1]:8080/a%5Bb%5D?c">x</a> <a href="https://[::ffff:192.0.2.1]">https://[::ffff:192.0.2.1]</a></p>` + "\n"},
		{"[x](http://[::1) [y](http://[]/) [z](http://[::1]a/) [w](http://[::g]/) [v](http://[a\"b]/) [u](http://[a]/) [t](http://[::1]:x/) [s](http://[::1]:123456/)", "<p>x y z w v u t s</p>\n"},
		{`[x](http://a.org/"onmouseover="alert(1) "a\"><b>")`, `<p><a href="http://a.org/%22onmouseover=%22alert(1)" title="a&#34;&gt;&lt;b&gt;">x</a></p>` + "\n"},
		{"[x](<http://a.org/<>)", "<p>[x](&lt;http://a.org/&lt;&gt;)</p>\n"},
		{"```\"><script>alert(1)</script>\nx\n```", `<pre><code class="language-&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;">x` + "\n</code></pre>\n"},
		{"# <b onclick=x>", `<h1 id="b-onclickx">&lt;b onclick=x&gt; <a class="anchor" href="#b-onclickx">#</a></h1>` + "\n"},
	} {
		if got := render(t, c.in); got != c.want {
			t.Errorf("Render(%q)\n got: %q\nwant: %q", c.in, got, c.want)
		}
		checkHTML(t, c.in, render(t, c.in))
	}
	if d, err := Render(context.Background(), []byte("[a](b.md)"), Options{}); err != nil || string(d.HTML) != "<p>a</p>\n" {
		t.Errorf("with no resolver, a relative link renders %q", d.HTML)
	}
}

// GitHub's ids for real headings of docs/.
func TestSlug(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"web-export (the-valley/web-export/v2)", "web-export-the-valleyweb-exportv2"},
		{"Purpose and principles", "purpose-and-principles"},
		{"`valley plan`", "valley-plan"},
		{"Ownership: who owns what", "ownership-who-owns-what"},
		{"CLI commands", "cli-commands"},
		{"valley-web, the read side", "valley-web-the-read-side"},
		{"snake_case and   spaces", "snake_case-and---spaces"},
		{"Ünïcode Wörds 2", "ünïcode-wörds-2"},
		{"?!", ""},
	} {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// adversarial is inputs of about size bytes, each built to take a naive
// renderer quadratic time or memory, or to nest without end.
func adversarial(size int) map[string]string {
	fill := func(unit string) string { return strings.Repeat(unit, size/len(unit)) }
	// table is a table of cols columns, whose header and delimiter row
	// are followed by empty rows to fill size.
	table := func(cols int) string {
		head := strings.Repeat("|", cols+1) + "\n" + strings.Repeat("|-", cols) + "|\n"
		return head + strings.Repeat("|\n", max(0, size-len(head))/2)
	}
	// nest is n openers of a delimiter, then n closers, as many as size
	// holds and as MaxInlineNodes parses.
	nest := func(d string) string {
		n := min(size/(2*len(d)+4), MaxInlineNodes/8)
		return strings.Repeat(d+"a ", n) + strings.Repeat(" a"+d, n)
	}
	// refs is a definition whose destination takes half of size, used
	// as often as the other half holds use and as MaxInlineNodes parses.
	refs := func(use string) string {
		def := "[a]: http://example.org/" + strings.Repeat("x", size/2) + "\n\n"
		return def + strings.Repeat(use, min((size-len(def))/len(use), MaxInlineNodes/8))
	}
	links := min(size/12, MaxInlineNodes/8)
	// heavy is the content that allocates most: a paragraph past
	// MaxInlineNodes, then empty headings past MaxBlocks.
	heavy := strings.Repeat("*_~", MaxInlineNodes/3+1) + "\n\n" + fill("#\n")
	// padded is s, built to stay under MaxInlineNodes, followed by
	// heavy to fill size.
	padded := func(s string) string {
		return s + "\n\n" + heavy[:max(0, size-len(s)-2)]
	}
	// labels is a run of brackets around a, each ] of which ends a
	// candidate label: 500 of them, from 1 byte to 999.
	labels := fill(strings.Repeat("[", 500) + "a" + strings.Repeat("]", 500))
	inputs := map[string]string{
		"stars":       fill("*a"),
		"underscores": fill("_a "),
		"mixed":       fill("*_~"),
		"brackets":    fill("["),
		"closers":     fill("]"),
		"links":       fill("[]("),
		"titles":      fill("[](x \"[](x '"),
		"parens":      strings.Repeat("[](", size/1000) + fill("()")[3*(size/1000):],
		"backticks":   fill("`a``b```c````"),
		"ticks": func() string {
			var b strings.Builder
			for i := 1; b.Len()+i%1400+2 <= size; i++ {
				b.WriteString(strings.Repeat("`", i%1400+1) + "x")
			}
			return b.String()
		}(),
		"autolinks":   fill("<a"),
		"emails":      fill("<a@"),
		"bare":        fill("(http://a"),
		"entities":    fill("&#"),
		"quotes":      fill(">"),
		"lists":       fill("- "),
		"nested":      fill("- > 1. "),
		"definitions": fill("[a]: b\n") + "[a]",
		"refs":        fill("[a] [b][c] "),
		"table":       "|a|\n|-|\n" + fill("|")[8:],
		"headings":    fill("# a\n"),
		"lines":       fill("a  \n"),
		"fences":      fill("```\n"),
		"setext":      fill("a\n=\n"),
		"paragraphs":  fill("a\n\n"),
		// A block for every two bytes, and then for every byte.
		"empty headings": fill("#\n"),
		"items":          fill("- - a\n"),
		"deep items":     fill(strings.Repeat("- ", MaxDepth) + "a\n"),
		// A paragraph past MaxInlineNodes, then blocks past MaxBlocks.
		"both caps": heavy[:size],
		// A header with a column for every ten bytes, and short rows.
		"wide table": table(size / 10),
		// A table as wide as one may be, with short rows.
		"narrow table": table(MaxTableColumns),
		// A bare link followed by a ) for each byte.
		"trailing parens": "(http://a" + fill(")")[9:],
		// A long destination used in one paragraph, and in many.
		"expansion":         refs("[a] "),
		"expansions":        refs("[a]\n\n"),
		"backslash":         fill(`[x](http\z/../../target) `),
		"hosts":             fill(`[x](https://) `),
		"emphasis":          nest("*"),
		"strong":            nest("**"),
		"strike":            nest("~~"),
		"links in emphasis": strings.Repeat("*a [", links) + strings.Repeat("](b) a*", links),
		// Candidate labels with no definition, and with one.
		"labels":          labels,
		"labels, defined": "[x]: y\n\n" + labels[:len(labels)-9],
	}
	// Those that stay under MaxInlineNodes, padded to full size.
	for _, name := range []string{"emphasis", "strong", "strike", "links in emphasis", "expansion", "expansions"} {
		inputs[name+" padded"] = padded(inputs[name])
	}
	return inputs
}

func TestBounds(t *testing.T) {
	ctx := context.Background()
	if _, err := Render(ctx, make([]byte, MaxInput+1), Options{}); err != ErrTooLong {
		t.Errorf("an input past MaxInput gave %v", err)
	}
	deep := strings.Repeat("> ", 100) + "x"
	got := render(t, deep)
	if n := strings.Count(got, "<blockquote>"); n != MaxDepth {
		t.Errorf("100 quotes nested %d deep", n)
	}
	// Adversarial inputs of MaxInput bytes each render in time
	// near-linear in their size, within the output bound the server
	// gives, and as whole HTML however they are cut.
	const maxOutput = 2 << 20
	out := map[string]Doc{}
	for name, src := range adversarial(MaxInput) {
		start := time.Now()
		d, err := Render(ctx, []byte(src), Options{Resolve: resolve, MaxOutput: maxOutput})
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("%s took %v", name, took)
		}
		if len(d.HTML) > maxOutput {
			t.Errorf("%s rendered %d bytes, past MaxOutput", name, len(d.HTML))
		}
		checkHTML(t, name, string(d.HTML))
		out[name] = d
	}
	html := func(name string) string { return string(out[name].HTML) }
	if strings.Contains(html("wide table"), "<table") {
		t.Errorf("a header of %d columns made a table", MaxInput/10)
	}
	cells := strings.Count(html("narrow table"), "<td") + strings.Count(html("narrow table"), "<th")
	if cells > MaxTableCells || cells <= MaxTableCells-MaxTableColumns {
		t.Errorf("the tables hold %d cells, want at most %d", cells, MaxTableCells)
	}
	if n := strings.Count(html("trailing parens"), `<a href="http://a">`); n != 1 {
		t.Errorf("a bare link before a run of ) rendered %d links", n)
	}
	for _, name := range []string{"expansion", "expansions"} {
		if !out[name].Truncated {
			t.Errorf("%s: a destination of %d bytes used over and over fit in %d bytes", name, MaxInput/2, maxOutput)
		}
	}
	for _, name := range []string{"backslash", "hosts"} {
		if strings.Contains(html(name), "<a") {
			t.Errorf("%s: a destination with a backslash, or with no host, is a link", name)
		}
	}
	for name, tag := range map[string]string{"emphasis": "<em>", "strong": "<strong>", "strike": "<del>", "links in emphasis": "<em>"} {
		for _, name := range []string{name, name + " padded"} {
			if n := strings.Count(html(name), tag); n != MaxDepth {
				t.Errorf("%s nested %d deep, want %d", name, n, MaxDepth)
			}
		}
	}
}

// The caps on brackets, inline nodes and blocks each turn what passes
// them into text.
func TestCaps(t *testing.T) {
	ctx := context.Background()
	opt := Options{Resolve: resolve}
	// Each element nests MaxDepth deep, and a link, an image or an
	// autolink within them all is its text.
	for _, link := range []string{"[x](b)", "![x](b)", "<http://a.org>", "http://a.org"} {
		src := strings.Repeat("*a ", MaxDepth+8) + link + strings.Repeat(" a*", MaxDepth+8)
		got := render(t, src)
		if n := strings.Count(got, "<em>"); n != MaxDepth {
			t.Errorf("%s nested in emphasis: %d deep, want %d", link, n, MaxDepth)
		}
		if strings.Contains(got, "<a") {
			t.Errorf("%s past MaxDepth is a link: %q", link, got)
		}
	}

	// The [ past maxBrackets open is text, and the last one open closes.
	src := strings.Repeat("[", maxBrackets+10) + "a](b)"
	d, u, err := renderDoc(ctx, []byte(src), opt)
	if err != nil {
		t.Fatal(err)
	}
	want := "<p>" + strings.Repeat("[", maxBrackets-1) + `<a href="/r/b">` + strings.Repeat("[", 10) + "a</a></p>\n"
	if string(d.HTML) != want || u.brackets != maxBrackets {
		t.Errorf("%d brackets open at most, and gave %.80q…", u.brackets, d.HTML)
	}

	// The block that passes MaxInlineNodes, and each block after it, is
	// its text.
	src = "*a*\n\n" + strings.Repeat("[", MaxInlineNodes) + "\n\n*b* [c](d)\n\n# *e*"
	d, u, err = renderDoc(ctx, []byte(src), opt)
	if err != nil {
		t.Fatal(err)
	}
	got := string(d.HTML)
	if u.nodes <= MaxInlineNodes || !strings.HasPrefix(got, "<p><em>a</em></p>\n<p>[[[") ||
		!strings.HasSuffix(got, "[[[</p>\n<p>*b* [c](d)</p>\n"+`<h1 id="e">*e* <a class="anchor" href="#e">#</a></h1>`+"\n") {
		t.Errorf("past %d inline nodes, gave %.40q…%q", u.nodes, got, got[max(0, len(got)-120):])
	}

	// The line that would pass MaxBlocks, less the one block kept for
	// it, is with the input after it one block of preformatted text. It
	// takes back the containers it opened, and a table's header stays
	// in its paragraph.
	heads := func(n int) string { return strings.Repeat("#\n", n) }
	for _, c := range []struct {
		src, end string
		blocks   int
	}{
		{heads(MaxBlocks-1) + "# a\n<b>\n\n", "</h1>\n<pre><code># a\n&lt;b&gt;\n</code></pre>\n", MaxBlocks},
		{heads(MaxBlocks-3) + "> - > a\nb", "</h1>\n<pre><code>&gt; - &gt; a\nb\n</code></pre>\n", MaxBlocks - 2},
		{heads(MaxBlocks-2) + "| a |\n| - |\n| b |", "</h1>\n<p>| a |</p>\n<pre><code>| - |\n| b |\n</code></pre>\n", MaxBlocks},
	} {
		d, u, err = renderDoc(ctx, []byte(c.src), opt)
		if err != nil {
			t.Fatal(err)
		}
		got = string(d.HTML)
		if u.blocks != c.blocks || strings.Contains(got, "<blockquote>") || !strings.HasSuffix(got, c.end) {
			t.Errorf("%d blocks, want %d, ending %q", u.blocks, c.blocks, got[max(0, len(got)-80):])
		}
	}

	// A label holds at most 999 characters, however many bytes they take.
	for _, c := range []struct {
		chars int
		link  bool
	}{{maxLabel, true}, {maxLabel + 1, false}} {
		src = "[" + strings.Repeat("é", c.chars) + "]: /x\n\n[" + strings.Repeat("É", c.chars) + "]"
		if got := render(t, src); strings.Contains(got, "<a") != c.link {
			t.Errorf("a label of %d characters gave %.40q", c.chars, got)
		}
	}

	// Past maxLabelBytes of labels looked up, a reference is text. With
	// no definition, none is looked up.
	unit := strings.Repeat("[", 500) + "b" + strings.Repeat("]", 500)
	for _, def := range []string{"", "[a]: /x\n\n"} {
		src = def + "[a] " + strings.Repeat(unit, 40) + " [a]"
		d, u, err = renderDoc(ctx, []byte(src), opt)
		if err != nil {
			t.Fatal(err)
		}
		got = string(d.HTML)
		first, labels := "[a] [[[", 0
		if def != "" {
			first, labels = `<a href="/r/x">a</a> [[[`, maxLabelBytes
		}
		if u.labels != labels || !strings.HasPrefix(got, "<p>"+first) || !strings.HasSuffix(got, "]]] [a]</p>\n") {
			t.Errorf("%q: %d bytes of labels looked up, giving %.40q…%q", def, u.labels, got, got[max(0, len(got)-20):])
		}
	}
}

// maxAlloc bounds what one render of a MaxInput document allocates.
const maxAlloc = 128 << 20

// Every adversarial input renders within maxAlloc: the bytes it allocates
// in all, and so the most its heap grows at once, which is measured too.
// Each is MaxInput bytes, or has a version padded to MaxInput, and holds
// at most MaxBlocks blocks.
func TestAllocation(t *testing.T) {
	inputs := adversarial(MaxInput)
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	var worst string
	var most uint64
	for _, name := range names {
		src := []byte(inputs[name])
		if _, full := inputs[name+" padded"]; len(src) < MaxInput-MaxInput/100 && !full {
			t.Errorf("%s is %d bytes, short of MaxInput, with no padded version", name, len(src))
		}
		var u usage
		total, peak := allocation(func() {
			var err error
			if _, u, err = renderDoc(context.Background(), src, Options{Resolve: resolve, MaxOutput: 2 << 20}); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("%-24s %4d KiB: allocated %5.1f MiB, heap peak %5.1f MiB", name, len(src)>>10, mib(total), mib(peak))
		if u.blocks > MaxBlocks {
			t.Errorf("%s holds %d blocks, past MaxBlocks", name, u.blocks)
		}
		if total > maxAlloc || peak > maxAlloc {
			t.Errorf("%s allocated %.1f MiB, with a heap peak of %.1f MiB, past %d MiB", name, mib(total), mib(peak), maxAlloc>>20)
		}
		if total > most {
			worst, most = name, total
		}
	}
	t.Logf("the most allocated is %.1f MiB, by %s", mib(most), worst)
}

func mib(n uint64) float64 { return float64(n) / (1 << 20) }

// allocation runs f, and gives the bytes it allocated and the most the
// heap grew while it ran, sampled each millisecond.
func allocation(f func()) (total, peak uint64) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	stop, sampled := make(chan struct{}), make(chan uint64)
	go func() {
		var m runtime.MemStats
		var high uint64
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			runtime.ReadMemStats(&m)
			high = max(high, m.HeapAlloc)
			select {
			case <-stop:
				sampled <- high
				return
			case <-tick.C:
			}
		}
	}()
	f()
	runtime.ReadMemStats(&after)
	close(stop)
	high := max(<-sampled, after.HeapAlloc)
	return after.TotalAlloc - before.TotalAlloc, high - min(high, before.HeapAlloc)
}

// A document past MaxOutput is cut after its last whole block, with the
// blocks that hold it closed.
func TestTruncation(t *testing.T) {
	src := "# Title\n\n> - one\n>\n>   two\n> - " + strings.Repeat("three ", 100) + "\n\nafter"
	for max := 1; max < 1000; max++ {
		d, err := Render(context.Background(), []byte(src), Options{MaxOutput: max})
		if err != nil {
			t.Fatal(err)
		}
		got := string(d.HTML)
		if len(got) > max {
			t.Fatalf("MaxOutput %d: %d bytes", max, len(got))
		}
		checkHTML(t, src, got)
		if d.Truncated != !strings.Contains(got, "after") {
			t.Fatalf("MaxOutput %d: Truncated is %v for %q", max, d.Truncated, got)
		}
		if (len(d.Headings) == 1) != strings.Contains(got, "<h1") {
			t.Fatalf("MaxOutput %d: %d headings for %q", max, len(d.Headings), got)
		}
	}
	d, _ := Render(context.Background(), []byte(src), Options{MaxOutput: 200})
	if want := "<blockquote>\n<ul>\n<li>\n<p>one</p>\n<p>two</p>\n</li>\n</ul>\n</blockquote>\n"; !strings.HasSuffix(string(d.HTML), want) {
		t.Errorf("a cut inside a list in a quote gave %q", d.HTML)
	}
}

// Rendering stops once its context is done.
func TestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, src := range adversarial(MaxInput) {
		if _, err := Render(ctx, []byte(src), Options{Resolve: resolve}); err != context.Canceled {
			t.Errorf("%s: with its context done, Render gave %v", name, err)
		}
	}
}

// The renderer's output holds only its own elements and attributes, every
// href is allowed or resolved, and nothing active appears.
func FuzzRender(f *testing.F) {
	for _, seed := range []string{
		"# h\n\n*a* **b** ~~c~~ `d` [e](f \"g\") <http://h.org> ![i](j)\n",
		"<script>alert(1)</script>", "[x](javascript:alert(1))", "[x](&#106;avascript:1)",
		"```\"><svg onload=x>\ny\n```", "> - [ ] a\n>   b\n\n| a | b |\n|:-|-:|\n| <c> | d |\n",
		"[a]: <b\"c> 'd\"e'\n[a]", "<a href=\"x\" style=\"y\">", "http://a.org/<b>?\"'", "&lt;script&gt;",
		"*a _b *c_ d* e_", "1. a\n\n   b\n2) c", "    code\n\tmore", "[![a](b)](c)", "<foo@bar.org>",
		`[x](http\z/../../target)`, `[x](https:\\evil.org/)`, "[x](http:../../target)", "# !!!",
		`[x](http://a.org/b\c)`, `[x](a&#92;b)`, "[x](https://)", "<https://>", "[x](https://a@:1/)",
	} {
		f.Add(seed)
	}
	// Each adversarial input, small.
	for _, seed := range adversarial(2048) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		d, err := Render(context.Background(), []byte(src), Options{Resolve: resolve})
		if err != nil {
			t.Fatal(err)
		}
		checkHTML(t, src, string(d.HTML))
	})
}

// allowed is every element the renderer writes, with its attributes.
var allowed = map[string]map[string]bool{
	"h1": {"id": true}, "h2": {"id": true}, "h3": {"id": true}, "h4": {"id": true}, "h5": {"id": true}, "h6": {"id": true},
	"p": {}, "br": {}, "em": {}, "strong": {}, "del": {}, "code": {"class": true}, "pre": {}, "blockquote": {},
	"ul": {}, "ol": {"start": true}, "li": {}, "hr": {}, "table": {}, "thead": {}, "tbody": {}, "tr": {},
	"th": {"class": true}, "td": {"class": true}, "a": {"href": true, "title": true, "class": true},
}

var void = map[string]bool{"br": true, "hr": true}

// checkHTML tokenizes out and fails unless it is well formed, with only
// allowed elements and attributes, quoted values that hold no raw <, >
// or quote, and hrefs that are http, https, mailto or resolved.
func checkHTML(t *testing.T, src, out string) {
	t.Helper()
	fail := func(why string) { t.Helper(); t.Fatalf("Render(%q) = %q: %s", src, out, why) }
	low := strings.ToLower(out)
	for _, active := range []string{"<script", "<img", "<svg", "<iframe", "<input", "<form"} {
		if strings.Contains(low, active) {
			fail("holds " + active)
		}
	}
	var open []string
	for i := 0; i < len(out); {
		switch out[i] {
		case '>', '"':
			fail("a raw > or quote in text")
		case '&':
			j := strings.IndexByte(out[i:], ';')
			if j < 2 || j > 8 {
				fail("a bare &")
			}
			i += j + 1
			continue
		case '<':
		default:
			i++
			continue
		}
		end := strings.IndexByte(out[i:], '>')
		if end < 0 {
			fail("an unclosed tag")
		}
		tag := out[i+1 : i+end]
		i += end + 1
		if strings.HasPrefix(tag, "/") {
			if len(open) == 0 || open[len(open)-1] != tag[1:] {
				fail("a mismatched </" + tag[1:] + ">")
			}
			open = open[:len(open)-1]
			continue
		}
		if strings.Contains(tag, "style=") || strings.Contains(tag, " on") {
			fail("a tag with a style or an event handler")
		}
		name, attrs, _ := strings.Cut(tag, " ")
		allow, ok := allowed[name]
		if !ok {
			fail("an element " + name)
		}
		for attrs != "" {
			key, rest, ok := strings.Cut(attrs, `="`)
			value, more, ok2 := strings.Cut(rest, `"`)
			if !ok || !ok2 || !allow[key] || strings.ContainsAny(value, "<>'") {
				fail("an attribute " + attrs)
			}
			scheme, _, _ := strings.Cut(strings.ToLower(value), ":")
			if key == "href" && !(scheme == "http" || scheme == "https" || scheme == "mailto" || strings.HasPrefix(value, "/r/") || strings.HasPrefix(value, "#")) {
				fail("an href " + value)
			}
			attrs = strings.TrimPrefix(more, " ")
		}
		if name == "a" {
			for _, o := range open {
				if o == "a" {
					fail("a link inside a link")
				}
			}
		}
		if !void[name] {
			open = append(open, name)
		}
	}
	if len(open) > 0 {
		fail("unclosed " + strings.Join(open, ", "))
	}
}

// The repository's own documents render with every relative link naming
// a file of the tree, and every fragment a heading of its target. The
// targets that are not documents are checked where the whole tree is
// present: in a checkout, and in the prose-format check.
func TestRepositoryDocs(t *testing.T) {
	root := filepath.Join("..", "..")
	docs, _ := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	docs = append(docs, filepath.Join(root, "README.md"))
	_, err := os.Stat(filepath.Join(root, "flake.nix"))
	whole := err == nil
	ids := map[string]map[string]bool{}
	type ref struct{ from, target, frag string }
	var refs []ref
	for _, doc := range docs {
		rel, _ := filepath.Rel(root, doc)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		d, u, err := renderDoc(context.Background(), src, Options{MaxOutput: 2 << 20, Resolve: func(dest string) (string, bool) {
			p, frag, _ := strings.Cut(dest, "#")
			target := rel
			if p != "" {
				target = path.Join(path.Dir(rel), p)
			}
			refs = append(refs, ref{rel, target, frag})
			return "/r/" + target, true
		}})
		if err != nil {
			t.Fatal(err)
		}
		// Each renders whole, far from every cap.
		t.Logf("%s: %d KiB, %d blocks, %d inline nodes, %d brackets open at most, %d bytes of labels", rel, len(src)>>10, u.blocks, u.nodes, u.brackets, u.labels)
		if d.Truncated || u.blocks > MaxBlocks/4 || u.nodes > MaxInlineNodes/4 || u.brackets > maxBrackets/4 || u.labels > maxLabelBytes/4 {
			t.Errorf("%s: truncated %v, with %d blocks, %d inline nodes, %d brackets open and %d bytes of labels; a cap is too near", rel, d.Truncated, u.blocks, u.nodes, u.brackets, u.labels)
		}
		ids[rel] = map[string]bool{}
		for _, h := range d.Headings {
			ids[rel][h.ID] = true
		}
	}
	for _, doc := range []string{"docs/reference.md", "docs/architecture.md"} {
		if ids[doc] == nil {
			t.Errorf("%s was not rendered", doc)
		}
	}
	if len(ids) < 5 || len(refs) < 100 {
		t.Fatalf("read %d documents with %d links; the tree's docs are not where the test looks", len(ids), len(refs))
	}
	for _, r := range refs {
		headings, isDoc := ids[r.target]
		switch {
		case isDoc && r.frag != "" && !headings[r.frag]:
			t.Errorf("%s links %s#%s, which names no heading", r.from, r.target, r.frag)
		case !isDoc && strings.HasPrefix(r.target, "../"):
			t.Errorf("%s links %s, above the tree", r.from, r.target)
		case !isDoc && whole:
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(r.target))); err != nil {
				t.Errorf("%s links %s, which does not exist", r.from, r.target)
			}
		}
	}
}

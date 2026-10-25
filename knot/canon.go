package knot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The canonical form of a knot's file. Go's standard library has no YAML
// parser, so the live graph is written in one form the Go code owns: a
// strict subset of YAML that is still YAML, so the CUE schema and the
// knowledge lint read the same file. The frontmatter is one line per key,
// in the order Fields lists them:
//
//	---
//	type: outcome
//	id: oc-1a2b3c4
//	status: open
//	title: "Ship the plan graph"
//	created: 2026-10-15
//	created_by: "operator"
//	parent: oc-7654321
//	blocked_by: ["app:oc-0123456", bd-89abcde]
//	---
//
//	The body, as markdown.
//
// type, id, status, created, parent and graduated_into are bare tokens.
// Every other value is a JSON string, so no title, name or link is read as
// anything else. A list is a flow list, sorted, of local IDs bare and of
// everything else as JSON strings. blocked_by is written on every outcome
// and bug, empty or not, and every other optional key only when it holds
// something. The body follows the closing --- after one blank line, with
// no blank line at its end, and a knot with no body ends at the ---.
//
// ParseKnot reads a file leniently enough to say which rule it breaks,
// and then refuses it unless rendering what it read gives back the same
// bytes. So there is exactly one way to write each knot.

const fence = "---\n"

// keyShape is a line's key, as YAML would read one from it.
var keyShape = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// checkText refuses content that is not valid UTF-8, or that holds a
// control character other than newline and tab. U+FFFE and U+FFFF are
// refused too: YAML does not allow them, so the CUE schema could not read
// a file holding one.
func checkText(path string, data []byte) *Error {
	if !utf8.Valid(data) {
		return refuse(RuleText, path, "is not valid UTF-8")
	}
	for i, r := range string(data) {
		if (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7f && r <= 0x9f) || r == 0xfffe || r == 0xffff {
			return refuse(RuleText, path, fmt.Sprintf("holds the control character %U at byte %d; a knot's text holds no control character but newline and tab", r, i))
		}
	}
	return nil
}

// frontmatter splits a file into its frontmatter lines and its body. The
// file opens with a --- line, and the next --- line closes the
// frontmatter.
func frontmatter(path string, data []byte) (lines []string, body string, err *Error) {
	text := string(data)
	if !strings.HasPrefix(text, fence) {
		return nil, "", refuse(RuleCanonical, path, "does not open with a --- line: the file opens with its frontmatter")
	}
	rest := text[len(fence):]
	if strings.HasPrefix(rest, fence) {
		return nil, rest[len(fence):], nil
	}
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return nil, "", refuse(RuleCanonical, path, "has no --- line closing its frontmatter")
	}
	return strings.Split(rest[:end], "\n"), rest[end+1+len(fence):], nil
}

// readBody reads what follows the frontmatter leniently: one blank line
// and then the body. Whether it is written canonically is for the
// comparison with Render.
func readBody(rest string) string {
	return strings.TrimPrefix(rest, "\n")
}

// ParseKnot reads a knot's file. slug is the slug its filename carries,
// and path is named in a refusal. It refuses a file that breaks any rule
// about one knot alone; edges to other knots are the graph's to check.
func ParseKnot(path, slug string, data []byte) (*Knot, error) {
	k, err := parseKnot(path, slug, data)
	if err != nil {
		return nil, err
	}
	return k, nil
}

func parseKnot(path, slug string, data []byte) (*Knot, *Error) {
	if len(data) > MaxKnotSize {
		return nil, refuse(RuleLimit, path, fmt.Sprintf("is %d bytes, more than the %d a knot may be", len(data), MaxKnotSize))
	}
	if err := checkText(path, data); err != nil {
		return nil, err
	}
	lines, rest, err := frontmatter(path, data)
	if err != nil {
		return nil, err
	}
	k := &Knot{Slug: slug, Body: readBody(rest)}
	seen := map[string]bool{}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			key, value = strings.TrimSuffix(line, ":"), ""
		}
		f, known := FieldNamed(key)
		switch {
		case !known && keyShape.MatchString(key):
			return nil, refuse(RuleFieldOnType, path, fmt.Sprintf("holds the key %s, and a knot's keys are %s", key, strings.Join(FieldNames(""), ", ")))
		case !known:
			return nil, refuse(RuleCanonical, path, fmt.Sprintf("holds the line %q, and each line of a knot's frontmatter is one key and its value", line))
		}
		if seen[key] {
			return nil, refuse(RuleCanonical, path, fmt.Sprintf("names %s twice", key))
		}
		seen[key] = true
		if err := k.read(path, f, value); err != nil {
			return nil, err
		}
	}
	if err := k.check(path); err != nil {
		return nil, err
	}
	if k.Body != NormalBody(k.Body) {
		return nil, refuse(RuleCanonical, path, "its body opens or ends with a blank line, or does not end with a newline")
	}
	if want := Render(k); !bytes.Equal(want, data) {
		return nil, refuse(RuleCanonical, path, "is not in canonical form, which is:\n"+string(want))
	}
	return k, nil
}

// read sets one field from its written value.
func (k *Knot) read(path string, f Field, value string) *Error {
	var s string
	var list []string
	var err error
	switch f.Kind {
	case "list":
		list, err = readList(value)
	default:
		s, err = readScalar(value)
	}
	if err != nil {
		return refuse(RuleCanonical, path, fmt.Sprintf("%s: %v", f.Name, err))
	}
	switch f.Name {
	case "type":
		k.Type = s
	case "id":
		k.ID = s
	case "status":
		k.Status = s
	case "title":
		k.Title = s
	case "created":
		k.Created = s
	case "created_by":
		k.CreatedBy = s
	case "source":
		k.Source = s
	case "parent":
		k.Parent = s
	case "owner":
		k.Owner = s
	case "blocked_by":
		k.BlockedBy = list
	case "claimed_by":
		k.ClaimedBy = s
	case "claimed_at":
		k.ClaimedAt = s
	case "changes":
		k.Changes = list
	case "target":
		k.Target = s
	case "graduated_into":
		k.GraduatedInto = s
	}
	return nil
}

// readScalar reads a bare token or a JSON string.
func readScalar(value string) (string, error) {
	if strings.HasPrefix(value, `"`) {
		var s string
		if err := json.Unmarshal([]byte(value), &s); err != nil {
			return "", fmt.Errorf("%s is not a JSON string", value)
		}
		return s, nil
	}
	if value == "" || strings.ContainsAny(value, " \t[]{},#&*!|>'%@`") {
		return "", fmt.Errorf("%q is neither a bare token nor a JSON string", value)
	}
	return value, nil
}

// readList reads a flow list of bare tokens and JSON strings.
func readList(value string) ([]string, error) {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("%q is not a flow list, [a, b]", value)
	}
	inner := value[1 : len(value)-1]
	list := []string{}
	for len(inner) > 0 {
		var item string
		if strings.HasPrefix(inner, `"`) {
			end := 1
			for end < len(inner) && inner[end] != '"' {
				if inner[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(inner) {
				return nil, fmt.Errorf("%s holds an unclosed string", value)
			}
			if err := json.Unmarshal([]byte(inner[:end+1]), &item); err != nil {
				return nil, fmt.Errorf("%s holds a string that is not JSON", value)
			}
			inner = inner[end+1:]
		} else {
			cut := strings.Index(inner, ",")
			if cut < 0 {
				cut = len(inner)
			}
			item = strings.TrimSpace(inner[:cut])
			inner = inner[cut:]
			if item == "" {
				return nil, fmt.Errorf("%s holds an empty entry", value)
			}
		}
		list = append(list, item)
		inner = strings.TrimSpace(inner)
		if inner == "" {
			break
		}
		if !strings.HasPrefix(inner, ",") {
			return nil, fmt.Errorf("%s does not separate its entries with commas", value)
		}
		inner = strings.TrimSpace(inner[1:])
		if inner == "" {
			return nil, fmt.Errorf("%s ends with a comma", value)
		}
	}
	return list, nil
}

// Render writes a knot in its canonical form.
func Render(k *Knot) []byte {
	var b bytes.Buffer
	b.WriteString(fence)
	line := func(key, value string) { b.WriteString(key + ": " + value + "\n") }
	work := slices.Contains(workTypes, k.Type)
	line("type", k.Type)
	line("id", k.ID)
	line("status", k.Status)
	line("title", quote(k.Title))
	line("created", k.Created)
	line("created_by", quote(k.CreatedBy))
	if k.Source != "" {
		line("source", quote(k.Source))
	}
	if k.Parent != "" {
		line("parent", k.Parent)
	}
	if k.Owner != "" {
		line("owner", quote(k.Owner))
	}
	if work || len(k.BlockedBy) > 0 {
		line("blocked_by", renderList(k.BlockedBy))
	}
	if k.ClaimedBy != "" {
		line("claimed_by", quote(k.ClaimedBy))
	}
	if k.ClaimedAt != "" {
		line("claimed_at", quote(k.ClaimedAt))
	}
	if len(k.Changes) > 0 {
		line("changes", renderList(k.Changes))
	}
	if k.Target != "" {
		line("target", quote(k.Target))
	}
	if k.GraduatedInto != "" {
		line("graduated_into", k.GraduatedInto)
	}
	b.WriteString(fence)
	if k.Body != "" {
		b.WriteString("\n" + k.Body)
	}
	return b.Bytes()
}

// NormalBody is text as a knot's or a comment's body holds it: with no
// blank line at either end, no space at the end of the text, and one
// newline after its last line, or "" when it holds nothing.
func NormalBody(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderList(list []string) string {
	sorted := slices.Clone(list)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	items := make([]string, len(sorted))
	for i, item := range sorted {
		if IsLocalID(item) {
			items[i] = item
		} else {
			items[i] = quote(item)
		}
	}
	return "[" + strings.Join(items, ", ") + "]"
}

// quote writes s as a JSON string, leaving <, > and & as they are.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// ParseComment reads a comment's file:
//
//	---
//	author: "alice"
//	edited: "2026-10-25T15:00:00Z"
//	edited_by: "operator"
//	---
//
//	The comment, as markdown.
//
// author comes first. edited and edited_by follow it, in that order, on a
// comment that has been edited, and a comment holds both or neither. Each
// value is a JSON string. A comment that was never edited is the author
// line alone, so a comment written before edits existed reads as it
// always has. The body is required, and the form is as canonical as a
// knot's.
func ParseComment(path string, data []byte) (*Comment, error) {
	c, err := parseComment(path, data)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func parseComment(path string, data []byte) (*Comment, *Error) {
	if len(data) > MaxCommentSize {
		return nil, refuse(RuleLimit, path, fmt.Sprintf("is %d bytes, more than the %d a comment may be", len(data), MaxCommentSize))
	}
	if err := checkText(path, data); err != nil {
		return nil, err
	}
	lines, rest, err := frontmatter(path, data)
	if err != nil {
		return nil, err
	}
	c := &Comment{Body: readBody(rest)}
	fields := map[string]*string{"author": &c.Author, "edited": &c.Edited, "edited_by": &c.EditedBy}
	seen := map[string]bool{}
	for _, line := range lines {
		key, value, _ := strings.Cut(line, ": ")
		field := fields[key]
		switch {
		case field == nil:
			return nil, refuse(RuleCanonical, path, fmt.Sprintf("holds the line %q, and a comment's frontmatter is author: \"<principal>\", and on an edited comment edited: \"<UTC time>\" and edited_by: \"<principal>\" after it", line))
		case seen[key]:
			return nil, refuse(RuleCanonical, path, fmt.Sprintf("names %s twice", key))
		}
		seen[key] = true
		s, rerr := readScalar(value)
		if rerr != nil {
			return nil, refuse(RuleCanonical, path, key+": "+rerr.Error())
		}
		*field = s
	}
	switch {
	case !seen["author"]:
		return nil, refuse(RuleCanonical, path, "a comment's frontmatter opens with author: \"<principal>\"")
	case !IsName(c.Author):
		return nil, refuse(RuleFieldShape, path, fmt.Sprintf("its author %q is not a principal's name", c.Author))
	case seen["edited"] && !IsTimestamp(c.Edited):
		return nil, refuse(RuleFieldShape, path, fmt.Sprintf("edited is %q, and it is a UTC time, YYYY-MM-DDTHH:MM:SSZ", c.Edited))
	case seen["edited_by"] && !IsName(c.EditedBy):
		return nil, refuse(RuleFieldShape, path, fmt.Sprintf("edited_by is %q, and it is a principal's name", c.EditedBy))
	case seen["edited"] != seen["edited_by"]:
		return nil, refuse(RuleProvenance, path, "edited and edited_by come together: an edit names when it was made and who made it")
	case c.Body == "":
		return nil, refuse(RuleRequired, path, "a comment has a body")
	case c.Body != NormalBody(c.Body):
		return nil, refuse(RuleCanonical, path, "its body opens or ends with a blank line, or does not end with a newline")
	}
	if want := RenderComment(c); !bytes.Equal(want, data) {
		return nil, refuse(RuleCanonical, path, "is not in canonical form, which is:\n"+string(want))
	}
	return c, nil
}

// RenderComment writes a comment in its canonical form.
func RenderComment(c *Comment) []byte {
	front := "author: " + quote(c.Author) + "\n"
	if c.Edited != "" {
		front += "edited: " + quote(c.Edited) + "\n"
	}
	if c.EditedBy != "" {
		front += "edited_by: " + quote(c.EditedBy) + "\n"
	}
	return []byte(fence + front + fence + "\n" + c.Body)
}

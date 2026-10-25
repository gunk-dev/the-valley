package change

import (
	"bytes"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

const (
	id1   = "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz"
	head1 = HeadPrefix + "1111111111111111111111111111111111111111"
	head2 = HeadPrefix + "2222222222222222222222222222222222222222"
)

// record is a well-formed record with two versions, as text, so each case
// below can change one thing about it.
func record() string {
	return `{
  "schema": "valley-change-record/v1",
  "change_id": "` + id1 + `",
  "target": "main",
  "owner": "alice",
  "state": "ready",
  "seq": 2,
  "versions": [
    {"n": 1, "head": "` + head1 + `", "title": "the first", "branch": "topic", "uploader": "alice"},
    {"n": 2, "head": "` + head2 + `", "title": "the second", "branch": "", "uploader": "operator"}
  ],
  "active": 2
}
`
}

func TestAWellFormedRecordReadsWhole(t *testing.T) {
	r, err := Parse([]byte(record()))
	if err != nil {
		t.Fatal(err)
	}
	want := &Record{
		Schema: Schema, ChangeID: id1, Target: "main", Owner: "alice", State: Ready, Seq: 2,
		Versions: []Version{
			{N: 1, Head: head1, Title: "the first", Branch: "topic", Uploader: "alice"},
			{N: 2, Head: head2, Title: "the second", Branch: "", Uploader: "operator"},
		},
		Active: 2,
	}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("Parse = %+v, want %+v", r, want)
	}
	encoded, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(encoded)
	if err != nil || !reflect.DeepEqual(again, r) {
		t.Errorf("an encoded record read back as %+v, %v", again, err)
	}
	for _, state := range []string{Draft, Withdrawn} {
		r, err := Parse([]byte(strings.Replace(record(), `"state": "ready"`, `"state": "`+state+`"`, 1)))
		if err != nil || r.State != state {
			t.Errorf("a %s record read as %+v, %v", state, r, err)
		}
	}
}

// Each case changes one thing about a well-formed record, and each change
// is one the reading has to refuse: two readers that disagree about a
// record's contents could disagree about who owns it or what its versions
// are.
func TestTheReadingIsStrict(t *testing.T) {
	r := record()
	swap := func(from, to string) string {
		if !strings.Contains(r, from) {
			t.Fatalf("the record holds no %q to change", from)
		}
		return strings.Replace(r, from, to, 1)
	}
	for _, c := range []struct {
		what, text, says string
	}{
		// Encoding.
		{"bytes that are not UTF-8", swap("the first", "the \xff first"), "not valid UTF-8"},
		{"an overlong encoding", swap("the first", "the \xc0\xaf first"), "not valid UTF-8"},
		{"a lone surrogate escape", swap("the first", `the \ud800 first`), "surrogate"},
		{"a low surrogate escape alone", swap("the first", `the \udc00 first`), "surrogate"},
		{"an unknown escape", swap("the first", `the \x41 first`), "escape"},
		{"a raw control character", swap("the first", "the \x01 first"), "control character"},

		// Structure.
		{"a duplicate top-level key", swap(`"seq": 2,`, `"seq": 2, "seq": 2,`), `"seq" twice`},
		{"a duplicate key in a version", swap(`"n": 1,`, `"n": 1, "n": 1,`), `"n" twice`},
		{"a duplicate key under another spelling", swap(`"seq": 2,`, `"seq": 2, "seq": 2,`), `"seq" twice`},
		{"an unknown top-level field", swap(`"seq": 2,`, `"seq": 2, "extra": 1,`), `"extra"`},
		{"an unknown version field", swap(`"n": 1,`, `"n": 1, "note": "x",`), `"note"`},
		{"a missing field", swap(`"seq": 2,`, ``), `no field "seq"`},
		{"a missing version field", swap(`"branch": "topic", `, ``), `no field "branch"`},
		{"trailing data", r + "{}", "more after the value"},
		{"a second value", r + "1", "more after the value"},
		{"a trailing comma", swap(`"active": 2`, `"active": 2,`), "a key"},
		{"no object at all", `[]`, "where an object is expected"},
		{"a truncated record", r[:len(r)/2], "ends where"},
		{"nesting past the bound", strings.Repeat("[", 20) + strings.Repeat("]", 20), "nests values"},

		// Types.
		{"seq as a string", swap(`"seq": 2`, `"seq": "2"`), "where an integer is expected"},
		{"seq as a fraction", swap(`"seq": 2`, `"seq": 2.0`), "where an integer is expected"},
		{"seq with an exponent", swap(`"seq": 2`, `"seq": 2e0`), "where an integer is expected"},
		{"seq negative", swap(`"seq": 2`, `"seq": -2`), "where an integer is expected"},
		{"active as null", swap(`"active": 2`, `"active": null`), "where an integer is expected"},
		{"owner as a number", swap(`"owner": "alice"`, `"owner": 7`), "where a string is expected"},
		{"versions as an object", `{"schema": "valley-change-record/v1", "change_id": "` + id1 +
			`", "target": "main", "owner": "o", "state": "ready", "seq": 1, "versions": {}, "active": 1}`,
			"where an array is expected"},

		// Ranges.
		{"seq zero", swap(`"seq": 2`, `"seq": 0`), "outside 1 to 100000"},
		{"seq past its bound", swap(`"seq": 2`, `"seq": 100001`), "outside 1 to 100000"},
		{"seq past any integer", swap(`"seq": 2`, `"seq": 99999999999999999999999`), "outside 1 to 100000"},
		{"active past its bound", swap(`"active": 2`, `"active": 1001`), "outside 1 to 1000"},
		{"active naming no version", swap(`"active": 2`, `"active": 3`), "names none of its 2 versions"},
		{"n zero", swap(`"n": 1`, `"n": 0`), "outside 1 to 1000"},

		// Order.
		{"a skipped n", swap(`"n": 2`, `"n": 3`), "no gap"},
		{"versions out of order", swap(`"n": 1`, `"n": 2`), "no gap"},
		{"no version", `{"schema": "valley-change-record/v1", "change_id": "` + id1 +
			`", "target": "main", "owner": "o", "state": "ready", "seq": 1, "versions": [], "active": 1}`,
			"holds no version"},

		// Formats.
		{"another schema", swap(`change-record/v1`, `change-record/v2`), "its schema"},
		{"a short change ID", swap(id1, id1[:31]), "not a change ID"},
		{"a change ID outside k to z", swap(id1, "a"+id1[1:]), "not a change ID"},
		{"another state", swap(`"state": "ready"`, `"state": "merged"`), "its state"},
		{"an uppercase head", swap(head1, strings.ToUpper(head1)), "not git-sha1:"},
		{"a SHA-256 head", swap(head1, "git-sha256:"+strings.Repeat("1", 64)), "not git-sha1:"},
		{"an empty title", swap(`"the first"`, `""`), "title"},
		{"a title past 200 bytes", swap(`"the first"`, `"`+strings.Repeat("é", 101)+`"`), "title"},
		{"an escaped control character in a title", swap(`"the first"`, `"the\nfirst"`), "title"},
		{"a C1 control character in a title", swap(`"the first"`, `"the`+"\u0085"+`first"`), "title"},
		{"a branch git would refuse", swap(`"branch": "topic"`, `"branch": "to..pic"`), "not a branch name"},
		{"an owner that is no principal", swap(`"owner": "alice"`, `"owner": "-alice"`), "not a principal name"},
		{"an empty uploader", swap(`"uploader": "operator"`, `"uploader": ""`), "not a principal name"},
		{"a target that is no branch", swap(`"target": "main"`, `"target": "ma in"`), "its target"},
	} {
		_, err := Parse([]byte(c.text))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: error = %v, want one saying %q", c.what, err, c.says)
		}
	}

	// A title of exactly 200 bytes, and white space anywhere JSON allows
	// it, are fine.
	if _, err := Parse([]byte(swap(`"the first"`, `"`+strings.Repeat("é", 100)+`"`))); err != nil {
		t.Errorf("a 200-byte title: %v", err)
	}
	if _, err := Parse([]byte("\r\n\t " + strings.ReplaceAll(r, ": ", " :\t") + "\n\n")); err != nil {
		t.Errorf("white space: %v", err)
	}
}

func TestATitleIsMadeFromAnySubject(t *testing.T) {
	for subject, want := range map[string]string{
		"fix the hook":                 "fix the hook",
		"tab\there":                    "tab here",
		"bad\xffbyte":                  "bad�byte",
		"":                             "(no subject)",
		"\x01\x02":                     "(no subject)",
		strings.Repeat("é", 150):       strings.Repeat("é", 100),
		strings.Repeat("a", 199) + "é": strings.Repeat("a", 199),
	} {
		got := Title(subject)
		if got != want || !ValidTitle(got) {
			t.Errorf("Title(%q) = %q, want %q", subject, got, want)
		}
	}
}

func TestAHandleIsTheShortestUniquePrefix(t *testing.T) {
	ids := []string{
		"nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
		"nxvpzzzzqtsrzyoukklnpmqrstuvwxyz",
		"nxvpzzzyqtsrzyoukklnpmqrstuvwxyz",
		"kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk",
	}
	for id, want := range map[string]string{
		ids[0]: "nxvpw",
		ids[1]: "nxvpzzzz",
		ids[2]: "nxvpzzzy",
		ids[3]: "kkkk",
	} {
		if got := Handle(id, ids); got != want {
			t.Errorf("Handle(%s) = %s, want %s", id, got, want)
		}
		if got := Matching(want, ids); len(got) != 1 || got[0] != id {
			t.Errorf("Matching(%s) = %v, want %s alone", want, got, id)
		}
	}
	if got := Matching("nxvp", ids); len(got) != 3 {
		t.Errorf("Matching(nxvp) = %v, want three", got)
	}
	for _, name := range []string{"nxv", "topic", "nxvp/x", ids[0] + "k"} {
		if got := Matching(name, ids); got != nil {
			t.Errorf("Matching(%q) = %v, want nothing", name, got)
		}
	}
}

func TestANewIDIsInJJsForm(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := NewID()
		if err != nil || !ValidID(id) || seen[id] {
			t.Fatalf("NewID = %q, %v", id, err)
		}
		seen[id] = true
	}
}

// ValidBranch stands in for `git check-ref-format refs/heads/<name>`, and a
// ref name check is wrong exactly at git's edge cases, so it is held to git
// over each of them.
func TestValidBranchAgreesWithGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git to compare with")
	}
	names := []string{
		"main", "topic/one", "a/b/c", "-dash", "@", "a@b", "a@{b", "@{", "a.b", ".a", "a/.b",
		"a.", "a/b.", "a..b", "a.lock", "a/b.lock", "a.lock/b", "a.lockx", "a//b", "/a", "a/",
		"a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b", "a\tb", "a\x7fb", "a\x01b",
		"café", "日本", "a{b", "a}b", "a!b", "a#b", "a'b", "a\"b", "a,b", "a;b", "a|b", "a=b",
		"...", ".", "..", "a/..", "a/./b", "x.lock.lock", "@/a", "a/@", "HEAD", "-", "a\xffb",
	}
	for c := byte(0x20); c < 0x7f; c++ {
		names = append(names, string(c), "a"+string(c)+"b", string(c)+"z", "z"+string(c))
	}
	for _, name := range names {
		err := exec.Command(git, "check-ref-format", "refs/heads/"+name).Run()
		if got, want := ValidBranch(name), err == nil; got != want {
			t.Errorf("ValidBranch(%q) = %v; git check-ref-format says %v", name, got, want)
		}
	}
	if ValidBranch("") {
		t.Error("an empty branch name was accepted")
	}
}

func TestARefNamesItsTargetAndChange(t *testing.T) {
	for ref, ok := range map[string]bool{
		Ref("main", id1):                         true,
		Ref("release/1", id1):                    true,
		Ref("main", id1[:31]):                    false,
		Ref("main", id1) + "/x":                  false,
		RefPrefix + id1:                          false,
		Ref("ma..in", id1):                       false,
		"refs/the-valley/change/main/" + id1:     false,
		"refs/the-valley/changes//" + id1:        false,
		fmt.Sprintf("%smain/%s", RefPrefix, id1): true,
	} {
		if _, _, got := ParseRef(ref); got != ok {
			t.Errorf("ParseRef(%q) ok = %v, want %v", ref, got, ok)
		}
	}
}

func TestEncodeRefusesWhatParseWould(t *testing.T) {
	r, err := Parse([]byte(record()))
	if err != nil {
		t.Fatal(err)
	}
	r.Versions[1].N = 3
	if _, err := Encode(r); err == nil || !strings.Contains(err.Error(), "no gap") {
		t.Errorf("Encode of a record with a gap: %v", err)
	}
	r.Versions[1].N = 2
	r.Versions[1].Branch = "to\xffpic"
	if _, err := Encode(r); err == nil || !strings.Contains(err.Error(), "not a branch name in UTF-8") {
		t.Errorf("Encode of a branch that is not UTF-8: %v", err)
	}
	r.Versions[1].Branch = ""
	r.Versions[1].Title = "<&>"
	out, err := Encode(r)
	if err != nil || !bytes.Contains(out, []byte(`"<&>"`)) {
		t.Errorf("Encode escaped a title it need not: %s, %v", out, err)
	}
}

// A request name of the form c-<change ID> is reserved for the change with
// that ID, and nothing else is: the integrator reads a request so named as
// that change's, and a name one letter off is a branch's.
func TestOnlyCAndAChangeIDIsAChangesRequestName(t *testing.T) {
	if got := RequestName(id1); got != "c-"+id1 {
		t.Fatalf("RequestName = %q", got)
	}
	if id, ok := RequestID(RequestName(id1)); !ok || id != id1 {
		t.Fatalf("RequestID(RequestName) = %q, %v", id, ok)
	}
	for _, name := range []string{
		id1,                  // no prefix
		"c-" + id1[:31],      // 31 letters
		"c-" + id1 + "k",     // 33 letters
		"c-" + "a" + id1[1:], // a letter before k
		"C-" + id1,           // another prefix
		"c-" + strings.ToUpper(id1),
		"topic-c-" + id1,
	} {
		if _, ok := RequestID(name); ok {
			t.Errorf("RequestID(%q) reads a change ID", name)
		}
	}
}

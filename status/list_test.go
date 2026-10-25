package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"the-valley/change"
)

// A project's URL keeps the instance's transport, user and host, and
// replaces the last path segment alone.
func TestProjectURL(t *testing.T) {
	for _, c := range []struct{ instance, want string }{
		{"git@valley-host:valley.git", "git@valley-host:app.git"},
		{"git@valley-host:valley", "git@valley-host:app"},
		{"valley-host:srv/git/valley.git", "valley-host:srv/git/app.git"},
		{"git@valley-host:/srv/git/valley.git/", "git@valley-host:/srv/git/app.git"},
		{"ssh://git@valley-host/valley.git", "ssh://git@valley-host/app.git"},
		{"ssh://git@valley-host:2222/srv/git/valley.git", "ssh://git@valley-host:2222/srv/git/app.git"},
		{"ssh://git@[fe80::1]:22/valley.git", "ssh://git@[fe80::1]:22/app.git"},
		{"https://git.example/valley/valley", "https://git.example/valley/app"},
		{"file:///srv/git/valley.git", "file:///srv/git/app.git"},
		{"/srv/git/valley.git", "/srv/git/app.git"},
		{"./a:b/valley.git", "./a:b/app.git"},
	} {
		got, err := projectURL(c.instance, "app")
		if err != nil || got != c.want {
			t.Errorf("projectURL(%q) = %q, %v; want %q", c.instance, got, err, c.want)
		}
	}
	for _, bad := range []string{"ssh://valley-host", "ssh://valley-host/", "git@valley-host:", "git@valley-host:.git"} {
		if got, err := projectURL(bad, "app"); err == nil {
			t.Errorf("projectURL(%q) = %q, where it has no path to replace", bad, got)
		}
	}
}

func TestHostOf(t *testing.T) {
	for url, want := range map[string]string{
		"git@valley-host:app.git":                 "valley-host",
		"ssh://git@valley-host:2222/app.git":      "valley-host:2222",
		"https://user@git.example/valley/app.git": "git.example",
		"/srv/git/app.git":                        "localhost",
		"file:///srv/git/app.git":                 "localhost",
	} {
		if got := hostOf(url); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", url, got, want)
		}
	}
}

// pointerURL reads policy/valley as bin/valley's pointer_url does.
func TestPointerURL(t *testing.T) {
	got := pointerURL("# the owning valley\n\n  git@valley-host:valley.git  # floor\nother\n")
	if got != "git@valley-host:valley.git" {
		t.Errorf("pointerURL = %q", got)
	}
	if got := pointerURL("# nothing\n\n"); got != "" {
		t.Errorf("pointerURL of comments alone = %q", got)
	}
}

// Every handle among changes listed from several repositories is unique,
// and minimal: one letter shorter, down to four, and another change
// listed shares it.
func TestHandlesUniqueAndMinimal(t *testing.T) {
	app := []string{
		"nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
		"nxvpwlzzqtsrzyoukklnpmqrstuvwxyz",
		"xunxkkkkkkkkkkkkkkkkkkkkkkkkkkkk",
	}
	valley := []string{
		"nxvpwlkmqtsrzyoukklnpmqrstuvwxyk", // shares 31 letters with app's first
		"xunykkkkkkkkkkkkkkkkkkkkkkkkkkkk",
		"qqqqkkkkkkkkkkkkkkkkkkkkkkkkkkkk",
	}
	repos := []listed{
		{source: source{name: "app"}, snap: &snapshot{all: recordsOf(app)}},
		{source: source{name: "valley"}, snap: &snapshot{all: recordsOf(valley)}},
		{source: source{name: "ghost", err: errUnread}},
	}
	got := handles(repos)
	h := map[string]string{}
	for _, id := range app {
		h[id] = got[handleKey{"app", "main", id}]
	}
	for _, id := range valley {
		h[id] = got[handleKey{"valley", "main", id}]
	}
	all := append(append([]string(nil), app...), valley...)
	seen := map[string]string{}
	for _, id := range all {
		handle := h[id]
		if !strings.HasPrefix(id, handle) || len(handle) < 4 {
			t.Fatalf("%s has the handle %q", id, handle)
		}
		if other, ok := seen[handle]; ok {
			t.Fatalf("%s and %s share the handle %s", id, other, handle)
		}
		seen[handle] = id
		for _, other := range all {
			if other != id && strings.HasPrefix(other, handle) {
				t.Fatalf("the handle %s of %s is a prefix of %s too", handle, id, other)
			}
		}
		if shorter := handle[:len(handle)-1]; len(shorter) >= 4 {
			shared := false
			for _, other := range all {
				shared = shared || other != id && strings.HasPrefix(other, shorter)
			}
			if !shared {
				t.Errorf("the handle %s of %s is not minimal: %s names it alone", handle, id, shorter)
			}
		}
	}
	if h["xunxkkkkkkkkkkkkkkkkkkkkkkkkkkkk"] != "xunx" || h["qqqqkkkkkkkkkkkkkkkkkkkkkkkkkkkk"] != "qqqq" {
		t.Errorf("four-letter handles grew: %v", h)
	}
	if got := h["nxvpwlkmqtsrzyoukklnpmqrstuvwxyz"]; len(got) != 32 {
		t.Errorf("an ID sharing 31 letters with another listed elsewhere has the handle %s", got)
	}
}

// One change ID recorded in two repositories has a handle qualified by
// its repository in each, and the other changes keep theirs.
func TestHandlesQualifiedAcrossRepos(t *testing.T) {
	shared := "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz"
	repos := []listed{
		{source: source{name: "app"}, snap: &snapshot{all: recordsOf([]string{shared, "xunxkkkkkkkkkkkkkkkkkkkkkkkkkkkk"})}},
		{source: source{name: "origin"}, snap: &snapshot{all: recordsOf([]string{shared})}},
	}
	h := handles(repos)
	want := handleMap{
		{"app", "main", shared}:                             "app:nxvp",
		{"origin", "main", shared}:                          "origin:nxvp",
		{"app", "main", "xunxkkkkkkkkkkkkkkkkkkkkkkkkkkkk"}: "xunx",
	}
	if len(h) != len(want) {
		t.Fatalf("handles = %v, want %v", h, want)
	}
	for k, v := range want {
		if h[k] != v {
			t.Errorf("the handle of %v is %q, want %q", k, h[k], v)
		}
	}
}

// A URL from data that git would read as an option, or that names a
// remote helper or another scheme, is refused. ssh, git, https, file,
// scp-like and local URLs are read.
func TestCheckURL(t *testing.T) {
	for _, ok := range []string{
		"git@valley-host:valley.git", "valley-host:valley", "ssh://git@valley-host:22/valley.git",
		"ssh://git@[::1]/valley.git", "git://valley-host/valley.git", "https://git.example/valley",
		"file:///srv/git/valley.git", "/srv/git/valley.git", "./valley.git",
	} {
		if err := checkURL(ok); err != nil {
			t.Errorf("checkURL(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "--upload-pack=touch /tmp/pwned; git-upload-pack", "-oProxyCommand=sh:x",
		"ext::sh -c touch% /tmp/pwned", "fd::0", "remote-x::address", "http://git.example/valley",
		"ftp://git.example/valley", "ssh://-oProxyCommand=sh/valley", "git@host:q\nvalley", "git@host:q\x1bvalley",
	} {
		if err := checkURL(bad); err == nil {
			t.Errorf("checkURL(%q) let it through", bad)
		}
	}
	if url, err := projectURL("ext::sh -c x/valley", "app"); err == nil {
		t.Errorf("projectURL derived %q from a remote helper's URL", url)
	}
}

var errUnread = failure("could not read")

func recordsOf(ids []string) []recorded {
	var all []recorded
	for _, id := range ids {
		all = append(all, recorded{id: id, record: &change.Record{Target: "main"}})
	}
	return all
}

// listingWith is a valley-changes/v1 document holding one change whose one
// version has the title and branch given.
func listingWith(title, branch string) jsonChanges {
	tip := strings.Repeat("b", 40)
	return jsonChanges{Schema: changesSchema, Host: "valley-host", Repos: []jsonRepo{{
		Repo: "app", URL: "git@valley-host:app.git",
		Changes: []jsonChange{{
			ChangeID: "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz", Handle: "nxvp", Target: "main",
			Owner: "alice", State: "ready", Record: strings.Repeat("c", 40), Seq: 1, Active: 1,
			Versions: []jsonVersion{{
				N: 1, Head: strings.Repeat("a", 40), Title: title, Branch: branch, Uploader: "alice",
				Landing: notLanded, Outcome: &versionOutcome{Decision: "failed", Head: strings.Repeat("a", 40), Tip: &tip},
			}},
			Includes: []string{},
		}},
		Drafts:   &[]jsonChange{{ChangeID: "qqqqkkkkkkkkkkkkkkkkkkkkkkkkkkkk", Handle: "qqqq", Target: "main", Owner: "alice", State: "draft", Record: tip, Seq: 2, Active: 1, Versions: []jsonVersion{{N: 1, Head: tip, Title: title, Branch: branch, Uploader: "alice", Landing: notLanded}}, Includes: []string{}}},
		Branches: []jsonBranch{{Branch: branch, Head: tip}},
	}}}
}

// The writer puts out one line, with no control character in it, that
// reads back as exactly the document written. A string that is not UTF-8
// is refused rather than written as another one.
func FuzzWriteDocument(f *testing.F) {
	for _, seed := range [][2]string{
		{"fix the frobnicator", "topic"},
		{"tab\there, newline\nthere, nul\x00, del\x7f, esc\x1b[31m", "a/b"},
		{"non-ASCII: café, 漢字, emoji 🎉, U+2028   and U+2029  ", "zweig/ä"},
		{"quotes \" and backslash \\ and <html> & ampersand", "x"},
		{"� replacement already there", "y"},
		{"not UTF-8: \xff\xfe", "z"},
		{"title", "branch\xc3"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, title, branch string) {
		doc := listingWith(title, branch)
		var buf bytes.Buffer
		err := writeDocument(&buf, doc)
		if !utf8.ValidString(title) || !utf8.ValidString(branch) {
			if err == nil {
				t.Fatalf("a string that is not UTF-8 was written: %q", buf.String())
			}
			return
		}
		if err != nil {
			t.Fatalf("writeDocument: %v", err)
		}
		out := buf.Bytes()
		if len(out) == 0 || out[len(out)-1] != '\n' || bytes.Count(out, []byte("\n")) != 1 {
			t.Fatalf("not one line: %q", out)
		}
		for _, c := range out[:len(out)-1] {
			if c < 0x20 {
				t.Fatalf("a raw control character %q in %q", c, out)
			}
		}
		var back jsonChanges
		dec := json.NewDecoder(bytes.NewReader(out))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&back); err != nil {
			t.Fatalf("does not read back: %v: %q", err, out)
		}
		v := back.Repos[0].Changes[0].Versions[0]
		d := (*back.Repos[0].Drafts)[0].Versions[0]
		if v.Title != title || v.Branch != branch || back.Repos[0].Branches[0].Branch != branch || d.Title != title || d.Branch != branch {
			t.Fatalf("read back as %q, %q, where %q, %q was written", v.Title, v.Branch, title, branch)
		}
		if back.Schema != changesSchema || v.LandedCommit != nil || v.Request != nil || *v.Outcome.Tip != strings.Repeat("b", 40) {
			t.Fatalf("the fields around the strings moved: %q", out)
		}
	})
}

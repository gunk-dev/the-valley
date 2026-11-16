package approval

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"the-valley/note"
)

const keysBelow = "# Compiled from the identity registry — do not edit.\nexample-operator+3649fd12+AT/3+vgQhY5cdQ4AqspQ1A2yW60nn4j1FOyx04WfuQjc\n"

func midnight(t *testing.T, day string) time.Time {
	t.Helper()
	d, err := time.Parse(dayLayout, day)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A stamp is written in one form, and reads back as the Authority it was
// written from: with a SHA-1 or a SHA-256 commit, and with no provenance
// at all. The keys below it read as they do without it.
func TestAStampReadsBackAsWritten(t *testing.T) {
	day := midnight(t, "2026-11-16")
	for _, c := range []struct {
		a    Authority
		line string
	}{
		{Authority{Ref: "refs/heads/main", Commit: strings.Repeat("5f", 20), Day: day},
			"# valley-approval-authority-v1 ref=refs/heads/main commit=" + strings.Repeat("5f", 20) + " day=2026-11-16\n"},
		{Authority{Ref: "refs/heads/registry", Commit: strings.Repeat("0a", 32), Day: day},
			"# valley-approval-authority-v1 ref=refs/heads/registry commit=" + strings.Repeat("0a", 32) + " day=2026-11-16\n"},
		{Authority{Day: day}, "# valley-approval-authority-v1 ref=none commit=none day=2026-11-16\n"},
	} {
		line, err := c.a.Stamp()
		if err != nil {
			t.Fatalf("%+v: %v", c.a, err)
		}
		if line != c.line {
			t.Errorf("%+v stamps %q, want %q", c.a, line, c.line)
		}
		file := []byte(line + keysBelow)
		got, err := ReadAuthority(bytes.NewReader(file))
		if err != nil || got != c.a {
			t.Errorf("%q reads as %+v, %v", line, got, err)
		}
		if string(StripAuthority(file)) != keysBelow {
			t.Errorf("stripping %q leaves %q", line, StripAuthority(file))
		}
		keys, err := note.ParseVerifierKeys(file)
		if err != nil || len(keys) != 1 || keys[0].Name != "example-operator" {
			t.Errorf("the keys below %q read as %v, %v", line, keys, err)
		}
	}
}

// Stamp writes nothing ReadAuthority would refuse.
func TestAStampThatWouldNotReadIsNotWritten(t *testing.T) {
	day := midnight(t, "2026-11-16")
	commit := strings.Repeat("5f", 20)
	for what, a := range map[string]Authority{
		"a short ref":                {Ref: "main", Commit: commit, Day: day},
		"a ref holding a space":      {Ref: "refs/heads/a b", Commit: commit, Day: day},
		"a ref too long for a stamp": {Ref: "refs/heads/" + strings.Repeat("a", MaxStamp), Commit: commit, Day: day},
		"a ref with no commit":       {Ref: "refs/heads/main", Day: day},
		"a commit with no ref":       {Commit: commit, Day: day},
		"an abbreviated commit":      {Ref: "refs/heads/main", Commit: commit[:12], Day: day},
		"an uppercase commit":        {Ref: "refs/heads/main", Commit: strings.ToUpper(commit), Day: day},
		"a day that is not midnight": {Ref: "refs/heads/main", Commit: commit, Day: day.Add(time.Hour)},
	} {
		if line, err := a.Stamp(); err == nil {
			t.Errorf("%s was stamped %q", what, line)
		}
	}
}

// A file is refused unless its first line is one stamp of this version
// that reads, and no other line opens as a stamp.
func TestAStampThatDoesNotReadIsRefused(t *testing.T) {
	commit := strings.Repeat("5f", 20)
	good := "# valley-approval-authority-v1 ref=refs/heads/main commit=" + commit + " day=2026-11-16\n"
	if _, err := ReadAuthority(strings.NewReader(good + keysBelow)); err != nil {
		t.Fatal(err)
	}
	for what, file := range map[string]string{
		"no stamp":                    keysBelow,
		"an empty file":               "",
		"the stamp after the header":  "# Compiled\n" + good,
		"a stamp of a later version":  "# valley-approval-authority-v2 ref=refs/heads/main commit=" + commit + " day=2026-11-16\n",
		"a stamp with no newline":     strings.TrimSuffix(good, "\n"),
		"a stamp closed by CRLF":      strings.TrimSuffix(good, "\n") + "\r\n",
		"the fields out of order":     "# valley-approval-authority-v1 commit=" + commit + " ref=refs/heads/main day=2026-11-16\n",
		"two spaces between fields":   "# valley-approval-authority-v1 ref=refs/heads/main  commit=" + commit + " day=2026-11-16\n",
		"a trailing space":            strings.TrimSuffix(good, "\n") + " \n",
		"a field more":                strings.TrimSuffix(good, "\n") + " by=operator\n",
		"a field missing":             "# valley-approval-authority-v1 ref=refs/heads/main commit=" + commit + "\n",
		"a short ref":                 "# valley-approval-authority-v1 ref=main commit=" + commit + " day=2026-11-16\n",
		"a ref ending in .lock":       "# valley-approval-authority-v1 ref=refs/heads/main.lock commit=" + commit + " day=2026-11-16\n",
		"an abbreviated commit":       "# valley-approval-authority-v1 ref=refs/heads/main commit=5f5f5f5f day=2026-11-16\n",
		"an uppercase commit":         "# valley-approval-authority-v1 ref=refs/heads/main commit=" + strings.ToUpper(commit) + " day=2026-11-16\n",
		"a ref named and no commit":   "# valley-approval-authority-v1 ref=refs/heads/main commit=none day=2026-11-16\n",
		"a commit named and no ref":   "# valley-approval-authority-v1 ref=none commit=" + commit + " day=2026-11-16\n",
		"a day that is not a day":     "# valley-approval-authority-v1 ref=refs/heads/main commit=" + commit + " day=2026-02-30\n",
		"a day written another way":   "# valley-approval-authority-v1 ref=refs/heads/main commit=" + commit + " day=2026-11-16T00:00:00Z\n",
		"a day with no leading zeros": "# valley-approval-authority-v1 ref=refs/heads/main commit=" + commit + " day=2026-1-6\n",
	} {
		if a, err := ReadAuthority(strings.NewReader(file)); err == nil {
			t.Errorf("%s read as %+v", what, a)
		}
	}
}

// endless is a stream that never ends, of one byte over and over, after
// what it opens with. reads counts the bytes taken from it.
type endless struct {
	opening []byte
	fill    byte
	reads   int
}

func (e *endless) Read(p []byte) (int, error) {
	n := copy(p, e.opening)
	e.opening = e.opening[n:]
	for i := n; i < len(p); i++ {
		p[i] = e.fill
	}
	e.reads += len(p)
	return len(p), nil
}

// The stamp is read from the first MaxStamp bytes alone, however large the
// file. A stamp followed by a file that never ends reads, and a first line
// that never ends is refused, each after at most MaxStamp bytes. A second
// line opening like a stamp is not looked for: the first line is the
// stamp.
func TestTheStampIsReadFromTheFirstLineAlone(t *testing.T) {
	good := "# valley-approval-authority-v1 ref=refs/heads/main commit=" + strings.Repeat("5f", 20) + " day=2026-11-16\n"
	file := &endless{opening: []byte(good + keysBelow), fill: '\n'}
	if a, err := ReadAuthority(file); err != nil || a.Ref != "refs/heads/main" {
		t.Errorf("a stamped file that never ends reads as %+v, %v", a, err)
	}
	if file.reads > MaxStamp {
		t.Errorf("reading the stamp took %d bytes", file.reads)
	}
	line := &endless{opening: []byte("# valley-approval-authority-v1 ref=refs/heads/"), fill: 'a'}
	if a, err := ReadAuthority(line); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Errorf("a first line that never ends reads as %+v, %v", a, err)
	}
	if line.reads > MaxStamp {
		t.Errorf("refusing a long first line took %d bytes", line.reads)
	}
	if a, err := ReadAuthority(strings.NewReader(good + good)); err != nil || a.Commit != strings.Repeat("5f", 20) {
		t.Errorf("a stamp with another below it reads as %+v, %v", a, err)
	}
	// Reading the stamp leaves the rest of the file to be read.
	r := strings.NewReader(good + keysBelow)
	if _, err := ReadAuthority(r); err != nil {
		t.Fatal(err)
	}
	if rest, err := io.ReadAll(r); err != nil || string(rest) != keysBelow {
		t.Errorf("after the stamp, the file holds %q, %v", rest, err)
	}
	// A stream whose first line has arrived is not read further: a reader
	// that fails on the next read, as a pipe whose writer stays open would
	// block on it, still reads.
	if a, err := ReadAuthority(&oneLine{line: []byte(good)}); err != nil || a.Ref != "refs/heads/main" {
		t.Errorf("a stamp followed by a failing read reads as %+v, %v", a, err)
	}
	if _, err := ReadAuthority(&oneLine{line: []byte("# valley-approval-authority-v1 ref=refs/heads/main")}); err == nil || !strings.Contains(err.Error(), "the stream stalls") {
		t.Errorf("a stream that fails before its first newline: %v", err)
	}
}

// A stamp whose newline is byte MaxStamp reads. One a byte longer, whose
// newline would be byte MaxStamp+1, is refused after MaxStamp bytes, with
// nothing past them read.
func TestTheStampEndsWithinMaxStampBytes(t *testing.T) {
	commit := strings.Repeat("5f", 20)
	line := func(n int) string {
		t.Helper()
		short := "# valley-approval-authority-v1 ref=refs/heads/ commit=" + commit + " day=2026-11-16\n"
		return strings.Replace(short, "refs/heads/", "refs/heads/"+strings.Repeat("a", n-len(short)), 1)
	}
	at := line(MaxStamp)
	if len(at) != MaxStamp || at[MaxStamp-1] != '\n' {
		t.Fatalf("the line is %d bytes, not %d ending in its newline", len(at), MaxStamp)
	}
	a, err := ReadAuthority(strings.NewReader(at + keysBelow))
	if err != nil {
		t.Fatalf("a stamp of %d bytes: %v", MaxStamp, err)
	}
	if again, err := a.Stamp(); err != nil || again != at {
		t.Errorf("a stamp of %d bytes is written back as %q, %v", MaxStamp, again, err)
	}
	past := &endless{opening: []byte(line(MaxStamp + 1)), fill: '\n'}
	if a, err := ReadAuthority(past); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Errorf("a stamp of %d bytes reads as %+v, %v", MaxStamp+1, a, err)
	}
	if past.reads != MaxStamp {
		t.Errorf("refusing a stamp of %d bytes took %d bytes, not %d", MaxStamp+1, past.reads, MaxStamp)
	}
}

// A reader that keeps giving nothing, and no error, is given up on.
func TestAReaderThatGivesNothingIsGivenUpOn(t *testing.T) {
	if a, err := ReadAuthority(nothing{}); !errors.Is(err, io.ErrNoProgress) {
		t.Errorf("a reader that gives nothing reads as %+v, %v", a, err)
	}
}

// nothing gives no bytes and no error, every time.
type nothing struct{}

func (nothing) Read([]byte) (int, error) { return 0, nil }

// oneLine gives its line, and fails every read after it.
type oneLine struct {
	line []byte
}

func (o *oneLine) Read(p []byte) (int, error) {
	if len(o.line) == 0 {
		return 0, errors.New("the stream stalls")
	}
	n := copy(p, o.line)
	o.line = o.line[n:]
	return n, nil
}

// Stripping drops the stamp line of this version and nothing else: a file
// that does not open with one is kept byte for byte.
func TestStrippingDropsTheStampLineAlone(t *testing.T) {
	for _, file := range []string{
		keysBelow,
		"",
		"# Compiled\n# valley-approval-authority-v1 ref=none commit=none day=2026-11-16\n",
		"# valley-approval-authority-v2 ref=none commit=none day=2026-11-16\n" + keysBelow,
	} {
		if got := string(StripAuthority([]byte(file))); got != file {
			t.Errorf("stripping %q left %q", file, got)
		}
	}
	if got := string(StripAuthority([]byte("# valley-approval-authority-v1 ref=none commit=none day=2026-11-16"))); got != "" {
		t.Errorf("stripping a lone stamp left %q", got)
	}
	// A stamp line closed by CRLF is dropped whole, carriage return
	// included, though it does not read as a stamp.
	crlf := "# valley-approval-authority-v1 ref=none commit=none day=2026-11-16\r\n"
	if got := string(StripAuthority([]byte(crlf + keysBelow))); got != keysBelow {
		t.Errorf("stripping a CRLF stamp left %q", got)
	}
	// A first line longer than a stamp is no stamp line, and is kept.
	long := "# valley-approval-authority-v1 ref=refs/heads/" + strings.Repeat("a", 2*MaxStamp) + "\n" + keysBelow
	if got := string(StripAuthority([]byte(long))); got != long {
		t.Errorf("stripping a first line of %d bytes left %d bytes", len(long), len(got))
	}
}

// A rendering is fresh while the stamped ref still holds the stamped
// commit and the day is the stamped day, at any hour of it. Another
// commit, the day before, the day after, and no provenance are stale.
func TestFreshnessIsTheCommitAndTheDay(t *testing.T) {
	commit, other := strings.Repeat("5f", 20), strings.Repeat("6e", 20)
	a := Authority{Ref: "refs/heads/main", Commit: commit, Day: midnight(t, "2026-11-16")}
	at := func(s string) time.Time {
		t.Helper()
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	for _, now := range []string{"2026-11-16T00:00:00Z", "2026-11-16T23:59:59Z", "2026-11-16T20:00:00-03:00"} {
		if err := a.Fresh(commit, at(now)); err != nil {
			t.Errorf("at %s: %v", now, err)
		}
	}
	for what, c := range map[string]struct {
		a       Authority
		current string
		now     string
	}{
		"another commit":        {a, other, "2026-11-16T12:00:00Z"},
		"the day before":        {a, commit, "2026-11-15T23:59:59Z"},
		"the day after":         {a, commit, "2026-11-17T00:00:00Z"},
		"the day after, in UTC": {a, commit, "2026-11-16T22:00:00-03:00"},
		"no provenance":         {Authority{Day: a.Day}, "", "2026-11-16T12:00:00Z"},
	} {
		if err := c.a.Fresh(c.current, at(c.now)); err == nil {
			t.Errorf("%s is fresh", what)
		}
	}
}

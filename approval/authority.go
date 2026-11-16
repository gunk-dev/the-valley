package approval

// The authority stamp: the first line of each approval signers file the
// identity compiler writes, saying which registry the keys below it were
// compiled from and for which day.
//
//	# valley-approval-authority-v1 ref=refs/heads/main commit=5f0c… day=2026-11-16
//
// ref is the full ref name the registry was read at, commit the commit
// that ref held when the compiler resolved it, and day the UTC day expiry
// was judged against. Every key in the file is read from that one commit
// and judged on that one day, so the stamp says exactly what the file is a
// rendering of. A file with no commit to name says ref=none commit=none.
//
// The line is a comment to the note format, so every reader of verifier
// keys reads a stamped file as it reads the keys alone
// (note.ParseVerifierKeys).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"the-valley/change"
)

const (
	// AuthorityStamp opens the stamp line, version included.
	AuthorityStamp = "# valley-approval-authority-v1 "

	// authorityFamily opens every version's stamp, so a later version's
	// line is told from a comment and refused as unsupported.
	authorityFamily = "# valley-approval-authority-"

	// MaxStamp is the most bytes a stamp line holds, its newline
	// included. A reader of the stamp reads no more of a file than this,
	// however large the file or its first line.
	MaxStamp = 512

	// maxEmptyReads is how many reads in a row may give nothing before a
	// reader of the stamp gives up, as bufio's readers do.
	maxEmptyReads = 100

	// noProvenance stands for the ref and the commit of a file compiled
	// from no commit.
	noProvenance = "none"

	dayLayout = "2006-01-02"
)

// Authority is what a stamp says.
type Authority struct {
	// Ref is the full ref name the registry was read at, and Commit the
	// full object ID of the commit it held. Both are "" for a file with
	// no commit provenance.
	Ref, Commit string

	// Day is the UTC day the compilation judged expiry on, at midnight.
	Day time.Time
}

// Stamp writes the stamp line, closing newline included. It refuses an
// Authority ReadAuthority would not read back.
func (a Authority) Stamp() (string, error) {
	ref, commit := a.Ref, a.Commit
	if ref == "" && commit == "" {
		ref, commit = noProvenance, noProvenance
	}
	line := fmt.Sprintf("%sref=%s commit=%s day=%s\n", AuthorityStamp, ref, commit, a.Day.UTC().Format(dayLayout))
	if len(line) > MaxStamp {
		return "", fmt.Errorf("the authority stamp would be %d bytes, more than the %d a stamp holds", len(line), MaxStamp)
	}
	got, err := parseStamp(strings.TrimSuffix(line, "\n"))
	if err != nil {
		return "", err
	}
	if !got.Day.Equal(a.Day) {
		return "", fmt.Errorf("the day %s is not a UTC midnight", a.Day.Format(time.RFC3339))
	}
	return line, nil
}

// ReadAuthority reads the stamp of an approval signers file from r: its
// first line, closed by a newline. It reads one byte at a time and stops
// at the newline, so r holds the rest of the file afterwards, unread. It
// reads at most MaxStamp bytes, refusing a first line longer than that
// without reading on. A file with no stamp, a later version's stamp, and a
// stamp that does not read are refused. The stamp is the first line, and
// a later line opening like one is a comment to every reader, this one
// included.
func ReadAuthority(r io.Reader) (Authority, error) {
	head := make([]byte, 0, MaxStamp)
	one := make([]byte, 1)
	for empty := 0; len(head) < MaxStamp; {
		n, err := r.Read(one)
		head = append(head, one[:n]...)
		if n == 0 && err == nil {
			// A reader that keeps giving nothing is given up on, as the io
			// readers that wrap another give up on one.
			if empty++; empty == maxEmptyReads {
				return Authority{}, io.ErrNoProgress
			}
			continue
		}
		empty = 0
		if n == 1 && one[0] == '\n' {
			break
		} else if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return Authority{}, err
		}
	}
	first, _, closed := bytes.Cut(head, []byte("\n"))
	line := string(first)
	switch {
	case !strings.HasPrefix(line, authorityFamily):
		return Authority{}, fmt.Errorf("the file does not open with an authority stamp")
	case !strings.HasPrefix(line, AuthorityStamp):
		return Authority{}, fmt.Errorf("the authority stamp %q is not a version this program reads", line)
	case !closed && len(head) == MaxStamp:
		return Authority{}, fmt.Errorf("the file's first line is longer than the %d bytes an authority stamp holds", MaxStamp)
	case !closed:
		return Authority{}, fmt.Errorf("the authority stamp is not closed by a newline")
	}
	return parseStamp(line)
}

// parseStamp reads one stamp line, newline removed: the prefix, then
// ref=, commit= and day=, in that order, one space apart.
func parseStamp(line string) (Authority, error) {
	fields := strings.Split(strings.TrimPrefix(line, AuthorityStamp), " ")
	var values [3]string
	for i, key := range []string{"ref=", "commit=", "day="} {
		if len(fields) != 3 || !strings.HasPrefix(fields[i], key) {
			return Authority{}, fmt.Errorf("the authority stamp %q is not ref=, commit= and day=, one space apart", line)
		}
		values[i] = strings.TrimPrefix(fields[i], key)
	}
	ref, commit, day := values[0], values[1], values[2]

	var a Authority
	switch {
	case ref == noProvenance && commit == noProvenance:
	case ref == noProvenance || commit == noProvenance:
		return Authority{}, fmt.Errorf("the authority stamp names a ref or a commit without the other")
	case !fullRef(ref):
		return Authority{}, fmt.Errorf("the authority stamp's ref %q is not a full ref name", ref)
	case !isHex(commit, 40) && !isHex(commit, 64):
		return Authority{}, fmt.Errorf("the authority stamp's commit %q is not a full lowercase object ID", commit)
	default:
		a.Ref, a.Commit = ref, commit
	}
	parsed, err := time.Parse(dayLayout, day)
	if err != nil || parsed.Format(dayLayout) != day {
		return Authority{}, fmt.Errorf("the authority stamp's day %q is not a calendar day", day)
	}
	a.Day = parsed
	return a, nil
}

// fullRef reports whether ref is a full ref name git accepts: refs/, then
// a name the branch rule accepts (change.ValidBranch).
func fullRef(ref string) bool {
	rest, ok := strings.CutPrefix(ref, "refs/")
	return ok && change.ValidBranch(rest)
}

// StripAuthority is file without its stamp line: the first line, when it
// opens with AuthorityStamp and ends within MaxStamp bytes, up to and
// including its newline. Every other byte is kept. A file that does not
// open with the stamp, or whose first line is longer than a stamp, is
// returned as it is, and so no more than MaxStamp bytes are looked at.
func StripAuthority(file []byte) []byte {
	if !bytes.HasPrefix(file, []byte(AuthorityStamp)) {
		return file
	}
	head := file[:min(len(file), MaxStamp)]
	end := bytes.IndexByte(head, '\n')
	switch {
	case end >= 0:
		return file[end+1:]
	case len(file) <= MaxStamp:
		return nil
	}
	return file
}

// Fresh says whether the file the stamp opens is a rendering of the
// registry as it is now: current is the commit the stamp's Ref holds now,
// in the repository the registry is read from, and now is the time.
// It is fresh when its commit is current and its day is now's UTC day. A
// file from another day is stale whatever its commit, because expiry was
// judged on that day. A file with no commit provenance is never fresh.
func (a Authority) Fresh(current string, now time.Time) error {
	today := now.UTC().Format(dayLayout)
	switch {
	case a.Commit == "":
		return fmt.Errorf("the approval keys name no registry commit")
	case current != a.Commit:
		return fmt.Errorf("the approval keys were compiled from %s at %s, which holds %s now", a.Ref, a.Commit, current)
	case a.Day.Format(dayLayout) != today:
		return fmt.Errorf("the approval keys were compiled for %s, and today is %s", a.Day.Format(dayLayout), today)
	}
	return nil
}

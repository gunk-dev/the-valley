// Writing a change's record on origin, and adding a version to it.
//
// A record is written as a commit holding record.json, and pushed to the
// change's record ref leased on the record commit that was read. Writing
// the commit and pushing it are separate steps, so a caller can keep the
// commit's ID and its lease before the push. The push's outcome is read
// from git's porcelain report, so a push origin refused is told apart from
// one whose outcome is unknown, as after a lost connection or an answer
// origin never gave. The record commit is kept either way: a caller that
// cannot tell whether origin took it can compare origin's ref with it
// later.

package rebase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"the-valley/change"
)

// WriteRecord writes a record commit holding r, with the parents given,
// and returns its ID. Its tree holds record.json alone, as a plain file,
// which is the only shape the hook accepts.
func WriteRecord(ctx context.Context, g Git, r *change.Record, message string, parents ...string) (string, error) {
	data, err := change.Encode(r)
	if err != nil {
		return "", err
	}
	if len(data) > change.MaxRecordBlob {
		return "", fmt.Errorf("the change's record would be %d bytes, more than the %d a record may be", len(data), change.MaxRecordBlob)
	}
	return writeOneFile(ctx, g, "record", "record.json", data, message, parents...)
}

// writeOneFile writes a commit whose tree holds one plain file, with the
// parents given, and returns its ID. what names the file in errors.
func writeOneFile(ctx context.Context, g Git, what, name string, data []byte, message string, parents ...string) (string, error) {
	blob, err := g.input(ctx, false, bytes.NewReader(data), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", errors.New("could not write the " + what)
	}
	tree, err := g.input(ctx, false, strings.NewReader("100644 blob "+blob+"\t"+name+"\n"), "mktree")
	if err != nil {
		return "", errors.New("could not write the " + what + "'s tree")
	}
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	commit, err := g.input(ctx, false, strings.NewReader(message+"\n"), append(args, "-F", "-")...)
	if err != nil {
		return "", errors.New("could not write the " + what + " commit")
	}
	return commit, nil
}

// Push is what became of a ref a push asked origin to move.
type Push int

const (
	Unpushed Push = iota // no push was made
	Refused              // origin refused it, and the ref did not move
	Pushed               // origin took it
	Unknown              // the push failed, and whether origin took it is not known
)

func (p Push) String() string {
	switch p {
	case Refused:
		return "refused"
	case Pushed:
		return "pushed"
	case Unknown:
		return "unknown"
	}
	return "unpushed"
}

// RefusedRef is a ref origin refused, and git's reason, as its porcelain
// report gives it.
type RefusedRef struct {
	Ref, Why string
}

// push runs one push to origin, with --porcelain and the arguments given,
// and reads what became of it: Pushed when git exits zero, and otherwise
// what readPush makes of git's report. No push is made under a context
// already done.
func push(ctx context.Context, g Git, args ...string) (Push, []RefusedRef) {
	if ctx.Err() != nil {
		return Unpushed, nil
	}
	cmd := g.command(ctx, append([]string{"push", "--porcelain"}, args...)...)
	cmd.Stderr = g.Stderr
	out, err := cmd.Output()
	switch {
	case finished(err) == nil:
		return Pushed, nil
	case cmd.Process == nil:
		return Unpushed, nil
	}
	return readPush(string(out))
}

// readPush reads a failed push's porcelain report. git prints one line for
// each ref: a flag, the refspec, and a summary with any reason after it in
// parentheses, such as "!\trefs/x:refs/x\t[rejected] (stale info)" (see
// print_ref_status in git's transport.c).
//
// The push is Refused, with each ref refused, only when git reports a ref
// refused outright. That is "[rejected]", which git decides before it
// sends anything, or "[remote rejected]", which is origin's own answer.
// Any other line flagged "!" makes the push Unknown. git prints
// "[remote failure] (remote failed to report status)" for a ref origin
// sent no status for, and origin may have taken that ref before its
// answer was lost. A flag this reader does not know, or a report that
// names no ref refused, is Unknown too.
func readPush(out string) (Push, []RefusedRef) {
	var refused []RefusedRef
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 || len(f[0]) != 1 {
			continue
		}
		summary, _, _ := strings.Cut(f[2], " (")
		switch {
		case f[0] == "!" && (summary == "[rejected]" || summary == "[remote rejected]"):
			_, to, _ := strings.Cut(f[1], ":")
			refused = append(refused, RefusedRef{Ref: to, Why: f[2]})
		case !strings.Contains(" +-*=", f[0]):
			return Unknown, nil
		}
	}
	if len(refused) > 0 {
		return Refused, refused
	}
	return Unknown, nil
}

// RecordWrite is a record commit pushed to a change's record ref, or a
// report commit pushed to its report ref, and what became of the push.
type RecordWrite struct {
	Ref    string // the change's record ref, or its report ref
	Old    string // the commit the push was leased on; "" for a ref that did not exist
	Commit string // the commit written
	Push   Push
	Why    string // git's reason, when origin refused it
}

// PushRecord moves the change's record ref on origin from old to commit,
// leased on old: the push is refused if the ref has moved since it was
// read, and a record that did not exist must still not exist. The hook's
// refusal reaches Stderr through git.
func PushRecord(ctx context.Context, g Git, id, old, commit string) RecordWrite {
	return pushLeased(ctx, g, change.Ref(Target, id), old, commit)
}

// pushLeased moves ref on origin from old to commit, leased on old.
func pushLeased(ctx context.Context, g Git, ref, old, commit string) RecordWrite {
	w := RecordWrite{Ref: ref, Old: old, Commit: commit}
	var refused []RefusedRef
	w.Push, refused = push(ctx, g, "--quiet", "--force-with-lease="+ref+":"+old, "origin", commit+":"+ref)
	if len(refused) > 0 {
		w.Why = refused[0].Why
	}
	return w
}

// CheckRange refuses a change whose commits are not one line: the range
// from the merge base of origin's main and the head, to the head, must hold
// no merge commit, and the merge base must be one commit.
func CheckRange(ctx context.Context, g Git, tip, head string) error {
	out, err := g.output(ctx, true, "merge-base", "--all", tip, head)
	bases := strings.Fields(out)
	switch {
	case err != nil && len(bases) == 0:
		return fmt.Errorf("%s shares no history with origin's %s, so it is no change to it", short(head), Target)
	case len(bases) > 1:
		return fmt.Errorf("%s has %d merge bases with origin's %s, so the commits of the change are ambiguous", short(head), len(bases), Target)
	}
	merges, err := g.output(ctx, true, "rev-list", "--min-parents=2", bases[0]+".."+head)
	if err != nil {
		return fmt.Errorf("could not read the commits from origin's %s to %s", Target, short(head))
	}
	if merges != "" {
		first := strings.Fields(merges)[0]
		return fmt.Errorf("the commits from origin's %s to %s include the merge %s, and a change is a line of commits", Target, short(head), short(first))
	}
	return nil
}

// HeadIDs reads the change IDs a commit carries: the change-id header in
// the commit object itself, never text in its message, and the Change-Id
// trailer, as git interpret-trailers parses the message.
func HeadIDs(ctx context.Context, g Git, head string) (header, trailer string, err error) {
	raw, err := g.output(ctx, true, "cat-file", "commit", head)
	if err != nil {
		return "", "", errors.New("could not read the commit " + short(head))
	}
	headers, message, _ := strings.Cut(raw, "\n\n")
	var found []string
	for _, line := range strings.Split(headers, "\n") {
		// A line opening with a space continues the header above it.
		if v, ok := strings.CutPrefix(line, "change-id "); ok {
			found = append(found, v)
		}
	}
	switch {
	case len(found) > 1:
		return "", "", errors.New(short(head) + " has more than one change-id header")
	case len(found) == 1 && !change.ValidID(found[0]):
		return "", "", fmt.Errorf("%s has the change-id header %q, which is not a change ID", short(head), found[0])
	case len(found) == 1:
		header = found[0]
	}

	parsed, err := g.input(ctx, true, strings.NewReader(message), "interpret-trailers", "--parse")
	if err != nil {
		return "", "", errors.New("could not read the trailers of " + short(head))
	}
	found = nil
	for _, line := range strings.Split(parsed, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Change-Id") {
			found = append(found, strings.TrimSpace(value))
		}
	}
	switch {
	case len(found) > 1:
		return "", "", errors.New(short(head) + " has more than one Change-Id trailer")
	case len(found) == 1 && !change.ValidID(found[0]):
		return "", "", fmt.Errorf("%s has the Change-Id trailer %q, which is not a change ID", short(head), found[0])
	case len(found) == 1:
		trailer = found[0]
	}
	return header, trailer, nil
}

// CheckActive refuses to add a version unless the change is ready at an
// active version whose head is ifActive, as `valley ready --if-active`
// does. before is the record as read, or nil for none.
func CheckActive(handle string, before *change.Record, ifActive string) error {
	switch {
	case before == nil:
		return fmt.Errorf("--if-active %s: origin holds no record of %s, so no version is added", short(ifActive), handle)
	case before.State != change.Ready:
		return fmt.Errorf("%s is %s, not ready, so no version is added: --if-active adds one only while the change is ready", handle, before.State)
	case HeadOf(before.ActiveVersion()) != ifActive:
		return fmt.Errorf("%s v%d is at %s, not %s, so no version is added", handle, before.Active, short(HeadOf(before.ActiveVersion())), short(ifActive))
	}
	return nil
}

// NextRecord is before with v added as its next version, active and
// ready, numbered after its last. It refuses a change ready at v's head
// already, and a record that has as many versions, or has been updated as
// many times, as a record may be.
func NextRecord(handle string, before *change.Record, v change.Version) (*change.Record, error) {
	active := before.ActiveVersion()
	switch {
	case active.Head == v.Head && before.State == change.Ready:
		return nil, fmt.Errorf("%s v%d is at %s already; a new version needs a new head", handle, active.N, short(HeadOf(v)))
	case active.Head != v.Head && len(before.Versions) >= change.MaxVersions:
		return nil, fmt.Errorf("%s has %d versions, the most a change may have", handle, len(before.Versions))
	case before.Seq >= change.MaxSeq:
		return nil, fmt.Errorf("%s's record has been updated %d times, the most a record may be", handle, before.Seq)
	}
	after := *before
	v.N = before.Last().N + 1
	after.Versions = append(append([]change.Version(nil), before.Versions...), v)
	after.Active, after.State, after.Seq = v.N, change.Ready, before.Seq+1
	return &after, nil
}

// ReadyMessage is the message of a record commit that readies a version.
func ReadyMessage(handle, id string, n int, head string) string {
	return fmt.Sprintf("valley ready: %s v%d\n\nChange %s, version %d, at %s.", handle, n, id, n, head)
}

// Append is a version to add to a change's record, as
// `valley ready --if-active <IfActive> --head <Head> --change <ID> <Branch>`
// adds it.
type Append struct {
	ID        string // the change
	Branch    string // the branch the version names; Head must be on it on origin
	Head      string // the head the version records, as a full object ID
	IfActive  string // the head the change must still be ready at
	Principal string // the principal the push authenticates as, named as the version's uploader
}

// RestackAppend is a version the restack grant adds to a ready change: the
// head a restack moved the change's branch to. The version names the
// branch of the active version and keeps its title, as the hook requires
// of a version the restack grant authorises (valleyhook/record.go). The
// branch on origin must be at Head exactly.
type RestackAppend struct {
	ID        string // the change
	Head      string // the head the restack moved the branch to, as a full object ID
	IfActive  string // the head the change must still be ready at: the one the restack rebased
	Principal string // the principal the push authenticates as, named as the version's uploader
}

// Appended is what became of an Append.
type Appended struct {
	Handle  string      // the change's handle on origin
	Version int         // the version added; 0 when none was
	Write   RecordWrite // the record commit and what became of its push; Write.Commit is "" when none was written
	Err     error       // why no version was added; nil when one was
}

// PendingVersion is a version written into a record commit and not yet
// pushed. A caller that has to know after a crash whether origin took the
// commit keeps Commit and Old somewhere durable before it calls Push.
// Origin's record ref at Commit then means the push was taken, and at Old
// that it was not.
type PendingVersion struct {
	ID      string         // the change
	Handle  string         // its handle on origin
	Ref     string         // its record ref
	Old     string         // the record commit read, which the push is leased on
	Commit  string         // the record commit written
	Record  *change.Record // the record Commit holds
	Version int            // the version it adds
	Head    string         // the head that version records
	Branch  string         // the branch that version names
	restack bool
}

// AppendVersion adds a.Head to the change's record as its next version,
// only while the change is still ready at a.IfActive. It reads the record
// and pushes the new one leased on the record it read, so nothing can
// unready the change or ready another version between that check and the
// write. It is BuildVersion and Push in one call.
func AppendVersion(ctx context.Context, g Git, a Append) Appended {
	p, err := buildVersion(ctx, g, a, false)
	if err != nil {
		return Appended{Handle: p.Handle, Err: err}
	}
	return p.Push(ctx, g)
}

// BuildVersion reads origin and writes the record commit that adds a.Head
// as the change's next version, as AppendVersion does, and pushes nothing.
// The version's title is a.Head's commit subject.
func BuildVersion(ctx context.Context, g Git, a Append) (*PendingVersion, error) {
	p, err := buildVersion(ctx, g, a, false)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// BuildRestack reads origin and writes the record commit that adds a.Head
// as the change's next version under the restack grant, and pushes
// nothing. The change must be ready at a.IfActive. Its active version must
// name a branch, and origin's branch must be at a.Head exactly: a head
// further along it is the owner's work, which no restack records. The new
// version keeps the active version's branch and title.
//
// Push reads the branch once more before it pushes. No read can be atomic
// with the push, though: the push is leased on the record ref alone, and
// git compares only the refs a push changes, so the branch can still move
// between that read and the record's write.
func BuildRestack(ctx context.Context, g Git, a RestackAppend) (*PendingVersion, error) {
	p, err := buildVersion(ctx, g, Append{ID: a.ID, Head: a.Head, IfActive: a.IfActive, Principal: a.Principal}, true)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// buildVersion is BuildVersion, and BuildRestack when restack is set. The
// PendingVersion it returns with an error names the change, and its
// handle once origin has been read.
func buildVersion(ctx context.Context, g Git, a Append, restack bool) (*PendingVersion, error) {
	p := &PendingVersion{ID: a.ID, restack: restack}
	switch {
	case !change.ValidPrincipal(a.Principal):
		return p, fmt.Errorf("%q is not a principal's name", a.Principal)
	case !restack && !change.ValidBranch(a.Branch):
		return p, errors.New("not a branch name: " + a.Branch)
	case !change.ValidID(a.ID):
		return p, fmt.Errorf("%q is not a change ID", a.ID)
	case !IsObjectID(a.Head) || !IsObjectID(a.IfActive):
		return p, errors.New("a version is added at a head given as a full object ID, while the change is at another given the same way")
	}
	// A restack reads its branch from the record, so it lists every branch
	// in the same read as the record.
	branches := "refs/heads/" + a.Branch
	if restack {
		branches = "refs/heads/*"
	}
	o, err := ListOrigin(ctx, g, append([]string{"refs/heads/" + Target, branches}, RecordPatterns...)...)
	if err != nil {
		return p, err
	}
	tip := o.Refs["refs/heads/"+Target]
	if tip == "" {
		return p, errors.New("origin has no " + Target + " branch, so a change has nothing to target")
	}
	p.Ref = change.Ref(Target, a.ID)
	p.Old = o.Refs[p.Ref]
	var before *change.Record
	if restack {
		if before, err = readBefore(ctx, g, p.Old); err != nil {
			return p, err
		}
		p.Handle = o.Handle(a.ID)
		if err := CheckActive(p.Handle, before, a.IfActive); err != nil {
			return p, err
		}
		active := before.ActiveVersion()
		if active.Branch == "" {
			return p, fmt.Errorf("%s v%d names no branch, so no restack moved one and no version is added", p.Handle, active.N)
		}
		a.Branch = active.Branch
		if err := atHead(o, a.Branch, a.Head); err != nil {
			return p, err
		}
	}
	remote := o.Refs["refs/heads/"+a.Branch]
	if err := Fetch(ctx, g, tip, remote); err != nil {
		return p, err
	}
	head, err := g.output(ctx, true, "rev-parse", "--verify", "--quiet", a.Head+"^{commit}")
	if err != nil || remote == "" || !g.succeeds(ctx, true, "merge-base", "--is-ancestor", head, remote) {
		return p, errors.New(a.Head + " is not on origin's " + a.Branch + "; push it there first")
	}
	if err := CheckRange(ctx, g, tip, head); err != nil {
		return p, err
	}
	header, trailer, err := HeadIDs(ctx, g, head)
	if err != nil {
		return p, err
	}
	if header != "" && trailer != "" && header != trailer {
		return p, fmt.Errorf("%s has the change-id header %s and the Change-Id trailer %s, and they must agree", short(head), header, trailer)
	}
	var title string
	if restack {
		title = before.ActiveVersion().Title
	} else {
		if before, err = readBefore(ctx, g, p.Old); err != nil {
			return p, err
		}
		p.Handle = o.Handle(a.ID)
		if err := CheckActive(p.Handle, before, a.IfActive); err != nil {
			return p, err
		}
		subject, err := g.output(ctx, true, "log", "-1", "--format=%s", head)
		if err != nil {
			return p, errors.New("could not read the subject of " + short(head))
		}
		title = change.Title(subject)
	}
	after, err := NextRecord(p.Handle, before, change.Version{
		Head: change.HeadPrefix + head, Title: title, Branch: a.Branch, Uploader: a.Principal,
	})
	if err != nil {
		return p, err
	}
	commit, err := WriteRecord(ctx, g, after, ReadyMessage(p.Handle, a.ID, after.Active, head), p.Old, head)
	if err != nil {
		return p, err
	}
	p.Commit, p.Record, p.Version, p.Head, p.Branch = commit, after, after.Active, head, a.Branch
	return p, nil
}

// readBefore reads the record a record commit on origin holds, or nil for
// none.
func readBefore(ctx context.Context, g Git, old string) (*change.Record, error) {
	if old == "" {
		return nil, nil
	}
	if err := Fetch(ctx, g, old); err != nil {
		return nil, err
	}
	return ReadRecord(ctx, g, old)
}

// atHead refuses a branch that o does not list at head exactly.
func atHead(o Origin, branch, head string) error {
	switch at := o.Refs["refs/heads/"+branch]; {
	case at == "":
		return fmt.Errorf("origin has no %s, so it is not at %s and no version is added", branch, short(head))
	case at != head:
		return fmt.Errorf("origin's %s is at %s, not %s, so no version is added: a restack records only the head it moved the branch to", branch, short(at), short(head))
	}
	return nil
}

// Push pushes the record commit to the change's record ref, leased on
// Old. A restack's push first reads origin's branch once more, and pushes
// nothing unless the branch is still at Head (see BuildRestack).
func (p *PendingVersion) Push(ctx context.Context, g Git) Appended {
	ap := Appended{Handle: p.Handle, Write: RecordWrite{Ref: p.Ref, Old: p.Old, Commit: p.Commit}}
	if p.restack {
		o, err := ListOrigin(ctx, g, "refs/heads/"+p.Branch)
		if err == nil {
			err = atHead(o, p.Branch, p.Head)
		}
		if err != nil {
			ap.Err = err
			return ap
		}
	}
	ap.Write = PushRecord(ctx, g, p.ID, p.Old, p.Commit)
	if ap.Write.Push != Pushed {
		ap.Err = errors.New("origin did not take the record of " + p.ID)
		return ap
	}
	ap.Version = p.Version
	return ap
}

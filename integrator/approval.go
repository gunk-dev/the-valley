package main

// Whether a signed approval authorizes a recorded request.
//
// verifyApproval checks one approval note (the approval package) against
// the integration request it would let land: the note's signature under
// one selected list of approval keys, the scope and head it names, that
// head itself, and the change record's history from the record the
// approver signed to the record read as current. It reads the objects it
// is given by ID, the record commits between them and the type of each
// head they add, and nothing else: no ref, no file and no clock. Which
// list it is given, which record commit is current, and whether the
// inputs still hold once it has answered, are for its caller.
//
// Every value it did not write itself, from the query, the note, the
// list, the repository or git, is quoted in what it returns.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"the-valley/approval"
	"the-valley/change"
	"the-valley/note"
)

// The basenames of the two approval lists `identity compile` writes. A
// policy that requires a hardware key selects the hardware list, and every
// other policy the general one; neither stands in for the other.
const (
	approvalSignersFile         = "approval-signers"
	approvalSignersHardwareFile = "approval-signers-hardware"
)

// approvalQuery is what an approval is checked against. The caller reads
// every input and answers for where it came from: the evaluator cannot
// tell who published a record or a note, or whether a ref still holds
// what was read from it.
type approvalQuery struct {
	// note is the approval note's blob, as the caller read it at
	// approval.Ref, and record the change record commit the caller reads
	// as current, at change.Ref of the target and the change. Both are
	// object IDs, so what is checked cannot move while it is read.
	note, record string

	// instance, project, target and changeID are the scope the approval
	// must name: the instance identity (approval.InstanceID), the
	// project, the target as a branch name, and the change ID. Any
	// target a record can name is taken; which targets carry records is
	// the push hook's rule.
	instance, project, target, changeID string

	// head is the commit the request files, as an object ID.
	head string

	// hardware is whether the composed policy requires a hardware key.
	// authorityName and authority are the one list selected for it: its
	// basename and its whole content, as the caller read it.
	hardware      bool
	authorityName string
	authority     []byte
}

// proofKind names what a proof is made of.
type proofKind string

// proofSignedNote is a proof that is a signed approval note.
const proofSignedNote proofKind = "signed-note"

// proof is one piece of what authorizes a request, as it was verified.
type proof struct {
	kind       proofKind
	signedNote signedNoteProof
}

// signedNoteProof is a verified approval note and what it was verified
// against. It shares no memory with the query.
type signedNoteProof struct {
	statement approval.Statement
	// note is the note's exact bytes.
	note []byte
	// authorityName and authority are the selected list the signature
	// verified under, whole.
	authorityName string
	authority     []byte
	// records is each record commit the history walk read, from the
	// current record to the signed one.
	records []string
}

// approvalReason is why an approval does not authorize a request.
type approvalReason string

const (
	// approvalInvalid is a note that is missing, not a blob, past
	// approval.MaxNote, not in an approval note's form, or whose
	// signature does not verify under the listed key it names.
	approvalInvalid approvalReason = "approval-invalid"
	// authorityInvalid is a list that is not the one the policy selects,
	// or that does not read as verifier keys.
	authorityInvalid approvalReason = "authority-invalid"
	// keyNotListed is a note signed by a key the selected list does not
	// hold under the approver's name. An empty list holds none.
	keyNotListed approvalReason = "key-not-listed"
	// scopeMismatch is an approval naming another instance, project,
	// target or change.
	scopeMismatch approvalReason = "scope-mismatch"
	// requestMismatch is an approval of another head than the request's,
	// a request head that is not a commit in the repository, or an
	// approved version that does not have that head in the signed or the
	// current record.
	requestMismatch approvalReason = "request-mismatch"
	// historyInvalid is a record history that readyHistory does not walk
	// from the current record to the signed one, or a record on it that
	// is missing or malformed.
	historyInvalid approvalReason = "history-invalid"
	// notReady is a record from the signed one to the current one that is
	// not ready.
	notReady approvalReason = "not-ready"
)

// approvalRefusal is a refusal's reason, and what was found.
type approvalRefusal struct {
	reason approvalReason
	why    string
}

func (r *approvalRefusal) Error() string { return string(r.reason) + ": " + r.why }

func refuse(reason approvalReason, format string, args ...any) error {
	return &approvalRefusal{reason: reason, why: fmt.Sprintf(format, args...)}
}

// approvalResult is what verifyApproval decides, read through its
// methods. Authorized is the one way to learn that an approval authorizes
// a request, and only verifyApproval's success sets what it reports, so
// the zero value, a refusal and an error all report not authorized.
type approvalResult struct {
	authorized bool
	proofs     []proof
	refusal    *approvalRefusal
	err        error
}

// Authorized returns the proofs and true when the approval authorizes the
// request, and nil and false otherwise.
func (r approvalResult) Authorized() ([]proof, bool) {
	if !r.authorized || len(r.proofs) == 0 {
		return nil, false
	}
	return r.proofs, true
}

// Refusal is why the approval does not authorize the request, or nil when
// it does or when nothing was decided.
func (r approvalResult) Refusal() *approvalRefusal { return r.refusal }

// Err is why nothing was decided: a query that is not well formed, ctx
// ending, or git failing to answer. It is nil when a decision was made.
func (r approvalResult) Err() error { return r.err }

// verifyApproval decides whether the approval note q.note authorizes the
// request q describes, in repo. When it does, the result's Authorized
// reports the proofs, one signed-note proof; otherwise it reports false,
// and the result carries the refusal or the error.
//
// ctx must carry a deadline or a cancellation: the evaluator sets none,
// and a git that stops answering holds it until ctx ends.
//
// The approval authorizes the request when:
//
//   - q.authorityName is the list q.hardware selects, and q.authority
//     reads as verifier keys;
//   - the note is a blob of at most approval.MaxNote bytes in an approval
//     note's form, signed by a key the list holds under the statement's
//     principal, who need not own the change;
//   - the statement names q's instance, project, target and change, and
//     q.head as its head, and q.head is a commit;
//   - the record it signed (statement.Record) and q.record are records of
//     q's change and target, both ready, and both give the signed version
//     the signed head;
//   - readyHistory walks from q.record to the signed record.
func verifyApproval(ctx context.Context, repo string, q approvalQuery) approvalResult {
	proofs, err := checkApproval(ctx, repo, q)
	var refusal *approvalRefusal
	switch {
	case errors.As(err, &refusal):
		return approvalResult{refusal: refusal}
	case err != nil:
		return approvalResult{err: err}
	}
	return approvalResult{authorized: true, proofs: proofs}
}

func checkApproval(ctx context.Context, repo string, q approvalQuery) ([]proof, error) {
	for _, id := range []string{q.note, q.record, q.head} {
		if !isObjectID(id) {
			return nil, fmt.Errorf("%q is not 40 lowercase hex digits", id)
		}
	}
	if !change.ValidBranch(q.target) || !change.ValidID(q.changeID) {
		return nil, fmt.Errorf("the target %q or the change %q is not one a record names", q.target, q.changeID)
	}

	selected := approvalSignersFile
	if q.hardware {
		selected = approvalSignersHardwareFile
	}
	if q.authorityName != selected {
		return nil, refuse(authorityInvalid, "the policy selects %s, and the list given is %q", selected, q.authorityName)
	}
	authority := bytes.Clone(q.authority)
	keys, err := note.ParseVerifierKeys(authority)
	if err != nil {
		return nil, refuse(authorityInvalid, "%s does not read: %q", selected, err.Error())
	}

	objects, err := openObjectReader(ctx, repo)
	if err != nil {
		return nil, err
	}
	defer objects.close()

	body, err := objects.read(q.note, "blob", approval.MaxNote)
	if err != nil {
		return nil, asRefusal(err, approvalInvalid, "the approval note %q", q.note)
	}
	s, err := openApproval(body, keys, selected)
	if err != nil {
		return nil, err
	}
	switch {
	case s.Instance != q.instance:
		return nil, refuse(scopeMismatch, "it approves for the instance %q, not %q", s.Instance, q.instance)
	case s.Project != q.project:
		return nil, refuse(scopeMismatch, "it approves for the project %q, not %q", s.Project, q.project)
	case s.Target != q.target:
		return nil, refuse(scopeMismatch, "it approves landing on %q, not %q", s.Target, q.target)
	case s.Change != q.changeID:
		return nil, refuse(scopeMismatch, "it approves the change %q, not %q", s.Change, q.changeID)
	case s.Head != change.HeadPrefix+q.head:
		return nil, refuse(requestMismatch, "it approves the head %q, and the request files %q", s.Head, q.head)
	}
	// Whatever the walk reads, the head that would land is a commit: when
	// the signed record is the current one, the walk checks no head.
	if kind, _, err := objects.info(q.head); err != nil {
		return nil, asRefusal(err, requestMismatch, "the request's head %q", q.head)
	} else if kind != "commit" {
		return nil, refuse(requestMismatch, "the request's head %q is a %q, and a head is a commit", q.head, kind)
	}

	signed := strings.TrimPrefix(s.Record, change.HeadPrefix)
	walked, err := readyHistory(objects, q.record, signed, q.target, q.changeID, s.Version, s.Head)
	if err != nil {
		return nil, err
	}
	return []proof{{kind: proofSignedNote, signedNote: signedNoteProof{
		statement:     s,
		note:          body,
		authorityName: q.authorityName,
		authority:     authority,
		records:       walked,
	}}}, nil
}

// openApproval reads an approval note's form and verifies its signature
// under keys. ParseNote holds the note to one signature named by the
// statement's principal, and a verifier key matches a signature only under
// its own name, so the key that verifies is listed under that principal.
func openApproval(body []byte, keys []note.VerifierKey, list string) (approval.Statement, error) {
	s, err := approval.ParseNote(body)
	if err != nil {
		return approval.Statement{}, refuse(approvalInvalid, "the approval note does not read: %q", err.Error())
	}
	_, sigs, err := note.Split(body)
	if err != nil {
		return approval.Statement{}, refuse(approvalInvalid, "the approval note does not read: %q", err.Error())
	}
	listed := false
	for _, k := range keys {
		listed = listed || k.Name == sigs[0].Name && k.Hash == sigs[0].Hash
	}
	if !listed {
		return approval.Statement{}, refuse(keyNotListed, "it is signed by %q, a key %s does not list", fmt.Sprintf("%s+%08x", sigs[0].Name, sigs[0].Hash), list)
	}
	if _, err := approval.Verify(body, keys); err != nil {
		return approval.Statement{}, refuse(approvalInvalid, "its signature does not verify: %q", err.Error())
	}
	return s, nil
}

// readyHistory walks first parents from the current record commit to the
// signed one, and stops there. It returns each record commit it read, the
// current one first.
//
// The signed record must be ready and give version n the head head; so
// must the current one, and the two may be the same commit. Each record
// after the signed one must be ready, as must the record before it, and
// must be what the push hook's new-version row admits
// (valleyhook/record.go, checkUpdate and checkNewVersion): two parents,
// the first the record it replaces and the second a commit, the head of
// the one version it adds; every earlier version and the owner kept; the
// seq raised by one; and the new version, at a head other than the active
// one, made active. A record whose one parent is the record it replaces
// changes the state by the hook's rules (checkStateChange), so between two
// ready records it is refused.
//
// The walk does not check what the hook decides by who pushed: that the
// new version's uploader is the pusher, and that a restack keeps the
// active version's branch and title. Nor does it check that the target is
// one the hook keeps records for. It cannot establish who published a
// record, or that the current record is the one its ref holds; the caller
// reads that commit from the governed record ref.
//
// The second parent is never followed, so a signed record reachable only
// through a version's head is not reached. Nothing before the signed
// record is read: a withdrawal there, then a fresh ready and a fresh
// approval, is a history like any other. A record history deleted and
// written again with identical objects reads as the history it repeats;
// only retaining records rules that out.
//
// Each step raises the seq by one and adds a version, so the walk takes
// fewer steps than the current record holds versions, and stops once the
// seq reaches the signed record's.
func readyHistory(objects *objectReader, current, signed, target, id string, n int, head string) ([]string, error) {
	s, err := readRecordCommit(objects, signed, target, id)
	if err != nil {
		return nil, asRefusal(err, historyInvalid, "the signed record %q", signed)
	}
	if s.record.State != change.Ready {
		return nil, refuse(notReady, "the signed record %q says the change is %q", signed, s.record.State)
	}
	if !namesVersion(s.record, n, head) {
		return nil, refuse(requestMismatch, "the signed record %q gives v%d no head %q", signed, n, head)
	}

	walked := []string{current}
	at := current
	cur, err := readRecordCommit(objects, current, target, id)
	if err != nil {
		return nil, asRefusal(err, historyInvalid, "the current record %q", current)
	}
	if cur.record.State != change.Ready {
		return nil, refuse(notReady, "the current record %q says the change is %q", current, cur.record.State)
	}
	if !namesVersion(cur.record, n, head) {
		return nil, refuse(requestMismatch, "the current record %q gives v%d no head %q", current, n, head)
	}
	for at != signed {
		if cur.record.Seq <= s.record.Seq {
			return nil, refuse(historyInvalid, "the record %q at seq %d is not the signed record %q, whose seq is %d", at, cur.record.Seq, signed, s.record.Seq)
		}
		before := cur.parents[0]
		prev, err := readRecordCommit(objects, before, target, id)
		if err != nil {
			return nil, asRefusal(err, historyInvalid, "the record %q, first parent of %q,", before, at)
		}
		if prev.record.State != change.Ready {
			return nil, refuse(notReady, "the record %q, between the signed and the current record, says the change is %q", before, prev.record.State)
		}
		if len(cur.parents) != 2 {
			return nil, refuse(historyInvalid, "the record %q has one parent, and a ready record after the signed one adds a version: its parents are the record it replaces and the version's head", at)
		}
		added := cur.parents[1]
		if why := addedVersion(prev.record, cur.record, added); why != "" {
			return nil, refuse(historyInvalid, "the record %q does not add a version to %q: %s", at, before, why)
		}
		if kind, _, err := objects.info(added); err != nil {
			return nil, asRefusal(err, historyInvalid, "the head %q of the record %q", added, at)
		} else if kind != "commit" {
			return nil, refuse(historyInvalid, "the record %q adds a version at %q, a %q, and a head is a commit", at, added, kind)
		}
		walked = append(walked, before)
		at, cur = before, prev
	}
	return walked, nil
}

// addedVersion holds after to adding one version, at head h, to before, by
// the hook's new-version row (valleyhook/record.go, checkNewVersion) less
// its uploader rule. It returns why not, or "".
func addedVersion(before, after *change.Record, h string) string {
	if len(after.Versions) != len(before.Versions)+1 {
		return fmt.Sprintf("it holds %d versions, and the record before it %d", len(after.Versions), len(before.Versions))
	}
	for i, v := range before.Versions {
		if after.Versions[i] != v {
			return fmt.Sprintf("it changes version %d", v.N)
		}
	}
	v := after.Last()
	switch {
	case v.Head != change.HeadPrefix+h:
		return fmt.Sprintf("its new version names the head %q, and its second parent is %q", v.Head, h)
	case v.Head == before.ActiveVersion().Head:
		return "its new version's head is the active version's"
	case after.Active != v.N:
		return fmt.Sprintf("its active is %d, not its new version, %d", after.Active, v.N)
	case after.Seq != before.Seq+1:
		return fmt.Sprintf("its seq is %d, and the record before it is at %d", after.Seq, before.Seq)
	case after.Owner != before.Owner:
		return fmt.Sprintf("it changes the owner from %q to %q", before.Owner, after.Owner)
	}
	return ""
}

// namesVersion reports whether r gives version n the head head.
func namesVersion(r *change.Record, n int, head string) bool {
	return n >= 1 && n <= len(r.Versions) && r.Versions[n-1].Head == head
}

// recordCommit is a record commit as read: its parents, in order, and its
// record.
type recordCommit struct {
	parents []string
	record  *change.Record
}

// readRecordCommit reads the record commit id with the layout and bounds
// the push hook reads one with (valleyhook/record.go: readFileCommit, and
// checkCreate and checkUpdate for its parents): a commit of one or two
// parents whose tree holds record.json alone, as a plain file, each object
// held to its bound before its content is read, and a record of change
// changeID for target.
func readRecordCommit(objects *objectReader, id, target, changeID string) (recordCommit, error) {
	body, err := objects.read(id, "commit", change.MaxRecordCommit)
	if err != nil {
		return recordCommit{}, err
	}
	tree, parents, err := recordLinks(body)
	if err != nil {
		return recordCommit{}, err
	}
	if len(parents) < 1 || len(parents) > 2 {
		return recordCommit{}, objectRefusal{fmt.Sprintf("has %d parents, and a record commit has one or two", len(parents))}
	}
	entries, err := objects.read(tree, "tree", change.MaxRecordTree)
	if err != nil {
		return recordCommit{}, about(err, "has a tree that")
	}
	const entry = "100644 record.json\x00"
	if len(entries) != len(entry)+20 || !bytes.HasPrefix(entries, []byte(entry)) {
		return recordCommit{}, objectRefusal{"has a tree that does not hold record.json alone, as a plain file"}
	}
	data, err := objects.read(hex.EncodeToString(entries[len(entry):]), "blob", change.MaxRecordBlob)
	if err != nil {
		return recordCommit{}, about(err, "has a record.json that")
	}
	r, err := change.Parse(data)
	if err != nil {
		return recordCommit{}, objectRefusal{fmt.Sprintf("holds a record.json that does not read: %q", err.Error())}
	}
	if r.ChangeID != changeID || r.Target != target {
		return recordCommit{}, objectRefusal{fmt.Sprintf("records change %q for %q, not %q for %q", r.ChangeID, r.Target, changeID, target)}
	}
	return recordCommit{parents: parents, record: r}, nil
}

// recordLinks reads the tree and the parents out of a commit as git does,
// and as the hook does (valleyhook/record.go, commitLinks): a tree line
// first, then the parent lines that follow it directly. A parent line
// anywhere later is refused rather than read two ways.
func recordLinks(body []byte) (tree string, parents []string, err error) {
	header, _, _ := bytes.Cut(body, []byte("\n\n"))
	lines := strings.Split(string(header), "\n")
	tree, ok := strings.CutPrefix(lines[0], "tree ")
	if !ok || !isObjectID(tree) {
		return "", nil, objectRefusal{"does not open with its tree"}
	}
	rest := lines[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "parent ") {
		parent := strings.TrimPrefix(rest[0], "parent ")
		if !isObjectID(parent) {
			return "", nil, objectRefusal{"names a parent that is not an object ID"}
		}
		parents = append(parents, parent)
		rest = rest[1:]
	}
	for _, line := range rest {
		if strings.HasPrefix(line, "parent ") {
			return "", nil, objectRefusal{"names a parent after its other headers"}
		}
	}
	return tree, parents, nil
}

// objectRefusal is an object that is missing, or not what it must be. Any
// other error from reading one is git failing to answer.
type objectRefusal struct{ why string }

func (o objectRefusal) Error() string { return o.why }

// about names the object a refusal is about, inside the one read.
func about(err error, prefix string) error {
	var o objectRefusal
	if errors.As(err, &o) {
		return objectRefusal{prefix + " " + o.why}
	}
	return err
}

// asRefusal is err as a refusal for reason, about the object the format
// names, when err says the object is missing or malformed, and err itself
// otherwise.
func asRefusal(err error, reason approvalReason, format string, args ...any) error {
	var o objectRefusal
	if errors.As(err, &o) {
		return refuse(reason, "%s %s", fmt.Sprintf(format, args...), o.why)
	}
	return err
}

// stderrBound is how much of what git writes to stderr is kept for an
// error: room for the fatal line git ends with.
const stderrBound = 4 << 10

// objectReader reads objects by ID out of one `git cat-file
// --batch-command`, asking for an object's type and size before its
// content, so an object past a bound is never read. asked is each command
// sent, in order.
//
// git runs in the controller's environment (gitEnvironment), built here
// rather than inherited, so no GIT_ variable of the process's points it at
// other objects or configuration. Replacement refs are off, so an object
// ID reads as the object it names, and lazy fetching is off, so an object
// the repository lacks is missing rather than fetched from a promisor
// remote its configuration names.
//
// When ctx ends, git is killed and its pipes are closed, so a read or a
// write blocked on them returns.
type objectReader struct {
	ctx      context.Context
	cmd      *exec.Cmd
	in, pipe *os.File // the ends of git's stdin and stdout this process holds
	out      *bufio.Reader
	stderr   *firstBytes
	stop     func() bool
	closed   bool
	asked    []string
}

func openObjectReader(ctx context.Context, repo string) (*objectReader, error) {
	cmd := exec.CommandContext(ctx, "git", "--no-replace-objects", "cat-file", "--batch-command")
	cmd.Dir = repo
	cmd.Env = append(gitEnvironment(os.Environ()), "GIT_NO_LAZY_FETCH=1")
	stderr := &firstBytes{max: stderrBound}
	cmd.Stderr = stderr
	// Once git has exited or been killed, Wait waits this long at most
	// for stderr to close: only a process git started could hold it open
	// past git, and nothing in this read starts one.
	cmd.WaitDelay = time.Second
	// The pipes are made here rather than by exec, so every way of
	// failing to start closes each end it opened.
	stdin, in, err := os.Pipe()
	if err != nil {
		return nil, quotedError{"git cat-file has no input pipe", err}
	}
	pipe, stdout, err := os.Pipe()
	if err != nil {
		_, _ = stdin.Close(), in.Close()
		return nil, quotedError{"git cat-file has no output pipe", err}
	}
	cmd.Stdin, cmd.Stdout = stdin, stdout
	err = cmd.Start()
	// A started git holds its own copies of its ends.
	_, _ = stdin.Close(), stdout.Close()
	if err != nil {
		_, _ = in.Close(), pipe.Close()
		return nil, quotedError{"git cat-file does not start", err}
	}
	stop := context.AfterFunc(ctx, func() {
		_, _ = in.Close(), pipe.Close()
	})
	return &objectReader{ctx: ctx, cmd: cmd, in: in, pipe: pipe, out: bufio.NewReader(pipe), stderr: stderr, stop: stop}, nil
}

// info is the type and size of object id.
func (o *objectReader) info(id string) (kind string, size int, err error) {
	return o.ask("info", id)
}

func (o *objectReader) ask(command, id string) (kind string, size int, err error) {
	if err := o.ctx.Err(); err != nil {
		return "", 0, o.failed(err)
	}
	o.asked = append(o.asked, command+" "+id)
	if _, err := fmt.Fprintf(o.in, "%s %s\n", command, id); err != nil {
		return "", 0, o.failed(err)
	}
	header, err := o.out.ReadString('\n')
	if err != nil {
		return "", 0, o.failed(err)
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[0] == id && fields[1] == "missing" {
		return "", 0, objectRefusal{"is not in the repository"}
	}
	if len(fields) != 3 || fields[0] != id {
		return "", 0, fmt.Errorf("git cat-file answered %q for %q", strings.TrimSpace(header), id)
	}
	size, err = strconv.Atoi(fields[2])
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("git cat-file answered %q for %q", strings.TrimSpace(header), id)
	}
	return fields[1], size, nil
}

// read is the content of object id, which must be of type kind and at
// most max bytes.
func (o *objectReader) read(id, kind string, max int) ([]byte, error) {
	got, size, err := o.info(id)
	switch {
	case err != nil:
		return nil, err
	case got != kind:
		return nil, objectRefusal{fmt.Sprintf("is a %q, not a %s", got, kind)}
	case size > max:
		return nil, objectRefusal{fmt.Sprintf("is a %s of %d bytes, more than %d", kind, size, max)}
	}
	again, confirmed, err := o.ask("contents", id)
	if err != nil {
		return nil, err
	}
	if again != kind || confirmed != size {
		return nil, fmt.Errorf("git cat-file sized %q as a %s of %d bytes, and then read a %q of %d", id, kind, size, again, confirmed)
	}
	body := make([]byte, size+1) // the content and git's closing newline
	if _, err := io.ReadFull(o.out, body); err != nil {
		return nil, o.failed(err)
	}
	return body[:size], nil
}

// failed is the error for a read or a write of git's pipes that failed:
// ctx's, once it has ended, since that is what closes them, and otherwise
// what git wrote to stderr before it exited, once it is reaped.
func (o *objectReader) failed(err error) error {
	if ctxErr := o.ctx.Err(); ctxErr != nil {
		return quotedError{"git cat-file stopped", ctxErr}
	}
	o.close()
	return quotedError{fmt.Sprintf("git cat-file wrote %q to stderr", o.stderr.b), err}
}

// close kills git and reaps it. git is killed rather than asked to
// finish: a read that stopped early may have left content in its pipe.
func (o *objectReader) close() {
	if o.closed {
		return
	}
	o.closed = true
	o.stop()
	_ = o.cmd.Process.Kill()
	_ = o.cmd.Wait()
	_, _ = o.in.Close(), o.pipe.Close()
}

// quotedError is an error from the operating system or git, quoted, so a
// path or a line it carries cannot put a newline or a control byte into a
// message, while errors.Is and errors.As still reach it.
type quotedError struct {
	context string
	err     error
}

func (e quotedError) Error() string { return e.context + ": " + strconv.Quote(e.err.Error()) }

func (e quotedError) Unwrap() error { return e.err }

// firstBytes keeps the first max bytes written to it and drops the rest,
// taking every write whole so git never waits on it.
type firstBytes struct {
	b   []byte
	max int
}

func (f *firstBytes) Write(p []byte) (int, error) {
	if room := f.max - len(f.b); room > 0 {
		f.b = append(f.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// isObjectID reports whether s is a SHA-1 object ID, the one object format
// a record's heads name.
func isObjectID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

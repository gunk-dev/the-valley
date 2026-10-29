package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"the-valley/exactjson"
	"the-valley/readaccess"
	"the-valley/web/export"
)

const linkUsage = `usage: valley-web-publish link issue --principal NAME [--note TEXT] FLAGS
       valley-web-publish link list [--check] [CREDENTIAL_ID...] FLAGS
       valley-web-publish link revoke (CREDENTIAL_ID | --principal NAME | --all) FLAGS
       valley-web-publish link prune FLAGS

Issue, list and revoke read links: bearer credentials, each bound to one
registry principal, that let their holder read what that principal may
read at the destination, and nothing more. The host keeps each link's
verifier in the credential file and never its secret. Each policy pass
reads the file as it signs, and carries the active links to the edge in
the authorization. issue, revoke and prune hold the links lock, the file
lock in the credential file's directory, while they read and write the
file; a pass holds it from its read of the file until it has signed. So
a change waits for a pass that is signing, and a pass for a change, each
for at most a minute; a change then fails having changed nothing. The
lock must be a regular file owned by root, or by the user who runs the
command, and is made when it is missing.

  issue        make a link for the principal, and print it once on
               stdout: https://DESTINATION/-/redeem#ID.SECRET; refused
               unless the principal may read at least one project at
               the destination, by the registry and the projects'
               policy/read, as a policy pass reads them; refused too
               when the file would come within 64 KiB of the 1 MiB it
               may hold: prune it first
  list         show each link's ID, principal, state (active or
               revoked), delivery, issue time and note; delivery is
               enforced once the receiver acknowledged an authorization
               signed from the credential file's revision that set the
               link's state, or a later one, and pending before; it is
               read from the publisher's state, never from the edge,
               and says nothing of whether a link was redeemed
  revoke       revoke one link, every link of a principal, or every
               link, for good; revoked IDs stay in the file until
               prune removes them; a revoke is never refused for the
               file's size
  prune        remove from the file every revoked link whose
               revocation is enforced; the edge accepts only a link an
               authorization binds, so a link absent from the file is
               as revoked as one recorded revoked

FLAGS:
  --links FILE         the credential file (required), not named lock
  --state DIR          the publisher's state directory, whose delivery
                       record every verb reads (default as a pass)
  --principal NAME     issue: the principal the link identifies; revoke:
                       revoke every link of the principal
  --note TEXT          issue: a label list shows, at most 100 characters
                       and no control characters
  --all                revoke: revoke every link
  --check              list: exit 1, and say why, when a link shown is
                       pending
  --instance-repo, --destination, --host, --read-schema,
  --identity-schema, --registry-ref, --registry-dir
                       issue alone: as for a pass; issue needs
                       --instance-repo, --destination and --host`

// The credential file's limits. Reads and writes hold it to maxLinksFile
// bytes. Issue refuses to write it larger than maxLinksFile less
// linksHeadroom, which always fits a revocation of every active link: each
// adds a revoked time, at most 41 bytes with its indentation, and at most
// 18 more digits to its changed revision, so 1,000 links add at most
// 59,018 bytes with the file's own revision.
const (
	maxNote       = 100
	maxLinksFile  = 1 << 20
	linksHeadroom = 64 << 10
)

// linksFile is the credential file: one record per read link the host
// issued, revoked ones included, and the file's revision. Each change
// writes a revision greater than the file's and than every revision a
// pass signed from, so the revision only moves forward. The file never
// holds a secret. A missing or empty file holds no links, at revision 0.
type linksFile struct {
	Revision int64        `json:"revision"`
	Links    []linkRecord `json:"links"`
}

// linkRecord is one read link. Revoked is "" while it is active. Changed
// is the revision of the credential file that set the link's current
// state: the one its issue wrote, or its revocation.
type linkRecord struct {
	ID        string `json:"id"`
	Principal string `json:"principal"`
	Note      string `json:"note"`
	Verifier  string `json:"verifier"`
	Issued    string `json:"issued"`
	Revoked   string `json:"revoked,omitempty"`
	Changed   int64  `json:"changed"`
}

// readLinks reads the credential file. A missing or empty file holds no
// links. Anything else that is not a valid credential file is an error:
// unknown or misspelled keys, a repeated ID, a malformed field, or more
// active links than an authorization carries.
func readLinks(path string) (linksFile, error) {
	var f linksFile
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLinksFile+1))
	if err != nil {
		return f, err
	}
	if len(data) == 0 {
		return f, nil
	}
	if len(data) > maxLinksFile {
		return f, fmt.Errorf("%s is larger than %d bytes", path, maxLinksFile)
	}
	if err := exactjson.Decode(data, &f); err != nil {
		return f, fmt.Errorf("%s is not a credential file: %w", path, err)
	}
	if err := f.check(); err != nil {
		return f, fmt.Errorf("%s is not a valid credential file: %w", path, err)
	}
	return f, nil
}

func (f *linksFile) check() error {
	if f.Revision < 0 {
		return fmt.Errorf("its revision %d is negative", f.Revision)
	}
	seen := map[string]bool{}
	active := 0
	for i, l := range f.Links {
		switch {
		case !export.ValidLinkID(l.ID):
			return fmt.Errorf("link %d has the ID %q, which is not lk- and 16 lowercase hex digits", i, l.ID)
		case seen[l.ID]:
			return fmt.Errorf("the ID %s is in it twice", l.ID)
		case !export.ValidName(l.Principal) || l.Principal == readaccess.Anyone:
			return fmt.Errorf("%s names %q, which is not a principal", l.ID, l.Principal)
		case checkNote(l.Note) != nil:
			return fmt.Errorf("%s: %w", l.ID, checkNote(l.Note))
		case !export.ValidVerifier(l.Verifier):
			return fmt.Errorf("%s has a verifier that is not 64 lowercase hex digits", l.ID)
		case !validInstant(l.Issued):
			return fmt.Errorf("%s was issued at %q, which is not a time in RFC 3339, in UTC, to the second", l.ID, l.Issued)
		case l.Revoked != "" && !validInstant(l.Revoked):
			return fmt.Errorf("%s was revoked at %q, which is not a time in RFC 3339, in UTC, to the second", l.ID, l.Revoked)
		case l.Changed < 1 || l.Changed > f.Revision:
			return fmt.Errorf("%s was changed at revision %d, which is not from 1 to the file's revision, %d", l.ID, l.Changed, f.Revision)
		}
		seen[l.ID] = true
		if l.Revoked == "" {
			active++
		}
	}
	if active > export.MaxBindings {
		return fmt.Errorf("it holds %d active links, more than the %d an authorization carries", active, export.MaxBindings)
	}
	return nil
}

func checkNote(note string) error {
	if !utf8.ValidString(note) || utf8.RuneCountInString(note) > maxNote || strings.ContainsFunc(note, unicode.IsControl) {
		return fmt.Errorf("a note is at most %d characters of UTF-8, with no control characters", maxNote)
	}
	return nil
}

func validInstant(s string) bool {
	t, err := time.Parse(time.RFC3339, s)
	return err == nil && t.UTC().Format(time.RFC3339) == s
}

// active are the links not revoked.
func (f *linksFile) active() []linkRecord {
	var out []linkRecord
	for _, l := range f.Links {
		if l.Revoked == "" {
			out = append(out, l)
		}
	}
	return out
}

// nextRevision is the revision a change to the file writes: one more than
// the greater of the file's and the greatest a pass signed from. A change
// to a file restored from an older copy so still moves past every revision
// already published.
func nextRevision(f linksFile, d linkDelivery) int64 {
	return max(f.Revision, d.Signed) + 1
}

// encode is the credential file as writeLinks writes it. One larger than
// maxLinksFile bytes, which readLinks would refuse, is an error.
func (f linksFile) encode() ([]byte, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > maxLinksFile {
		return nil, fmt.Errorf("the credential file would be %d bytes, more than the %d it may hold; run valley-web-link prune", len(data), maxLinksFile)
	}
	return data, nil
}

// writeLinks writes the credential file durably, mode 0640, in the group
// of its directory, which the publisher reads it through.
func writeLinks(path string, f linksFile) error {
	data, err := f.encode()
	if err != nil {
		return err
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("could not read the group of %s", filepath.Dir(path))
	}
	return writeDurableGroup(path, data, 0o640, int(st.Gid))
}

// linksLockFile is the links lock's name in the credential file's
// directory.
const linksLockFile = "lock"

// linksWait is how long a change to the credential file, and a pass about
// to read it, wait for the links lock. A test shortens it.
var linksWait = time.Minute

// errLinksBusy is lockLinks's error when the links lock stayed held for
// linksWait.
var errLinksBusy = errors.New("the links lock stayed held")

// lockLinks takes the links lock: the file named lock in the credential
// file's directory. It serializes every change to the credential file with
// the read of it a pass signs. A change (issue, revoke, prune) holds it
// alone while it reads and writes the file. A pass takes it inside the
// authorization lock, just before it reads the file, and holds it until it
// has signed. So a pass signs the file as it is when it signs, and once a
// change has finished, every pass that signs after it reads it. A change
// takes no other lock, so neither waits for the other in a cycle.
//
// Under the module the directory is root's, mode 0750, and tmpfiles makes
// the lock a regular file owned by root and the group valley-web-links,
// mode 0640. The publisher opens and locks it through that group, and can
// make, replace or remove nothing in the directory. The lock is opened
// without following a symbolic link or waiting on a FIFO, made when it is
// missing, and refused unless it is a regular file owned by root or by the
// user who runs the command. A missing directory is an error that wraps
// os.ErrNotExist.
//
// lockLinks waits at most linksWait, calls waiting once if it has to
// wait, and then fails with errLinksBusy.
func lockLinks(links string, waiting func(lock string)) (func(), error) {
	path := filepath.Join(filepath.Dir(links), linksLockFile)
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o640)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("the links lock %s is a symbolic link; it must be a regular file owned by root", path)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !info.Mode().IsRegular():
		f.Close()
		return nil, fmt.Errorf("the links lock %s is a %s, not a regular file owned by root", path, fileKind(info.Mode()))
	case !ok:
		f.Close()
		return nil, fmt.Errorf("could not read the owner of the links lock %s", path)
	case st.Uid != 0 && int(st.Uid) != os.Geteuid():
		f.Close()
		return nil, fmt.Errorf("the links lock %s is owned by UID %d, not by root", path, st.Uid)
	}
	deadline := time.Now().Add(linksWait)
	for waited := false; ; waited = true {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() { f.Close() }, nil
		case !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR):
			f.Close()
			return nil, fmt.Errorf("could not lock %s: %w", path, err)
		case time.Now().After(deadline):
			f.Close()
			return nil, fmt.Errorf("%w: %s was held for longer than %s", errLinksBusy, path, linksWait)
		case !waited:
			waiting(path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// fileKind names a file type that is not a regular file, for messages.
func fileKind(m os.FileMode) string {
	switch {
	case m&os.ModeNamedPipe != 0:
		return "FIFO"
	case m.IsDir():
		return "directory"
	case m&os.ModeSocket != 0:
		return "socket"
	case m&os.ModeDevice != 0:
		return "device"
	}
	return "file of another kind"
}

// lockChange takes the links lock for a change to the credential file, as
// lockLinks says, making the file's directory when it is missing. It says
// on stderr when it waits, and fails having changed nothing when the wait
// runs out.
func lockChange(c linkConfig, stderr io.Writer) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(c.links), 0o750); err != nil {
		return nil, err
	}
	unlock, err := lockLinks(c.links, func(lock string) {
		fmt.Fprintf(stderr, "valley-web-publish link: waiting up to %s for the policy pass or the change that holds the links lock %s\n", linksWait, lock)
	})
	if errors.Is(err, errLinksBusy) {
		return nil, fmt.Errorf("%w; the credential file is unchanged: run the command again", err)
	}
	return unlock, err
}

// linkDeliveryFile is the publisher's state for the read links, in the
// state directory: the greatest credential file revision a pass signed
// from, and the last authorization the receiver acknowledged, with the
// revision it was signed from.
const linkDeliveryFile = "links-delivery"

// linkDelivery is the delivery record. Signed is the greatest credential
// file revision a pass read to sign an authorization from; it is written
// under the links lock, before that authorization is signed. Sequence is the last authorization
// the receiver acknowledged, and Revision the credential file revision it
// carried the links of, or 0 when it carried none read from a file.
type linkDelivery struct {
	Signed   int64 `json:"signed"`
	Sequence int64 `json:"sequence"`
	Revision int64 `json:"revision"`
}

// maxDelivery is the most bytes the delivery record may hold.
const maxDelivery = 4 << 10

// readDelivery reads the delivery record from the state directory. A
// missing one records nothing signed or delivered. A change runs as root,
// and the state directory is the publisher's, so the record is opened
// without following a symbolic link or waiting on a FIFO, and must be a
// regular file of at most maxDelivery bytes.
func readDelivery(state string) (linkDelivery, error) {
	var d linkDelivery
	path := filepath.Join(state, linkDeliveryFile)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return d, nil
	case errors.Is(err, syscall.ELOOP):
		return d, fmt.Errorf("%s is a symbolic link, not a delivery record", path)
	case err != nil:
		return d, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return d, err
	}
	if !info.Mode().IsRegular() {
		return d, fmt.Errorf("%s is a %s, not a delivery record", path, fileKind(info.Mode()))
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDelivery+1))
	if err != nil {
		return d, err
	}
	if len(data) > maxDelivery {
		return d, fmt.Errorf("%s is larger than %d bytes, so not a delivery record", path, maxDelivery)
	}
	if err := exactjson.Decode(data, &d); err != nil {
		return d, fmt.Errorf("%s is not a delivery record: %w", path, err)
	}
	return d, nil
}

func writeDelivery(state string, d linkDelivery) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return writeDurable(filepath.Join(state, linkDeliveryFile), append(data, '\n'), 0o600)
}

// linkRead is what a policy pass read of the read links: the bindings
// its authorization carries, the credential file revision they were read
// from, or 0 when they reflect no file, and, when the pass is to fail,
// why.
type linkRead struct {
	bindings []export.Binding
	revision int64
	err      error
}

// readBindings reads the read links an authorization carries, under the
// links lock, and returns the lock held: the pass releases it once it has
// signed. It runs under the authorization lock, after the policy check,
// every time the pass signs. Every change to the credential file holds the
// links lock, so an authorization carries the links as the file holds them
// when it is signed, and a pass signs no link revoked before then.
//
// A pass with no --links carries none, and so does one whose credential
// file's directory does not exist, as it holds no file. A links lock the
// pass cannot take within linksWait, or a credential file it cannot read,
// gives none and an error. The pass then signs and sends an authorization
// with no binding, so no link is active at the edge and a withdrawal still
// reaches it, and fails. Otherwise the file's
// revision is recorded as signed from, durably, under the links lock and
// before the authorization is signed, so a change reads it consistently.
func (p *publisher) readBindings() (func(), linkRead) {
	none := func() {}
	if p.cfg.links == "" {
		return none, linkRead{}
	}
	unlock, err := lockLinks(p.cfg.links, func(lock string) {
		p.warn("waiting up to %s for the change to the credential file that holds the links lock %s", linksWait, lock)
	})
	if errors.Is(err, os.ErrNotExist) {
		return none, linkRead{}
	}
	if err != nil {
		return none, p.noLinks(err)
	}
	f, err := readLinks(p.cfg.links)
	if err != nil {
		return unlock, p.noLinks(err)
	}
	d, err := readDelivery(p.cfg.state)
	if err != nil {
		return unlock, p.noLinks(err)
	}
	if f.Revision > d.Signed {
		d.Signed = f.Revision
		if err := writeDelivery(p.cfg.state, d); err != nil {
			return unlock, p.noLinks(err)
		}
	}
	r := linkRead{revision: f.Revision}
	for _, l := range f.active() {
		r.bindings = append(r.bindings, export.Binding{ID: l.ID, Principal: l.Principal, Verifier: l.Verifier})
	}
	return unlock, r
}

// noLinks is the read of a pass whose authorization carries no read
// links, and fails, for err.
func (p *publisher) noLinks(err error) linkRead {
	p.warn("%v; the authorization carries no read links", err)
	return linkRead{err: err}
}

// recordDelivery records that the receiver stored a, which carried the
// links of the credential file's revision, under the authorization lock.
func (p *publisher) recordDelivery(a *export.Authorization, revision int64) error {
	d, err := readDelivery(p.cfg.state)
	if err != nil {
		return err
	}
	d.Sequence, d.Revision = a.Sequence, revision
	return writeDelivery(p.cfg.state, d)
}

// linkConfig is the link command's flags.
type linkConfig struct {
	config
	verb      string
	ids       []string
	principal string
	note      string
	all       bool
	check     bool
}

// issueFlags are the flags only issue reads: what it needs to read the
// registry and the projects' policy/read as a pass does.
var issueFlags = []string{"instance-repo", "destination", "host", "read-schema", "identity-schema", "registry-ref", "registry-dir"}

func parseLinkArgs(args []string) (linkConfig, error) {
	c := linkConfig{config: config{maxBundles: 1 << 30, maxStorage: 16 << 30}}
	fs := flag.NewFlagSet("valley-web-publish link", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.links, "links", "", "")
	fs.StringVar(&c.state, "state", "", "")
	fs.StringVar(&c.instanceRepo, "instance-repo", "", "")
	fs.StringVar(&c.destination, "destination", "", "")
	fs.StringVar(&c.host, "host", "", "")
	fs.StringVar(&c.readSchema, "read-schema", os.Getenv("VALLEY_READ_SCHEMA"), "")
	fs.StringVar(&c.idSchema, "identity-schema", os.Getenv("VALLEY_IDENTITY_SCHEMA"), "")
	fs.StringVar(&c.registryRef, "registry-ref", "refs/heads/main", "")
	fs.StringVar(&c.registryDir, "registry-dir", "identity", "")
	fs.StringVar(&c.principal, "principal", "", "")
	fs.StringVar(&c.note, "note", "", "")
	fs.BoolVar(&c.all, "all", false, "")
	fs.BoolVar(&c.check, "check", false, "")
	// Flags may follow the verb and the IDs, as the wrapper appends the
	// module's.
	var words []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return c, err
		}
		if fs.NArg() == 0 {
			break
		}
		words = append(words, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(words) == 0 {
		return c, errors.New("give a verb: issue, list, revoke or prune")
	}
	c.verb, c.ids = words[0], words[1:]
	for _, id := range c.ids {
		if !export.ValidLinkID(id) {
			return c, fmt.Errorf("%q is not a credential ID: lk- and 16 lowercase hex digits", id)
		}
	}
	var given []string
	fs.Visit(func(f *flag.Flag) {
		if slices.Contains(issueFlags, f.Name) {
			given = append(given, "--"+f.Name)
		}
	})
	bad := func(format string, args ...any) (linkConfig, error) { return c, fmt.Errorf(format, args...) }
	switch {
	case c.links == "":
		return bad("--links is required")
	case filepath.Base(c.links) == linksLockFile:
		return bad("--links may not name a file %s: the links lock has that name", linksLockFile)
	case !slices.Contains([]string{"issue", "list", "revoke", "prune"}, c.verb):
		return bad("%q is not a verb: give issue, list, revoke or prune", c.verb)
	case c.note != "" && c.verb != "issue":
		return bad("--note goes with issue")
	case len(given) > 0 && c.verb != "issue":
		return bad("%s goes with issue", strings.Join(given, ", "))
	case c.all && c.verb != "revoke":
		return bad("--all goes with revoke")
	case c.check && c.verb != "list":
		return bad("--check goes with list")
	case c.principal != "" && c.verb != "issue" && c.verb != "revoke":
		return bad("--principal goes with issue and revoke")
	case len(c.ids) > 0 && c.verb == "prune":
		return bad("prune takes no credential ID")
	}
	switch c.verb {
	case "issue":
		switch {
		case len(c.ids) > 0:
			return bad("issue takes no credential ID")
		case !export.ValidName(c.principal) || c.principal == readaccess.Anyone:
			return bad("--principal %q is not a principal; issue needs one", c.principal)
		case checkNote(c.note) != nil:
			return bad("--note: %v", checkNote(c.note))
		case c.instanceRepo == "" || !filepath.IsAbs(c.instanceRepo):
			return bad("issue needs --instance-repo, an absolute local path")
		case !export.ValidName(c.destination):
			return bad("issue needs --destination, a name")
		case c.host == "":
			return bad("issue needs --host")
		case !readaccess.ValidRef(c.registryRef):
			return bad("--registry-ref %q is not a full ref name", c.registryRef)
		case c.readSchema == "" || c.idSchema == "":
			return bad("no schema: give --read-schema and --identity-schema, or set VALLEY_READ_SCHEMA and VALLEY_IDENTITY_SCHEMA")
		}
	case "revoke":
		given := 0
		for _, b := range []bool{len(c.ids) > 0, c.principal != "", c.all} {
			if b {
				given++
			}
		}
		switch {
		case given != 1 || len(c.ids) > 1:
			return bad("revoke takes one of a credential ID, --principal or --all")
		case c.principal != "" && !export.ValidName(c.principal):
			return bad("--principal %q is not a principal", c.principal)
		}
	}
	if c.state == "" {
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return bad("no state directory: give --state, or set XDG_STATE_HOME or HOME")
			}
			base = filepath.Join(home, ".local", "state")
		}
		c.state = filepath.Join(base, "valley", "web-publish")
	}
	return c, nil
}

// runLink is the link command.
func runLink(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c, err := parseLinkArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, linkUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley-web-publish link: %v\n%s\n", err, linkUsage)
		return 2
	}
	switch c.verb {
	case "issue":
		err = issueLink(ctx, c, stdout, stderr)
	case "revoke":
		err = revokeLinks(c, stdout, stderr)
	case "prune":
		err = pruneLinks(c, stdout, stderr)
	case "list":
		err = listLinks(c, stdout, stderr)
	}
	if errors.Is(err, errPending) {
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley-web-publish link: %v\n", err)
		return 1
	}
	return 0
}

// issueLink checks that the principal may read a project at the
// destination, then adds a link for it to the credential file and prints
// the link. It refuses to write the file within linksHeadroom of its
// limit, which it keeps for revocations.
func issueLink(ctx context.Context, c linkConfig, stdout, stderr io.Writer) error {
	readable, err := readableBy(ctx, c.config, c.principal, stderr)
	if err != nil {
		return err
	}
	if len(readable) == 0 {
		return fmt.Errorf("%s may read no project at %s: the registry grants it no edge read there that a project's policy/read consents to; no link is issued", c.principal, c.destination)
	}
	unlock, err := lockChange(c, stderr)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := readLinks(c.links)
	if err != nil {
		return err
	}
	d, err := readDelivery(c.state)
	switch {
	case err != nil:
		return err
	case len(f.active()) >= export.MaxBindings:
		return fmt.Errorf("%d links are active, the most an authorization carries; revoke one first", export.MaxBindings)
	}
	if err := noCoreDump(); err != nil {
		return fmt.Errorf("could not keep the secret out of a core dump: %w", err)
	}
	var id string
	for id == "" || slices.ContainsFunc(f.Links, func(l linkRecord) bool { return l.ID == id }) {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		id = "lk-" + hex.EncodeToString(b[:])
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	verifier := sha256.Sum256(secret[:])
	f.Revision = nextRevision(f, d)
	f.Links = append(f.Links, linkRecord{ID: id, Principal: c.principal, Note: c.note,
		Verifier: hex.EncodeToString(verifier[:]), Issued: time.Now().UTC().Format(time.RFC3339), Changed: f.Revision})
	data, err := f.encode()
	if err != nil {
		return err
	}
	if len(data) > maxLinksFile-linksHeadroom {
		return fmt.Errorf("the credential file would be %d bytes, within %d KiB of the %d it may hold, which is kept for revocations; run valley-web-link prune to remove the revocations the edge enforces, then issue again", len(data), linksHeadroom>>10, maxLinksFile)
	}
	if err := writeLinks(c.links, f); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "valley-web-publish link: issued %s for %s, who may read %s; the link is shown once, and the edge accepts it once a policy pass delivers it\n",
		id, c.principal, listNames(readable))
	fmt.Fprintf(stdout, "https://%s/-/redeem#%s.%s\n", c.destination, id, base64.RawURLEncoding.EncodeToString(secret[:]))
	return nil
}

// readableBy are the projects the principal may read at the destination,
// read as a policy pass reads them: the read grants at the destination in
// the registry, and the policy/read on main of each project a grant
// selects and the host declaration places in a group. It fetches into a
// cache of its own, which it removes, so it writes nothing of the
// publisher's.
func readableBy(ctx context.Context, cfg config, principal string, stderr io.Writer) ([]string, error) {
	p, err := newPublisher(cfg, io.Discard, stderr)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "valley-web-link-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	p.cache = tmp
	p.origin = filepath.Join(tmp, "origin.git")
	p.mirrors = filepath.Join(tmp, "repos")
	if err := p.access.CheckSchemas(ctx); err != nil {
		return nil, err
	}
	inst, err := p.fetchInstance(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := p.readGrants(ctx, inst)
	if err != nil {
		return nil, err
	}
	if len(grants[principal]) == 0 {
		return nil, fmt.Errorf("the registry grants %s no edge read at %s, or does not name %s; no link is issued", principal, cfg.destination, principal)
	}
	var readable []string
	for _, name := range inst.projects {
		if !grants.Selects(principal, name) || p.places.placements[name].Group == "" || !export.ValidName(name) {
			continue
		}
		mirror, tip, err := p.fetchMain(ctx, name, p.projectURL(name))
		if errors.Is(err, errNoMain) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: could not read main: %w", name, err)
		}
		consent, err := p.access.Consent(ctx, mirror, tip, name)
		var bad *readaccess.InvalidError
		switch {
		case errors.Is(err, readaccess.ErrMissing), errors.As(err, &bad):
			continue
		case err != nil:
			return nil, fmt.Errorf("%s: could not read its policy/read at %s: %w", name, short(tip), err)
		}
		if grants.May(principal, consent) {
			readable = append(readable, name)
		}
	}
	return readable, nil
}

// revokeLinks revokes the link with the ID given, every active link of
// the principal, or every active link, and prints each it revoked. Each
// link it revokes records the revision it writes. It writes the file only
// when it revoked a link.
func revokeLinks(c linkConfig, stdout, stderr io.Writer) error {
	unlock, err := lockChange(c, stderr)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := readLinks(c.links)
	if err != nil {
		return err
	}
	d, err := readDelivery(c.state)
	if err != nil {
		return err
	}
	next := nextRevision(f, d)
	now := time.Now().UTC().Format(time.RFC3339)
	found, changed := false, false
	for i, l := range f.Links {
		switch {
		case len(c.ids) > 0 && l.ID != c.ids[0], c.principal != "" && l.Principal != c.principal:
			continue
		}
		found = true
		if l.Revoked != "" {
			if len(c.ids) > 0 {
				fmt.Fprintf(stdout, "%s (%s) was revoked at %s\n", l.ID, l.Principal, l.Revoked)
			}
			continue
		}
		f.Links[i].Revoked, f.Links[i].Changed = now, next
		changed = true
		fmt.Fprintf(stdout, "revoked %s (%s)\n", l.ID, l.Principal)
	}
	switch {
	case len(c.ids) > 0 && !found:
		return fmt.Errorf("%s holds no link %s", c.links, c.ids[0])
	case !changed && len(c.ids) == 0:
		fmt.Fprintln(stdout, "no active link to revoke")
	}
	if !changed {
		return nil
	}
	f.Revision = next
	return writeLinks(c.links, f)
}

// pruneLinks removes the revoked links whose revocation is enforced: the
// receiver acknowledged an authorization that carried the links of the
// revision that revoked them, or of a later one. It prints each it
// removed, and writes the file at the next revision when it removed any.
//
// Pruning loses nothing. The edge accepts a link only while the stored
// authorization binds it, so a link absent from the file is as revoked as
// one recorded revoked, and no pass carries either. A pending revocation
// is kept until a pass delivers it.
func pruneLinks(c linkConfig, stdout, stderr io.Writer) error {
	unlock, err := lockChange(c, stderr)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := readLinks(c.links)
	if err != nil {
		return err
	}
	d, err := readDelivery(c.state)
	if err != nil {
		return err
	}
	kept, pending := []linkRecord{}, 0
	for _, l := range f.Links {
		switch {
		case l.Revoked == "":
			kept = append(kept, l)
		case d.Revision < l.Changed:
			kept = append(kept, l)
			pending++
		default:
			fmt.Fprintf(stdout, "pruned %s (%s), revoked at %s\n", l.ID, l.Principal, l.Revoked)
		}
	}
	if pending > 0 {
		fmt.Fprintf(stdout, "kept %d revoked links whose revocation is pending until a pass delivers it\n", pending)
	}
	if len(kept) == len(f.Links) {
		fmt.Fprintln(stdout, "no enforced revocation to prune")
		return nil
	}
	f.Revision, f.Links = nextRevision(f, d), kept
	return writeLinks(c.links, f)
}

// errPending is list --check's error when a link it shows is pending; it
// has said why.
var errPending = errors.New("pending")

// listLinks prints the links, or those with the IDs given, and how far
// each has reached the edge, from the publisher's delivery record. A link
// is enforced once the receiver acknowledged an authorization that
// carried the links of the revision that set its state, or of a later
// one: every authorization signed after that one carries the links of a
// revision at least as late, or none, unless the file is restored from an
// older copy.
func listLinks(c linkConfig, stdout, stderr io.Writer) error {
	f, err := readLinks(c.links)
	if err != nil {
		return err
	}
	d, err := readDelivery(c.state)
	if err != nil {
		return err
	}
	shown := f.Links
	if len(c.ids) > 0 {
		shown = nil
		for _, id := range c.ids {
			i := slices.IndexFunc(f.Links, func(l linkRecord) bool { return l.ID == id })
			if i < 0 {
				return fmt.Errorf("%s holds no link %s", c.links, id)
			}
			shown = append(shown, f.Links[i])
		}
	}
	switch {
	case d.Sequence == 0:
		fmt.Fprintf(stdout, "the receiver has acknowledged no authorization from this publisher's state; the credential file is at revision %d\n", f.Revision)
	case d.Revision == 0:
		fmt.Fprintf(stdout, "the receiver last acknowledged authorization %d, which carried no read links from the credential file, now at revision %d\n", d.Sequence, f.Revision)
	default:
		fmt.Fprintf(stdout, "the receiver last acknowledged authorization %d, which carried the links of revision %d of the credential file, now at revision %d\n",
			d.Sequence, d.Revision, f.Revision)
	}
	var problems []string
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPRINCIPAL\tSTATE\tDELIVERY\tISSUED\tNOTE")
	for _, l := range shown {
		state, delivery := "active", "enforced"
		if l.Revoked != "" {
			state = "revoked"
		}
		switch {
		case d.Revision < l.Changed && l.Revoked == "":
			delivery = "pending"
			problems = append(problems, fmt.Sprintf("%s: issued here; the edge does not accept it until a pass delivers it", l.ID))
		case d.Revision < l.Changed:
			delivery = "pending"
			problems = append(problems, fmt.Sprintf("%s: revoked here; the edge may accept it until a pass delivers the revocation", l.ID))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", l.ID, l.Principal, state, delivery, l.Issued, l.Note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if c.check && len(problems) > 0 {
		for _, line := range problems {
			fmt.Fprintln(stderr, line)
		}
		return errPending
	}
	return nil
}

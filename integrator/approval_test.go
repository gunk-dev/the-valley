package main

// Whether a signed approval authorizes a recorded request, read from real
// scratch repositories: notes signed with throwaway keys, and record
// commits written as loose objects, as the push hook admits them or as no
// hook would.

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"the-valley/approval"
	"the-valley/change"
	"the-valley/note"
)

var approvalInstance = strings.Repeat("5f", 32)

func approvalKey(label string) approval.Key {
	seed := sha256.Sum256([]byte("integrator approval test key " + label))
	return approval.NewKey(ed25519.NewKeyFromSeed(seed[:]))
}

// listing is an approval list naming each key under principal.
func listing(principal string, keys ...approval.Key) []byte {
	var b strings.Builder
	b.WriteString("# approval keys\n")
	for _, k := range keys {
		b.WriteString(note.NewVerifierKey(principal, k.Public()).String() + "\n")
	}
	return []byte(b.String())
}

// written is a record commit and the record it holds.
type written struct {
	oid string
	r   *change.Record
}

// entry is one tree entry: its mode as a tree object spells it, its name
// and its object.
type entry struct{ mode, name, id string }

type approvalFixture struct {
	t        *testing.T
	repo     string
	empty    string // the empty tree
	base     string // the commit every head is made on
	heads    int
	operator approval.Key
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	f := &approvalFixture{t: t, repo: t.TempDir(), operator: approvalKey("operator")}
	if _, err := git(f.repo, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	f.empty = f.tree()
	f.base = f.commit("base", f.empty)
	return f
}

// object writes body as a loose object of kind, whether or not git would
// write it, and returns its ID.
func (f *approvalFixture) object(kind string, body []byte) string {
	f.t.Helper()
	header := fmt.Sprintf("%s %d\x00", kind, len(body))
	sum := sha1.Sum(append([]byte(header), body...))
	id := hex.EncodeToString(sum[:])
	path := filepath.Join(f.repo, ".git", "objects", id[:2], id[2:])
	if _, err := os.Stat(path); err == nil {
		return id
	}
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write([]byte(header))
	w.Write(body)
	w.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o444); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *approvalFixture) tree(entries ...entry) string {
	var b bytes.Buffer
	for _, e := range entries {
		raw, err := hex.DecodeString(e.id)
		if err != nil {
			f.t.Fatal(err)
		}
		b.WriteString(e.mode + " " + e.name + "\x00")
		b.Write(raw)
	}
	return f.object("tree", b.Bytes())
}

// commit is a commit of tree on parents, with fixed dates, so equal
// content on equal parents is one commit.
func (f *approvalFixture) commit(message, tree string, parents ...string) string {
	var b strings.Builder
	b.WriteString("tree " + tree + "\n")
	for _, p := range parents {
		b.WriteString("parent " + p + "\n")
	}
	b.WriteString("author t <t@localhost> 0 +0000\ncommitter t <t@localhost> 0 +0000\n\n" + message + "\n")
	return f.object("commit", []byte(b.String()))
}

// head is a new commit on the base, unlike every other head.
func (f *approvalFixture) head() string {
	f.heads++
	return f.commit(fmt.Sprintf("head %d", f.heads), f.empty, f.base)
}

func (f *approvalFixture) blob(text string) string { return f.object("blob", []byte(text)) }

// recordTree is a tree holding text as record.json.
func (f *approvalFixture) recordTree(text string) string {
	return f.tree(entry{"100644", "record.json", f.blob(text)})
}

func (f *approvalFixture) encode(r *change.Record) string {
	f.t.Helper()
	data, err := change.Encode(r)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func (f *approvalFixture) write(r *change.Record, parents ...string) written {
	return written{oid: f.commit("a change record", f.recordTree(f.encode(r)), parents...), r: r}
}

func version(n int, head string) change.Version {
	return change.Version{N: n, Head: change.HeadPrefix + head, Title: "a version", Branch: "topic", Uploader: "owner"}
}

func clone(r *change.Record) *change.Record {
	c := *r
	c.Versions = slices.Clone(r.Versions)
	return &c
}

func headOf(v change.Version) string { return strings.TrimPrefix(v.Head, change.HeadPrefix) }

// created is a change's first record, ready at head, as the hook's create
// row admits it.
func (f *approvalFixture) created(head string) written {
	return f.write(&change.Record{
		Schema: change.Schema, ChangeID: readyID, Target: "main", Owner: "owner",
		State: change.Ready, Seq: 1, Versions: []change.Version{version(1, head)}, Active: 1,
	}, head)
}

// adding is prev's record with a version at head added, as the hook's
// new-version row admits it, unwritten.
func adding(prev written, head string) *change.Record {
	r := clone(prev.r)
	r.Versions = append(r.Versions, version(len(r.Versions)+1, head))
	r.Active, r.State, r.Seq = len(r.Versions), change.Ready, r.Seq+1
	return r
}

func (f *approvalFixture) versioned(prev written, head string) written {
	return f.write(adding(prev, head), prev.oid, head)
}

// moved changes prev's state, as the hook's unready, re-ready and withdraw
// rows admit it.
func (f *approvalFixture) moved(prev written, state string) written {
	r := clone(prev.r)
	r.State, r.Seq = state, r.Seq+1
	return f.write(r, prev.oid)
}

// approvalStatement approves version n of the record signed.
func approvalStatement(signed written, n int) approval.Statement {
	return approval.Statement{
		Instance: approvalInstance, Project: "app", Target: "main", Change: readyID,
		Version: n, Head: signed.r.Versions[n-1].Head, Record: change.HeadPrefix + signed.oid,
		Principal: "operator", Issued: time.Date(2026, 11, 6, 0, 43, 26, 0, time.UTC),
	}
}

func (f *approvalFixture) sign(s approval.Statement, k approval.Key) []byte {
	f.t.Helper()
	n, err := approval.Sign(s, k)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// approved is the query for a request at the head of version n of signed,
// with the operator's approval of it, and signed as the current record.
func (f *approvalFixture) approved(signed written, n int) approvalQuery {
	s := approvalStatement(signed, n)
	return approvalQuery{
		note: f.object("blob", f.sign(s, f.operator)), record: signed.oid,
		instance: approvalInstance, project: "app", target: "main", changeID: readyID,
		head:          strings.TrimPrefix(s.Head, change.HeadPrefix),
		authorityName: approvalSignersFile, authority: listing("operator", f.operator),
	}
}

// signedFor is the operator's approval of version n of a record held as
// the commit oid, whatever that commit holds.
func (f *approvalFixture) signedFor(oid string, r *change.Record, n int) approvalQuery {
	return f.approved(written{oid: oid, r: r}, n)
}

// outcome unpacks a result, holding it to exactly one of three: authorized
// with proofs, refused, or an error.
func outcome(t *testing.T, r approvalResult) ([]proof, *approvalRefusal, error) {
	t.Helper()
	proofs, ok := r.Authorized()
	decided := 0
	for _, b := range []bool{ok, r.Refusal() != nil, r.Err() != nil} {
		if b {
			decided++
		}
	}
	if decided != 1 || ok != (len(proofs) > 0) {
		t.Fatalf("authorized %v with %d proofs, refusal %v, err %v", ok, len(proofs), r.Refusal(), r.Err())
	}
	return proofs, r.Refusal(), r.Err()
}

func verify(f *approvalFixture, q approvalQuery) ([]proof, *approvalRefusal, error) {
	f.t.Helper()
	return outcome(f.t, verifyApproval(context.Background(), f.repo, q))
}

func TestAnApprovalAuthorizesTheRequestItsHistoryLeadsTo(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(f *approvalFixture) (q approvalQuery, walked []string)
	}{
		{"the current record is the signed one", func(f *approvalFixture) (approvalQuery, []string) {
			c1 := f.created(f.head())
			return f.approved(c1, 1), []string{c1.oid}
		}},
		{"versions were added since the signed record", func(f *approvalFixture) (approvalQuery, []string) {
			c1 := f.created(f.head())
			c2 := f.versioned(c1, f.head())
			c3 := f.versioned(c2, f.head())
			q := f.approved(c1, 1)
			q.record = c3.oid
			return q, []string{c3.oid, c2.oid, c1.oid}
		}},
		{"the approval came after a withdrawal and a fresh ready", func(f *approvalFixture) (approvalQuery, []string) {
			c1 := f.created(f.head())
			c3 := f.moved(f.moved(c1, change.Withdrawn), change.Ready)
			c4 := f.versioned(c3, f.head())
			q := f.approved(c3, 1)
			q.record = c4.oid
			return q, []string{c4.oid, c3.oid}
		}},
		{"the approved head is at two versions, and the approval names the later", func(f *approvalFixture) (approvalQuery, []string) {
			h1 := f.head()
			c3 := f.versioned(f.versioned(f.created(h1), f.head()), h1)
			return f.approved(c3, 3), []string{c3.oid}
		}},
		{"the approved head is at two versions, and the approval names the earlier", func(f *approvalFixture) (approvalQuery, []string) {
			h1 := f.head()
			c3 := f.versioned(f.versioned(f.created(h1), f.head()), h1)
			return f.approved(c3, 1), []string{c3.oid}
		}},
		{"the policy requires hardware, and the hardware list holds the key", func(f *approvalFixture) (approvalQuery, []string) {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.hardware, q.authorityName = true, approvalSignersHardwareFile
			return q, []string{c1.oid}
		}},
		{"the walk is as long as the record bounds allow", func(f *approvalFixture) (approvalQuery, []string) {
			// Each step adds a version, and the versions a record holds
			// are bounded by change.MaxRecordBlob before change.MaxVersions.
			h1, h2 := f.head(), f.head()
			other := map[string]string{h1: h2, h2: h1}
			c1 := f.created(h1)
			walked, cur := []string{c1.oid}, c1
			for {
				head := other[headOf(cur.r.Last())]
				next := adding(cur, head)
				if len(f.encode(next)) > change.MaxRecordBlob {
					break
				}
				cur = f.write(next, cur.oid, head)
				walked = append([]string{cur.oid}, walked...)
			}
			if len(walked) < 100 {
				f.t.Fatalf("the longest walk is %d records", len(walked))
			}
			f.t.Logf("the longest walk reads %d records", len(walked))
			q := f.approved(c1, 1)
			q.record = cur.oid
			return q, walked
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newApprovalFixture(t)
			q, walked := c.setup(f)
			authority := bytes.Clone(q.authority)
			proofs, refusal, err := verify(f, q)
			if err != nil || refusal != nil {
				t.Fatalf("refused: %v %v", refusal, err)
			}
			if len(proofs) != 1 || proofs[0].kind != proofSignedNote {
				t.Fatalf("proofs = %+v, want one signed note", proofs)
			}
			p := proofs[0].signedNote
			q.authority[0] ^= 0xff
			body, err := git(f.repo, "cat-file", "blob", q.note)
			if err != nil {
				t.Fatal(err)
			}
			s, err := approval.ParseNote([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case string(p.note) != body:
				t.Errorf("the proof holds the note %q, and the blob is %q", p.note, body)
			case p.statement != s:
				t.Errorf("the proof holds the statement %+v, and the note %+v", p.statement, s)
			case p.authorityName != q.authorityName || !bytes.Equal(p.authority, authority):
				t.Errorf("the proof holds the list %s %q, and the query gave %s %q", p.authorityName, p.authority, q.authorityName, authority)
			case !slices.Equal(p.records, walked):
				t.Errorf("the walk read %d records %q, want %d", len(p.records), p.records, len(walked))
			}
		})
	}
}

func TestAnApprovalIsRefusedWithItsReason(t *testing.T) {
	missing := strings.Repeat("ab", 20)
	for _, c := range []struct {
		name  string
		want  approvalReason
		setup func(f *approvalFixture) approvalQuery
	}{
		// The note.
		{"the note is not in the repository", approvalInvalid, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.note = missing
			return q
		}},
		{"the note is a commit", approvalInvalid, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.note = f.base
			return q
		}},
		{"the note is past the bound", approvalInvalid, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.note = f.blob(strings.Repeat("x", approval.MaxNote+1))
			return q
		}},
		{"the note is an attestation signed with an approval key", approvalInvalid, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			seed := sha256.Sum256([]byte("integrator approval test key operator"))
			n, err := note.Sign([]byte(note.StatementType+"\nsubject.primary valley-tree-v1\n"), "operator", ed25519.NewKeyFromSeed(seed[:]))
			if err != nil {
				f.t.Fatal(err)
			}
			q.note = f.object("blob", n)
			return q
		}},
		{"the note carries a second signature", approvalInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			seed := sha256.Sum256([]byte("a second signer"))
			n, err := note.AddSignature(f.sign(approvalStatement(c1, 1), f.operator), "other", ed25519.NewKeyFromSeed(seed[:]))
			if err != nil {
				f.t.Fatal(err)
			}
			q.note = f.object("blob", n)
			return q
		}},
		{"the signed text was altered", approvalInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.note = f.object("blob", bytes.Replace(f.sign(approvalStatement(c1, 1), f.operator), []byte("00:43:26Z"), []byte("00:43:27Z"), 1))
			return q
		}},

		// The list.
		{"the key is not listed", keyNotListed, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.authority = listing("operator", approvalKey("another"))
			return q
		}},
		{"the key is listed under another principal", keyNotListed, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.authority = listing("alice", f.operator)
			return q
		}},
		{"the list is empty", keyNotListed, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.authority = nil
			return q
		}},
		{"the list holds only comments", keyNotListed, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.authority = listing("operator")
			return q
		}},
		{"the policy requires hardware, and only the general list holds the key", keyNotListed, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.hardware, q.authorityName, q.authority = true, approvalSignersHardwareFile, listing("operator", approvalKey("hardware"))
			return q
		}},
		{"the policy requires hardware, and the general list is given", authorityInvalid, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.hardware = true
			return q
		}},
		{"the policy requires no hardware, and the hardware list is given", authorityInvalid, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.authorityName = approvalSignersHardwareFile
			return q
		}},
		{"the list does not read", authorityInvalid, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.authority = append(listing("operator", f.operator), "not a key\n"...)
			return q
		}},

		// The scope and the request.
		{"another instance", scopeMismatch, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.instance = strings.Repeat("6e", 32)
			return q
		}},
		{"another project", scopeMismatch, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.project = "site"
			return q
		}},
		{"another target", scopeMismatch, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.target = "release"
			return q
		}},
		{"another change", scopeMismatch, func(f *approvalFixture) approvalQuery {
			q := f.approved(f.created(f.head()), 1)
			q.changeID = draftID
			return q
		}},
		{"the request files another head", requestMismatch, func(f *approvalFixture) approvalQuery {
			c2 := f.versioned(f.created(f.head()), f.head())
			q := f.approved(c2, 1)
			q.head = headOf(c2.r.Versions[1])
			return q
		}},
		{"the approved head is at two versions, and the approval names one between", requestMismatch, func(f *approvalFixture) approvalQuery {
			h1 := f.head()
			c3 := f.versioned(f.versioned(f.created(h1), f.head()), h1)
			s := approvalStatement(c3, 3)
			s.Version = 2
			q := f.approved(c3, 3)
			q.note = f.object("blob", f.sign(s, f.operator))
			return q
		}},
		{"the signed and the current record give the version different heads", requestMismatch, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			h := f.head()
			r := adding(c1, h)
			r.Versions[0] = version(1, f.head())
			q.record = f.write(r, c1.oid, h).oid
			return q
		}},
		{"the approved head is a blob", requestMismatch, func(f *approvalFixture) approvalQuery {
			return f.approved(f.created(f.blob("not a commit\n")), 1)
		}},
		{"the approved head is a tree", requestMismatch, func(f *approvalFixture) approvalQuery {
			return f.approved(f.created(f.empty), 1)
		}},
		{"the approved head is not in the repository", requestMismatch, func(f *approvalFixture) approvalQuery {
			return f.approved(f.created(missing), 1)
		}},

		// Readiness.
		{"the signed record is a draft", notReady, func(f *approvalFixture) approvalQuery {
			return f.approved(f.moved(f.created(f.head()), change.Draft), 1)
		}},
		{"the current record is withdrawn", notReady, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.record = f.moved(c1, change.Withdrawn).oid
			return q
		}},
		{"the change was withdrawn and readied again since the approval", notReady, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.record = f.versioned(f.moved(f.moved(c1, change.Withdrawn), change.Ready), f.head()).oid
			return q
		}},
		{"the change was a draft since the approval", notReady, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.record = f.versioned(f.moved(c1, change.Draft), f.head()).oid
			return q
		}},
		{"a replacement ref stands a ready record in for the current one", notReady, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.record = f.moved(c1, change.Draft).oid
			if _, err := git(f.repo, "update-ref", "refs/replace/"+q.record, f.versioned(c1, f.head()).oid); err != nil {
				f.t.Fatal(err)
			}
			return q
		}},

		// The signed record.
		{"the signed record is not in the repository", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			return f.signedFor(missing, c1.r, 1)
		}},
		{"the signed record is a tree", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			return f.signedFor(f.recordTree(f.encode(c1.r)), c1.r, 1)
		}},
		{"the signed record's tree is a blob", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.blob(f.encode(c1.r)), h), c1.r, 1)
		}},
		{"the signed record's record.json is a tree", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.tree(entry{"100644", "record.json", f.empty}), h), c1.r, 1)
		}},
		{"the signed record's tree holds a second file", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			blob := f.blob(f.encode(c1.r))
			return f.signedFor(f.commit("a change record", f.tree(entry{"100644", "record.json", blob}, entry{"100644", "zz.json", blob}), h), c1.r, 1)
		}},
		{"the signed record's record.json is executable", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.tree(entry{"100755", "record.json", f.blob(f.encode(c1.r))}), h), c1.r, 1)
		}},
		{"the signed record's record.json is a symlink", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.tree(entry{"120000", "record.json", f.blob(f.encode(c1.r))}), h), c1.r, 1)
		}},
		{"the signed record's record.json is a submodule", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.tree(entry{"160000", "record.json", h}), h), c1.r, 1)
		}},
		{"the signed record's record.json is a directory", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.tree(entry{"40000", "record.json", f.recordTree(f.encode(c1.r))}), h), c1.r, 1)
		}},
		{"the signed record's file is at another path", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.tree(entry{"100644", "record.jsn", f.blob(f.encode(c1.r))}), h), c1.r, 1)
		}},
		{"the signed record's record.json does not read", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.recordTree("{}\n"), h), c1.r, 1)
		}},
		{"the signed record's record.json repeats a field", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			text := strings.Replace(f.encode(c1.r), `"state": "ready",`, `"state": "ready", "state": "ready",`, 1)
			return f.signedFor(f.commit("a change record", f.recordTree(text), h), c1.r, 1)
		}},
		{"the signed record's record.json holds an unknown field", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			text := strings.Replace(f.encode(c1.r), `"state": "ready",`, `"state": "ready", "approved": true,`, 1)
			return f.signedFor(f.commit("a change record", f.recordTree(text), h), c1.r, 1)
		}},
		{"the signed record has three parents", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			return f.signedFor(f.commit("a change record", f.recordTree(f.encode(c1.r)), h, f.head(), f.head()), c1.r, 1)
		}},
		{"the signed record is another change's", historyInvalid, func(f *approvalFixture) approvalQuery {
			h := f.head()
			c1 := f.created(h)
			other := clone(c1.r)
			other.ChangeID = draftID
			return f.signedFor(f.write(other, h).oid, c1.r, 1)
		}},

		// The walk.
		{"the current record is older than the signed one", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(f.versioned(c1, f.head()), 1)
			q.record = c1.oid
			return q
		}},
		{"the current record descends from another history", historyInvalid, func(f *approvalFixture) approvalQuery {
			h1 := f.head()
			c1 := f.created(h1)
			q := f.approved(c1, 1)
			r := clone(c1.r)
			r.Owner = "alice"
			other := f.write(r, h1)
			q.record = f.versioned(f.versioned(other, f.head()), f.head()).oid
			return q
		}},
		{"a ready record follows the signed one with one parent", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			r := clone(c1.r)
			r.Seq++
			q.record = f.write(r, c1.oid).oid
			return q
		}},
		{"an update replaces a version rather than adding one", historyInvalid, func(f *approvalFixture) approvalQuery {
			c2 := f.versioned(f.created(f.head()), f.head())
			q := f.approved(c2, 1)
			h := f.head()
			r := clone(c2.r)
			r.Versions[1], r.Seq = version(2, h), r.Seq+1
			q.record = f.write(r, c2.oid, h).oid
			return q
		}},
		{"an update deletes a version", historyInvalid, func(f *approvalFixture) approvalQuery {
			c2 := f.versioned(f.created(f.head()), f.head())
			q := f.approved(c2, 1)
			r := clone(c2.r)
			r.Versions, r.Active, r.Seq = r.Versions[:1], 1, r.Seq+1
			q.record = f.write(r, c2.oid, headOf(r.Versions[0])).oid
			return q
		}},
		{"an update alters an earlier version", historyInvalid, func(f *approvalFixture) approvalQuery {
			c2 := f.versioned(f.created(f.head()), f.head())
			q := f.approved(c2, 2)
			h := f.head()
			r := adding(c2, h)
			r.Versions[0].Title = "another title"
			q.record = f.write(r, c2.oid, h).oid
			return q
		}},
		{"an update changes the owner", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			h := f.head()
			r := adding(c1, h)
			r.Owner = "alice"
			q.record = f.write(r, c1.oid, h).oid
			return q
		}},
		{"an update changes the target", historyInvalid, func(f *approvalFixture) approvalQuery {
			h1 := f.head()
			c1 := f.created(h1)
			q := f.approved(c1, 1)
			r := clone(c1.r)
			r.Target = "release"
			elsewhere := f.write(r, h1)
			h := f.head()
			back := adding(elsewhere, h)
			back.Target = "main"
			q.record = f.write(back, elsewhere.oid, h).oid
			return q
		}},
		{"an update skips a seq", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			h := f.head()
			r := adding(c1, h)
			r.Seq++
			q.record = f.write(r, c1.oid, h).oid
			return q
		}},
		{"an update leaves the new version inactive", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			h := f.head()
			r := adding(c1, h)
			r.Active = 1
			q.record = f.write(r, c1.oid, h).oid
			return q
		}},
		{"an update's second parent is not its new version's head", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.record = f.write(adding(c1, f.head()), c1.oid, f.head()).oid
			return q
		}},
		{"an update's new version's head is a blob", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			q.record = f.versioned(c1, f.blob("not a commit\n")).oid
			return q
		}},
		{"the signed record is reached only through a second parent", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			signed := f.versioned(c1, f.head())
			sibling := f.versioned(c1, f.head())
			q := f.approved(signed, 1)
			q.record = f.versioned(sibling, signed.oid).oid
			return q
		}},
		{"the current record's first parent is not in the repository", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			h := f.head()
			q.record = f.write(adding(c1, h), missing, h).oid
			return q
		}},
		{"the current record's record.json is past the bound", historyInvalid, func(f *approvalFixture) approvalQuery {
			c1 := f.created(f.head())
			q := f.approved(c1, 1)
			h := f.head()
			padded := f.encode(adding(c1, h)) + strings.Repeat(" ", change.MaxRecordBlob)
			q.record = f.commit("a change record", f.recordTree(padded), c1.oid, h)
			return q
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newApprovalFixture(t)
			proofs, refusal, err := verify(f, c.setup(f))
			switch {
			case err != nil:
				t.Fatalf("err = %v, want the refusal %s", err, c.want)
			case refusal == nil:
				t.Fatalf("authorized with %+v, want the refusal %s", proofs, c.want)
			case refusal.reason != c.want:
				t.Fatalf("refused with %v, want %s", refusal, c.want)
			}
			t.Log(refusal)
		})
	}
}

// An object past its bound is refused on its size: its content is never
// asked for.
func TestARecordObjectPastItsBoundIsNotRead(t *testing.T) {
	f := newApprovalFixture(t)
	h := f.head()
	text := recordJSON(t, readyID, change.Ready, h)
	blob := f.blob(text)
	tree := f.recordTree(text)
	bigCommit := f.commit(strings.Repeat("x", change.MaxRecordCommit), tree, h)
	bigTree := f.tree(entry{"100644", strings.Repeat("r", change.MaxRecordTree), blob})
	treeCommit := f.commit("a change record", bigTree, h)
	bigBlob := f.blob(text + strings.Repeat(" ", change.MaxRecordBlob))
	blobTree := f.tree(entry{"100644", "record.json", bigBlob})
	blobCommit := f.commit("a change record", blobTree, h)
	for _, c := range []struct {
		name, commit string
		asked        []string
	}{
		{"the commit", bigCommit, []string{"info " + bigCommit}},
		{"the tree", treeCommit, []string{"info " + treeCommit, "contents " + treeCommit, "info " + bigTree}},
		{"record.json", blobCommit, []string{"info " + blobCommit, "contents " + blobCommit, "info " + blobTree, "contents " + blobTree, "info " + bigBlob}},
	} {
		t.Run(c.name, func(t *testing.T) {
			objects, err := openObjectReader(context.Background(), f.repo)
			if err != nil {
				t.Fatal(err)
			}
			defer objects.close()
			_, err = readRecordCommit(objects, c.commit, "main", readyID)
			if !errors.As(err, new(objectRefusal)) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if !slices.Equal(objects.asked, c.asked) {
				t.Errorf("asked %q, want %q", objects.asked, c.asked)
			}
		})
	}
}

// git reads the repository it is given, whatever GIT_ variables the
// process holds.
func TestAnApprovalIsReadFromItsRepositoryWhateverTheEnvironmentSays(t *testing.T) {
	f := newApprovalFixture(t)
	q := f.approved(f.created(f.head()), 1)
	other := t.TempDir()
	if _, err := git(other, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(other, ".git", "objects"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.useReplaceRefs")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	if proofs, refusal, err := verify(f, q); err != nil || refusal != nil || len(proofs) != 1 {
		t.Fatalf("proofs %+v, refusal %v, err %v", proofs, refusal, err)
	}
}

// What decides nothing is an error: a query that names no object, a
// repository git cannot read, and a context that has ended.
func TestAnApprovalThatCannotBeCheckedIsAnError(t *testing.T) {
	f := newApprovalFixture(t)
	q := f.approved(f.created(f.head()), 1)
	bad := q
	bad.record = "main"
	if _, refusal, err := verify(f, bad); err == nil || refusal != nil {
		t.Errorf("a record named by a ref: refusal %v, err %v", refusal, err)
	}
	if _, refusal, err := outcome(t, verifyApproval(context.Background(), t.TempDir()+"/absent", q)); err == nil || refusal != nil {
		t.Errorf("a repository that does not exist: refusal %v, err %v", refusal, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, refusal, err := outcome(t, verifyApproval(ctx, f.repo, q)); !errors.Is(err, context.Canceled) || refusal != nil {
		t.Errorf("an ended context: refusal %v, err %v", refusal, err)
	}
}

// An object the repository lacks is missing, even when its configuration
// names a promisor remote that holds it.
func TestAnApprovalIsNeverFetched(t *testing.T) {
	f := newApprovalFixture(t)
	q := f.approved(f.created(f.head()), 1)
	remote := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "--bare", remote},
		{"push", "--quiet", remote, q.note + ":refs/tags/approval", f.base + ":refs/heads/main"},
		{"config", "core.repositoryformatversion", "1"},
		{"config", "extensions.partialClone", "origin"},
		{"config", "remote.origin.url", remote},
		{"config", "remote.origin.promisor", "true"},
	} {
		if _, err := git(f.repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	note := q.note
	if err := os.Remove(filepath.Join(f.repo, ".git", "objects", note[:2], note[2:])); err != nil {
		t.Fatal(err)
	}
	_, refusal, err := verify(f, q)
	if err != nil || refusal == nil || refusal.reason != approvalInvalid {
		t.Fatalf("refusal %v, err %v, want the note missing", refusal, err)
	}
}

// Authorized is true only as verifyApproval's success sets it.
func TestOnlyASuccessfulCheckAuthorizes(t *testing.T) {
	for name, r := range map[string]approvalResult{
		"the zero value":           {},
		"proofs alone":             {proofs: []proof{{kind: proofSignedNote}}},
		"authorized with no proof": {authorized: true},
		"a refusal":                {refusal: &approvalRefusal{reason: notReady}},
		"an error":                 {err: errors.New("git failed")},
	} {
		if proofs, ok := r.Authorized(); ok || proofs != nil {
			t.Errorf("%s: authorized %v with %v", name, ok, proofs)
		}
	}
}

// An error from the operating system keeps its identity, and its text is
// quoted: a path cannot put a newline or an escape into a message.
func TestAnOperatingSystemErrorIsQuoted(t *testing.T) {
	f := newApprovalFixture(t)
	q := f.approved(f.created(f.head()), 1)
	repo := filepath.Join(t.TempDir(), "a\nb\x1b[31m")
	_, refusal, err := outcome(t, verifyApproval(context.Background(), repo, q))
	var pathErr *os.PathError
	switch {
	case err == nil || refusal != nil:
		t.Fatalf("refusal %v, err %v, want an error", refusal, err)
	case !errors.As(err, &pathErr) || pathErr.Path != repo:
		t.Errorf("the error %v does not unwrap to the path error", err)
	case strings.ContainsAny(err.Error(), "\n\x1b"):
		t.Errorf("the error %q holds the path unquoted", err.Error())
	}
}

package main

// A signed approval (approve.go): the approval key a command signs with,
// what the host publishes about approval keys, and the note an approval
// publishes beside its request.
//
// An approval key is selected by a flag or the environment, never by
// default:
//
//	--approval-key FILE   VALLEY_APPROVAL_KEY=FILE   an unencrypted OpenSSH ed25519 key
//	--approval-piv        VALLEY_APPROVAL_PIV=SLOT   the key in a YubiKey's PIV slot
//
// A flag beats the environment, and at each level the two kinds exclude
// each other. --approval-piv signs on VALLEY_APPROVAL_PIV's slot when it is
// set, and on 9c otherwise; VALLEY_APPROVAL_PIV_READER picks the reader.
// The key that signs evidence (VALLEY_ATTEST_KEY) is another power and is
// never read here. With no key selected, nothing in this file runs: approve
// files the request as it does unsigned, and says no signed approval was
// written.
//
// With a key selected, an approval of a change with a record signs one
// statement (../approval): the instance identity the host publishes, the
// project origin's URL names, main, the change, the version and head the
// approval bound, the record commit that version was read from, the
// principal the key is published under, and the time. Before any check
// runs, approve reads the publication once and pins its tree, and refuses
// a software key that it does not list. After the checks the key signs: a
// PIV slot's session is opened, asks for the PIN and a touch, and is closed
// again. The note verifies under the signing key before anything is
// pushed. The publication is read again just before the push, and an
// approval whose publication changed meanwhile is refused. The note goes
// to approval.Ref(main, id) in the one push that files the request, leased
// against what approve read there, nothing included. Whatever stops the
// signing, nothing is pushed: approve never falls back to filing unsigned
// or to another key.
//
// The published lists say which keys the host lists for approving, which
// is what a client can check. What the host decides when it lands a change
// is its own reading of its own files.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	approvals "the-valley/approval"
	"the-valley/change"
	"the-valley/note"
)

// startPIV starts the PIV signer's helper (approval.StartPIV). A test
// holds a start open through it.
var startPIV = approvals.StartPIV

// pivSigner is the valley-piv-signer helper a PIV approval key signs
// through. The package fixes it to the helper's store path at build time
// where the helper builds, Linux, so no PATH changes which program
// receives the statements. Elsewhere it is empty, and a PIV key cannot be
// selected.
var pivSigner = ""

// The publication's files about approval keys (integrator/signers.go).
const (
	signersApprovers = "approval-signers"
	signersHardware  = "approval-signers-hardware"
	signersInstance  = "instance-id"
)

// approvalFlags are the approval-key flags one command was given.
type approvalFlags struct {
	file string
	piv  bool
}

// take reads the approval-key flag at args[i], if it is one, and returns
// the index of the last argument it read.
func (f *approvalFlags) take(args []string, i int) (last int, ok bool, err error) {
	value := ""
	switch arg := args[i]; {
	case arg == "--approval-piv":
		f.piv = true
		return i, true, nil
	case arg == "--approval-key" && i+1 < len(args):
		value, i = args[i+1], i+1
	case strings.HasPrefix(arg, "--approval-key="):
		value = strings.TrimPrefix(arg, "--approval-key=")
	default:
		return i, false, nil
	}
	if value == "" {
		return i, true, failure("--approval-key needs a key file")
	}
	f.file = value
	return i, true, nil
}

// approvalKey is the approval key a command signs with: a software key's
// file, or a YubiKey's PIV slot.
type approvalKey struct {
	file         string // a software key; "" for a PIV slot
	slot, reader string // the PIV slot and reader; "" for the helper's defaults
	from         string // what selected it
}

// key is the approval key the flags select, or else the environment, or
// nil when neither selects one.
func (f approvalFlags) key() (*approvalKey, error) {
	reader := os.Getenv("VALLEY_APPROVAL_PIV_READER")
	switch {
	case f.file != "" && f.piv:
		return nil, failure("--approval-key and --approval-piv select two approval keys; give one")
	case f.file != "":
		return &approvalKey{file: f.file, from: "--approval-key"}, nil
	case f.piv:
		return pivKey(os.Getenv("VALLEY_APPROVAL_PIV"), reader, "--approval-piv")
	}
	file, slot := os.Getenv("VALLEY_APPROVAL_KEY"), os.Getenv("VALLEY_APPROVAL_PIV")
	switch {
	case file != "" && slot != "":
		return nil, failure("VALLEY_APPROVAL_KEY and VALLEY_APPROVAL_PIV select two approval keys; unset one, or give --approval-key or --approval-piv")
	case file != "":
		return &approvalKey{file: file, from: "VALLEY_APPROVAL_KEY"}, nil
	case slot != "":
		return pivKey(slot, reader, "VALLEY_APPROVAL_PIV")
	}
	return nil, nil
}

// pivKey is a PIV slot selected as the approval key, where this build has
// a PIV signer.
func pivKey(slot, reader, from string) (*approvalKey, error) {
	if pivSigner == "" {
		return nil, failure("hardware signing is not available on this platform; " + from + " selects it")
	}
	return &approvalKey{slot: slot, reader: reader, from: from}, nil
}

// software reads a software key.
func (k *approvalKey) software() (approvals.Key, error) {
	raw, err := os.ReadFile(k.file)
	if err != nil {
		return approvals.Key{}, fmt.Errorf("the approval key %s (from %s) cannot be read: %v", k.file, k.from, err)
	}
	key, err := approvals.ParseKey(raw)
	if err != nil {
		return approvals.Key{}, fmt.Errorf("the approval key %s (from %s): %v", k.file, k.from, err)
	}
	return key, nil
}

// open is the key's signer, and what closes it: a software key read, or a
// PIV session started, whose touch requests go to prompt.
//
// The session's helper is a's child from the moment it starts, so a
// signal that interrupts the approval, while the helper reaches the device
// or waits for the PIN, stops it and waits for it: the helper then resets
// the YubiKey and the terminal it reads the PIN on. Whether the approval
// is interrupted, the helper's start and its registration are one
// critical section under a.mu, which interrupt reads the child under: an
// interrupt either comes first, and no helper starts, or finds it
// registered. The terminal's settings are saved before the helper starts,
// and put back whenever it has ended with them changed, as one killed
// while echo was off leaves them.
func (k *approvalKey) open(a *approval, prompt io.Writer) (approvals.Signer, func(), error) {
	if k.file != "" {
		key, err := k.software()
		return key, func() {}, err
	}
	tty := saveTerminal()
	done := make(chan struct{})
	a.mu.Lock()
	if a.interrupted {
		a.mu.Unlock()
		tty.release()
		return nil, nil, errors.New("the approval was interrupted before the PIV signer started")
	}
	p, err := startPIV(pivSigner, k.slot, k.reader, prompt)
	if err == nil {
		a.child, a.childDone, a.tty = p.Process(), done, tty
	}
	a.mu.Unlock()
	if err != nil {
		tty.release()
		return nil, nil, err
	}
	closeSigner := func() {
		p.Close()
		tty.restore()
		a.mu.Lock()
		interrupted := a.interrupted
		a.child, a.tty = nil, nil
		close(done)
		a.mu.Unlock()
		// An interrupt restores the terminal once done is closed, through
		// this descriptor, and then ends the process; it is not released
		// under it.
		if !interrupted {
			tty.release()
		}
	}
	if err := p.Open(); err != nil {
		closeSigner()
		return nil, nil, err
	}
	return p, closeSigner, nil
}

// approvalAuthority is what the host publishes about approval keys, read
// from one publication tree: the instance's identity and the keys of both
// approval signers files. An empty file lists no key, so no key of it
// approves.
type approvalAuthority struct {
	instance, oid, where string // where it was read, and the tree
	id                   string // instance-id
	approvers, hardware  []note.VerifierKey
}

// readApprovalAuthority reads the publication's approval files. A
// publication that is missing, or whose approval files are missing, are
// not files or do not read, is an error.
func readApprovalAuthority(g git) (*approvalAuthority, error) {
	p, err := readSigners(g)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errors.New("the host publishes no " + signersRef + ", so no approval key is known")
	}
	read := func(file string) (string, error) {
		if p.kinds[file] != "100644 blob" {
			return "", fmt.Errorf("%s holds no %s file", p.where, file)
		}
		body, err := g.restricted().output(true, "cat-file", "blob", p.blobs[file])
		if err != nil {
			return "", fmt.Errorf("could not read %s in %s", file, p.where)
		}
		return body, nil
	}
	a := &approvalAuthority{instance: p.instance, oid: p.oid, where: p.where}
	if a.id, err = read(signersInstance); err != nil {
		return nil, err
	}
	if len(a.id) != 64 || strings.Trim(a.id, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("%s in %s is not 64 lowercase hex digits", signersInstance, p.where)
	}
	for file, into := range map[string]*[]note.VerifierKey{signersApprovers: &a.approvers, signersHardware: &a.hardware} {
		body, err := read(file)
		if err != nil {
			return nil, err
		}
		if *into, err = note.ParseVerifierKeys([]byte(body)); err != nil {
			return nil, fmt.Errorf("%s in %s does not read: %v", file, p.where, err)
		}
	}
	return a, nil
}

// unchanged reports whether the host still publishes the tree a was read
// from.
func (a *approvalAuthority) unchanged(g git) (bool, error) {
	now, err := remoteRef(g.restricted(), a.instance, signersRef)
	return now == a.oid, err
}

// principal is the one name approval-signers lists pub under, and whether
// approval-signers-hardware lists pub under that name too. A key listed
// under two names is refused, as is a key approval-signers-hardware does
// not list when hardware is required. why is the refusal's reason, short
// enough for a verdict line.
func (a *approvalAuthority) principal(pub []byte, hardwareRequired bool) (name string, hardware bool, why string) {
	names := map[string]bool{}
	for _, k := range a.approvers {
		if bytes.Equal(k.Pub, pub) {
			names[k.Name] = true
		}
	}
	switch len(names) {
	case 0:
		return "", false, "approval key is not listed in " + signersApprovers
	case 1:
	default:
		return "", false, "approval key is listed under several principals"
	}
	for n := range names {
		name = n
	}
	for _, k := range a.hardware {
		hardware = hardware || (k.Name == name && bytes.Equal(k.Pub, pub))
	}
	if hardwareRequired && !hardware {
		return "", false, "policy requires a hardware key, and this key is none"
	}
	return name, hardware, ""
}

// signedApproval is the note one approval signed, stored in this
// repository, and what -v says of it.
type signedApproval struct {
	blob, principal string
	keyhash         uint32
	hardware        bool
}

// prepareSigning reads, before any check runs, what a signed approval is
// held to: the publication, and for a software key that it lists the key.
// A PIV key is read when it signs. hardware is whether the policy requires
// a hardware key.
func (a *approval) prepareSigning(hardware bool) error {
	auth, err := readApprovalAuthority(a.g)
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(err.Error()))
		return a.declined("could not read the published approval keys", act{})
	}
	a.auth, a.hardware = auth, hardware
	if a.key.file == "" {
		return nil
	}
	k, err := a.key.software()
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(err.Error()))
		return a.declined("could not read the approval key", act{})
	}
	if _, _, why := auth.principal(k.Public(), hardware); why != "" {
		return a.declined(why, act{})
	}
	return nil
}

// sign signs the approval of the version bound, as the statement names it
// from what approve read, and stores the note in this repository. A
// signer that cannot sign, or whose note does not verify under its own
// key, refuses the approval.
func (a *approval) sign() (*signedApproval, error) {
	if a.frozenRecord == "" || a.frozenVersion == "" {
		return nil, a.declined("no recorded version is bound to sign", act{})
	}
	project, err := originName(a.g)
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(err.Error()))
		return nil, a.declined("no project to sign the approval for", act{})
	}
	version, _ := strconv.Atoi(a.frozenVersion)
	signer, closeSigner, err := a.key.open(a, a.stderr)
	if err != nil {
		return nil, a.notSigned(err)
	}
	defer closeSigner()
	pub := signer.Public()
	principal, hardware, why := a.auth.principal(pub, a.hardware)
	if why != "" {
		return nil, a.declined(why, act{})
	}
	s := approvals.Statement{Instance: a.auth.id, Project: project, Target: recordTarget, Change: a.id,
		Version: version, Head: change.HeadPrefix + a.bound, Record: change.HeadPrefix + a.frozenRecord,
		Principal: principal, Issued: time.Now().UTC().Truncate(time.Second)}
	n, err := approvals.Sign(s, signer)
	if err != nil {
		return nil, a.notSigned(err)
	}
	blob, err := a.g.input(true, bytes.NewReader(n), "hash-object", "-w", "--stdin")
	if err != nil {
		return nil, a.declined("could not store the signed approval", act{})
	}
	return &signedApproval{blob: blob, principal: principal, keyhash: note.KeyHash(principal, pub), hardware: hardware}, nil
}

// notSigned refuses an approval its key did not sign, saying why on a
// line of its own: a device's refusal, such as a PIN cancelled or no
// touch, or a signature that does not verify under the signer's key. What
// a key file or the helper says is held to one line of printable text.
func (a *approval) notSigned(err error) error {
	if !a.stopped() {
		fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(err.Error()))
	}
	return a.declined("approval not signed", act{})
}

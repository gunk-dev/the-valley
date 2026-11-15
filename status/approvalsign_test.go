package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	approvals "the-valley/approval"
	"the-valley/change"
	"the-valley/note"
)

// signWorld is a project whose main names an instance repository, which
// publishes approval keys at refs/the-valley/signers, and a ready change
// in it, topic, recorded as id. Its policy requires no check, so approving
// runs nothing and needs no attest.
type signWorld struct {
	t                     *testing.T
	dir, work             string
	origin, valley        string
	id, head              string
	operator              ed25519.PrivateKey // listed in approval-signers
	operatorFile          string
	instanceID            string
	stdout, stderr        bytes.Buffer
	floor                 string
	signers, hardware     string // the published approval files: the operator's software key and device, and the device
	omitApprovalFiles     bool
	malformedApprovalFile bool
}

func seeded(name string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(name))
	return ed25519.NewKeyFromSeed(seed[:])
}

// signSchema is the verification schema, found before any test changes
// directory.
var signSchema, _ = filepath.Abs("../schema/verification.cue")

func newSignWorld(t *testing.T, configure func(w *signWorld)) *signWorld {
	schema := signSchema
	dir := t.TempDir()
	w := &signWorld{t: t, dir: dir, work: filepath.Join(dir, "project"), origin: filepath.Join(dir, "project.git"),
		valley: filepath.Join(dir, "valley.git"), id: strings.Repeat("m", 32), operator: seeded("operator"),
		floor: "floor: classes: everything: {paths: \"**\": true, requires: {}}\n"}
	w.hardware = note.NewVerifierKey("operator", seeded("device").Public().(ed25519.PublicKey)).String() + "\n"
	w.signers = note.NewVerifierKey("operator", w.operator.Public().(ed25519.PublicKey)).String() + "\n" + w.hardware
	if configure != nil {
		configure(w)
	}
	for k, v := range map[string]string{"HOME": dir, "XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"XDG_STATE_HOME": filepath.Join(dir, "state"), "GIT_CONFIG_NOSYSTEM": "1", "VALLEY_KNOWN_SIGNERS": "",
		"VALLEY_INTEGRATOR_SIGNER": "", "VALLEY_APPROVAL_KEY": "", "VALLEY_APPROVAL_PIV": "",
		"VALLEY_APPROVAL_PIV_READER": "", "VALLEY_VERIFICATION_SCHEMA": schema, "VALLEY_TEST_FAKE_PIV": ""} {
		t.Setenv(k, v)
	}
	w.operatorFile = filepath.Join(dir, "operator.key")
	if err := os.WriteFile(w.operatorFile, opensshKey(w.operator), 0o600); err != nil {
		t.Fatal(err)
	}

	// The instance: its floor on main, and its publication.
	valleyWork := filepath.Join(dir, "valley-work")
	w.run("", "git", "init", "--quiet", "--bare", "--initial-branch=main", w.valley)
	w.run("", "git", "init", "--quiet", "--initial-branch=main", valleyWork)
	w.write(valleyWork, "policy/instance/floor.cue", "package verification\n\n"+w.floor)
	w.run(valleyWork, "git", "add", "-A")
	w.run(valleyWork, "git", "-c", "user.name=t", "-c", "user.email=t@localhost", "commit", "--quiet", "-m", "floor")
	w.run(valleyWork, "git", "push", "--quiet", w.valley, "main")
	w.instanceID = strings.Repeat("ab", 32)
	w.publish(nil)

	// The project, and its change.
	w.run("", "git", "init", "--quiet", "--bare", "--initial-branch=main", w.origin)
	w.run("", "git", "clone", "--quiet", w.origin, w.work)
	for k, v := range map[string]string{"user.name": "sign-test", "user.email": "sign-test@localhost", "valley.principal": "alice"} {
		w.git("config", k, v)
	}
	w.git("checkout", "--quiet", "-b", "main")
	w.write(w.work, "policy/valley", w.valley+"\n")
	w.write(w.work, "policy/project/policy.cue", "package verification\n\nproject: {}\n")
	w.git("add", "-A")
	w.git("commit", "--quiet", "-m", "base")
	w.git("push", "--quiet", "origin", "main")
	t.Chdir(w.work)
	w.git("checkout", "--quiet", "-b", "topic")
	w.write(w.work, "readme.md", "a readme\n")
	w.git("add", "readme.md")
	w.git("commit", "--quiet", "-m", "a readme")
	w.git("push", "--quiet", "origin", "topic")
	w.head = w.git("rev-parse", "HEAD")
	w.valleyCLI("ready", "--change", w.id, "topic")
	return w
}

// publish writes the instance's publication, with extra files beside the
// ones the world was configured with.
func (w *signWorld) publish(extra map[string]string) {
	files := map[string]string{
		"known-signers":   note.NewVerifierKey("integrator", seeded("integrator").Public().(ed25519.PublicKey)).String() + "\n",
		"integrator-name": "integrator\n",
	}
	if !w.omitApprovalFiles {
		files[signersApprovers], files[signersHardware] = w.signers, w.hardware
		files[signersInstance] = w.instanceID + "\n"
	}
	if w.malformedApprovalFile {
		files[signersApprovers] = "not a verifier key\n"
	}
	for k, v := range extra {
		files[k] = v
	}
	var entries strings.Builder
	for name, body := range files {
		blob := w.runInput(w.valley, body, "git", "hash-object", "-w", "--stdin")
		fmt.Fprintf(&entries, "100644 blob %s\t%s\n", blob, name)
	}
	tree := w.runInput(w.valley, entries.String(), "git", "mktree")
	w.run(w.valley, "git", "update-ref", signersRef, tree)
}

func (w *signWorld) run(dir string, args ...string) string {
	return w.runInput(dir, "", args...)
}

func (w *signWorld) runInput(dir, stdin string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (w *signWorld) git(args ...string) string {
	w.t.Helper()
	return w.run(w.work, append([]string{"git"}, args...)...)
}

func (w *signWorld) write(dir, path, body string) {
	w.t.Helper()
	path = filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *signWorld) valleyCLI(args ...string) {
	w.t.Helper()
	var out bytes.Buffer
	if code := run(args, &out, &out, func(int) { w.t.Fatal("ended by a signal") }); code != 0 {
		w.t.Fatalf("valley %s: %s", strings.Join(args, " "), out.String())
	}
}

// approve runs valley approve with args, and returns its exit status. The
// verb holds SIGINT and SIGTERM until the process exits, so a signal
// another test sends itself later reaches its end path, which leaves the
// goroutine it was called on, as it would leave the process.
func (w *signWorld) approve(args ...string) int {
	w.stdout.Reset()
	w.stderr.Reset()
	return run(append([]string{"approve"}, args...), &w.stdout, &w.stderr, func(int) { runtime.Goexit() })
}

// onOrigin is the object origin's ref names, or "".
func (w *signWorld) onOrigin(ref string) string {
	out, _ := exec.Command("git", "-C", w.origin, "rev-parse", "--verify", "--quiet", ref).Output()
	return strings.TrimSpace(string(out))
}

func (w *signWorld) request() string {
	return w.onOrigin("refs/the-valley/integration-requests/main/c-" + w.id)
}
func (w *signWorld) approvalRef() string {
	return w.onOrigin(approvals.Ref(recordTarget, w.id))
}
func (w *signWorld) record() string { return w.onOrigin(change.Ref(recordTarget, w.id)) }

// published is the statement origin's approval note carries, verified
// under keys.
func (w *signWorld) published(keys string) approvals.Statement {
	w.t.Helper()
	n, err := exec.Command("git", "-C", w.origin, "cat-file", "blob", approvals.Ref(recordTarget, w.id)).Output()
	if err != nil {
		w.t.Fatalf("origin holds no approval of %s: %v", w.id, err)
	}
	authority, err := note.ParseVerifierKeys([]byte(keys))
	if err != nil {
		w.t.Fatal(err)
	}
	s, err := approvals.Verify(n, authority)
	if err != nil {
		w.t.Fatalf("the published approval does not verify: %v\n%s", err, n)
	}
	return s
}

// signedAs checks the published statement: the instance, the project, the
// change at v1 and its head, the record commit given, and the principal.
func (w *signWorld) signedAs(keys, record string) {
	w.t.Helper()
	s := w.published(keys)
	if s.Instance != w.instanceID || s.Project != "project" || s.Target != "main" || s.Change != w.id ||
		s.Version != 1 || s.Head != change.HeadPrefix+w.head || s.Record != change.HeadPrefix+record ||
		s.Principal != "operator" || time.Since(s.Issued) > time.Hour {
		w.t.Errorf("the published statement is %+v; want instance %s, project, change %s v1 at %s, record %s, operator",
			s, w.instanceID, w.id, w.head, record)
	}
	if w.request() != w.head {
		w.t.Errorf("the request stands at %q, not %s", w.request(), w.head)
	}
}

// nothingPublished fails unless the last approval refused, with why on
// stderr, and left neither a request nor an approval on origin.
func (w *signWorld) nothingPublished(status int, why string) {
	w.t.Helper()
	if status != 1 || !strings.Contains(w.stderr.String(), why) {
		w.t.Errorf("exit %d, stderr %q; want 1 and %q", status, w.stderr.String(), why)
	}
	if r, a := w.request(), w.approvalRef(); r != "" || a != "" {
		w.t.Errorf("a refused approval left request %q and approval %q on origin", r, a)
	}
}

const unsignedLine = "no signed approval was written: no approval key is selected\n"

// The approve verb with a software key publishes the note beside the
// request, in one push: the statement names what approve bound, and
// verifies under approval-signers.
func TestSignedApproveVerbPublishesTheNote(t *testing.T) {
	w := newSignWorld(t, nil)
	record := w.record()
	if status := w.approve("-v", "--head", w.head, "--approval-key", w.operatorFile, w.id); status != 0 {
		t.Fatalf("exit %d\n%s%s", status, w.stdout.String(), w.stderr.String())
	}
	w.signedAs(w.signers, record)
	out := w.stdout.String()
	if !strings.Contains(out, "signed and approved · no checks required") || strings.Contains(out, unsignedLine) {
		t.Errorf("the verdict does not say the approval was signed:\n%s", out)
	}
	if !strings.Contains(out, "  signed   operator, key ") || !strings.Contains(out, "not listed as a hardware key") {
		t.Errorf("-v does not say who signed:\n%s", out)
	}
}

// With no approval key selected, approve files as it does unsigned and
// says no signed approval was written: a recorded change while the
// publication's approval files do not read, and a plain branch while the
// publication has none.
func TestUnsignedApproveIsUnchanged(t *testing.T) {
	w := newSignWorld(t, func(w *signWorld) { w.malformedApprovalFile = true })
	if status := w.approve("--head", w.head, w.id); status != 0 {
		t.Fatalf("exit %d\n%s%s", status, w.stdout.String(), w.stderr.String())
	}
	if w.request() != w.head || w.approvalRef() != "" || !strings.HasSuffix(w.stdout.String(), unsignedLine) {
		t.Errorf("request %q, approval %q, stdout:\n%s", w.request(), w.approvalRef(), w.stdout.String())
	}

	w = newSignWorld(t, func(w *signWorld) { w.omitApprovalFiles = true })
	w.git("push", "--quiet", "origin", "topic:plain")
	if status := w.approve("origin/plain"); status != 0 {
		t.Fatalf("exit %d\n%s%s", status, w.stdout.String(), w.stderr.String())
	}
	if w.onOrigin("refs/the-valley/integration-requests/main/plain") != w.head ||
		!strings.HasSuffix(w.stdout.String(), unsignedLine) {
		t.Errorf("the plain branch was not filed unsigned:\n%s%s", w.stdout.String(), w.stderr.String())
	}
}

// With a key selected, approve refuses whatever stops the signing, and
// publishes nothing: no request, no approval.
func TestSignedApprovalRefusals(t *testing.T) {
	stranger := seeded("stranger")
	for _, c := range []struct {
		name      string
		configure func(w *signWorld)
		args      func(w *signWorld) []string
		why       string
	}{
		{name: "plain branch", why: "no recorded version to sign",
			args: func(w *signWorld) []string {
				w.git("push", "--quiet", "origin", "topic:plain")
				return []string{"--approval-key", w.operatorFile, "origin/plain"}
			}},
		{name: "unlisted key", why: "approval key is not listed in approval-signers",
			configure: func(w *signWorld) {
				w.signers = note.NewVerifierKey("operator", stranger.Public().(ed25519.PublicKey)).String() + "\n"
			}},
		{name: "two principals", why: "approval key is listed under several principals",
			configure: func(w *signWorld) {
				w.signers += note.NewVerifierKey("alice", w.operator.Public().(ed25519.PublicKey)).String() + "\n"
			}},
		{name: "hardware required", why: "policy requires a hardware key, and this key is none",
			configure: func(w *signWorld) { w.floor += "floor: approval: hardware: true\n" }},
		{name: "revoked", why: "approval key is not listed in approval-signers",
			configure: func(w *signWorld) { w.signers = "" }},
		{name: "no approval files", why: "could not read the published approval keys",
			configure: func(w *signWorld) { w.omitApprovalFiles = true }},
		{name: "malformed approval file", why: "could not read the published approval keys",
			configure: func(w *signWorld) { w.malformedApprovalFile = true }},
		{name: "hook refuses", why: "origin refused the push",
			configure: func(w *signWorld) {}, args: func(w *signWorld) []string {
				w.write(w.origin, "hooks/pre-receive", "#!/bin/sh\n! grep -q refs/the-valley/approvals/\n")
				if err := os.Chmod(filepath.Join(w.origin, "hooks/pre-receive"), 0o755); err != nil {
					t.Fatal(err)
				}
				return []string{"--head", w.head, "--approval-key", w.operatorFile, w.id}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newSignWorld(t, c.configure)
			args := []string{"--head", w.head, "--approval-key", w.operatorFile, w.id}
			if c.args != nil {
				args = c.args(w)
			}
			w.nothingPublished(w.approve(args...), c.why)
		})
	}

	// Both kinds selected at one level are refused before anything runs;
	// a flag beats the environment.
	w := newSignWorld(t, nil)
	t.Setenv("VALLEY_APPROVAL_KEY", w.operatorFile)
	t.Setenv("VALLEY_APPROVAL_PIV", "9c")
	w.nothingPublished(w.approve("--head", w.head, w.id), "select two approval keys")
	w.nothingPublished(w.approve("--head", w.head, "--approval-piv", "--approval-key", w.operatorFile, w.id), "select two approval keys")
	// Where this build has no PIV signer, selecting a PIV slot is refused.
	t.Setenv("VALLEY_APPROVAL_KEY", "")
	saved := pivSigner
	pivSigner = ""
	w.nothingPublished(w.approve("--head", w.head, "--approval-piv", w.id), "hardware signing is not available on this platform")
	pivSigner = saved
	t.Setenv("VALLEY_APPROVAL_KEY", filepath.Join(w.dir, "missing.key"))
	if status := w.approve("--head", w.head, "--approval-key", w.operatorFile, w.id); status != 0 {
		t.Fatalf("--approval-key did not beat the environment: exit %d\n%s", status, w.stderr.String())
	}
	w.signedAs(w.signers, w.record())
}

// A YubiKey's PIV slot, through a stand-in for valley-piv-signer: this
// test binary, with VALLEY_TEST_FAKE_PIV naming what the device does when
// it is asked to sign. A device that signs publishes the note. One that is
// absent, or not touched, publishes nothing. The device's touch is where
// the rest of the world moves: the publication changing, the change
// withdrawn, or a new version readied, after which the approval still
// names the version and record it read.
func TestPIVApproval(t *testing.T) {
	saved := pivSigner
	pivSigner = os.Args[0]
	t.Cleanup(func() { pivSigner = saved })
	for _, c := range []struct{ scenario, why string }{
		{"absent", "no YubiKey could be reached"},
		{"touch", "it was not touched in time"},
		{"publish", "the published approval keys changed meanwhile"},
		{"withdraw", "change is withdrawn"},
	} {
		t.Run(c.scenario, func(t *testing.T) {
			w := newSignWorld(t, nil)
			t.Setenv("VALLEY_TEST_FAKE_PIV", c.scenario)
			t.Setenv("VALLEY_TEST_FAKE_PIV_VALLEY", w.valley)
			w.nothingPublished(w.approve("--head", w.head, "--approval-piv", w.id), c.why)
		})
	}
	for _, scenario := range []string{"sign", "new-version"} {
		t.Run(scenario, func(t *testing.T) {
			w := newSignWorld(t, func(w *signWorld) { w.floor += "floor: approval: hardware: true\n" })
			record := w.record()
			t.Setenv("VALLEY_TEST_FAKE_PIV", scenario)
			t.Setenv("VALLEY_APPROVAL_PIV", "9c")
			if status := w.approve("--head", w.head, w.id); status != 0 {
				t.Fatalf("exit %d\n%s%s", status, w.stdout.String(), w.stderr.String())
			}
			w.signedAs(w.hardware, record)
			if scenario == "new-version" && w.record() == record {
				t.Error("the stand-in readied no new version while the device signed")
			}
			if !strings.Contains(w.stderr.String(), "touch the YubiKey (serial 12345678)") {
				t.Errorf("no touch was asked for:\n%s", w.stderr.String())
			}
		})
	}
}

// An approval of head A, where the request already stands at A, pushes
// the note alone. Another approver replacing the request and its approval
// between approve's one read of both and its push refuses that push,
// through the lease on the approval, so origin keeps the other approver's
// request and approval together. A receive-pack that writes both refs
// first, once, stands in for the other approver. (The hook refuses a note
// of A beside a request at B however the lease reads: valleyhook's tests.)
func TestAnApprovalPublishedMeanwhileRefusesThePush(t *testing.T) {
	w := newSignWorld(t, nil)
	if status := w.approve("--head", w.head, w.id); status != 0 || w.request() != w.head {
		t.Fatalf("filing the request unsigned: exit %d\n%s", status, w.stderr.String())
	}
	racer := filepath.Join(w.dir, "racing-receive-pack")
	if err := os.Symlink(os.Args[0], racer); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(w.dir, "race-armed")
	w.write(w.dir, "race-armed", "")
	w.git("config", "remote.origin.receivepack", racer)
	request := "refs/the-valley/integration-requests/main/c-" + w.id
	t.Setenv("VALLEY_TEST_RACE", marker+"\t"+request+"\t"+approvals.Ref(recordTarget, w.id))
	status := w.approve("--head", w.head, "--approval-key", w.operatorFile, w.id)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the racing receive-pack never ran")
	}
	if status != 1 || !strings.Contains(w.stderr.String(), "another approval of the change was published meanwhile") {
		t.Errorf("exit %d, stderr %q", status, w.stderr.String())
	}
	other := w.onOrigin("refs/heads/main")
	if w.request() != other || w.approvalRef() != w.run(w.origin, "git", "hash-object", "/dev/null") {
		t.Errorf("origin holds the request at %s and the approval at %s, not the other approver's", w.request(), w.approvalRef())
	}
}

// A signal to approve while the PIV signer's helper reaches the device,
// or waits for the PIN, stops the helper and waits for it before the
// command exits 130, and nothing is published. The stand-in device writes
// its process ID to a FIFO before it sends its key (init-wait), or once
// it is asked to sign (pin-wait), and waits for a signal.
func TestASignalStopsThePIVSigner(t *testing.T) {
	saved := pivSigner
	pivSigner = os.Args[0]
	t.Cleanup(func() { pivSigner = saved })
	for _, scenario := range []string{"init-wait", "pin-wait"} {
		t.Run(scenario, func(t *testing.T) {
			w := newSignWorld(t, nil)
			ended := make(chan int, 1)
			pid := w.helperWaiting(scenario, func() {
				run([]string{"approve", "--head", w.head, "--approval-piv", w.id}, &w.stdout, &w.stderr,
					func(status int) { ended <- status; runtime.Goexit() })
			}, ended)
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case status := <-ended:
				if status != interruptedStatus {
					t.Errorf("approve ended with %d, not %d", status, interruptedStatus)
				}
			case <-time.After(time.Minute):
				t.Fatal("approve did not end on SIGTERM")
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Errorf("the PIV signer's helper %d was not stopped and waited for: %v", pid, err)
			}
			if r, a := w.request(), w.approvalRef(); r != "" || a != "" {
				t.Errorf("an interrupted approval left request %q and approval %q", r, a)
			}
		})
	}
}

// An interrupt that arrives after the PIV signer's helper has started,
// and before it is registered as approve's child, waits for the
// registration, then stops the helper and waits for it. A start function
// the test controls holds that window open: it starts the stand-in, which
// waits for a signal before it sends its key, and then blocks until the
// interrupt has begun.
func TestAnInterruptDuringTheHelpersStartStopsIt(t *testing.T) {
	savedSigner, savedStart, savedBegins := pivSigner, startPIV, interruptBegins
	t.Cleanup(func() { pivSigner, startPIV, interruptBegins = savedSigner, savedStart, savedBegins })
	pivSigner = os.Args[0]
	w := newSignWorld(t, nil)
	fifo := filepath.Join(w.dir, "waiting")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VALLEY_TEST_FAKE_PIV", "init-wait")
	t.Setenv("VALLEY_TEST_FAKE_PIV_FIFO", fifo)
	go os.ReadFile(fifo)

	started, release, began := make(chan int, 1), make(chan struct{}), make(chan struct{}, 1)
	startPIV = func(program, slot, reader string, prompt io.Writer) (*approvals.PIVSession, error) {
		p, err := savedStart(program, slot, reader, prompt)
		if err == nil {
			started <- p.Process().Pid
			<-release
		}
		return p, err
	}
	// Approvals earlier tests left watching take the signal too; only
	// this one writes to this world's stdout.
	interruptBegins = func(a *approval) {
		if a.stdout == io.Writer(&w.stdout) {
			select {
			case began <- struct{}{}:
			default:
			}
		}
	}
	ended := make(chan int, 1)
	go run([]string{"approve", "--head", w.head, "--approval-piv", w.id}, &w.stdout, &w.stderr,
		func(status int) { ended <- status; runtime.Goexit() })

	var pid int
	select {
	case pid = <-started:
	case status := <-ended:
		t.Fatalf("approve ended with %d before the helper started\n%s", status, w.stderr.String())
	case <-time.After(time.Minute):
		t.Fatal("the helper never started")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-began:
	case <-time.After(time.Minute):
		t.Fatal("the interrupt never began")
	}
	close(release)
	select {
	case status := <-ended:
		if status != interruptedStatus {
			t.Errorf("approve ended with %d, not %d", status, interruptedStatus)
		}
	case <-time.After(time.Minute):
		t.Fatal("approve did not end on SIGTERM")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the PIV signer's helper %d, started as the interrupt came, was not stopped and waited for: %v", pid, err)
	}
	if r, a := w.request(), w.approvalRef(); r != "" || a != "" {
		t.Errorf("an interrupted approval left request %q and approval %q", r, a)
	}
}

// helperWaiting runs approve, which ends by sending its status to ended,
// with the stand-in device of scenario, and returns the device's process
// ID once it says, through a FIFO, that it is waiting.
func (w *signWorld) helperWaiting(scenario string, approve func(), ended chan int) int {
	w.t.Helper()
	fifo := filepath.Join(w.dir, "waiting")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		w.t.Fatal(err)
	}
	w.t.Setenv("VALLEY_TEST_FAKE_PIV", scenario)
	w.t.Setenv("VALLEY_TEST_FAKE_PIV_FIFO", fifo)
	waiting := make(chan string, 1)
	go approve()
	go func() {
		pid, _ := os.ReadFile(fifo)
		waiting <- strings.TrimSpace(string(pid))
	}()
	select {
	case p := <-waiting:
		pid, _ := strconv.Atoi(p)
		return pid
	case status := <-ended:
		w.t.Fatalf("approve ended with %d before the device was waiting\n%s", status, w.stderr.String())
	case <-time.After(time.Minute):
		w.t.Fatal("the device never said it was waiting")
	}
	return 0
}

// waitingFor writes this process's ID to the FIFO the test names, after
// stop is set up to catch the signals that end the stand-in.
func waitingFor(stop chan os.Signal) bool {
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	f, err := os.OpenFile(os.Getenv("VALLEY_TEST_FAKE_PIV_FIFO"), os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return f.Close() == nil
}

// extraFakePIV are stand-in scenarios a platform's tests add, run once
// the stand-in is asked to sign, as the helper asks for the PIN.
var extraFakePIV = map[string]func() int{}

// What a publication or a key file says reaches the terminal as one line
// of printable text: an escape byte in an approval-signers entry, or in a
// key file's key type, is written escaped.
func TestApprovalDiagnosticsAreEscaped(t *testing.T) {
	w := newSignWorld(t, func(w *signWorld) { w.signers = "operator\x1b[2J+00000000+AAAA\n" })
	w.nothingPublished(w.approve("--head", w.head, "--approval-key", w.operatorFile, w.id), "could not read the published approval keys")
	bad := filepath.Join(w.dir, "bad.key")
	if err := os.WriteFile(bad, opensshKeyOfKind(w.operator, "ssh-\x1b]0;title\x07"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr := w.stderr.String()
	w = newSignWorld(t, nil)
	w.nothingPublished(w.approve("--head", w.head, "--approval-key", bad, w.id), "could not read the approval key")
	for _, says := range []string{stderr, w.stderr.String()} {
		if strings.ContainsAny(says, "\x1b\x07") || !strings.Contains(says, `\x1b`) {
			t.Errorf("a diagnostic was not escaped: %q", says)
		}
	}
}

// Review's [a]pprove signs as the approve verb does with a key, and says
// no signed approval was written without one.
func TestReviewApprovalSigns(t *testing.T) {
	for _, key := range []bool{true, false} {
		w := newSignWorld(t, nil)
		record := w.record()
		answers := filepath.Join(w.dir, "answers")
		w.write(w.dir, "answers", "a\n")
		stdin, err := os.Open(answers)
		if err != nil {
			t.Fatal(err)
		}
		defer stdin.Close()
		var selected *approvalKey
		if key {
			selected = &approvalKey{file: w.operatorFile, from: "--approval-key"}
		}
		g := git{env: gitEnvironment(os.Environ()), stderr: &w.stderr, dir: w.work}
		o := reviewChange(reviewStep{g: g, key: selected, stdin: stdin, stdout: &w.stdout, stderr: &w.stderr,
			signers: &signerSource{g: g, stderr: &w.stderr}, interrupts: make(chan os.Signal),
			end: func(int, stepOutcome) { t.Fatal("the review was ended by a signal") }}, w.id)
		if o.kind != "approved" {
			t.Fatalf("the review ended %s\n%s%s", o.kind, w.stdout.String(), w.stderr.String())
		}
		if key {
			w.signedAs(w.signers, record)
		} else if w.approvalRef() != "" || !strings.Contains(w.stdout.String(), unsignedLine) {
			t.Errorf("an unsigned review left approval %q, stdout:\n%s", w.approvalRef(), w.stdout.String())
		}
	}
}

// opensshKey is priv as the OpenSSH private key file ssh-keygen writes
// for an ed25519 key with no passphrase.
func opensshKey(priv ed25519.PrivateKey) []byte { return opensshKeyOfKind(priv, "ssh-ed25519") }

// opensshKeyOfKind is that file with another key type written in it.
func opensshKeyOfKind(priv ed25519.PrivateKey, kindName string) []byte {
	str := func(b []byte) []byte { return append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...) }
	pub := priv.Public().(ed25519.PublicKey)
	kind := str([]byte(kindName))
	section := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 7), 7)
	section = append(append(append(append(section, kind...), str(pub)...), str(priv)...), str(nil)...)
	for i := byte(1); len(section)%8 != 0; i++ {
		section = append(section, i)
	}
	body := append([]byte("openssh-key-v1\x00"), str([]byte("none"))...)
	body = append(append(body, str([]byte("none"))...), str(nil)...)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = append(append(body, str(append(kind, str(pub)...))...), str(section)...)
	return pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: body})
}

// fakePIV speaks valley-piv-signer's protocol (approval/piv.go) for the
// device seeded "device", which approval-signers-hardware lists. Asked to
// sign, it does what the scenario says first.
func fakePIV(scenario string) int {
	if scenario == "init-wait" {
		stop := make(chan os.Signal, 1)
		if waitingFor(stop) {
			<-stop
		}
		return 1
	}
	if scenario == "absent" {
		fmt.Println("error device no YubiKey could be reached: PCSC error")
		return 1
	}
	device := seeded("device")
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "YubiKey PIV Attestation 9c"},
		NotBefore: time.Unix(0, 0), NotAfter: time.Unix(1<<33, 0)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, device.Public(), device)
	if err != nil {
		fmt.Println("error device", err)
		return 1
	}
	fmt.Printf("key 12345678 %x\n", der)
	var line string
	var n int
	if _, err := fmt.Scanf("sign %d %s\n", &n, &line); err != nil {
		return 0
	}
	text := make([]byte, n)
	if _, err := io.ReadFull(os.Stdin, text); err != nil {
		return 1
	}
	id := strings.Repeat("m", 32)
	quiet := func(args ...string) {
		if code := cli(args, io.Discard, os.Stderr, os.Exit); code != 0 {
			os.Exit(code)
		}
	}
	switch scenario {
	case "pin-wait", "echo-off":
		if f := extraFakePIV[scenario]; f != nil {
			return f()
		}
		stop := make(chan os.Signal, 1)
		if waitingFor(stop) {
			<-stop
		}
		fmt.Println("error cancelled")
		return 1
	case "touch":
		fmt.Println("touch\nerror touch")
		return 1
	case "publish":
		repo := os.Getenv("VALLEY_TEST_FAKE_PIV_VALLEY")
		blob, err := exec.Command("git", "-C", repo, "hash-object", "-w", "/dev/null").Output()
		if err != nil {
			return 1
		}
		out, err := exec.Command("git", "-C", repo, "ls-tree", signersRef).Output()
		if err != nil {
			return 1
		}
		mktree := exec.Command("git", "-C", repo, "mktree")
		mktree.Stdin = strings.NewReader(string(out) + "100644 blob " + strings.TrimSpace(string(blob)) + "\tchanged\n")
		tree, err := mktree.Output()
		if err != nil || exec.Command("git", "-C", repo, "update-ref", signersRef, strings.TrimSpace(string(tree))).Run() != nil {
			return 1
		}
	case "withdraw":
		quiet("withdraw", id)
	case "new-version":
		os.WriteFile("readme.md", []byte("a better readme\n"), 0o644)
		if exec.Command("git", "commit", "--quiet", "-am", "a better readme").Run() != nil ||
			exec.Command("git", "push", "--quiet", "origin", "HEAD:topic").Run() != nil {
			return 1
		}
		quiet("ready", "--change", id, "topic")
	}
	fmt.Printf("touch\nsignature %x\n", ed25519.Sign(device, text))
	io.Copy(io.Discard, os.Stdin)
	return 0
}

// racingReceivePack writes the refs VALLEY_TEST_RACE names in the
// repository it receives into, once, while the marker file it names is
// there, and then receives the push: an integration request at main, and
// any other ref at the empty blob.
func racingReceivePack(args []string) int {
	race := strings.Split(os.Getenv("VALLEY_TEST_RACE"), "\t")
	repo := args[len(args)-1]
	if os.Remove(race[0]) == nil {
		blob, err := exec.Command("git", "-C", repo, "hash-object", "-w", "/dev/null").Output()
		main, merr := exec.Command("git", "-C", repo, "rev-parse", "refs/heads/main").Output()
		if err != nil || merr != nil {
			return 1
		}
		for _, ref := range race[1:] {
			value := blob
			if strings.HasPrefix(ref, "refs/the-valley/integration-requests/") {
				value = main
			}
			if exec.Command("git", "-C", repo, "update-ref", ref, strings.TrimSpace(string(value))).Run() != nil {
				return 1
			}
		}
	}
	cmd := exec.Command("git", append([]string{"receive-pack"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

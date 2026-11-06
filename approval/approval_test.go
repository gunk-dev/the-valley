package approval

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"the-valley/note"
)

var update = flag.Bool("update", false, "rewrite the vectors under testdata/")

func key(label string) Key {
	seed := sha256.Sum256([]byte("approval test key " + label))
	return NewKey(ed25519.NewKeyFromSeed(seed[:]))
}

func example() Statement {
	return Statement{
		Instance:  strings.Repeat("5f", 32),
		Project:   "app",
		Target:    "main",
		Change:    "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
		Version:   2,
		Head:      "git-sha1:" + strings.Repeat("2", 40),
		Record:    "git-sha1:" + strings.Repeat("3", 40),
		Principal: "operator",
		Issued:    time.Date(2026, 11, 6, 0, 43, 26, 0, time.UTC),
	}
}

func authority(principal string, keys ...Key) []note.VerifierKey {
	var out []note.VerifierKey
	for _, k := range keys {
		out = append(out, note.NewVerifierKey(principal, k.Public()))
	}
	return out
}

// pinned holds got to the vector in testdata/name, or rewrites it.
func pinned(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s:\ngot\n%s\nwant\n%s", path, got, want)
	}
}

func TestVectors(t *testing.T) {
	text, err := example().Encode()
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, "approval.statement", text)
	n, err := Sign(example(), key("operator"))
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, "approval.note", n)
	s, err := Verify(n, authority("operator", key("operator")))
	if err != nil {
		t.Fatal(err)
	}
	if s != example() {
		t.Fatalf("verified %+v, want %+v", s, example())
	}
	id, err := InstanceID("sha1", []string{strings.Repeat("b", 40), strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, "instance-id", []byte(id+"\n"))
}

// The largest statement the form allows is the bound itself, and the
// hardware acceptance signs it.
func TestBound(t *testing.T) {
	s := example()
	s.Target = strings.Repeat("t", MaxStatement-len(mustEncode(t, s))+len(s.Target))
	text := mustEncode(t, s)
	if len(text) != MaxStatement {
		t.Fatalf("the largest statement is %d bytes", len(text))
	}
	pinned(t, "largest.statement", text)
	if _, err := Parse(text); err != nil {
		t.Fatal(err)
	}
	s.Target += "t"
	if _, err := s.Encode(); err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("a statement over the bound encoded, or did not name the bound: %v", err)
	}
	if _, err := Sign(s, failing{}); err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("a statement over the bound reached the signer: %v", err)
	}
	if _, err := Parse(append(text, 'x')); err == nil {
		t.Fatal("text over the bound parsed")
	}
}

func mustEncode(t *testing.T, s Statement) []byte {
	t.Helper()
	text, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestParseRefuses(t *testing.T) {
	good := string(mustEncode(t, example()))
	swap := func(old, new string) string { return strings.Replace(good, old, new, 1) }
	for name, text := range map[string]string{
		"attestation type":   swap(Type, note.StatementType),
		"line missing":       swap("record git-sha1:"+strings.Repeat("3", 40)+"\n", ""),
		"line added":         good + "expires 2026-11-07T00:43:26Z\n",
		"lines swapped":      swap("project app\ntarget main", "target main\nproject app"),
		"unknown key":        swap("project app", "projects app"),
		"repeated line":      swap("target main\n", "target main\ntarget main\n"),
		"two spaces":         swap("project app", "project  app"),
		"trailing space":     swap("project app", "project app "),
		"empty value":        swap("project app", "project "),
		"leading zero":       swap("version 2", "version 02"),
		"version zero":       swap("version 2", "version 0"),
		"version too large":  swap("version 2", "version 1001"),
		"upper hex":          swap("instance 5f5f", "instance 5F5f"),
		"short instance":     swap("instance 5f5f", "instance 5f"),
		"untyped head":       swap("head git-sha1:", "head "),
		"sha256 head":        swap("head git-sha1:", "head git-sha256:"),
		"bad change id":      swap("change nx", "change ax"),
		"bad target":         swap("target main", "target ma..in"),
		"bad principal":      swap("principal operator", "principal -operator"),
		"local time":         swap("00:43:26Z", "00:43:26+01:00"),
		"fractional second":  swap("00:43:26Z", "00:43:26.5Z"),
		"no closing newline": strings.TrimSuffix(good, "\n"),
		"carriage return":    swap("project app\n", "project app\r\n"),
		"empty":              "",
	} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

// Every field is under the signature: a note whose statement is altered
// in any one value does not verify.
func TestFieldMutations(t *testing.T) {
	n, err := Sign(example(), key("operator"))
	if err != nil {
		t.Fatal(err)
	}
	keys := authority("operator", key("operator"))
	lines := strings.Split(string(n), "\n")
	for i := 1; i <= 9; i++ {
		mutated := slicesClone(lines)
		l := mutated[i]
		last := l[len(l)-1]
		repl := byte('1')
		if last == '1' {
			repl = '2'
		}
		mutated[i] = l[:len(l)-1] + string(repl)
		if _, err := Verify([]byte(strings.Join(mutated, "\n")), keys); err == nil {
			t.Errorf("altering %q verified", l)
		}
	}
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

func TestVerifyRefuses(t *testing.T) {
	operator := key("operator")
	keys := authority("operator", operator)
	good, err := Sign(example(), operator)
	if err != nil {
		t.Fatal(err)
	}
	attestation := []byte(note.StatementType + "\nsubject.primary valley-tree-v1\n")
	wrongType, _ := note.Sign(attestation, "operator", operator.priv)
	unknown, _ := Sign(example(), key("stranger"))
	other := example()
	other.Principal = "alice"
	otherText := mustEncode(t, other)
	mismatch, _ := note.Sign(otherText, "operator", operator.priv)
	twice, _ := note.AddSignature(good, "operator", key("spare").priv)
	noncanonical, _ := note.Sign(bytes.Replace(mustEncode(t, example()), []byte("version 2"), []byte("version 02"), 1), "operator", operator.priv)
	for name, n := range map[string][]byte{
		"wrong namespace":    wrongType,
		"unknown key":        unknown,
		"principal mismatch": mismatch,
		"two signatures":     twice,
		"noncanonical text":  noncanonical,
		"no signature block": mustEncode(t, example()),
		"garbage signature":  append(mustEncode(t, example()), "\n— operator AAAA\n"...),
		"truncated":          good[:len(good)-10],
		"oversized":          append(bytes.Repeat([]byte("x"), MaxNote), good...),
	} {
		if _, err := Verify(n, keys); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

// Which keys a reader holds is which approvals count. A software key's
// approval verifies under approval-signers, which lists it, and is refused
// under approval-signers-hardware, which lists only hardware keys.
func TestAuthoritySelection(t *testing.T) {
	software, hardware := key("software"), key("hardware")
	all := []byte("# approval-signers\n" + note.NewVerifierKey("operator", software.Public()).String() + "\n" +
		note.NewVerifierKey("operator", hardware.Public()).String() + "\n")
	hw := []byte("# approval-signers-hardware\n" + note.NewVerifierKey("operator", hardware.Public()).String() + "\n")
	read := func(b []byte) []note.VerifierKey {
		keys, err := note.ParseVerifierKeys(b)
		if err != nil {
			t.Fatal(err)
		}
		return keys
	}
	n, err := Sign(example(), software)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(n, read(all)); err != nil {
		t.Fatalf("a software key under ordinary authority: %v", err)
	}
	if _, err := Verify(n, read(hw)); err == nil {
		t.Fatal("a software key verified under hardware-only authority")
	}
	if n, err = Sign(example(), hardware); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(n, read(hw)); err != nil {
		t.Fatalf("a hardware key under hardware-only authority: %v", err)
	}
}

type failing struct{}

func (failing) Public() ed25519.PublicKey   { return key("operator").Public() }
func (failing) Sign([]byte) ([]byte, error) { return nil, errors.New("the device went away") }

// otherText signs something other than the statement it is given.
type otherText struct{ Key }

func (o otherText) Sign(text []byte) ([]byte, error) { return o.Key.Sign(append(text, 'x')) }

func TestSigningFailure(t *testing.T) {
	if n, err := Sign(example(), failing{}); err == nil || n != nil {
		t.Fatalf("a failing signer gave a note: %q, %v", n, err)
	}
	if n, err := Sign(example(), otherText{key("operator")}); err == nil || n != nil {
		t.Fatalf("a signature over other text was sealed: %q, %v", n, err)
	}
}

func TestInstanceIDRefuses(t *testing.T) {
	a := strings.Repeat("a", 40)
	for name, c := range map[string]struct {
		format string
		roots  []string
	}{
		"unknown format": {"md5", []string{a}},
		"no roots":       {"sha1", nil},
		"twice":          {"sha1", []string{a, a}},
		"wrong length":   {"sha256", []string{a}},
		"upper hex":      {"sha1", []string{strings.ToUpper(a)}},
	} {
		if _, err := InstanceID(c.format, c.roots); err == nil {
			t.Errorf("%s: derived an identity", name)
		}
	}
}

func TestParseKey(t *testing.T) {
	raw, err := os.ReadFile("testdata/software.key")
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParseKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(example(), k); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKey(raw[:len(raw)/2]); err == nil {
		t.Fatal("a truncated key parsed")
	}
}

// The PIV session, against a stand-in for valley-piv-signer: this test
// binary, run with APPROVAL_FAKE_PIV naming what the device does.
func TestMain(m *testing.M) {
	if scenario := os.Getenv("APPROVAL_FAKE_PIV"); scenario != "" {
		os.Exit(fakePIV(scenario))
	}
	os.Exit(m.Run())
}

func device() ed25519.PrivateKey { return key("device").priv }

func fakePIV(scenario string) int {
	if scenario == "absent" {
		fmt.Println("error device no YubiKey could be reached: PCSC error")
		return 1
	}
	pinPolicy := map[string]byte{"pin-always": 3, "pin-never": 1}[scenario]
	if pinPolicy == 0 {
		pinPolicy = 2
	}
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: "YubiKey PIV Attestation 9c"},
		NotBefore:       time.Unix(0, 0),
		NotAfter:        time.Unix(1<<33, 0),
		ExtraExtensions: []pkix.Extension{{Id: policyOID, Value: []byte{pinPolicy, 2}}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, device().Public(), device())
	if err != nil {
		fmt.Println("error device", err)
		return 1
	}
	fmt.Printf("key 12345678 %x\n", der)
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return 0
		}
		var n int
		var mode string
		fmt.Sscanf(line, "sign %d %s", &n, &mode)
		text := make([]byte, n)
		io.ReadFull(in, text)
		if want := map[byte]string{1: "none", 2: "session", 3: "each"}[pinPolicy]; mode != want {
			fmt.Println("error request the mode is", mode, "not", want)
			return 1
		}
		switch scenario {
		case "pin":
			fmt.Println("error pin 2")
			return 1
		case "cancelled":
			fmt.Println("error cancelled")
			return 1
		case "touch":
			fmt.Println("touch\nerror touch")
			return 1
		case "other-text":
			text = append(text, 'x')
		}
		fmt.Printf("touch\nsignature %x\n", ed25519.Sign(device(), text))
	}
}

func openFake(t *testing.T, scenario string) (*PIVSession, *bytes.Buffer, error) {
	t.Setenv("APPROVAL_FAKE_PIV", scenario)
	var prompt bytes.Buffer
	p, err := OpenPIV(os.Args[0], "", "", &prompt)
	if p != nil {
		t.Cleanup(func() { p.Close() })
	}
	return p, &prompt, err
}

func TestPIVSession(t *testing.T) {
	for _, scenario := range []string{"session", "pin-always", "pin-never"} {
		p, prompt, err := openFake(t, scenario)
		if err != nil {
			t.Fatalf("%s: %v", scenario, err)
		}
		keys := authority("operator", NewKey(device()))
		for v := 1; v <= 3; v++ {
			s := example()
			s.Version = v
			n, err := Sign(s, p)
			if err != nil {
				t.Fatalf("%s: signature %d: %v", scenario, v, err)
			}
			if _, err := Verify(n, keys); err != nil {
				t.Fatalf("%s: signature %d: %v", scenario, v, err)
			}
		}
		if got := strings.Count(prompt.String(), "touch the YubiKey (serial 12345678)"); got != 3 {
			t.Fatalf("%s: %d touch prompts for 3 signatures:\n%s", scenario, got, prompt)
		}
	}
}

func TestPIVRefusals(t *testing.T) {
	if _, _, err := openFake(t, "absent"); !isKind(err, "device") {
		t.Fatalf("no device: %v", err)
	}
	for scenario, kind := range map[string]string{"pin": "pin", "cancelled": "cancelled", "touch": "touch"} {
		p, _, err := openFake(t, scenario)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := Sign(example(), p); n != nil || !isKind(err, kind) {
			t.Fatalf("%s: %q, %v", scenario, n, err)
		}
		// The session is over: nothing more is sent.
		if _, err := Sign(example(), p); !isKind(err, kind) {
			t.Fatalf("%s: a second signature after the refusal: %v", scenario, err)
		}
	}
	p, _, err := openFake(t, "other-text")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := Sign(example(), p); n != nil || err == nil {
		t.Fatalf("a device signature over other text was sealed: %q", n)
	}
	p, _, err = openFake(t, "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Sign(make([]byte, MaxStatement+1)); err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("text over the bound was sent: %v", err)
	}
}

func isKind(err error, kind string) bool {
	var e *PIVError
	return errors.As(err, &e) && e.Kind == kind
}

// The packaged helper, with no smart card service to reach: it refuses
// in the form the session reads. PCSCLITE_CSOCK_NAME points pcsc-lite at
// a socket that does not exist, so no device is ever reached.
func TestPIVHelperWithoutDevice(t *testing.T) {
	helper := os.Getenv("VALLEY_PIV_SIGNER")
	if helper == "" {
		t.Skip("VALLEY_PIV_SIGNER names no helper; approval-unit sets it")
	}
	t.Setenv("PCSCLITE_CSOCK_NAME", t.TempDir()+"/no-pcscd")
	if _, err := OpenPIV(helper, "9c", "", io.Discard); !isKind(err, "device") {
		t.Fatalf("the helper without a device: %v", err)
	}
	if _, err := OpenPIV(helper, "77", "", io.Discard); !isKind(err, "request") {
		t.Fatalf("the helper given no PIV slot: %v", err)
	}
}

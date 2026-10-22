package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"the-valley/note"
)

// The device fixtures, from the operator's YubiKey 5C (firmware 5.7.4): an
// Ed25519 key generated in PIV slot 9c with PIN policy always and touch
// policy always, the slot certificate the YubiKey wrote for it, its f9
// certificate, and a 2075-byte text the key signed with a touch
// (`yubico-piv-tool -a verify-pin --sign -A ED25519 -s 9c`).
const (
	deviceKey    = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIZNSh7kPQN53thBJoBVdGUpUJAkQAb4nsNo/pYJGVIX"
	deviceSerial = "33986790"
)

func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func deviceAttestation(t *testing.T) attestation {
	return attestation{Slot: testdata(t, "valley-approve.attest.pem"), F9: testdata(t, "yubico-f9.pem")}
}

func devicePublic(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, err := sshEd25519Public(deviceKey)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// The registry's ssh-ed25519 line and the PEM public key the YubiKey
// exported are one key.
func TestTheDeviceKeyIsTheExportedKey(t *testing.T) {
	block, _ := pem.Decode([]byte(testdata(t, "valley-approve.pub.pem")))
	exported, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !devicePublic(t).Equal(exported) {
		t.Fatalf("%s is not the exported key", deviceKey)
	}
}

// The pinned root is the one Yubico publishes, by the fingerprint Yubico
// publishes for it, and each pinned intermediate is a PIV one beneath it.
func TestThePinnedCertificatesAreYubicosPIVChain(t *testing.T) {
	const fingerprint = "62:76:0C:6A:6E:F9:16:79:F4:54:C8:90:2B:80:FD:00:98:25:B3:F2:5D:A9:0F:1F:BA:CE:2E:C6:58:6C:D5:A8"
	block, _ := pem.Decode(yubicoRootPEM)
	sum := sha256.Sum256(block.Bytes)
	got := strings.ToUpper(hex.EncodeToString(sum[:]))
	if got != strings.ReplaceAll(fingerprint, ":", "") {
		t.Errorf("the pinned root's SHA-256 is %s, not Yubico Attestation Root 1's", got)
	}
	root, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if root.Subject.CommonName != "Yubico Attestation Root 1" {
		t.Errorf("the pinned root is %q", root.Subject.CommonName)
	}

	var names []string
	for rest := yubicoIntermediatesPEM; ; {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, cert.Subject.CommonName)
		if _, err := cert.Verify(x509.VerifyOptions{
			Roots: yubico.roots, Intermediates: yubico.intermediates,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		}); err != nil {
			t.Errorf("the pinned %s does not verify to the root: %v", cert.Subject.CommonName, err)
		}
	}
	want := []string{
		"Yubico Attestation Intermediate A 1",
		"Yubico Attestation Intermediate B 1",
		"Yubico PIV Attestation A 1",
		"Yubico PIV Attestation B 1",
		"Yubico PIV Attestation B2 1",
	}
	if !slices.Equal(names, want) {
		t.Errorf("the pinned intermediates are %q, want %q", names, want)
	}
}

// The real device's attestation verifies with Go's crypto/x509, through
// its f9 certificate, Yubico PIV Attestation B 1 and Yubico Attestation
// Intermediate B 1, to the pinned root, and says what the device spike
// observed.
func TestTheDeviceAttestationVerifies(t *testing.T) {
	got, err := verifyAttestation(deviceAttestation(t), devicePublic(t), day(t, "2026-10-22"), yubico)
	if err != nil {
		t.Fatal(err)
	}
	if !got.hardware() {
		t.Error("the device's touch-always key is not a hardware key")
	}
	const want = "YubiKey serial " + deviceSerial + ", firmware 5.7.4, PIN policy always, touch policy always"
	if got.String() != want {
		t.Errorf("reported %q, want %q", got, want)
	}
}

// Each way the device's own attestation can be wrong is refused. None of
// them leaves a key that is merely not hardware: each is an error.
func TestAttestationsThatDoNotVerifyAreRefused(t *testing.T) {
	good := deviceAttestation(t)
	pub := devicePublic(t)
	on := day(t, "2026-10-22")
	other := newTestPKI(t)

	slotDER := pemBytes(t, good.Slot)
	f9DER := pemBytes(t, good.F9)

	// The touch policy is inside what Yubico's signature covers: the
	// policy extension's value, 03 02, edited to 03 03 (cached).
	policy := []byte{0x06, 0x0a, 0x2b, 0x06, 0x01, 0x04, 0x01, 0x82, 0xc4, 0x0a, 0x03, 0x08, 0x04, 0x02, 0x03, 0x02}
	if bytes.Count(slotDER, policy) != 1 {
		t.Fatal("the slot certificate's policy extension is not where this test looks")
	}
	cached := bytes.Replace(slotDER, policy, append(policy[:len(policy)-1:len(policy)-1], 0x03), 1)

	for name, c := range map[string]struct {
		a     attestation
		pub   ed25519.PublicKey
		on    time.Time
		trust trust
		want  string
	}{
		"another root": {good, pub, on, other.trust, "does not verify"},
		"no intermediates": {good, pub, on,
			trust{roots: yubico.roots, intermediates: x509.NewCertPool()}, "does not verify"},
		"a slot certificate with its policy edited": {
			attestation{Slot: pemText(cached), F9: good.F9}, pub, on, yubico, "does not verify"},
		"a slot certificate with its signature altered": {
			attestation{Slot: pemText(flipLast(slotDER)), F9: good.F9}, pub, on, yubico, "does not verify"},
		"an f9 certificate with its signature altered": {
			attestation{Slot: good.Slot, F9: pemText(flipLast(f9DER))}, pub, on, yubico, "does not verify"},
		"the slot certificate given as f9": {
			attestation{Slot: good.Slot, F9: good.Slot}, pub, on, yubico, "does not verify"},
		"another key":                   {good, ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)), on, yubico, "a different key"},
		"a day before the certificates": {good, pub, day(t, "2024-11-30"), yubico, "does not verify"},
		"a slot that is not PEM": {
			attestation{Slot: "MIICJzCCAQ+gAwIBAgIQ", F9: good.F9}, pub, on, yubico, "not PEM"},
		"text beside the PEM": {
			attestation{Slot: good.Slot + "trailing words\n", F9: good.F9}, pub, on, yubico, "beside its one PEM block"},
		"text before the PEM": {
			attestation{Slot: "leading words\n" + good.Slot, F9: good.F9}, pub, on, yubico, "beside its one PEM block"},
		"two certificates as f9": {
			attestation{Slot: good.Slot, F9: good.F9 + good.F9}, pub, on, yubico, "beside its one PEM block"},
		"a public key as the slot": {
			attestation{Slot: testdata(t, "valley-approve.pub.pem"), F9: good.F9}, pub, on, yubico, "not a plain certificate"},
	} {
		_, err := verifyAttestation(c.a, c.pub, c.on, c.trust)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one saying %q", name, err, c.want)
		}
	}
}

// The touch policy decides: only always makes a hardware key. The device
// fixture is touch-always, so the other policies are attested here under
// a stand-in for Yubico's PKI.
func TestOnlyTouchAlwaysIsHardware(t *testing.T) {
	pki := newTestPKI(t)
	pub, _ := testKey("approver")
	for touch, want := range map[byte]bool{1: false, 2: true, 3: false} {
		got, err := verifyAttestation(pki.attest(t, pub, policyExtension(3, touch)), pub, day(t, "2026-10-22"), pki.trust)
		if err != nil {
			t.Fatalf("touch policy %d: %v", touch, err)
		}
		if got.hardware() != want {
			t.Errorf("touch policy %d: hardware = %v, want %v", touch, got.hardware(), want)
		}
	}
}

// What a stand-in device can attest that Yubico's would not: no policy at
// all, a key that is not Ed25519, and a slot certificate that an
// intermediate signed instead of the device's f9.
func TestStandInAttestationsThatAreRefused(t *testing.T) {
	pki := newTestPKI(t)
	pub, _ := testKey("approver")
	on := day(t, "2026-10-22")

	if _, err := verifyAttestation(pki.attest(t, pub), pub, on, pki.trust); err == nil ||
		!strings.Contains(err.Error(), "no PIN and touch policy") {
		t.Errorf("no policy extension: error = %v", err)
	}

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyAttestation(pki.attest(t, &ec.PublicKey, policyExtension(3, 2)), pub, on, pki.trust); err == nil ||
		!strings.Contains(err.Error(), "not an ed25519 one") {
		t.Errorf("an ECDSA key: error = %v", err)
	}

	direct := pki.issue(t, pki.intermediate, pki.intermediateKey, leafTemplate(policyExtension(3, 2)), pub)
	a := attestation{Slot: pemText(direct.Raw), F9: pemText(pki.f9.Raw)}
	if _, err := verifyAttestation(a, pub, on, pki.trust); err == nil ||
		!strings.Contains(err.Error(), "without its f9 certificate") {
		t.Errorf("a slot certificate the intermediate signed: error = %v", err)
	}
}

// approvalRegistry exercises every reason a key is or is not in the
// approval files. founder governs and approves with three keys: an
// ordinary one, the device's touch-always key, and a stand-in key whose
// touch is cached. second approves under the grant's first name with a
// stand-in touch-always key. departed has expired, pusher holds no
// approve grant, and robot is not a human, so none of their keys appear.
func approvalRegistry(t *testing.T, pki testPKI) registry {
	t.Helper()
	r := governedRegistry()
	r.Boundaries["approve"] = boundary{Kind: boundaryApprove}
	r.Boundaries["request"] = boundary{Kind: boundaryRequest}
	approve := grant{Boundary: "approve"}
	attestedKey := func(seed string, touch byte) key {
		pub, line := testKey(seed)
		a := pki.attest(t, pub, policyExtension(3, touch))
		return key{Class: sshEd25519, Bound: "hardware", Public: line, Attestation: &a}
	}
	plainKey := func(seed string) key {
		_, line := testKey(seed)
		return key{Class: sshEd25519, Bound: "host", Public: line}
	}

	founder := r.Principals["founder"]
	device := deviceAttestation(t)
	founder.Keys = append(founder.Keys,
		key{Class: sshEd25519, Bound: "hardware", Public: deviceKey, Attestation: &device},
		attestedKey("cached", 3),
	)
	founder.Grants["approve"] = approve
	r.Principals["founder"] = founder

	r.Principals["second"] = principal{
		Kind:   kindHuman,
		Keys:   []key{attestedKey("second", 2)},
		Grants: map[string]grant{"request": {Boundary: "request"}},
	}
	r.Principals["departed"] = principal{
		Kind:    kindHuman,
		Keys:    []key{attestedKey("departed", 2)},
		Grants:  map[string]grant{"approve": approve},
		Expires: "2026-09-01",
	}
	r.Principals["pusher"] = principal{
		Kind:   kindHuman,
		Keys:   []key{plainKey("pusher")},
		Grants: map[string]grant{"push": {Boundary: "push"}},
	}
	r.Principals["robot"] = principal{
		Kind:    "machine",
		Keys:    []key{attestedKey("robot", 2)},
		Grants:  map[string]grant{"approve": approve},
		Expires: "2027-01-01",
	}
	return r
}

// The whole of both files, byte for byte, with the lines written by the
// note package rather than by the compiler.
func TestApprovalFilesListHumanApproversAndTheirHardwareKeys(t *testing.T) {
	pki := newTestPKI(t)
	trustBoth(t, pki)
	r := approvalRegistry(t, pki)
	a, err := render(r, day(t, "2026-10-22"), nil)
	if err != nil {
		t.Fatal(err)
	}

	line := func(name, public string) string {
		pub, err := sshEd25519Public(public)
		if err != nil {
			t.Fatal(err)
		}
		return note.NewVerifierKey(name, pub).String()
	}
	_, cached := testKey("cached")
	_, second := testKey("second")
	founderPlain := r.Principals["founder"].Keys[0].Public
	all := []string{line("founder", founderPlain), line("founder", deviceKey), line("founder", cached), line("second", second)}
	hardware := []string{line("founder", deviceKey), line("second", second)}
	for _, c := range []struct {
		name  string
		got   []byte
		count int
		head  string
		want  []string
	}{
		{"approval-signers", a.approvalSigners, a.approvers, approvalSignersHeader, all},
		{"approval-signers-hardware", a.approvalHardware, a.hardware, approvalHardwareHeader, hardware},
	} {
		slices.Sort(c.want)
		want := c.head + strings.Join(c.want, "\n") + "\n"
		if string(c.got) != want {
			t.Errorf("%s =\n%s\nwant\n%s", c.name, c.got, want)
		}
		if c.count != len(c.want) {
			t.Errorf("%s: count %d, want %d", c.name, c.count, len(c.want))
		}
	}

	// Each attested key of a principal still in force is reported for
	// review, and the expired one is not.
	for _, want := range []string{
		"founder: key 1 is on YubiKey serial " + deviceSerial + ", firmware 5.7.4, PIN policy always, touch policy always; it approves as a hardware key",
		"founder: key 2 is on YubiKey serial 12345678, firmware 5.7.4, PIN policy always, touch policy cached; it approves, but not as a hardware key, because its touch policy is not always",
		"robot: key 0 is on YubiKey serial 12345678, firmware 5.7.4, PIN policy always, touch policy always; its principal approves nothing",
	} {
		if !slices.Contains(a.notes, want) {
			t.Errorf("no report %q among %q", want, a.notes)
		}
	}
	for _, n := range a.notes {
		if strings.HasPrefix(n, "departed: key") {
			t.Errorf("an expired entry's key was reported: %q", n)
		}
	}
}

// A registry without approvers compiles both files holding only their
// headers, and the note reader reads each as no keys.
func TestARegistryWithoutApproversCompilesEmptyFiles(t *testing.T) {
	a, err := render(governedRegistry(), day(t, "2026-10-22"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range [][]byte{a.approvalSigners, a.approvalHardware} {
		keys, err := note.ParseVerifierKeys(f)
		if err != nil || len(keys) != 0 {
			t.Errorf("%q: %d keys, %v", f, len(keys), err)
		}
	}
	if string(a.approvalSigners) != approvalSignersHeader || string(a.approvalHardware) != approvalHardwareHeader {
		t.Errorf("the empty files are %q and %q", a.approvalSigners, a.approvalHardware)
	}
}

// Rendering is reproducible: the order of principals and keys in the
// registry does not reach the files.
func TestApprovalFilesRenderTheSameWhateverTheOrder(t *testing.T) {
	pki := newTestPKI(t)
	trustBoth(t, pki)
	r := approvalRegistry(t, pki)
	first, err := render(r, day(t, "2026-10-22"), nil)
	if err != nil {
		t.Fatal(err)
	}
	founder := r.Principals["founder"]
	slices.Reverse(founder.Keys)
	r.Principals["founder"] = founder
	second, err := render(r, day(t, "2026-10-22"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.approvalSigners, second.approvalSigners) || !bytes.Equal(first.approvalHardware, second.approvalHardware) {
		t.Error("reordering founder's keys changed the approval files")
	}
}

// A key that approves belongs to one principal, so the render fails when
// another principal lists it too, whatever that principal holds.
func TestAKeyThatApprovesBelongsToOnePrincipal(t *testing.T) {
	pki := newTestPKI(t)
	trustBoth(t, pki)
	for name, holder := range map[string]principal{
		"another approver":            {Kind: kindHuman, Grants: map[string]grant{"approve": {Boundary: "approve"}}},
		"a principal holding nothing": {Kind: kindHuman, Grants: map[string]grant{}},
		"a machine":                   {Kind: "machine", Grants: map[string]grant{}, Expires: "2027-01-01"},
	} {
		r := approvalRegistry(t, pki)
		device := deviceAttestation(t)
		holder.Keys = []key{{Class: sshEd25519, Bound: "hardware", Public: deviceKey, Attestation: &device}}
		r.Principals["impostor"] = holder
		_, err := render(r, day(t, "2026-10-22"), nil)
		if err == nil || !strings.Contains(err.Error(), "founder: key 1 is also impostor's, and a key that approves belongs to one principal") {
			t.Errorf("%s holding founder's approval key: error = %v", name, err)
		}
	}
}

// An attestation that is offered and does not verify fails the whole
// render. It is never read as no attestation, which would compile the key
// into approval-signers as an ordinary key.
func TestAnAttestationThatDoesNotVerifyFailsTheRender(t *testing.T) {
	r := governedRegistry()
	r.Boundaries["approve"] = boundary{Kind: boundaryApprove}
	founder := r.Principals["founder"]
	founder.Grants["approve"] = grant{Boundary: "approve"}
	// The device's attestation, offered for another key.
	device := deviceAttestation(t)
	founder.Keys[0].Attestation = &device
	r.Principals["founder"] = founder
	_, err := render(r, day(t, "2026-10-22"), nil)
	if err == nil || !strings.Contains(err.Error(), "founder: key 0: the slot certificate attests a different key") {
		t.Errorf("render error = %v", err)
	}
}

// The device's own signature, made with a touch, opens against both files
// the device's registry entry compiles to. The same key without its
// attestation is in approval-signers alone, and the signature does not
// open against the hardware file.
func TestTheDeviceSignatureOpensAgainstTheCompiledFiles(t *testing.T) {
	text := []byte(testdata(t, "spike.txt"))
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(testdata(t, "spike.sig.b64")))
	if err != nil {
		t.Fatal(err)
	}
	signed := func(name string) []byte {
		blob := binary.BigEndian.AppendUint32(nil, note.KeyHash(name, devicePublic(t)))
		return append(append(append([]byte{}, text...), '\n'),
			note.Mark+name+" "+base64.StdEncoding.EncodeToString(append(blob, sig...))+"\n"...)
	}
	compiled := func(attested bool) artifacts {
		r := governedRegistry()
		r.Boundaries["approve"] = boundary{Kind: boundaryApprove}
		founder := r.Principals["founder"]
		founder.Grants["approve"] = grant{Boundary: "approve"}
		k := key{Class: sshEd25519, Bound: "hardware", Public: deviceKey}
		if attested {
			device := deviceAttestation(t)
			k.Attestation = &device
		}
		founder.Keys = append(founder.Keys, k)
		r.Principals["founder"] = founder
		a, err := render(r, day(t, "2026-10-22"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	opens := func(file []byte) bool {
		keys, err := note.ParseVerifierKeys(file)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = note.Open(signed("founder"), keys)
		return err == nil
	}

	a := compiled(true)
	if !opens(a.approvalSigners) || !opens(a.approvalHardware) {
		t.Error("the device's signature does not open against the files its attested key compiles to")
	}
	a = compiled(false)
	if !opens(a.approvalSigners) {
		t.Error("the device's signature does not open against approval-signers without the attestation")
	}
	if opens(a.approvalHardware) {
		t.Error("the device's key is in the hardware file without its attestation")
	}
}

// ----------------------------------------------------------------------
// A stand-in for Yubico's PKI: a root, one intermediate beneath it, and
// one device whose f9 certificate the intermediate signed. It attests
// whatever key and policy a test asks for, which a real YubiKey only does
// for the keys it holds.

type testPKI struct {
	trust           trust
	root            *x509.Certificate
	intermediate    *x509.Certificate
	intermediateKey ed25519.PrivateKey
	f9              *x509.Certificate
	f9Key           ed25519.PrivateKey
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	var p testPKI
	rootPub, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	p.root = p.issue(t, nil, rootKey, caTemplate("Test Attestation Root", 2), rootPub)
	interPub, interKey, _ := ed25519.GenerateKey(rand.Reader)
	p.intermediate = p.issue(t, p.root, rootKey, caTemplate("Test PIV Attestation", 1), interPub)
	p.intermediateKey = interKey
	f9Pub, f9Key, _ := ed25519.GenerateKey(rand.Reader)
	f9 := caTemplate("YubiKey PIV Attestation", 0)
	f9.MaxPathLenZero = true
	p.f9 = p.issue(t, p.intermediate, interKey, f9, f9Pub)
	p.f9Key = f9Key
	p.trust = trust{roots: x509.NewCertPool(), intermediates: x509.NewCertPool()}
	p.trust.roots.AddCert(p.root)
	p.trust.intermediates.AddCert(p.intermediate)
	return p
}

// attest is the stand-in device's attestation of a key in a slot.
func (p testPKI) attest(t *testing.T, pub any, extensions ...pkix.Extension) attestation {
	t.Helper()
	slot := p.issue(t, p.f9, p.f9Key, leafTemplate(extensions...), pub)
	return attestation{Slot: pemText(slot.Raw), F9: pemText(p.f9.Raw)}
}

func (testPKI) issue(t *testing.T, parent *x509.Certificate, parentKey ed25519.PrivateKey, template *x509.Certificate, pub any) *x509.Certificate {
	t.Helper()
	if parent == nil {
		parent = template
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func caTemplate(name string, pathLen int) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            pathLen,
		KeyUsage:              x509.KeyUsageCertSign,
	}
}

// leafTemplate is a slot certificate as a YubiKey writes one: firmware
// 5.7.4, serial 12345678, and the extensions given.
func leafTemplate(extensions ...pkix.Extension) *x509.Certificate {
	serial, _ := asn1.Marshal(big.NewInt(12345678))
	return &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "YubiKey PIV Attestation 9c"},
		NotBefore:    time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		ExtraExtensions: append([]pkix.Extension{
			{Id: extFirmware, Value: []byte{5, 7, 4}},
			{Id: extSerial, Value: serial},
		}, extensions...),
	}
}

func policyExtension(pin, touch byte) pkix.Extension {
	return pkix.Extension{Id: extPolicy, Value: []byte{pin, touch}}
}

// trustBoth has the render trust the stand-in PKI beside Yubico's, so one
// registry can hold the device's real attestation and stand-in ones.
func trustBoth(t *testing.T, p testPKI) {
	t.Helper()
	pinned := yubico
	t.Cleanup(func() { yubico = pinned })
	yubico = trust{roots: pinned.roots.Clone(), intermediates: pinned.intermediates.Clone()}
	yubico.roots.AddCert(p.root)
	yubico.intermediates.AddCert(p.intermediate)
}

// testKey is an Ed25519 public key made from a seed, and its registry
// line.
func testKey(seed string) (ed25519.PublicKey, string) {
	s := sha256.Sum256([]byte(seed))
	pub := ed25519.NewKeyFromSeed(s[:]).Public().(ed25519.PublicKey)
	var blob []byte
	for _, f := range [][]byte{[]byte(sshEd25519), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(f)))
		blob = append(blob, f...)
	}
	return pub, sshEd25519 + " " + base64.StdEncoding.EncodeToString(blob)
}

func pemText(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func pemBytes(t *testing.T, text string) []byte {
	t.Helper()
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		t.Fatal("not PEM")
	}
	return block.Bytes
}

// flipLast changes the last byte of a certificate: a bit of its signature.
func flipLast(der []byte) []byte {
	out := slices.Clone(der)
	out[len(out)-1] ^= 1
	return out
}

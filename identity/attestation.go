package main

// A key's attestation: the evidence that its private half was generated
// on a YubiKey, in a PIV slot, and cannot leave it. The registry carries
// two certificates per attested key, as PEM:
//
//   - slot: the certificate the YubiKey writes for the slot holding the
//     key (`yubico-piv-tool -a attest -s 9c`). It holds the public key and
//     says, in Yubico's extensions, which device made it and how the slot
//     may be used: whether signing needs the PIN, and whether it needs a
//     touch.
//   - f9: the YubiKey's own attestation certificate, read from slot f9
//     (`yubico-piv-tool -a read-certificate -s f9`). It signed the slot
//     certificate, and Yubico signed it at manufacture.
//
// The compiler, not the registry, decides which issuers count. It carries
// Yubico Attestation Root 1 and the PIV intermediates beneath it
// (yubico/), and no registry entry can add another. A key counts as a
// hardware key when its slot certificate verifies, through its f9
// certificate and those intermediates, to that root; when the key it
// attests is the entry's public key; and when its touch policy is always.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// The pinned certificates, from https://developers.yubico.com/PKI/.
// attestation-root-1.pem is yubico-ca-1.pem, byte for byte: Yubico
// Attestation Root 1, which signs the attestations of YubiKeys from
// firmware 5.7.4 on, and whose SHA-256 fingerprint Yubico publishes and
// the unit tests pin. piv-intermediates.pem is the five certificates of
// yubico-intermediate.pem on a PIV path beneath it: Yubico Attestation
// Intermediate A 1 and B 1, and Yubico PIV Attestation A 1, B 1 and B2 1.
// The FIDO, OpenPGP, SD and YubiHSM intermediates are left out, so a
// certificate issued under another application of the device attests no
// PIV key.
var (
	//go:embed yubico/attestation-root-1.pem
	yubicoRootPEM []byte
	//go:embed yubico/piv-intermediates.pem
	yubicoIntermediatesPEM []byte
)

// trust is the issuers an attestation must verify to.
type trust struct {
	roots         *x509.CertPool
	intermediates *x509.CertPool
}

// yubico is the trust every compilation uses. The tests replace it to
// sign attestations of their own.
var yubico = mustTrust(yubicoRootPEM, yubicoIntermediatesPEM)

func mustTrust(roots, intermediates []byte) trust {
	t := trust{roots: x509.NewCertPool(), intermediates: x509.NewCertPool()}
	if !t.roots.AppendCertsFromPEM(roots) || !t.intermediates.AppendCertsFromPEM(intermediates) {
		panic("identity: the pinned Yubico certificates do not parse")
	}
	return t
}

// The extensions Yubico writes into a slot certificate, under its arc
// 1.3.6.1.4.1.41482.3 (https://developers.yubico.com/PIV/Introduction/PIV_attestation.html).
var (
	extFirmware = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 41482, 3, 3}
	extSerial   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 41482, 3, 7}
	extPolicy   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 41482, 3, 8}
)

// The policies a slot certificate may state, in the two bytes of its
// policy extension: the PIN policy first, then the touch policy. These are
// the values Yubico specifies, and the only ones read. Any other value
// fails the compilation, because a policy this program cannot interpret
// says nothing about whether a signature needed a touch.
const touchAlways = 2

var (
	pinPolicies   = map[byte]string{1: "never", 2: "once", 3: "always"}
	touchPolicies = map[byte]string{1: "never", touchAlways: "always", 3: "cached"}
)

// attestation is the block a key entry may carry (schema/identity.cue).
type attestation struct {
	Slot string `json:"slot"`
	F9   string `json:"f9"`
}

// attested is what a verified attestation says about its key.
type attested struct {
	serial   *big.Int
	firmware []byte
	pin      byte
	touch    byte
}

// hardware is whether the key qualifies for the hardware approval file:
// every signature it makes needed a touch.
func (a attested) hardware() bool {
	return a.touch == touchAlways
}

// String is the line the compiler reports, for the people reviewing an
// enrollment.
func (a attested) String() string {
	firmware := "unstated"
	if len(a.firmware) == 3 {
		firmware = fmt.Sprintf("%d.%d.%d", a.firmware[0], a.firmware[1], a.firmware[2])
	}
	serial := "unstated"
	if a.serial != nil {
		serial = a.serial.String()
	}
	return fmt.Sprintf("YubiKey serial %s, firmware %s, PIN policy %s, touch policy %s",
		serial, firmware, pinPolicies[a.pin], touchPolicies[a.touch])
}

// verifyAttestation verifies a key's attestation as of one day, and
// returns what it says. pub is the key the entry declares. Every failure
// is an error: an attestation that is present and does not verify fails
// the compilation, rather than leaving the key a software key.
func verifyAttestation(a attestation, pub ed25519.PublicKey, day time.Time, t trust) (attested, error) {
	var out attested
	slot, err := readCertificate("slot", a.Slot)
	if err != nil {
		return out, err
	}
	f9, err := readCertificate("f9", a.F9)
	if err != nil {
		return out, err
	}

	// The slot certificate verifies to the pinned root, through the f9
	// certificate and the pinned intermediates. The extended key usage is
	// left open: these certificates attest keys and serve no TLS, and Go's
	// default would ask for a server certificate.
	intermediates := t.intermediates.Clone()
	intermediates.AddCert(f9)
	chains, err := slot.Verify(x509.VerifyOptions{
		Roots:         t.roots,
		Intermediates: intermediates,
		CurrentTime:   day,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return out, fmt.Errorf("the attestation does not verify to Yubico Attestation Root 1: %w", err)
	}
	// And the f9 certificate is what signed it: the slot certificate is
	// the device's statement, not something an intermediate issued.
	for _, chain := range chains {
		if len(chain) < 2 || !chain[1].Equal(f9) {
			return out, fmt.Errorf("the slot certificate verifies without its f9 certificate")
		}
	}

	attestedKey, ok := slot.PublicKey.(ed25519.PublicKey)
	if !ok {
		return out, fmt.Errorf("the slot certificate attests a key of type %s, not Ed25519", slot.PublicKeyAlgorithm)
	}
	if !attestedKey.Equal(pub) {
		return out, fmt.Errorf("the slot certificate attests a different key from the entry's public key")
	}

	policy := extension(slot.Extensions, extPolicy)
	switch {
	case policy == nil:
		return out, fmt.Errorf("the slot certificate states no PIN and touch policy")
	case len(policy) != 2:
		return out, fmt.Errorf("the slot certificate's PIN and touch policy is %d byte(s), not 2", len(policy))
	}
	out.pin, out.touch = policy[0], policy[1]
	if _, ok := pinPolicies[out.pin]; !ok {
		return out, fmt.Errorf("the slot certificate states PIN policy 0x%02x, which Yubico does not specify", out.pin)
	}
	if _, ok := touchPolicies[out.touch]; !ok {
		return out, fmt.Errorf("the slot certificate states touch policy 0x%02x, which Yubico does not specify", out.touch)
	}
	out.firmware = extension(slot.Extensions, extFirmware)
	if der := extension(slot.Extensions, extSerial); der != nil {
		var serial *big.Int
		if rest, err := asn1.Unmarshal(der, &serial); err == nil && len(rest) == 0 {
			out.serial = serial
		}
	}
	return out, nil
}

// readCertificate reads one PEM certificate, and nothing around it.
func readCertificate(which, text string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(text))
	switch {
	case block == nil:
		return nil, fmt.Errorf("the %s certificate is not PEM", which)
	case block.Type != "CERTIFICATE" || len(block.Headers) != 0:
		return nil, fmt.Errorf("the %s PEM block is not a plain certificate", which)
	case !bytes.HasPrefix(bytes.TrimSpace([]byte(text)), []byte("-----BEGIN ")) || len(bytes.TrimSpace(rest)) != 0:
		return nil, fmt.Errorf("the %s certificate holds text beside its one PEM block", which)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the %s certificate: %w", which, err)
	}
	return cert, nil
}

// extension is the value of the extension with the given identifier, or
// nil.
func extension(extensions []pkix.Extension, id asn1.ObjectIdentifier) []byte {
	for _, e := range extensions {
		if e.Id.Equal(id) {
			return e.Value
		}
	}
	return nil
}

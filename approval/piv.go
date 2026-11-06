package approval

import (
	"bufio"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// PIVSession signs with a YubiKey's PIV slot through valley-piv-signer
// (approval/piv/valley-piv-signer.c), a helper on libykpiv that holds the
// YubiKey exclusively for the whole session and resets it when the session
// ends. The PIN is asked for once, on the terminal, by the helper; it never
// passes through this process. Each signature takes a touch when the key's
// touch policy says so.
//
// After any refusal the session is over: it sends nothing more, so a PIN
// the device refused is never sent again.
type PIVSession struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	pub    ed25519.PublicKey
	serial string
	pin    byte // the key's attested PIN policy
	touch  byte // and touch policy
	prompt io.Writer
	done   error
	waited bool
	status error // how the helper exited, once waited for
}

// PIVError is a refusal valley-piv-signer reports.
type PIVError struct {
	Kind   string // device, pin, pin-format, cancelled, touch or request
	Detail string
}

func (e *PIVError) Error() string {
	switch e.Kind {
	case "pin":
		return fmt.Sprintf("the YubiKey refused the PIN, and allows %s more tries; it is not sent again", e.Detail)
	case "pin-format":
		return "a PIN is 6 to 8 characters; what was entered was not sent to the YubiKey"
	case "cancelled":
		return "no PIN was entered"
	case "touch":
		return "the YubiKey did not sign: it was not touched in time"
	case "device":
		return "the YubiKey cannot sign: " + e.Detail
	}
	return "the PIV signer refused the request: " + e.Detail
}

// The attestation extension holding a key's PIN and touch policy, and the
// values Yubico gives them (identity/attestation.go).
var policyOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 41482, 3, 8}

const (
	pinNever    = 1
	pinAlways   = 3
	touchNever  = 1
	maxPIVReply = 16 << 10
)

// OpenPIV starts the helper at program on one slot ("9c" when empty) of
// the YubiKey on the first reader whose name holds reader (any, when
// empty), and reads the key the slot attests to. Touch requests are
// written to prompt.
func OpenPIV(program, slot, reader string, prompt io.Writer) (*PIVSession, error) {
	args := []string{}
	if slot != "" {
		args = append(args, "--slot", slot)
	}
	if reader != "" {
		args = append(args, "--reader", reader)
	}
	cmd := exec.Command(program, args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("the PIV signer did not start: %w", err)
	}
	p := &PIVSession{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 4096), prompt: prompt}
	word, rest, err := p.reply()
	if err == nil && word != "key" {
		err = fmt.Errorf("the PIV signer opened with %q, not its key", word)
	}
	if err == nil {
		err = p.readKey(rest)
	}
	if err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *PIVSession) readKey(rest string) error {
	serial, encoded, _ := strings.Cut(rest, " ")
	der, err := hex.DecodeString(encoded)
	if err != nil {
		return errors.New("the PIV signer's attestation certificate is not hex")
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("the slot's attestation certificate does not parse: %w", err)
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return errors.New("the slot holds no ed25519 key; an approval is an ed25519 signature")
	}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(policyOID) && len(ext.Value) == 2 {
			p.pin, p.touch = ext.Value[0], ext.Value[1]
		}
	}
	p.pub, p.serial = pub, serial
	return nil
}

// Public is the key the slot attests to.
func (p *PIVSession) Public() ed25519.PublicKey { return p.pub }

// Sign asks the device to sign text. Text over MaxStatement is refused
// here, before the device is asked.
func (p *PIVSession) Sign(text []byte) ([]byte, error) {
	if p.done != nil {
		return nil, p.done
	}
	if len(text) == 0 || len(text) > MaxStatement {
		return nil, fmt.Errorf("the text is %d bytes; the PIV signer signs 1 to %d", len(text), MaxStatement)
	}
	mode := map[byte]string{pinNever: "none", pinAlways: "each"}[p.pin]
	if mode == "" {
		mode = "session"
	}
	if _, err := fmt.Fprintf(p.in, "sign %d %s\n%s", len(text), mode, text); err != nil {
		return nil, p.end(fmt.Errorf("the PIV signer stopped reading: %w", err))
	}
	word, rest, err := p.reply()
	if err != nil {
		return nil, p.end(err)
	}
	if word == "touch" {
		if p.touch != touchNever {
			fmt.Fprintf(p.prompt, "touch the YubiKey (serial %s) to sign\n", p.serial)
		}
		if word, rest, err = p.reply(); err != nil {
			return nil, p.end(err)
		}
	}
	if word != "signature" {
		return nil, p.end(fmt.Errorf("the PIV signer replied %q, not a signature", word))
	}
	sig, err := hex.DecodeString(rest)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, p.end(errors.New("the PIV signer's signature is not 64 bytes of hex"))
	}
	return sig, nil
}

// reply reads one line from the helper. A refusal is returned as a
// *PIVError.
func (p *PIVSession) reply() (word, rest string, err error) {
	var line []byte
	for {
		chunk, isPrefix, err := p.out.ReadLine()
		if err != nil {
			return "", "", fmt.Errorf("the PIV signer ended without replying: %w", p.exit(err))
		}
		line = append(line, chunk...)
		if len(line) > maxPIVReply {
			return "", "", errors.New("the PIV signer's reply is too long")
		}
		if !isPrefix {
			break
		}
	}
	word, rest, _ = strings.Cut(string(line), " ")
	if word == "error" {
		kind, detail, _ := strings.Cut(rest, " ")
		return "", "", &PIVError{Kind: kind, Detail: detail}
	}
	return word, rest, nil
}

// exit waits for a helper whose output ended, and says how it ended.
func (p *PIVSession) exit(err error) error {
	if werr := p.wait(); werr != nil {
		return werr
	}
	return err
}

func (p *PIVSession) wait() error {
	if !p.waited {
		p.waited, p.status = true, p.cmd.Wait()
	}
	return p.status
}

func (p *PIVSession) end(err error) error {
	p.done = err
	p.Close()
	return err
}

// Close ends the session: the helper reads the end of its input,
// disconnects from the device, and exits.
func (p *PIVSession) Close() error {
	p.in.Close()
	io.Copy(io.Discard, io.LimitReader(p.out, maxPIVReply))
	return p.wait()
}

// PIVSerial is the serial number of the YubiKey the session signs on.
func (p *PIVSession) PIVSerial() string { return p.serial }

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"the-valley/note"
)

// The state directory holds:
//
//	lock          held for the whole of a pass
//	key           the export key, PKCS #8 in PEM, mode 0400
//	verifier      its verifier line, under the signing name
//	acknowledged  the export the receiver last said it serves
const (
	keyFile          = "key"
	verifierFile     = "verifier"
	acknowledgedFile = "acknowledged"
)

// The calls writeFile and writeDurable sync and rename with. A test
// replaces them to make one fail, as a crash or a full disk would.
var (
	syncFile   = (*os.File).Sync
	renameFile = os.Rename
)

// lockFile takes an exclusive flock(2) lock on path, waiting for a pass
// that holds it to finish.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("could not lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}

// loadKey reads the export key from the state directory, or makes one the
// first time. The key is made under a temporary name and renamed into
// place, so a start killed half way leaves no key, and the next start
// makes one. The private key is never printed. Its verifier line is
// written beside it on every pass, and said whenever it changes: a new key,
// or a new signing name.
func (p *publisher) loadKey() (ed25519.PrivateKey, error) {
	path := filepath.Join(p.cfg.state, keyFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = makeKey(path)
	}
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PEM private key", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an Ed25519 key", path)
	}
	if err := note.CheckName(p.signer); err != nil {
		return nil, err
	}
	line := note.NewVerifierKey(p.signer, key.Public().(ed25519.PublicKey)).String() + "\n"
	verifier := filepath.Join(p.cfg.state, verifierFile)
	if old, err := os.ReadFile(verifier); err == nil && bytes.Equal(old, []byte(line)) {
		return key, nil
	}
	if err := writeFile(verifier, []byte(line), 0o644); err != nil {
		return nil, err
	}
	p.say("the export key's verifier line is %s", bytes.TrimSuffix([]byte(line), []byte("\n")))
	return key, nil
}

// makeKey makes a new export key at path and returns its PEM.
func makeKey(path string) ([]byte, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := writeFile(path, data, 0o400); err != nil {
		return nil, err
	}
	return data, nil
}

// writeFile writes data to path whole or not at all: to a temporary file
// beside it, synced, then renamed over it.
func writeFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".new-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return renameFile(f.Name(), path)
}

// acknowledgedSchema is the acknowledged file's schema.
const acknowledgedSchema = "valley-web-publish-acknowledged/v1"

// acknowledged is the export the receiver last said it serves: in a
// commit's reply, or in an inventory whose active export had the same
// digest. A pass records it only from the receiver's reply, never from an
// upload it merely attempted.
type acknowledged struct {
	Schema      string `json:"schema"`
	Receiver    string `json:"receiver"`
	Destination string `json:"destination"`
	Digest      string `json:"digest"`
	// Sequence is the receiver's active sequence. No later manifest is
	// signed with a sequence at or below it.
	Sequence int64 `json:"sequence"`
	// At is when the receiver said so, in RFC 3339.
	At string `json:"at"`
}

// readAcknowledged reads the last acknowledgement, or none.
func (p *publisher) readAcknowledged() (acknowledged, error) {
	data, err := os.ReadFile(filepath.Join(p.cfg.state, acknowledgedFile))
	if errors.Is(err, os.ErrNotExist) {
		return acknowledged{}, nil
	}
	if err != nil {
		return acknowledged{}, err
	}
	var a acknowledged
	if err := json.Unmarshal(data, &a); err != nil || a.Schema != acknowledgedSchema {
		p.warn("%s is not an acknowledgement this publisher wrote; it is set aside", filepath.Join(p.cfg.state, acknowledgedFile))
		return acknowledged{}, nil
	}
	return a, nil
}

// covers reports whether the acknowledgement makes an upload of the export
// with digest needless: the same receiver and destination acknowledged the
// same digest less than recheck ago. Past recheck the publisher asks
// again, so a receiver that lost its state is synchronised again within
// recheck.
func (a acknowledged) covers(cfg config, digest string, now time.Time, recheck time.Duration) bool {
	if a.Receiver != cfg.receiver || a.Destination != cfg.destination || a.Digest != digest {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, a.At)
	return err == nil && now.Sub(at) < recheck && !at.After(now)
}

// recordAcknowledged writes an acknowledgement the receiver gave.
func (p *publisher) recordAcknowledged(digest string, sequence int64) error {
	a := acknowledged{
		Schema: acknowledgedSchema, Receiver: p.cfg.receiver, Destination: p.cfg.destination,
		Digest: digest, Sequence: sequence, At: p.now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(p.cfg.state, acknowledgedFile), append(data, '\n'), 0o600)
}

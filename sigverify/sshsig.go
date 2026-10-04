package sigverify

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// The SSHSIG format, from PROTOCOL.sshsig in openssh-portable.
const (
	sshsigMagic   = "SSHSIG"
	sshsigVersion = 1
	armorBegin    = "-----BEGIN SSH SIGNATURE-----"
	armorEnd      = "-----END SSH SIGNATURE-----"
)

// envelope is a parsed SSHSIG blob: who signed, in which namespace, with
// which hash, and the signature itself.
type envelope struct {
	publicKey []byte
	namespace string
	hashAlg   string
	signature []byte
}

// parseSignature reads an SSHSIG in either form: armored, as
// `ssh-keygen -Y sign` writes it and git embeds it in a tag, or the raw
// binary blob inside the armor.
func parseSignature(sig []byte) (*envelope, error) {
	blob := sig
	if bytes.HasPrefix(sig, []byte(armorBegin)) {
		var err error
		if blob, err = dearmor(sig); err != nil {
			return nil, err
		}
	}
	if !bytes.HasPrefix(blob, []byte(sshsigMagic)) {
		return nil, errors.New("it is neither an armored SSH signature nor an SSHSIG blob")
	}
	r := reader{rest: blob[len(sshsigMagic):]}
	version := r.u32()
	env := &envelope{
		publicKey: r.str(),
		namespace: string(r.str()),
	}
	// The reserved field is read and ignored, as ssh-keygen does. The bytes
	// a key signs always hold an empty reserved field, so what the envelope
	// carries here cannot change what was signed. It does mean two
	// envelopes can carry one signature, which is one reason signature
	// bytes are no release identity.
	r.str()
	env.hashAlg = string(r.str())
	env.signature = r.str()
	if err := r.end(); err != nil {
		return nil, fmt.Errorf("the SSHSIG blob %w", err)
	}
	if version != sshsigVersion {
		return nil, fmt.Errorf("it is SSHSIG version %d, and only version %d exists", version, sshsigVersion)
	}
	return env, nil
}

// dearmor follows sshsig_dearmor in OpenSSH, more strictly. The header
// opens the input and the footer follows a newline, as there. Between them
// is base64 wrapped onto lines, with canonical padding and nothing else.
// After the footer only whitespace may follow, where ssh-keygen ignores
// anything at all.
func dearmor(armored []byte) ([]byte, error) {
	rest := armored[len(armorBegin):]
	switch {
	case bytes.HasPrefix(rest, []byte("\r\n")):
		rest = rest[2:]
	case bytes.HasPrefix(rest, []byte("\n")):
		rest = rest[1:]
	default:
		return nil, errors.New("the armor header is not followed by a newline")
	}
	end := bytes.Index(rest, []byte("\n"+armorEnd))
	if end < 0 {
		return nil, errors.New("the armor has no footer")
	}
	if after := rest[end+1+len(armorEnd):]; len(bytes.TrimSpace(after)) != 0 {
		return nil, errors.New("the armor footer is followed by more than whitespace")
	}
	encoded := strings.NewReplacer("\r", "", "\n", "").Replace(string(rest[:end]))
	blob, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("the armored signature is not canonical base64: %w", err)
	}
	return blob, nil
}

// signedData rebuilds the bytes a signer's key signed for message: the
// magic preamble, the namespace, an empty reserved field, the hash
// algorithm, and the hash of the message.
func signedData(namespace, hashAlg string, message []byte) ([]byte, error) {
	var sum []byte
	switch hashAlg {
	case "sha512":
		s := sha512.Sum512(message)
		sum = s[:]
	case "sha256":
		s := sha256.Sum256(message)
		sum = s[:]
	default:
		return nil, fmt.Errorf("the message hash %q is not one SSHSIG allows", hashAlg)
	}
	data := []byte(sshsigMagic)
	data = appendString(data, []byte(namespace))
	data = appendString(data, nil)
	data = appendString(data, []byte(hashAlg))
	data = appendString(data, sum)
	return data, nil
}

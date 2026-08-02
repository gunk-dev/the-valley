package main

// The envelope: a detached SSHSIG signature over the statement's canonical
// bytes, under a namespace of this project's own. No git object is signed
// (ida-51605e8) — the signature covers the statement and nothing else, so
// it survives a rebase exactly as the subject digest does.
//
// ssh-keygen is the implementation. It is the reference SSHSIG signer, it
// is already on any machine that pushes over ssh, and using it keeps this
// program's dependencies to the Go standard library.

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// signDetached signs data with the key at keyPath and returns the armored
// signature.
func signDetached(keyPath, namespace string, data []byte) ([]byte, error) {
	if keyPath == "" {
		return nil, fmt.Errorf("no signing key: pass --key. The host signs; an unsigned statement is not an attestation")
	}
	if _, err := os.Stat(keyPath); err != nil {
		return nil, fmt.Errorf("signing key %s: %w", keyPath, err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-f", keyPath, "-n", namespace, "-")
	cmd.Stdin = bytes.NewReader(data)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh-keygen -Y sign: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// keyFingerprint returns the signer's key fingerprint twice: as hex, which
// names the ref, and in OpenSSH's own SHA256:base64 rendering, which is
// what a reader sees everywhere else. Both are SHA-256 of the same public
// key blob; base64 cannot name a ref because it contains '/'.
func keyFingerprint(keyPath string) (hexPrint, sshPrint string, err error) {
	cmd := exec.Command("ssh-keygen", "-l", "-f", keyPath)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("ssh-keygen -l -f %s: %w: %s", keyPath, err, strings.TrimSpace(errb.String()))
	}
	fields := strings.Fields(out.String())
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "SHA256:") {
		return "", "", fmt.Errorf("ssh-keygen -l -f %s: unexpected output %q", keyPath, strings.TrimSpace(out.String()))
	}
	sshPrint = fields[1]
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sshPrint, "SHA256:"))
	if err != nil {
		return "", "", fmt.Errorf("fingerprint %s: %w", sshPrint, err)
	}
	return hex.EncodeToString(raw), sshPrint, nil
}

// findPrincipal asks the allowed-signers file who signed. A signature by a
// key the file does not list has no principal, which is the failure a
// verifier wants: not "bad signature" but "not a signer we accept".
func findPrincipal(allowedSigners, sigPath string) (string, error) {
	cmd := exec.Command("ssh-keygen", "-Y", "find-principals", "-s", sigPath, "-f", allowedSigners)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("no principal in %s signed this statement: %s", allowedSigners, strings.TrimSpace(errb.String()))
	}
	principals := strings.Fields(out.String())
	if len(principals) == 0 {
		return "", fmt.Errorf("no principal in %s signed this statement", allowedSigners)
	}
	return principals[0], nil
}

// verifyDetached checks a signature over data against an allowed-signers
// file, and returns what ssh-keygen said about it.
func verifyDetached(allowedSigners, principal, namespace, sigPath string, data []byte) (string, error) {
	cmd := exec.Command("ssh-keygen", "-Y", "verify",
		"-f", allowedSigners, "-I", principal, "-n", namespace, "-s", sigPath)
	cmd.Stdin = bytes.NewReader(data)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(errb.String()+out.String()))
	}
	return strings.TrimSpace(errb.String() + out.String()), nil
}

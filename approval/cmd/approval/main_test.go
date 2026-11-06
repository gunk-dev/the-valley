package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/approval"
	"the-valley/note"
)

// A statement written, signed with a software key and verified, through
// the command; then refused under authority that does not list the key.
func TestStatementSignVerify(t *testing.T) {
	dir := t.TempDir()
	var out, errs bytes.Buffer
	if code := run([]string{"statement", "--instance", strings.Repeat("5f", 32), "--project", "app",
		"--target", "main", "--change", "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz", "--version", "2",
		"--head", "git-sha1:" + strings.Repeat("2", 40), "--record", "git-sha1:" + strings.Repeat("3", 40),
		"--principal", "operator", "--issued", "2026-11-06T00:43:26Z"}, &out, &errs); code != 0 {
		t.Fatalf("statement: %d: %s", code, errs.String())
	}
	want, err := os.ReadFile("../../testdata/approval.statement")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("statement wrote\n%s", out.String())
	}
	statement := filepath.Join(dir, "a.statement")
	if err := os.WriteFile(statement, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"sign", "--key", "../../testdata/software.key", statement}, &out, &errs); code != 0 {
		t.Fatalf("sign: %d: %s", code, errs.String())
	}

	raw, err := os.ReadFile("../../testdata/software.key")
	if err != nil {
		t.Fatal(err)
	}
	k, err := approval.ParseKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	signers := filepath.Join(dir, "approval-signers")
	os.WriteFile(signers, []byte(note.NewVerifierKey("operator", k.Public()).String()+"\n"), 0o644)
	out.Reset()
	if code := run([]string{"verify", "--signers", signers, statement + ".note"}, &out, &errs); code != 0 {
		t.Fatalf("verify: %d: %s%s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "operator approves change nxvpwlkmqtsrzyoukklnpmqrstuvwxyz version 2") {
		t.Fatalf("verify printed %q", out.String())
	}

	hardware := filepath.Join(dir, "approval-signers-hardware")
	os.WriteFile(hardware, []byte("# no hardware key enrolled\n"), 0o644)
	if code := run([]string{"verify", "--signers", hardware, statement + ".note"}, &out, &errs); code != 1 {
		t.Fatalf("verify under empty authority exited %d", code)
	}
	other := approval.NewKey(make([]byte, 64))
	os.WriteFile(hardware, []byte(note.NewVerifierKey("operator", other.Public()).String()+"\n"), 0o644)
	if code := run([]string{"verify", "--signers", hardware, statement + ".note"}, &out, &errs); code != 1 {
		t.Fatalf("verify under authority without the key exited %d", code)
	}
}

package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketsRefusesASocketAtAnyDepth(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tmux-1000"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tmux-1000", "notes"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sockets(dir); err != nil {
		t.Fatalf("a directory without sockets: %v", err)
	}
	l, err := net.Listen("unix", filepath.Join(dir, "tmux-1000", "default"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := sockets(dir); err == nil || !strings.Contains(err.Error(), "tmux-1000/default") {
		t.Fatalf("a socket under the directory: got %v, want a refusal naming it", err)
	}
}

func TestVersionComparesTheFirstLine(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "klaus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'klaus version 0.1.0-unknown'\necho more\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := version(bin, "klaus version 0.1.0-unknown"); err != nil {
		t.Fatalf("the stamp it prints: %v", err)
	}
	if err := version(bin, "klaus version 0.1.0-1111111"); err == nil {
		t.Fatal("another stamp was accepted")
	}
	if err := version(filepath.Join(t.TempDir(), "missing"), "x"); err == nil {
		t.Fatal("a program that does not run was accepted")
	}
}

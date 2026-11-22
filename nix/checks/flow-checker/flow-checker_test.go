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

func TestVersionComparesTheWholeOutput(t *testing.T) {
	dir := t.TempDir()
	script := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	exact := script("klaus", `[ "$*" = "flow --version" ] && echo 'klaus version 0.1.0-unknown'`+"\n")
	if err := version("klaus version 0.1.0-unknown", []string{exact, "flow"}); err != nil {
		t.Fatalf("the stamp it prints, given its arguments before --version: %v", err)
	}
	if err := version("klaus version 0.1.0-1111111", []string{exact, "flow"}); err == nil {
		t.Fatal("another stamp was accepted")
	}
	for name, body := range map[string]string{
		"more":    "echo 'klaus version 0.1.0-unknown'; echo more\n",
		"unended": "printf 'klaus version 0.1.0-unknown'\n",
		"stderr":  "echo 'klaus version 0.1.0-unknown'; echo note >&2\n",
		"failing": "echo 'klaus version 0.1.0-unknown'; exit 1\n",
	} {
		if err := version("klaus version 0.1.0-unknown", []string{script(name, body)}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := version("x", []string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("a program that does not run was accepted")
	}
}

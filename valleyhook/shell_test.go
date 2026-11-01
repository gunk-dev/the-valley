package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A push is refused until the host converges, so what reads as a push has
// to be everything git-shell would run as receive-pack. git-shell turns
// "git" and any whitespace into "git-" before it looks the command up.
func TestEverySpellingOfReceivePackIsAPush(t *testing.T) {
	for command, push := range map[string]bool{
		"git-receive-pack 'project.git'":   true,
		"git receive-pack 'project.git'":   true,
		"git\treceive-pack 'project.git'":  true,
		"git-receive-pack":                 true,
		"git-upload-pack 'project.git'":    false,
		"git upload-pack 'project.git'":    false,
		"git-upload-archive 'project.git'": false,
		"env":                              false,
	} {
		if got := isPush(command); got != push {
			t.Errorf("isPush(%q) = %v, want %v", command, got, push)
		}
	}
}

// The telemetry command is the shell's own: refused unless the host
// serves telemetry, the key names a principal, the command parses and the
// principal holds the grant; otherwise relayed to the collector as one
// request line naming the principal the shell derived, whatever the
// command said.
func TestTheTelemetryCommandIsRelayedOnlyWhole(t *testing.T) {
	dir := t.TempDir()
	grantsFile := filepath.Join(dir, "grants")
	os.WriteFile(grantsFile, []byte("observe operator\nreport alice\n"), 0o600)
	socket := filepath.Join(dir, "sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := make(chan string, 8)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			got <- line
			c.Write([]byte(`{"kind":"end"}` + "\n"))
			c.Close()
		}
	}()
	run := func(cmd, principal, sock string) (int, string) {
		var out, errs strings.Builder
		status := telemetryCommand(cmd, principal, sock, grantsFile, strings.NewReader(""), &out, &errs)
		return status, out.String() + errs.String()
	}
	for _, c := range []struct{ cmd, principal, sock, why string }{
		{"valley-telemetry sources", "operator", "", "serves no telemetry"},
		{"valley-telemetry sources", "", socket, "names a principal"},
		{"valley-telemetry sources; sh", "operator", socket, "not a telemetry operation"},
		{"valley-telemetry read --source=/etc/shadow", "operator", socket, "not a source name"},
		{"valley-telemetry sources", "alice", socket, "does not hold the telemetry observe grant"},
		{"valley-telemetry report", "operator", socket, "does not hold the telemetry report grant"},
	} {
		if status, said := run(c.cmd, c.principal, c.sock); status == 0 || !strings.Contains(said, c.why) {
			t.Errorf("%q as %q: %d %q", c.cmd, c.principal, status, said)
		}
	}
	select {
	case line := <-got:
		t.Fatalf("a refused command reached the collector: %s", line)
	default:
	}
	if status, said := run("valley-telemetry read --source=worker --logs", "operator", socket); status != 0 || !strings.Contains(said, `"end"`) {
		t.Fatalf("relayed: %d %q", status, said)
	}
	if line := <-got; line != `{"v":1,"principal":"operator","op":"read","source":"worker","logs":true}`+"\n" {
		t.Fatalf("the collector was sent %q", line)
	}
}

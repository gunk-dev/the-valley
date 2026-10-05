package main

import "testing"

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

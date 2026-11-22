package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// secretPushStderr is a refused push whose other lines echo a URL with
// credentials and a remote helper's command line, as git prints them.
const secretPushStderr = "fatal: unable to access 'https://user:secret@host/x?token=y'z&k=secret': retrying\n" +
	"helper: ext::ssh -i /home/operator/.ssh/secret-key example.org %S repo\n" +
	"remote: policy denies refs/klaus/data        \r\n" +
	"remote: \x1b[31msee the policy\x1b[0m\n" +
	"error: failed to push some refs to 'ext::ssh -i /home/operator/.ssh/secret-key example.org %S repo'\n"

// porcelain is git push --porcelain's stdout for one ref with summary, after
// the destination line that echoes the remote's URL.
func porcelain(summary string) string {
	return "To https://user:secret@host/x?token=y'z&k=secret\n!\t0123abcd:refs/klaus/data\t" + summary + "\nDone\n"
}

func TestParsePushError(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr, class, message string
	}{
		{"refused, other lines dropped", porcelain("[remote rejected] (pre-receive hook declined)"), secretPushStderr, PushRefused,
			"remote: policy denies refs/klaus/data\nremote: [31msee the policy[0m\n[remote rejected] (pre-receive hook declined)"},
		{"an address in the reason", porcelain("[remote rejected] (permission denied for https://user:secret@example.org/x?token=secret)"), "", PushRefused,
			"[remote rejected] " + PushWithheld},
		{"an address in a remote line", "", "remote: mirror operator@example.org:secret refused\nremote: try later\n", PushOther,
			PushWithheld + "\nremote: try later"},
		{"a destination path made to look like a remote line",
			"", "fatal: '/srv/x\nremote: see user:secret@example.org/x?token=secret' does not appear to be a git repository\n" +
				"fatal: Could not read from remote repository.\n", PushNetwork, PushWithheld},
		{"a destination that forges a status line for another ref",
			"To \n!\tx:y\t[https://example.org/x?token=secret]\n\n!\t0123abcd:refs/klaus/data\t[remote rejected] (pre-receive hook declined)\nDone\n",
			"", PushRefused, "[remote rejected] (pre-receive hook declined)"},
		{"an address in the status", porcelain("[https://example.org/x?token=secret]"), "", PushRefused, PushWithheld},
		{"status lines come from stdout only", "", " ! [remote rejected] refs/klaus/data -> refs/klaus/data (declined)\n", PushOther, ""},
		{"not a fast-forward", porcelain("[rejected] (non-fast-forward)"), "hint: Updates were rejected\n", PushNotFastForward,
			"[rejected] (non-fast-forward)"},
		{"fetch first", porcelain("[rejected] (fetch first)"), "", PushNotFastForward, "[rejected] (fetch first)"},
		{"no report from the remote", porcelain("[remote failure] (remote failed to report status)"), "", PushUnconfirmed,
			"[remote failure] (remote failed to report status)"},
		{"authentication", "", "remote: Invalid username or password.\nfatal: Authentication failed for 'https://user:secret@host/x/'\n", PushAuthentication,
			"remote: Invalid username or password."},
		{"ssh key refused", "", "git@example.org: Permission denied (publickey).\nfatal: Could not read from remote repository.\n", PushAuthentication, ""},
		{"network", "", "fatal: unable to access 'https://user:secret@host/x?token=y/': Could not resolve host: host\n", PushNetwork, ""},
		{"other", "", "fatal: 'origin' does not appear to be a git repository\n", PushOther, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParsePushError("refs/klaus/data", tc.stdout, tc.stderr)
			if got.Class != tc.class || got.Message != tc.message {
				t.Errorf("ParsePushError = {%q, %q}, want {%q, %q}", got.Class, got.Message, tc.class, tc.message)
			}
			for _, leak := range []string{"secret", "token", "user:", "ext::", "https://", "example.org"} {
				if strings.Contains(got.Error(), leak) {
					t.Errorf("Error() = %q holds %q", got.Error(), leak)
				}
			}
		})
	}
}

// TestParsePushErrorBounded: a remote that prints megabytes leaves a message
// of at most MaxPushMessage bytes that ends in the cut marker.
func TestParsePushErrorBounded(t *testing.T) {
	stderr := "remote: START " + strings.Repeat("é", MaxPushMessage) + "\n"
	got := ParsePushError("refs/klaus/data", porcelain("[remote rejected] (pre-receive hook declined)"), stderr)
	if len(got.Message) > MaxPushMessage || !strings.HasPrefix(got.Message, "remote: START") || !strings.HasSuffix(got.Message, "\n"+PushMessageCut) {
		t.Errorf("message of %d bytes = %.40q...%q", len(got.Message), got.Message, got.Message[max(len(got.Message)-20, 0):])
	}
	if !utf8.ValidString(got.Message) {
		t.Errorf("message was not cut at a character boundary")
	}
	if got.Class != PushRefused {
		t.Errorf("class = %q, want %q from the status past the cut", got.Class, PushRefused)
	}
}

// TestPushDataRefRefused: a receive hook's refusal reaches the caller as a
// PushError with the hook's own words.
func TestPushDataRefRefused(t *testing.T) {
	ctx := context.Background()
	repo := initTestRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	if _, err := runGit(ctx, "", "init", "--bare", origin); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, repo, "remote", "add", "origin", origin); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\necho 'policy denies the data ref' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	const ref = "refs/klaus/data"
	if err := EnsureDataRef(ctx, repo, ref); err != nil {
		t.Fatal(err)
	}
	var pushErr *PushError
	if pushed, err := PushDataRef(ctx, repo, ref); !errors.As(err, &pushErr) || pushed != "" {
		t.Fatalf("PushDataRef = %q, %v, want a *PushError", pushed, err)
	}
	if pushErr.Class != PushRefused || pushErr.Message != "remote: policy denies the data ref\n[remote rejected] (pre-receive hook declined)" {
		t.Errorf("PushError = %+v, want refused with the hook's line and the ref's status", pushErr)
	}

	if err := os.Remove(filepath.Join(origin, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
	pushed, err := PushDataRef(ctx, repo, ref)
	if tip, _ := runGit(ctx, origin, "rev-parse", ref); err != nil || pushed == "" || pushed != tip {
		t.Errorf("PushDataRef = %q, %v, want origin's new tip %q", pushed, err, tip)
	}
}

// TestSyncToDataRefGivesUp: a data ref another writer moves during every
// attempt fails the sync after dataRefAttempts builds, and the other writer's
// commits stay on the ref.
func TestSyncToDataRefGivesUp(t *testing.T) {
	ctx := context.Background()
	repo := initTestRepo(t)
	const ref = "refs/klaus/data"
	if err := EnsureDataRef(ctx, repo, ref); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	moves := 0
	t.Cleanup(SetBeforeDataRefUpdate(func() {
		tip, _ := runGit(ctx, repo, "rev-parse", ref)
		other, err := runGitStdin(ctx, repo, "other writer", "commit-tree", tip+"^{tree}", "-p", tip)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runGit(ctx, repo, "update-ref", ref, other); err != nil {
			t.Fatal(err)
		}
		moves++
	}))

	if commit, err := SyncToDataRef(ctx, repo, ref, "Run a", map[string]string{"runs/a.json": file}); err == nil {
		t.Fatalf("SyncToDataRef = %q, want an error while the ref keeps moving", commit)
	}
	if moves != dataRefAttempts {
		t.Errorf("built %d times, want %d", moves, dataRefAttempts)
	}
	if n, _ := runGit(ctx, repo, "rev-list", "--count", ref); n != fmt.Sprint(dataRefAttempts+1) {
		t.Errorf("%s holds %s commits, want the initial one and the other writer's %d", ref, n, dataRefAttempts)
	}
}

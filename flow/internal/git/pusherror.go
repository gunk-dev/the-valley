package git

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Classes of a failed push.
const (
	PushRefused        = "refused"          // the remote, such as its receive hook, declined the ref
	PushAuthentication = "authentication"   // the remote did not accept the credentials
	PushNetwork        = "network"          // the remote could not be reached, or the connection broke
	PushNotFastForward = "not_fast_forward" // the remote's ref is not an ancestor of the pushed commit
	PushUnconfirmed    = "unconfirmed"      // the remote did not report the ref's status, so it may have updated it
	PushOther          = "other"
)

// MaxPushMessage bounds a PushError's Message, cut marker included, so a
// remote that prints megabytes stays far below the event log reader's line
// limit.
const MaxPushMessage = 4096

// PushMessageCut ends a Message cut at MaxPushMessage.
const PushMessageCut = "[truncated]"

// PushWithheld stands, whole, for a kept remote line, status or reason that
// contains "://" or "@". Such text can carry an address with credentials, and
// no part of it is kept, since cutting an address out reliably needs a URL
// parser.
const PushWithheld = "[withheld: it contains an address]"

// PushError is a failed push. Message holds the lines the remote sent
// ("remote: ...") on git's stderr, then the pushed ref's status as git push
// --porcelain reports it on stdout ("[remote rejected] (reason)"), without
// control characters and with PushWithheld for any line, status or reason
// holding an address. Every other line of git's output is dropped, because
// git echoes the remote's URL, credentials and all, in its own lines.
type PushError struct {
	Class   string
	Message string
}

func (e *PushError) Error() string {
	msg := "push failed (" + PushClassText(e.Class) + ")"
	if e.Message != "" {
		msg += ": " + strings.ReplaceAll(e.Message, "\n", "; ")
	}
	return msg
}

// PushClassText describes a push failure class in a few words.
func PushClassText(class string) string {
	switch class {
	case PushRefused:
		return "refused by the remote"
	case PushAuthentication:
		return "authentication"
	case PushNetwork:
		return "network"
	case PushNotFastForward:
		return "not a fast-forward"
	case PushUnconfirmed:
		return "unconfirmed by the remote"
	}
	return "other"
}

// ParsePushError describes a failed git push --porcelain of ref from its
// stdout and stderr.
func ParsePushError(ref, stdout, stderr string) *PushError {
	var kept []string
	for _, line := range pushLines(stderr) {
		if strings.HasPrefix(line, "remote:") {
			kept = append(kept, keptLine(line))
		}
	}
	class := ""
	for _, line := range strings.Split(stdout, "\n") {
		// A porcelain ref line is <flag> TAB <from>:<to> TAB <summary>, and a
		// failed ref's flag is "!". Only the line for ref counts: the
		// destination git prints before it can hold a newline and so forge a
		// line of its own.
		flag, rest, _ := strings.Cut(line, "\t")
		refs, summary, ok := strings.Cut(rest, "\t")
		_, to, _ := strings.Cut(refs, ":")
		if flag != "!" || !ok || to != ref {
			continue
		}
		status, reason := splitPushSummary(strings.TrimSpace(strings.Map(dropControl, summary)))
		if class == "" {
			class = statusClass(status, reason)
		}
		if reason == "" {
			kept = append(kept, keptLine(status))
		} else {
			kept = append(kept, keptLine(status, "("+reason+")"))
		}
	}
	if class == "" {
		class = connectionClass(stderr)
	}
	return &PushError{Class: class, Message: boundPushMessage(strings.Join(kept, "\n"))}
}

// pushLines splits git's stderr into lines without control characters.
func pushLines(s string) []string {
	var lines []string
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		lines = append(lines, strings.TrimSpace(strings.Map(dropControl, raw)))
	}
	return lines
}

// splitPushSummary splits a porcelain summary such as "[remote rejected]
// (pre-receive hook declined)" into its status and the reason in parentheses.
func splitPushSummary(summary string) (status, reason string) {
	end := strings.Index(summary, "]")
	if !strings.HasPrefix(summary, "[") || end < 0 {
		return summary, ""
	}
	reason = strings.TrimSpace(summary[end+1:])
	reason = strings.TrimSuffix(strings.TrimPrefix(reason, "("), ")")
	return summary[:end+1], reason
}

// statusClass classes a failed ref from its porcelain status and reason.
func statusClass(status, reason string) string {
	switch {
	case status == "[rejected]" && (reason == "non-fast-forward" || reason == "fetch first"):
		return PushNotFastForward
	case status == "[remote failure]":
		return PushUnconfirmed
	}
	return PushRefused
}

// connectionClass classes a push that failed before any ref's status, from
// what git and the remote said about the connection.
func connectionClass(stderr string) string {
	lower := strings.ToLower(stderr)
	for _, s := range []string{"authentication failed", "permission denied", "could not read username",
		"could not read password", "terminal prompts disabled", "invalid username or password",
		"returned error: 401", "returned error: 403"} {
		if strings.Contains(lower, s) {
			return PushAuthentication
		}
	}
	for _, s := range []string{"could not resolve host", "connection refused", "connection timed out",
		"connection reset", "network is unreachable", "no route to host", "failed to connect",
		"the remote end hung up", "could not read from remote repository", "unable to access", "early eof"} {
		if strings.Contains(lower, s) {
			return PushNetwork
		}
	}
	return PushOther
}

// keptLine joins the fields of a line Message keeps, each replaced whole by
// PushWithheld when it contains an address. Every kept line is built here.
func keptLine(fields ...string) string {
	for i, f := range fields {
		if strings.Contains(f, "://") || strings.Contains(f, "@") {
			fields[i] = PushWithheld
		}
	}
	return strings.Join(fields, " ")
}

func dropControl(r rune) rune {
	if r == '\t' {
		return ' '
	}
	if unicode.IsControl(r) {
		return -1
	}
	return r
}

func boundPushMessage(s string) string {
	if len(s) <= MaxPushMessage {
		return s
	}
	cut := MaxPushMessage - len(PushMessageCut) - 1
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n" + PushMessageCut
}

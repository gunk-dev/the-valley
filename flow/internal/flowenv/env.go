// Package flowenv reads flow environment variables under either their
// VALLEY_FLOW_* or KLAUS_* spellings, and sets them for the processes flow
// starts under the KLAUS_* spelling alone. No other flow code spells their
// names.
package flowenv

import (
	"os"
	"strings"
)

// Var identifies a flow environment variable by its two accepted names.
type Var struct {
	New    string
	Legacy string
}

// Accepted flow environment variables.
var (
	SessionID    = Var{New: "VALLEY_FLOW_SESSION_ID", Legacy: "KLAUS_SESSION_ID"}
	RunID        = Var{New: "VALLEY_FLOW_RUN_ID", Legacy: "KLAUS_RUN_ID"}
	RunEvents    = Var{New: "VALLEY_FLOW_RUN_EVENTS", Legacy: "KLAUS_RUN_EVENTS"}
	Backend      = Var{New: "VALLEY_FLOW_BACKEND", Legacy: "KLAUS_BACKEND"}
	AgentBackend = Var{New: "VALLEY_FLOW_AGENT_BACKEND", Legacy: "KLAUS_AGENT_BACKEND"}
	Scratch      = Var{New: "VALLEY_FLOW_SCRATCH", Legacy: "KLAUS_SCRATCH"}
	TmuxSocket   = Var{New: "VALLEY_FLOW_TMUX_SOCKET", Legacy: "KLAUS_TMUX_SOCKET"}
)

// All lists every flow environment variable that accepts both spellings.
var All = []Var{
	SessionID,
	RunID,
	RunEvents,
	Backend,
	AgentBackend,
	Scratch,
	TmuxSocket,
}

// Lookup returns the value of newName when set (even to empty), otherwise
// legacyName, and reports whether either was set.
func Lookup(newName, legacyName string) (string, bool) {
	if v, ok := os.LookupEnv(newName); ok {
		return v, true
	}
	return os.LookupEnv(legacyName)
}

// Get returns the value of newName when set (even to empty), otherwise
// legacyName (or "" when neither is set).
func Get(newName, legacyName string) string {
	v, _ := Lookup(newName, legacyName)
	return v
}

// Lookup returns v's value and whether either spelling is set.
func (v Var) Lookup() (string, bool) { return Lookup(v.New, v.Legacy) }

// Get returns v's value: New when set (even to empty), otherwise Legacy.
func (v Var) Get() string { return Get(v.New, v.Legacy) }

// Set returns env, a child process's environment, with v set to value under
// its legacy name, which every reader accepts, and without its new name,
// which readers prefer and so would override value.
func (v Var) Set(env []string, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name != v.New && name != v.Legacy {
			out = append(out, kv)
		}
	}
	return append(out, v.Legacy+"="+value)
}

// Export returns POSIX shell statements that set v to value, as Set does, for
// the commands after them.
func (v Var) Export(value string) string {
	return "unset " + v.New + "; export " + v.Legacy + "=" + shellQuote(value) + "; "
}

// EnvPrefix returns an env(1) prefix that runs the command after it with v
// set to value, as Set does.
func (v Var) EnvPrefix(value string) string {
	return "env -u " + v.New + " " + v.Legacy + "=" + shellQuote(value) + " "
}

// UnsetPrefix returns an env(1) prefix that runs the command after it with
// neither spelling of v.
func (v Var) UnsetPrefix() string {
	return "env -u " + v.New + " -u " + v.Legacy + " "
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

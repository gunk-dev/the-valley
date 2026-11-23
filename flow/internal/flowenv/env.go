// Package flowenv reads flow environment variables under either their
// VALLEY_FLOW_* or KLAUS_* spellings, and sets them for the processes flow
// starts by the host's naming generation (location.Writes): under legacy,
// the KLAUS_* spelling alone; under bridge, both spellings with one value,
// so a child of any build reads the value flow resolved. No other flow code
// spells their names.
package flowenv

import (
	"os"
	"slices"
	"strings"

	"the-valley/flow/internal/location"
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

// bridge reports whether the host's generation gives children both
// spellings.
func bridge() bool { return location.Writes() != location.Legacy }

// Set returns env, a child process's environment, with v set to value.
// Under the legacy generation that is its legacy name alone, which every
// reader accepts, without its new name, which readers prefer and so would
// override value; under bridge it is both names.
func (v Var) Set(env []string, value string) []string { return v.set(bridge(), env, value) }

func (v Var) set(both bool, env []string, value string) []string {
	out := v.without(env)
	if both {
		out = append(out, v.New+"="+value)
	}
	return append(out, v.Legacy+"="+value)
}

// without is env with neither of v's names.
func (v Var) without(env []string) []string {
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name != v.New && name != v.Legacy {
			out = append(out, kv)
		}
	}
	return out
}

// Export returns POSIX shell statements that set v to value, as Set does, for
// the commands after them.
func (v Var) Export(value string) string { return v.export(bridge(), value) }

func (v Var) export(both bool, value string) string {
	if both {
		return "export " + v.New + "=" + shellQuote(value) + " " + v.Legacy + "=" + shellQuote(value) + "; "
	}
	return "unset " + v.New + "; export " + v.Legacy + "=" + shellQuote(value) + "; "
}

// EnvPrefix returns an env(1) prefix that runs the command after it with v
// set to value, as Set does.
func (v Var) EnvPrefix(value string) string { return v.envPrefix(bridge(), value) }

func (v Var) envPrefix(both bool, value string) string {
	if both {
		return "env " + v.New + "=" + shellQuote(value) + " " + v.Legacy + "=" + shellQuote(value) + " "
	}
	return "env -u " + v.New + " " + v.Legacy + "=" + shellQuote(value) + " "
}

// UnsetPrefix returns an env(1) prefix that runs the command after it with
// neither spelling of v.
func (v Var) UnsetPrefix() string {
	return "env -u " + v.New + " -u " + v.Legacy + " "
}

// Name is the spelling instructions a child reads name v by: the legacy
// one, or the new one under bridge, where children get both.
func (v Var) Name() string {
	if bridge() {
		return v.New
	}
	return v.Legacy
}

// Normalize returns env, a child process's environment, with each variable
// of All as the child would resolve it: under bridge, a variable set under
// either name (even to empty) is set under both to the value Lookup would
// give, and one under neither stays under neither; under legacy, env as it
// is. As for exec.Cmd, a name's last entry in env is its value.
func Normalize(env []string) []string { return normalize(bridge(), env) }

func normalize(both bool, env []string) []string {
	if !both {
		return env
	}
	for _, v := range All {
		value, ok := lookupIn(env, v.New)
		if !ok {
			value, ok = lookupIn(env, v.Legacy)
		}
		if ok {
			env = v.set(true, env, value)
		} else {
			env = v.without(env)
		}
	}
	return env
}

func lookupIn(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// NormalizePrefix returns POSIX shell statements that give the commands
// after them each variable of All as this process resolves it (Lookup),
// under both names, or under neither when this process has it under
// neither: under bridge, for a pane, whose shell starts from the tmux
// server's environment rather than flow's. Under legacy it is empty.
func NormalizePrefix() string { return normalizePrefix(bridge()) }

func normalizePrefix(both bool) string {
	if !both {
		return ""
	}
	var b strings.Builder
	for _, v := range All {
		if value, ok := v.Lookup(); ok {
			b.WriteString(v.export(true, value))
		} else {
			b.WriteString(v.unset())
		}
	}
	return b.String()
}

// ClearPrefix returns POSIX shell statements that unset both names of each
// variable of All except keep, which the commands after them set
// themselves: under bridge, for a command on another host, whose shell
// starts from that host's environment and not flow's. Under legacy it is
// empty.
func ClearPrefix(keep ...Var) string { return clearPrefix(bridge(), keep) }

func clearPrefix(both bool, keep []Var) string {
	if !both {
		return ""
	}
	var b strings.Builder
	for _, v := range All {
		if !slices.Contains(keep, v) {
			b.WriteString(v.unset())
		}
	}
	return b.String()
}

// unset is the POSIX shell statement that unsets both of v's names.
func (v Var) unset() string { return "unset " + v.New + " " + v.Legacy + "; " }

// NormalizeProcess sets each variable of All in this process's environment
// under both names to the value Lookup gives, under bridge, so every child
// it starts inherits one value under both. Under legacy it changes nothing.
func NormalizeProcess() {
	if !bridge() {
		return
	}
	for _, v := range All {
		if value, ok := v.Lookup(); ok {
			_ = os.Setenv(v.New, value)
			_ = os.Setenv(v.Legacy, value)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

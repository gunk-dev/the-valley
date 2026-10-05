// The host the flake's mirror-e2e check drives. The URLs are relative on
// purpose: git resolves a relative URL against the pushing repository's
// directory, so the check can serve one of them from a sibling directory and
// leave the other pointing at nothing. A dead mirror must never reject the
// primary push, and this declaration is how that is exercised for real.
//
// The race mirror is an ssh URL. The check points the race repository's
// core.sshCommand at a stand-in that runs the remote git locally, so it can
// stall a push where git has read its local refs and not yet the mirror's:
// the window in which two pushers at once can rewind a mirror.
package valley

projects: {
	"mirror-pilot": mirrors: ["../mirror.git"]
	"dead-mirror": mirrors: ["../nope.git"]
	"race": mirrors: ["race-host:../race-mirror.git"]
}

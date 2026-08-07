// The host the flake's mirror-e2e check drives. The URLs are relative on
// purpose: git resolves a relative URL against the pushing repository's
// directory, so the check can serve one of them from a sibling directory and
// leave the other pointing at nothing. A dead mirror must never reject the
// primary push, and this declaration is how that is exercised for real.
package valley

projects: {
	"mirror-pilot": mirrors: ["../mirror.git"]
	"dead-mirror": mirrors: ["../nope.git"]
}

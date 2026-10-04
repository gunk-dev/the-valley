package main

import (
	"slices"
	"testing"
)

// The unit hands every controller its safe.directory exceptions as
// command-scope configuration, and git reads those entries only up to the
// count. Dropping everything else means renumbering what is kept, and an
// entry left past the count would be one git silently ignores.
func TestOnlySafeDirectoryConfigurationIsInheritedAndReplacementRefsAreOff(t *testing.T) {
	got := gitEnvironment([]string{
		"PATH=/bin",
		"GIT_DIR=/elsewhere",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.useReplaceRefs",
		"GIT_CONFIG_VALUE_0=true",
		"GIT_CONFIG_KEY_1=safe.directory",
		"GIT_CONFIG_VALUE_1=/srv/git/project.git",
		"GIT_CONFIG_KEY_2=safe.directory",
		"GIT_CONFIG_VALUE_2=/srv/git/instance.git",
		"GIT_CONFIG_PARAMETERS='core.usereplacerefs'='true'",
	})
	for _, want := range []string{
		"PATH=/bin",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=/srv/git/project.git",
		"GIT_CONFIG_KEY_1=safe.directory",
		"GIT_CONFIG_VALUE_1=/srv/git/instance.git",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("environment lacks %s: %q", want, got)
		}
	}
	for _, kv := range got {
		switch kv {
		case "GIT_DIR=/elsewhere", "GIT_CONFIG_VALUE_0=true", "GIT_CONFIG_PARAMETERS='core.usereplacerefs'='true'":
			t.Errorf("environment kept %s", kv)
		}
	}
}

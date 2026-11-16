package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"the-valley/composed"
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

// The approval inputs are paths, taken without opening them: a file in a
// directory that does not exist is taken, a relative path is taken
// absolute, and none is required. The two signers files are two files.
func TestTheApprovalInputsAreTakenAsPaths(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent", "approval-signers")
	a, err := newApprovalInputs(missing, "approval-signers-hardware", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.signers != missing || !filepath.IsAbs(a.hardware) || filepath.Base(a.hardware) != "approval-signers-hardware" || a.pushPolicy != "" {
		t.Errorf("the inputs were taken as %+v", a)
	}
	if a, err := newApprovalInputs("", "", ""); err != nil || a != (approvalInputs{}) {
		t.Errorf("no input was taken as %+v, %v", a, err)
	}
	if a, err := newApprovalInputs(missing, missing, ""); err == nil {
		t.Errorf("one file as both signers files was taken as %+v", a)
	}
}

// The approval inputs change no pass. A pass with the three paths given
// and a pass with none read the same inputs digest, refuse the same
// request with the same report, and leave the same outcome and inputs
// records.
func TestTheApprovalInputsChangeNoPass(t *testing.T) {
	s, r := refusedRequest(t)
	floor := t.TempDir()
	if err := os.WriteFile(filepath.Join(floor, "floor.cue"), []byte("package verification\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	known := filepath.Join(t.TempDir(), "known-signers")
	if err := os.WriteFile(known, []byte(verifierLine("valley-host/attestations", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	given, err := newApprovalInputs(filepath.Join(t.TempDir(), "approval-signers"),
		filepath.Join(t.TempDir(), "approval-signers-hardware"), filepath.Join(t.TempDir(), "push-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	type pass struct{ digest, report, memo, inputs string }
	run := func(a approvalInputs) pass {
		t.Helper()
		var out strings.Builder
		in := &integrator{
			repo: s.repo, project: "project", out: &out, signerName: "valley-host/attestations",
			knownSigners: known, policy: policySource{layer: composed.Floor{Path: floor}}, approval: a,
		}
		state, done := in.inputState()
		done()
		if err := in.reconcile(reasonOnce); err != nil {
			t.Fatal(err)
		}
		memo, err := in.readMemo(r)
		if err != nil {
			t.Fatal(err)
		}
		p := pass{state.digest, out.String(), memo, in.readInputs(r)}
		// The next pass starts from no record, as this one did.
		s.git("update-ref", "-d", in.memoRef(r))
		s.git("update-ref", "-d", in.inputsRef(r))
		return p
	}
	without, with := run(approvalInputs{}), run(given)
	if without != with {
		t.Errorf("a pass without the approval inputs gave\n%+v\nand one with them\n%+v", without, with)
	}
	if !strings.Contains(without.report, string(refusedNotReady)) || without.memo == "" || without.inputs != without.digest {
		t.Errorf("the pass refused nothing, so this test proves nothing: %+v", without)
	}
}

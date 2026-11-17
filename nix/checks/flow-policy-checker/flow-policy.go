// Command flow-policy holds the project policy's classification of the
// paths around klaus (flow/): what a change to one path requires, as `valley
// checks --names` derives it from the policy composed over a floor.
//
// It lays the policy out as an instance and a project layer, seeds one git
// repository with every path below, and for each path commits a change to
// it alone and compares the checks the change requires with what the path
// must, or must not, require. Any mismatch is printed and exits 1.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

var flowChecks = []string{"flow-build", "flow-e2e", "flow-unit", "flow-vet"}

// policyChecks are what a change to the policy requires: its own
// verification.
var policyChecks = []string{"cue-vet", "derive-unit", "own-policy", "policy-deriver"}

type scenario struct {
	path     string
	exactly  []string // when set, the change requires these and nothing else
	requires []string
	excludes []string
}

func scenarios() []scenario {
	s := []scenario{
		// klaus's Go, module files and fixtures: its four checks, and the
		// dependency boundary for what is Go.
		{path: "flow/internal/cmd/root.go", exactly: append([]string{"go-dependencies"}, flowChecks...)},
		{path: "flow/go.mod", exactly: append([]string{"go-dependencies"}, flowChecks...)},
		{path: "flow/go.sum", exactly: append([]string{"go-dependencies"}, flowChecks...)},
		{path: "flow/internal/scan/testdata/log.txt", exactly: flowChecks},
		// klaus's prose, which none of them reads: the floor's prose check
		// alone.
		{path: "flow/docs/guide.md", exactly: []string{"prose-format"}},
		{path: "flow/README.md", exactly: []string{"prose-format"}},
		// Their definition, and the packaging and inputs the checks share,
		// which this check reads too, and which keep the checks of their
		// other classes.
		{path: "nix/checks/flow.nix", requires: flowChecks, excludes: []string{"flow-policy"}},
		{path: "nix/packages.nix", requires: append([]string{"flow-policy", "valley-darwin-eval", "web-reader-browser-e2e", "web-push-e2e"}, flowChecks...)},
		{path: "nix/checks.nix", requires: append([]string{"flow-policy", "module-eval"}, flowChecks...)},
		{path: "flake.nix", requires: append([]string{"flow-policy", "module-eval"}, flowChecks...)},
		{path: "flake.lock", requires: append([]string{"flow-policy", "module-eval"}, flowChecks...)},
		// The checks' helper: the three checks that run it, not go vet.
		{path: "nix/checks/flow-checker/flow-checker.go", requires: []string{"flow-build", "flow-unit", "flow-e2e"}, excludes: []string{"flow-vet"}},
		// An unrelated module requires none of them.
		{path: "sigverify/keys.go", requires: []string{"sigverify-unit"}, excludes: flowChecks},
		// The policy requires its own verification, and this check, which
		// also reads the example floor and docs template, and no other
		// example floor.
		{path: "policy/project/policy.cue", requires: append([]string{"flow-policy"}, policyChecks...)},
		{path: "examples/policy/instance/floor-format.cue", requires: []string{"flow-policy"}},
		{path: "examples/policy/instance/floor-other.cue", excludes: []string{"flow-policy"}},
		// This check's program requires this check.
		{path: "nix/checks/flow-policy-checker/flow-policy.go", requires: []string{"flow-policy"}},
		{path: "nix/checks/flow-policy.nix", requires: []string{"flow-policy"}},
	}
	// The valley command the end-to-end suite runs and this check derives
	// with: its own source, the modules it builds with, its help texts, the
	// PIV signer, the delta vectors, the attestation helper on its PATH, and
	// the schemas they carry. Each requires flow-e2e and flow-policy, and
	// none of klaus's other checks.
	for _, p := range []string{
		"status/command.go",
		"status/help/usage.txt",
		"approval/approval.go",
		"approval/piv/valley-piv-signer.c",
		"attest/main.go",
		"change/change.go",
		"composed/composed.go",
		"delta/delta.go",
		"delta/testdata/vectors.py",
		"delta/testdata/valid/empty.delta",
		"derive/derive.go",
		"knot/knot.go",
		"knot/cmd/knotcheck/main.go",
		"note/note.go",
		"note/statement/statement.go",
		"rebase/rebase.go",
		"selfupdate/selfupdate.go",
		"telemetry/telemetry.go",
		"telemetry/valley-telemetry/main.go",
		"schema/verification.cue",
		"schema/attestation.cue",
	} {
		s = append(s, scenario{path: p, requires: []string{"flow-e2e", "flow-policy"}, excludes: []string{"flow-build", "flow-unit", "flow-vet"}})
	}
	// Files beside them that the valley command's build leaves out: the
	// approval command and its fixtures, the plan's golden files, the
	// attestation vectors, and prose.
	for _, p := range []string{
		"approval/cmd/approval/main.go",
		"approval/testdata/statement.json",
		"status/testdata/plan/one.txt",
		"attest/conformance/vectors.json",
		"delta/testdata/README.md",
		"status/notes.md",
	} {
		s = append(s, scenario{path: p, excludes: []string{"flow-e2e", "flow-policy"}})
	}
	return s
}

func main() {
	schema := flag.String("schema", "", "the verification schema")
	floor := flag.String("floor", "", "the instance floor")
	template := flag.String("template", "", "an instance template composed beside the floor")
	project := flag.String("project", "", "the project layer's directory")
	flag.Parse()
	failed, err := run(*schema, *floor, *template, *project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flow-policy:", err)
		os.Exit(2)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "flow-policy: %d paths classified wrongly\n", failed)
		os.Exit(1)
	}
}

func run(schema, floor, template, project string) (int, error) {
	tmp, err := os.MkdirTemp("", "flow-policy")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	instance, layer := filepath.Join(tmp, "policy", "instance"), filepath.Join(tmp, "policy", "project")
	for _, c := range []struct{ src, dst string }{{floor, filepath.Join(instance, "floor.cue")}, {template, filepath.Join(instance, "template.cue")}} {
		if err := copyFile(c.src, c.dst); err != nil {
			return 0, err
		}
	}
	cues, err := filepath.Glob(filepath.Join(project, "*.cue"))
	if err != nil || len(cues) == 0 {
		return 0, fmt.Errorf("no project layer in %s", project)
	}
	for _, c := range cues {
		if err := copyFile(c, filepath.Join(layer, filepath.Base(c))); err != nil {
			return 0, err
		}
	}

	repo := filepath.Join(tmp, "tree")
	env := append(os.Environ(), "HOME="+tmp, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=valley-check", "GIT_AUTHOR_EMAIL=valley-check@localhost",
		"GIT_COMMITTER_NAME=valley-check", "GIT_COMMITTER_EMAIL=valley-check@localhost")
	run := func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = repo, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out), nil
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return 0, err
	}
	if _, err := run("git", "init", "-q", "-b", "main"); err != nil {
		return 0, err
	}
	all := scenarios()
	for _, s := range all {
		if err := write(filepath.Join(repo, s.path), "seed\n"); err != nil {
			return 0, err
		}
	}
	if _, err := run("git", "add", "-A"); err != nil {
		return 0, err
	}
	if _, err := run("git", "commit", "-q", "-m", "base"); err != nil {
		return 0, err
	}

	failed := 0
	for _, s := range all {
		if _, err := run("git", "checkout", "-q", "-B", "change", "main"); err != nil {
			return 0, err
		}
		if err := write(filepath.Join(repo, s.path), "edit\n"); err != nil {
			return 0, err
		}
		if _, err := run("git", "commit", "-q", "-am", s.path); err != nil {
			return 0, err
		}
		out, err := run("valley", "checks", "--schema", schema, "--instance", instance, "--project", layer, "--names", "main", "HEAD")
		if err != nil {
			return 0, err
		}
		got := strings.Fields(out)
		slices.Sort(got)
		if problem := s.judge(got); problem != "" {
			fmt.Printf("✗ %s: %s; requires %s\n", s.path, problem, strings.Join(got, " "))
			failed++
			continue
		}
		fmt.Printf("✓ %s: %s\n", s.path, strings.Join(got, " "))
	}
	return failed, nil
}

// judge says how got, the sorted checks a change to s.path requires,
// differs from what s demands, or "".
func (s scenario) judge(got []string) string {
	if s.exactly != nil {
		want := slices.Clone(s.exactly)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			return "want exactly " + strings.Join(want, " ")
		}
	}
	var missing, extra []string
	for _, c := range s.requires {
		if !slices.Contains(got, c) {
			missing = append(missing, c)
		}
	}
	for _, c := range s.excludes {
		if slices.Contains(got, c) {
			extra = append(extra, c)
		}
	}
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "missing "+strings.Join(missing, " "))
	}
	if len(extra) > 0 {
		problems = append(problems, "must not require "+strings.Join(extra, " "))
	}
	return strings.Join(problems, "; ")
}

func write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return write(dst, string(b))
}

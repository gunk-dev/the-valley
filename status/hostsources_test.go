package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// hostLines prints nothing for healthy sources, one host entry with its
// collector reason and see command for a single-unit failure or non-unit
// state, each failing unit under the source when several fail, and caps
// the unit list at maxHostUnits with "and N more".
func TestHostLinesRendering(t *testing.T) {
	plain := terminal{width: 80}

	if got := plain.hostLines(nil); len(got) != 0 {
		t.Errorf("healthy hostLines = %q, want empty", got)
	}

	one := plain.hostLines([]jsonHostSource{{
		Source: "mirrors",
		State:  "failed",
		Reason: "valley-publish@app.service: activating, result exit-code, status 1",
		Units:  []string{"valley-publish@app.service"},
	}})
	wantOne := strings.Join([]string{
		"host  mirrors  failed · valley-publish@app.service: activating, result exit-code, status 1",
		"      see: valley telemetry read --source mirrors --logs",
	}, "\n")
	if got := strings.Join(one, "\n"); got != wantOne {
		t.Errorf("one failed source:\n got:\n%s\nwant:\n%s", got, wantOne)
	}

	several := plain.hostLines([]jsonHostSource{{
		Source: "mirrors",
		State:  "failed",
		Reason: "valley-publish@app.service: activating, result exit-code, status 1",
		Units: []string{
			"valley-publish@app.service",
			"valley-publish@docs.service",
			"valley-publish@site.service",
		},
	}})
	wantSeveral := strings.Join([]string{
		"host  mirrors  failed · valley-publish@app.service: activating, result exit-code, status 1",
		"      valley-publish@app.service",
		"      valley-publish@docs.service",
		"      valley-publish@site.service",
		"      see: valley telemetry read --source mirrors --logs",
	}, "\n")
	if got := strings.Join(several, "\n"); got != wantSeveral {
		t.Errorf("several failing units:\n got:\n%s\nwant:\n%s", got, wantSeveral)
	}

	capped := plain.hostLines([]jsonHostSource{{
		Source: "mirrors",
		State:  "failed",
		Reason: "valley-publish@api.service: failed, result exit-code, status 1",
		Units: []string{
			"valley-publish@api.service",
			"valley-publish@app.service",
			"valley-publish@blog.service",
			"valley-publish@docs.service",
			"valley-publish@site.service",
			"valley-publish@tools.service",
			"valley-publish@web.service",
		},
	}})
	wantCapped := strings.Join([]string{
		"host  mirrors  failed · valley-publish@api.service: failed, result exit-code, status 1",
		"      valley-publish@api.service",
		"      valley-publish@app.service",
		"      valley-publish@blog.service",
		"      valley-publish@docs.service",
		"      valley-publish@site.service",
		"      and 2 more",
		"      see: valley telemetry read --source mirrors --logs",
	}, "\n")
	if got := strings.Join(capped, "\n"); got != wantCapped {
		t.Errorf("capped failing units:\n got:\n%s\nwant:\n%s", got, wantCapped)
	}

	nonUnit := plain.hostLines([]jsonHostSource{
		{
			Source: "worker",
			State:  "stale",
			Reason: "run p1 recorded no end and no unit of the source is running: it was interrupted",
			Units:  []string{"valley-worker.service"},
		},
		{
			Source: "remote-receiver",
			State:  "unavailable",
			Reason: "receiver has reported nothing: this host has no observation of the source",
			Units:  []string{},
		},
	})
	wantNonUnit := strings.Join([]string{
		"host  worker  stale · run p1 recorded no end and no unit of the source is running: it was interrupted",
		"      see: valley telemetry read --source worker --logs",
		"host  remote-receiver  unavailable · receiver has reported nothing: this host has no observation of the source",
		"      see: valley telemetry read --source remote-receiver --logs",
	}, "\n")
	if got := strings.Join(nonUnit, "\n"); got != wantNonUnit {
		t.Errorf("stale and unavailable:\n got:\n%s\nwant:\n%s", got, wantNonUnit)
	}

	// Styled terminal highlights source name in bold and state in colour.
	styled := terminal{width: 80, bold: true}.hostLines([]jsonHostSource{{
		Source: "mirrors",
		State:  "failed",
		Reason: "valley-publish@app.service: failed, result exit-code, status 1",
		Units:  []string{"valley-publish@app.service"},
	}})
	if !strings.Contains(styled[0], "\x1b[1mmirrors\x1b[0m") || !strings.Contains(styled[0], "\x1b[31mfailed\x1b[0m") {
		t.Errorf("styled host line = %q", styled[0])
	}
}

// parseHostSources keeps only failed, stale and unavailable sources,
// extracts failing units by the collector's rule, sanitizes control
// characters, and rejects errors or truncated answers.
func TestParseHostSources(t *testing.T) {
	answer := strings.Join([]string{
		`{"kind":"header","host":"valley-host","format":"valley-telemetry/v1"}`,
		`{"kind":"source","name":"worker","state":"idle","detail":"no open run","unit_states":[{"Id":"valley-worker.service","LoadState":"loaded","ActiveState":"inactive","Result":"success"}]}`,
		`{"kind":"source","name":"integrator","state":"active","detail":"a unit is running","unit_states":[{"Id":"valley-integrator@app.service","LoadState":"loaded","ActiveState":"active","Result":"success"}]}`,
		`{"kind":"source","name":"bus","state":"disabled","detail":"not enabled in this host's configuration"}`,
		`{"kind":"source","name":"mirrors","state":"failed","detail":"valley-publish@app.service: activating, result exit-code, status 1\n\u001b[31mextra","unit_states":[` +
			`{"Id":"valley-publish@ok.service","LoadState":"loaded","ActiveState":"inactive","Result":"success"},` +
			`{"Id":"valley-publish@app.service","LoadState":"loaded","ActiveState":"activating","Result":"exit-code","ExecMainStatus":"1"},` +
			`{"Id":"valley-publish@site.service","LoadState":"loaded","ActiveState":"failed","Result":"exit-code","ExecMainStatus":"2"}]}`,
		`{"kind":"source","name":"backup","state":"stale","detail":"nothing received since 2026-01-01T00:00:00Z"}`,
		`{"kind":"source","name":"remote-receiver","state":"unavailable","detail":"receiver has reported nothing: this host has no observation of the source"}`,
		`{"kind":"end","truncated":false}`,
	}, "\n") + "\n"

	got, err := parseHostSources(strings.NewReader(answer))
	if err != nil {
		t.Fatalf("parseHostSources: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	if got[0].Source != "mirrors" || got[0].State != "failed" ||
		!slices.Equal(got[0].Units, []string{"valley-publish@app.service", "valley-publish@site.service"}) ||
		strings.ContainsAny(got[0].Reason, "\n\x1b") {
		t.Errorf("mirrors = %+v", got[0])
	}
	if got[1].Source != "backup" || got[1].State != "stale" || got[1].Units == nil || len(got[1].Units) != 0 {
		t.Errorf("backup = %+v", got[1])
	}
	if got[2].Source != "remote-receiver" || got[2].State != "unavailable" || got[2].Units == nil || len(got[2].Units) != 0 {
		t.Errorf("remote-receiver = %+v", got[2])
	}

	if _, err := parseHostSources(strings.NewReader(`{"kind":"error","error":"alice does not hold the observe grant"}` + "\n")); err == nil {
		t.Error("parseHostSources accepted an error line")
	}
	if _, err := parseHostSources(strings.NewReader(`{"kind":"header"}` + "\n" + `{"kind":"source","name":"mirrors","state":"failed","detail":"x"}` + "\n")); err == nil {
		t.Error("parseHostSources accepted an answer without end")
	}
}

// valley pending surfaces failed, stale and unavailable host sources
// above needs you and in --json host_sources, prints nothing for healthy
// sources, prints nothing and adds no warning without the observe grant
// or with no collector, and prints host entries once under --all.
func TestPendingHostSources(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	t.Setenv("VALLEY_VERIFICATION_SCHEMA", testSchema)
	_, hasCue := exec.LookPath("cue")

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	answerFile := filepath.Join(dir, "sources.jsonl")
	failFile := filepath.Join(dir, "ssh.fail")
	logFile := filepath.Join(dir, "ssh.log")
	sshScript := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
if [ -e '%s' ]; then
  echo 'ssh: connect to host valley-host port 22: Connection refused' >&2
  exit 255
fi
case "$*" in
  *"valley-telemetry sources"*)
    cat '%s'
    ;;
  *)
    echo '{"kind":"header","host":"valley-host","format":"valley-telemetry/v1"}'
    echo '{"kind":"end","cursor":"worker=0123456789abcdef:0","truncated":false}'
    ;;
esac
`, logFile, failFile, answerFile)
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(sshScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Two projects served beside one instance, so --all lists both.
	reposDir := filepath.Join(dir, "srv")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(cwd string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), cwd, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, name := range []string{"instance.git", "app.git", "site.git"} {
		runGit(reposDir, "init", "--quiet", "--bare", "--initial-branch=main", filepath.Join(reposDir, name))
	}
	// Seed instance with refs/the-valley/projects listing app and site.
	instWork := filepath.Join(dir, "inst-work")
	runGit(dir, "clone", "--quiet", filepath.Join(reposDir, "instance.git"), instWork)
	runGit(instWork, "config", "user.name", "operator")
	runGit(instWork, "config", "user.email", "operator@example.org")
	if err := os.MkdirAll(filepath.Join(instWork, "policy", "instance"), 0o755); err != nil {
		t.Fatal(err)
	}
	floor := "package verification\nfloor: {\n\tchecks: \"c-1\": {runner: \"nix\", attribute: \"c-1\"}\n\tclasses: src: {paths: \"src/**\": true, requires: \"c-1\": true}\n\tunclassified: \"c-1\": true\n}\n"
	if err := os.WriteFile(filepath.Join(instWork, "policy", "instance", "floor.cue"), []byte(floor), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(instWork, "add", ".")
	runGit(instWork, "commit", "--quiet", "-m", "instance")
	runGit(instWork, "push", "--quiet", "origin", "main")
	listBlob := bareGit(t, filepath.Join(reposDir, "instance.git"))("app\nsite\n", "hash-object", "-w", "--stdin")
	bareGit(t, filepath.Join(reposDir, "instance.git"))("", "update-ref", projectsRef, listBlob)

	// Seed app and site, each pointing policy/valley at instance.git.
	var appWork string
	for _, name := range []string{"app", "site"} {
		w := filepath.Join(dir, name+"-work")
		runGit(dir, "clone", "--quiet", filepath.Join(reposDir, name+".git"), w)
		runGit(w, "config", "user.name", "operator")
		runGit(w, "config", "user.email", "operator@example.org")
		if err := os.MkdirAll(filepath.Join(w, "policy"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w, "policy", "valley"), []byte(filepath.Join(reposDir, "instance.git")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(w, "add", ".")
		runGit(w, "commit", "--quiet", "-m", name)
		runGit(w, "push", "--quiet", "origin", "main")
		if name == "app" {
			appWork = w
		}
	}
	t.Chdir(appWork)

	writeAnswer := func(lines ...string) {
		t.Helper()
		os.Remove(failFile)
		body := `{"kind":"header","host":"valley-host","format":"valley-telemetry/v1"}` + "\n" +
			strings.Join(lines, "\n") + "\n" +
			`{"kind":"end","truncated":false}` + "\n"
		if err := os.WriteFile(answerFile, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runPending := func(args ...string) (string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"pending"}, args...), &stdout, &stderr, os.Exit); code != 0 {
			t.Fatalf("pending %v exited %d: %s", args, code, stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	parseJSON := func(out string) jsonChanges {
		t.Helper()
		var doc jsonChanges
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("pending --json: %v\n%s", err, out)
		}
		if doc.HostSources == nil {
			t.Fatalf("pending --json missing host_sources:\n%s", out)
		}
		return doc
	}

	// 1. Local non-SSH origin (no telemetry remote): no ssh called, no host entry, no warning.
	os.Remove(logFile)
	out, baseErrs := runPending()
	if hasCue == nil && baseErrs != "" {
		t.Errorf("local origin unexpected stderr: %q", baseErrs)
	}
	if out != "nothing needs you\n" {
		t.Errorf("local origin: out=%q errs=%q", out, baseErrs)
	}
	if _, err := os.Stat(logFile); err == nil {
		t.Error("local origin invoked ssh")
	}
	jout, jerrs := runPending("--json")
	if doc := parseJSON(jout); len(*doc.HostSources) != 0 || jerrs != "" {
		t.Errorf("local origin --json: host_sources=%+v errs=%q", *doc.HostSources, jerrs)
	}

	// Point telemetry at an ssh host for the remaining cases.
	runGit(appWork, "config", "valley.telemetry.remote", "ssh://git@valley-host/app.git")

	// 2. Healthy sources (idle, active, disabled): prints nothing, host_sources is [].
	writeAnswer(
		`{"kind":"source","name":"worker","state":"idle","detail":"no open run"}`,
		`{"kind":"source","name":"integrator","state":"active","detail":"a unit is running"}`,
		`{"kind":"source","name":"bus","state":"disabled","detail":"not enabled in this host's configuration"}`,
	)
	out, errs := runPending()
	if out != "nothing needs you\n" || errs != baseErrs {
		t.Errorf("healthy: out=%q errs=%q (base %q)", out, errs, baseErrs)
	}
	jout, _ = runPending("--json")
	if doc := parseJSON(jout); len(*doc.HostSources) != 0 {
		t.Errorf("healthy --json: host_sources=%+v", *doc.HostSources)
	}

	// 3. One failed source (one failing unit): shown above needs you and in --json.
	writeAnswer(
		`{"kind":"source","name":"worker","state":"idle","detail":"no open run"}`,
		`{"kind":"source","name":"mirrors","state":"failed","detail":"valley-publish@app.service: activating, result exit-code, status 1","unit_states":[{"Id":"valley-publish@app.service","LoadState":"loaded","ActiveState":"activating","Result":"exit-code","ExecMainStatus":"1"}]}`,
	)
	out, errs = runPending()
	wantOne := "host  mirrors  failed · valley-publish@app.service: activating, result exit-code, status 1\n" +
		"      see: valley telemetry read --source mirrors --logs\n" +
		"\n" +
		"nothing needs you\n"
	if out != wantOne || errs != baseErrs {
		t.Errorf("one failed source:\n got:\n%s\nwant:\n%s\nerrs=%q", out, wantOne, errs)
	}
	jout, _ = runPending("--json")
	doc := parseJSON(jout)
	if len(*doc.HostSources) != 1 || (*doc.HostSources)[0].Source != "mirrors" || (*doc.HostSources)[0].State != "failed" ||
		(*doc.HostSources)[0].Reason != "valley-publish@app.service: activating, result exit-code, status 1" ||
		!slices.Equal((*doc.HostSources)[0].Units, []string{"valley-publish@app.service"}) {
		t.Errorf("one failed source --json: %+v\nraw: %s", *doc.HostSources, jout)
	}

	// 4. --all across app and site: host entry printed once, sources read once.
	os.Remove(logFile)
	out, errs = runPending("--all")
	if out != wantOne || (hasCue == nil && errs != "") {
		t.Errorf("--all with one failed source:\n got:\n%s\nwant:\n%s\nerrs=%q", out, wantOne, errs)
	}
	sshLog, _ := os.ReadFile(logFile)
	if n := strings.Count(string(sshLog), "valley-telemetry sources"); n != 1 {
		t.Errorf("--all ran sources %d times, want 1:\n%s", n, sshLog)
	}

	// 5. Several failing units (3 failing, 1 healthy): each failing unit named.
	writeAnswer(
		`{"kind":"source","name":"mirrors","state":"failed","detail":"valley-publish@app.service: activating, result exit-code, status 1","unit_states":[` +
			`{"Id":"valley-publish@ok.service","LoadState":"loaded","ActiveState":"inactive","Result":"success"},` +
			`{"Id":"valley-publish@app.service","LoadState":"loaded","ActiveState":"activating","Result":"exit-code","ExecMainStatus":"1"},` +
			`{"Id":"valley-publish@docs.service","LoadState":"loaded","ActiveState":"failed","Result":"exit-code","ExecMainStatus":"1"},` +
			`{"Id":"valley-publish@site.service","LoadState":"loaded","ActiveState":"failed","Result":"exit-code","ExecMainStatus":"2"}]}`,
	)
	out, errs = runPending()
	wantSeveral := "host  mirrors  failed · valley-publish@app.service: activating, result exit-code, status 1\n" +
		"      valley-publish@app.service\n" +
		"      valley-publish@docs.service\n" +
		"      valley-publish@site.service\n" +
		"      see: valley telemetry read --source mirrors --logs\n" +
		"\n" +
		"nothing needs you\n"
	if out != wantSeveral || errs != baseErrs {
		t.Errorf("several failing units:\n got:\n%s\nwant:\n%s\nerrs=%q", out, wantSeveral, errs)
	}
	jout, _ = runPending("--json")
	doc = parseJSON(jout)
	if len(*doc.HostSources) != 1 || !slices.Equal((*doc.HostSources)[0].Units, []string{
		"valley-publish@app.service", "valley-publish@docs.service", "valley-publish@site.service",
	}) {
		t.Errorf("several failing units --json: %+v", *doc.HostSources)
	}

	// 6. Capped failing units (7 failing): first 5 named, then "and 2 more"; --json carries all 7.
	var unitJSON []string
	var allUnits []string
	for _, p := range []string{"api", "app", "blog", "docs", "site", "tools", "web"} {
		u := fmt.Sprintf("valley-publish@%s.service", p)
		allUnits = append(allUnits, u)
		unitJSON = append(unitJSON, fmt.Sprintf(`{"Id":%q,"LoadState":"loaded","ActiveState":"failed","Result":"exit-code","ExecMainStatus":"1"}`, u))
	}
	writeAnswer(
		`{"kind":"source","name":"mirrors","state":"failed","detail":"valley-publish@api.service: failed, result exit-code, status 1","unit_states":[` +
			strings.Join(unitJSON, ",") + `]}`,
	)
	out, errs = runPending()
	wantCapped := "host  mirrors  failed · valley-publish@api.service: failed, result exit-code, status 1\n" +
		"      valley-publish@api.service\n" +
		"      valley-publish@app.service\n" +
		"      valley-publish@blog.service\n" +
		"      valley-publish@docs.service\n" +
		"      valley-publish@site.service\n" +
		"      and 2 more\n" +
		"      see: valley telemetry read --source mirrors --logs\n" +
		"\n" +
		"nothing needs you\n"
	if out != wantCapped || errs != baseErrs {
		t.Errorf("capped failing units:\n got:\n%s\nwant:\n%s\nerrs=%q", out, wantCapped, errs)
	}
	jout, _ = runPending("--json")
	doc = parseJSON(jout)
	if len(*doc.HostSources) != 1 || !slices.Equal((*doc.HostSources)[0].Units, allUnits) {
		t.Errorf("capped failing units --json: %+v", *doc.HostSources)
	}

	// 7. No observe grant: no host entry, no warning on stderr, host_sources is [].
	if err := os.WriteFile(answerFile, []byte(`{"kind":"error","error":"alice does not hold the observe grant"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errs = runPending()
	if out != "nothing needs you\n" || errs != baseErrs {
		t.Errorf("no observe grant: out=%q errs=%q (base %q)", out, errs, baseErrs)
	}
	jout, jerrs = runPending("--json")
	if doc := parseJSON(jout); len(*doc.HostSources) != 0 || jerrs != "" {
		t.Errorf("no observe grant --json: host_sources=%+v errs=%q", *doc.HostSources, jerrs)
	}

	// 8. No collector (ssh fails): no host entry, no warning on stderr, host_sources is [].
	if err := os.WriteFile(failFile, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errs = runPending()
	if out != "nothing needs you\n" || errs != baseErrs {
		t.Errorf("no collector: out=%q errs=%q (base %q)", out, errs, baseErrs)
	}
	jout, jerrs = runPending("--json")
	if doc := parseJSON(jout); len(*doc.HostSources) != 0 || jerrs != "" {
		t.Errorf("no collector --json: host_sources=%+v errs=%q", *doc.HostSources, jerrs)
	}
}

// A host that accepts the sources request and stalls is bounded by
// hostSourcesTimeout, prints no host entry or warning, and leaves no ssh
// process running.
func TestPendingBoundsAStalledHostSourcesRead(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc to find a child left running in")
	}
	prev := hostSourcesTimeout
	hostSourcesTimeout = 250 * time.Millisecond
	defer func() { hostSourcesTimeout = prev }()

	dir := t.TempDir()
	toolsOnPath(t, dir, "git", "cue")
	ssh := "#!/bin/sh\necho '{\"kind\":\"header\",\"host\":\"valley-host\",\"format\":\"valley-telemetry/v1\"}'\nexec sleep 86395\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	sh, _ := policyProject(t, filepath.Join(dir, "origin"), 1)
	sh("config", "valley.telemetry.remote", "ssh://git@valley-host/origin.git")

	began := time.Now()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pending"}, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("pending: %s", stderr.String())
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("pending took %s beside a stalled sources read", took)
	}
	if stdout.String() != "nothing needs you\n" || stderr.Len() != 0 {
		t.Errorf("pending beside a stalled sources read printed %q and stderr %q", stdout.String(), stderr.String())
	}
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	for _, stat := range stats {
		data, err := os.ReadFile(stat)
		if err != nil {
			continue
		}
		f := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
		cmdline, _ := os.ReadFile(filepath.Join(filepath.Dir(stat), "cmdline"))
		if len(f) > 1 && f[1] == fmt.Sprint(os.Getpid()) && strings.Contains(string(cmdline), "86395") {
			t.Errorf("pending left its sources ssh running: %s", filepath.Dir(stat))
		}
	}
}

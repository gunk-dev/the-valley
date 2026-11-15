package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"the-valley/note/statement"
)

// refusedBySchema is the check name the cue stand-in refuses a statement
// for.
const refusedBySchema = "refused-by-schema"

// cueStandIn is cue vet as verify runs it: vet -c <schema> <file>...,
// which checks each file on its own and fails when any one fails. It
// refuses a statement that names refusedBySchema, and logs how many files
// each run was given to $ATTEST_CUE_LOG. With $ATTEST_CUE_STALL, a FIFO,
// it writes its process ID there and stalls instead.
func cueStandIn(args []string) int {
	if fifo := os.Getenv("ATTEST_CUE_STALL"); fifo != "" {
		os.WriteFile(fifo, []byte(strconv.Itoa(os.Getpid())+"\n"), 0)
		time.Sleep(24 * time.Hour)
	}
	if len(args) < 4 || args[0] != "vet" || args[1] != "-c" {
		fmt.Fprintf(os.Stderr, "cue stand-in: unexpected arguments %q\n", args)
		return 2
	}
	files := args[3:]
	if log, err := os.OpenFile(os.Getenv("ATTEST_CUE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(log, len(files))
		log.Close()
	}
	status := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil || strings.Contains(string(raw), refusedBySchema) {
			fmt.Printf("predicate.check.name: the stand-in schema refuses it:\n    ./%s\n", f)
			status = 1
		}
	}
	return status
}

// verifyRig is a repository named repo inside a scratch directory, with
// one commit, a known-keys file holding the worker's key, and notes
// written beside them.
type verifyRig struct {
	t                  *testing.T
	dir, repo, tree    string
	known, schema, log string
	bin                string // where a stand-in cue goes, or "" for the cue on PATH
	worker, stranger   signer
}

func newVerifyRig(t *testing.T, repo string, standIn bool) *verifyRig {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to run")
	}
	dir := t.TempDir()
	r := &verifyRig{t: t, dir: dir, repo: filepath.Join(dir, repo), known: filepath.Join(dir, "known"),
		schema: filepath.Join(dir, "attestation.cue"), log: filepath.Join(dir, "cue.log"),
		worker: testSigner(t, "worker.example/attestations"), stranger: testSigner(t, "stranger.example/attestations")}
	commit := []string{"-c", "user.name=t", "-c", "user.email=t@localhost", "commit", "--quiet"}
	if out, err := exec.Command("git", "init", "--quiet", r.repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(r.repo, "f"), []byte("f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "f"}, append(commit, "-m", "f")} {
		if out, err := exec.Command("git", append([]string{"-C", r.repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	entries, err := gitTreeEntries(r.repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	r.tree = statement.TreeDigest(entries)
	if err := os.WriteFile(r.known, []byte(r.worker.verifierKey().String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.schema, []byte("package attestation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if standIn {
		r.bin = filepath.Join(dir, "bin")
		if err := os.MkdirAll(r.bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(os.Args[0], filepath.Join(r.bin, "cue")); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// statement is a pure statement's JSON about subject, the check and its
// tree digest; extra adds members to its subject's digests and to the
// statement itself, each a JSON member.
func (r *verifyRig) statement(check, subject, digests, extra string) string {
	d := strings.Repeat("ab", 32)
	return fmt.Sprintf(`{"predicate": {"result": "passed", "output": {"valley-tree-v1": %q},
		"derivation": {"sha256": %q}, "inputs": {"valley-inputs-v1": %q},
		"check": {"runner": "nix", "attribute": %q, "name": %q}},
		"predicateType": "the-valley/check/pure/v1",
		"subject": {"digest": {"valley-tree-v1": %q%s}, "primary": "valley-tree-v1"},
		"statementType": "the-valley/attestation/v1"%s}`, d, d, d, check, check, subject, digests, extra)
}

// note writes the statement given as JSON, signed by by, to name.note.
func (r *verifyRig) note(name, statementJSON string, by signer) string {
	r.t.Helper()
	text, err := statement.RenderJSON([]byte(statementJSON))
	if err != nil {
		r.t.Fatal(err)
	}
	n, err := signNote(text, by)
	if err != nil {
		r.t.Fatal(err)
	}
	path := filepath.Join(r.dir, name+".note")
	if err := os.WriteFile(path, n, 0o644); err != nil {
		r.t.Fatal(err)
	}
	return path
}

// verify runs attest verify with flags and a --note for each path, and
// returns what it printed, its exit status, and how many files each cue
// run was given.
func (r *verifyRig) verify(flags []string, paths ...string) (stdout, stderr string, status int, cueRuns []string) {
	r.t.Helper()
	os.Remove(r.log)
	args := append([]string{"verify", "--repo", r.repo}, flags...)
	for _, p := range paths {
		args = append(args, "--note", p)
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "ATTEST_AS_MAIN=1", "ATTEST_CUE_LOG="+r.log)
	if r.bin != "" {
		cmd.Env = append(cmd.Env, "PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		status = exit.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	logged, _ := os.ReadFile(r.log)
	return out.String(), errb.String(), status, strings.Fields(string(logged))
}

// verdictOf is the verdict line attest verify --verdicts gives the note
// at place i, as a run of that note alone judged it.
func verdictOf(i int, stdout, stderr string, status int) string {
	if status != 0 {
		why := strings.TrimSuffix(strings.TrimPrefix(stderr, "attest: "), "\n")
		return fmt.Sprintf("verdict %d refused %s\n", i, strconv.Quote(why))
	}
	line := fmt.Sprintf("verdict %d verified", i)
	for _, l := range strings.Split(stdout, "\n") {
		if f := strings.Fields(l); len(f) == 3 && f[0] == "signature" && f[1] == "ok" {
			line += " " + strconv.Quote(f[2])
		}
	}
	return line + "\n"
}

// Verifying several notes with --verdicts gives each note the verdict a
// run of that note alone gives it, whatever refuses it: the schema,
// another tree, or a signer the reader does not hold. Nothing is printed
// but the verdicts, every value one line however it is spelt: the
// repository's path carries a newline, "verified" and a forged verdict,
// and the correctly signed note about another tree is still refused. The
// statements are vetted in one cue run when every one passes.
func TestEachNoteIsVerifiedAsItWouldBeAlone(t *testing.T) {
	r := newVerifyRig(t, "repo\nverified\nverdict 3 verified \"worker.example/attestations+00000000\"", true)
	passed := r.note("passed", r.statement("one", r.tree, "", ""), r.worker)
	again := r.note("again", r.statement("two", r.tree, "", ""), r.worker)
	notes := []string{
		passed,
		r.note("schema", r.statement(refusedBySchema, r.tree, "", ""), r.worker),
		r.note("elsewhere", r.statement("three", strings.Repeat("ab", 32), "", ""), r.worker),
		r.note("stranger", r.statement("four", r.tree, "", ""), r.stranger),
		again,
	}
	flags := []string{"--known-keys", r.known, "--schema", r.schema}
	var want strings.Builder
	refused := 0
	for i, n := range notes {
		stdout, stderr, status, _ := r.verify(flags, n)
		if status != 0 {
			refused++
		}
		want.WriteString(verdictOf(i+1, stdout, stderr, status))
	}
	want.WriteString("end 5\n")
	if refused != 3 {
		t.Fatalf("%d of the notes are refused alone, not the three made to be:\n%s", refused, want.String())
	}
	stdout, stderr, status, cueRuns := r.verify(append(flags, "--verdicts"), notes...)
	if stdout != want.String() {
		t.Errorf("verifying the notes together prints\n%s\nnot each one's verdict alone\n%s", stdout, want.String())
	}
	if status != 1 || stderr != "attest: 3 of 5 notes refused\n" {
		t.Errorf("a run that refuses 3 of 5 notes exits %d and says %q", status, stderr)
	}
	// One run of the four statements that parse fails, so each of them
	// is vetted alone.
	if strings.Join(cueRuns, " ") != "4 1 1 1 1" {
		t.Errorf("cue was run with these numbers of files: %v", cueRuns)
	}

	if stdout, _, status, cueRuns := r.verify(append(flags, "--verdicts"), passed, again); status != 0 ||
		strings.Join(cueRuns, " ") != "2" || !strings.HasSuffix(stdout, "\nend 2\n") {
		t.Errorf("two notes that verify exit %d after cue runs of %v files, printing\n%s", status, cueRuns, stdout)
	}
	missing := []string{"--known-keys", filepath.Join(r.dir, "missing"), "--schema", r.schema, "--verdicts"}
	if stdout, _, status, _ := r.verify(missing, passed, again); status != 1 ||
		!strings.HasPrefix(stdout, "verdict 1 refused \"open ") || !strings.HasSuffix(stdout, "\nend 2\n") {
		t.Errorf("a known-keys file that does not read exits %d after printing\n%s", status, stdout)
	}
	if stdout, _, status, _ := r.verify(flags, passed, again); status == 0 || stdout != "" {
		t.Errorf("two notes without --verdicts exit %d after printing\n%s", status, stdout)
	}
	for _, f := range [][]string{flags, append(flags, "--verdicts")} {
		if stdout, stderr, status, _ := r.verify(f, ""); status != 1 || stdout != "" || stderr != "attest: --note is required\n" {
			t.Errorf("an empty --note exits %d, printing %q and saying %q", status, stdout, stderr)
		}
	}
}

// cue vets each statement of a run on its own, never the statements
// merged: two statements that differ both pass, and two that are each
// incomplete alone, and complete when merged, are both refused. This is
// the real cue, under a schema that wants a git-sha1 digest and a
// provenance model, which only the two together hold.
func TestCueVetsEachStatementOnItsOwn(t *testing.T) {
	if _, err := exec.LookPath("cue"); err != nil {
		if os.Getenv("ATTEST_CUE_TESTS") == "require" {
			t.Fatal("no cue on PATH, and ATTEST_CUE_TESTS=require")
		}
		t.Skip("no cue to run; the nix check provides one")
	}
	r := newVerifyRig(t, "repo", false)
	schema := "package attestation\nsubject: digest: \"git-sha1\": string\nprovenance: model: string\n"
	if err := os.WriteFile(r.schema, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	sha := `, "git-sha1": "` + strings.Repeat("9", 40) + `"`
	provenance := `, "provenance": {"model": "a model", "harness": "a harness"}`
	flags := []string{"--known-keys", r.known, "--schema", r.schema, "--verdicts"}
	for _, c := range []struct {
		name  string
		notes []string
		want  string // each verdict's word, in order
	}{
		{"two that differ", []string{
			r.note("one", r.statement("one", r.tree, sha, provenance), r.worker),
			r.note("two", r.statement("two", r.tree, sha, provenance), r.worker)}, "verified verified"},
		{"two each incomplete alone", []string{
			r.note("digest", r.statement("one", r.tree, sha, ""), r.worker),
			r.note("model", r.statement("one", r.tree, "", provenance), r.worker)}, "refused refused"},
	} {
		stdout, _, _, _ := r.verify(flags, c.notes...)
		var got []string
		for _, line := range strings.Split(stdout, "\n") {
			if f := strings.Fields(line); len(f) > 2 && f[0] == "verdict" {
				got = append(got, f[2])
			}
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("%s: the verdicts are %v, not %s:\n%s", c.name, got, c.want, stdout)
		}
	}
	merged := []byte(r.statement("one", r.tree, sha, provenance))
	if raw, err := statement.RenderJSON(merged); err != nil {
		t.Fatal(err)
	} else if doc, err := statement.Parse(raw); err != nil {
		t.Fatal(err)
	} else if js, _ := json.Marshal(doc); vetStatement(r.schema, js) != nil {
		t.Errorf("the two incomplete statements merged do not satisfy the schema, so the test shows nothing")
	}
}

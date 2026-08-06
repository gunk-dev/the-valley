// integrator is the Phase 3 controller that lands changes by verifying
// evidence (design/roadmap.md). It is a single writer per target stream and
// the commit point of dcr-439b771: it applies a change's delta to the
// current tip, checks per required check that the contributor's evidence
// still transfers, and either fast-forwards the protected ref or says which
// checks need new evidence.
//
// It runs no check. Verification happens where a change is authored, and
// the serialized section here is digest comparison and a ref write.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const usage = `usage: integrator <command> [flags]

  reconcile  judge every pending integration request once, and act
  watch      reconcile on an interval until interrupted
  help       print this message

The integrator polls, deliberately. Integration requests are durable in
git — refs/the-valley/integration-requests/<target>/<change>, whose commit
is the change's head — and the bus is a projection over them, so the
controller level-triggers over the refs and converges. It publishes its
outcomes to the bus and consumes nothing.

The commit rule (dcr-439b771). The delta must apply cleanly to the tip; a
conflict is new authorship, so every check is re-demanded. Then, per check
the policy requires: a pure check transfers when its input-closure digest
recomputed on the landed tree equals the attested one, and an effectful
check transfers while no intervening landing has touched its requiring
class's paths and its observation is inside the validity window the policy
declares. Anything left standing lands; anything invalidated surfaces as
one request-stale event naming exactly those checks, and the request stays
for resubmission.

flags:
  --repo DIR          the served repository (default: here)
  --project NAME      the project name events carry (default: the repo's)
  --key PATH          ed25519 private key the integrator signs with
  --name NAME         the name that key signs under (default: this host's)
  --known-signers F   verifier keys whose attestations count, one per line
  --instance DIR      the instance policy layer, from the instance repo
  --project-policy P  the project policy layer, relative to the target tip
  --schema FILE       the verification schema
  --attest-schema F   the attestation schema
  --event-schema F    the event vocabulary payloads are vetted against
  --bus URL           nats server to publish outcomes to; empty publishes
                      nothing and still prints every payload
  --system SYSTEM     flake system for closure recomputation
  --interval D        watch: how long to wait between passes (default 15s)
  --now TIME          judge as at this RFC 3339 instant, for the effectful
                      window; defaults to the clock

--known-signers is the registry's interim compilation. Identity is a
governed registry (dcr-b87f6e8) whose entries compile into what each
enforcement boundary checks; no compiler exists yet, so the file is
supplied directly and is exactly what dcr-b87f6e8 calls the first
compilation's output. An attestation signed by nobody in it does not
transfer, and that is reported per check rather than treated as a forgery.
`

// integrator is one controller: which repository it serves, who it signs
// as, whose evidence it accepts, and where it finds the policy.
type integrator struct {
	repo    string
	project string
	out     *os.File

	// The programs it drives. Every judgement about a statement or a note
	// is attest's, and every judgement about what a policy says is the
	// deriver's and cue's.
	attest string
	valley string
	cue    string
	nats   string

	attestSchema string
	eventSchema  string
	policy       policySource

	key          string
	signerName   string
	keyHash      string
	knownSigners string

	bus    string
	system string

	// clock, when set, is the instant the effectful window is measured
	// against. Zero means the wall clock.
	clock time.Time
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "reconcile":
		err = run(os.Args[2:], false)
	case "watch":
		err = run(os.Args[2:], true)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "integrator: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "integrator: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, loop bool) error {
	fs := flag.NewFlagSet("integrator", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	repo := fs.String("repo", ".", "the served repository")
	project := fs.String("project", "", "the project name events carry")
	key := fs.String("key", "", "ed25519 private key")
	name := fs.String("name", defaultSignerName(), "the name that key signs under")
	known := fs.String("known-signers", "", "verifier keys whose attestations count")
	instance := fs.String("instance", "", "the instance policy layer")
	projectPolicy := fs.String("project-policy", "policy", "the project policy layer, relative to the target tip")
	schema := fs.String("schema", env("VALLEY_VERIFICATION_SCHEMA"), "the verification schema")
	attestSchema := fs.String("attest-schema", env("VALLEY_ATTEST_SCHEMA"), "the attestation schema")
	eventSchema := fs.String("event-schema", env("VALLEY_EVENT_SCHEMA"), "the event vocabulary")
	bus := fs.String("bus", "", "nats server to publish outcomes to")
	system := fs.String("system", "", "flake system")
	interval := fs.Duration("interval", 15*time.Second, "how long to wait between passes")
	now := fs.String("now", "", "judge as at this instant")
	if err := fs.Parse(args); err != nil {
		return err
	}

	in := &integrator{
		repo:         *repo,
		project:      *project,
		out:          os.Stdout,
		attestSchema: *attestSchema,
		eventSchema:  *eventSchema,
		key:          *key,
		signerName:   *name,
		knownSigners: *known,
		bus:          *bus,
		system:       *system,
	}
	for _, missing := range []struct{ flag, value string }{
		{"--key", in.key},
		{"--known-signers", in.knownSigners},
		{"--instance", *instance},
		{"--schema", *schema},
		{"--attest-schema", in.attestSchema},
	} {
		if missing.value == "" {
			return fmt.Errorf("%s is required", missing.flag)
		}
	}
	var err error
	for _, tool := range []struct {
		name string
		into *string
	}{{"attest", &in.attest}, {"valley", &in.valley}, {"cue", &in.cue}, {"nats", &in.nats}} {
		if *tool.into, err = exec.LookPath(tool.name); err != nil && tool.name != "nats" {
			return fmt.Errorf("%s is not on the path; the integrator drives it rather than reimplementing it", tool.name)
		}
	}
	in.policy = policySource{
		schema:   abs(*schema),
		instance: abs(*instance),
		project:  *projectPolicy,
		valley:   in.valley,
		cue:      in.cue,
	}
	if *now != "" {
		if in.clock, err = time.Parse(time.RFC3339, *now); err != nil {
			return fmt.Errorf("--now: %w", err)
		}
	}
	if in.repo, err = filepath.Abs(in.repo); err != nil {
		return err
	}
	if in.project == "" {
		in.project = projectName(in.repo)
	}
	if in.keyHash, err = in.readKeyHash(); err != nil {
		return err
	}

	if !loop {
		return in.reconcile()
	}
	for {
		if err := in.reconcile(); err != nil {
			fmt.Fprintf(os.Stderr, "integrator: %v\n", err)
		}
		time.Sleep(*interval)
	}
}

// readKeyHash asks attest for the verifier key this integrator signs under,
// and keeps the key hash: it is the segment that keeps one signer's
// attestation ref from racing another's (dcr-0de694f).
func (in *integrator) readKeyHash() (string, error) {
	out, err := exec.Command(in.attest, "key", "--key", in.key, "--name", in.signerName).Output()
	if err != nil {
		return "", fmt.Errorf("reading the signing key: %w", err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "+")
	if len(fields) != 3 {
		return "", fmt.Errorf("attest key printed %q", strings.TrimSpace(string(out)))
	}
	if _, err := strconv.ParseUint(fields[1], 16, 32); err != nil {
		return "", fmt.Errorf("attest key printed the hash %q", fields[1])
	}
	return fields[1], nil
}

// projectName is the name the events carry: the repository's directory,
// with a bare repo's .git suffix removed — the same name the post-receive
// hook derives.
func projectName(repo string) string {
	if bare, err := gitLine(repo, "rev-parse", "--is-bare-repository"); err == nil && bare == "true" {
		dir, _ := gitLine(repo, "rev-parse", "--absolute-git-dir")
		return strings.TrimSuffix(filepath.Base(dir), ".git")
	}
	top, err := gitLine(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return filepath.Base(repo)
	}
	return filepath.Base(top)
}

// defaultSignerName is the name this machine signs under: its own name and
// the suffix that keeps a key signing attestations distinct from the same
// key signing anything else (dcr-de9d996).
func defaultSignerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return host + "/attestations"
}

func env(name string) string { return os.Getenv(name) }

func abs(path string) string {
	if p, err := filepath.Abs(path); err == nil {
		return p
	}
	return path
}

// gitEnv runs a git command with extra environment, for the one place the
// integrator has to set authorship.
func (in *integrator) gitEnv(extra map[string]string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = in.repo
	cmd.Env = os.Environ()
	for k, v := range extra {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return string(out), nil
}

// writeTemp writes bytes to a file cue will read as JSON — the extension is
// how cue tells a data file from a schema.
func writeTemp(prefix string, body []byte) (string, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func removeTemp(path string) { _ = os.RemoveAll(filepath.Dir(path)) }

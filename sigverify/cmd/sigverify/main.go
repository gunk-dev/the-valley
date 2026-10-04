// sigverify verifies OpenSSH signatures and refuses a FIDO security-key
// signature that was made without a touch. See ../../README.md.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"the-valley/sigverify"
)

// gitProgram is the git the git-tag command runs. The Nix package sets it
// at build time to an absolute store path, so a shipped sigverify never
// looks git up on PATH. A plain `go build` leaves it to PATH.
var gitProgram = "git"

const usage = `usage: sigverify <command> [flags]

  verify   verify an SSH signature over the message on stdin
  git-tag  verify the SSH signature on an annotated git tag
  help     print this message

Both commands accept a signature only if all of these hold:

  - it is a valid SSHSIG over the message, in the expected namespace
  - an allowed-signers line lists its key, in a signer class the call
    accepts, for the principal, in that namespace, at the verify time
  - a security key's signature has the user-presence bit set; nothing
    turns this off
  - the signature proves user verification, if --require-uv is given or
    the signer's line carries verify-required

Signer classes: an allowed-signers line names a fido-sk signer (a FIDO
security key), a tkey-signer (an ssh-ed25519 key on a line marked
tkey-signer), or a software key (anything else). By default only fido-sk
signers are accepted.

On success stdout is "verified" followed by "name value" lines describing
the signature. On refusal stdout is exactly one line, "refused <reason>",
and stderr says why in a sentence. On error stdout is empty. The reasons
are listed in sigverify/README.md.

exit status:
  0  verified, and the whole report was written to stdout
  1  refused
  2  error: bad usage, an unreadable file, an invalid allowed-signers
     file, a git failure, or a failure to write the report

verify flags:
  --namespace NS          the namespace the signature must carry (required)
  --signature FILE        the armored or raw signature (required)

git-tag flags:
  --repo DIR              the repository (default ".")
  --tag NAME              the tag, resolved as refs/tags/NAME (required)

flags for both:
  --allowed-signers FILE  the OpenSSH allowed-signers file (required)
  --principal ID          the identity the signature must be valid for
                          (required, unless --any-principal is given)
  --any-principal         accept any line that lists the key, whatever
                          principals it names, even "!*"
  --allow-class CLASS     accept signers of CLASS: fido-sk, tkey-signer or
                          software. Repeat for more than one. Giving it
                          replaces the default, fido-sk.
  --allow-non-sk          also accept software keys, which prove nothing
                          about presence: adds software to the accepted
                          classes, whichever they are
  --require-uv            require user verification; only a security key
                          can prove it
  --verify-time TIME      judge validity at TIME, as YYYYMMDD[HHMM[SS]][Z]
                          (default: now)

git-tag reads the tag object with git, splits it into payload and
signature exactly as git verify-tag does, and verifies in the "git"
namespace. It then refuses a tag signed under a different name than NAME,
and a tag that does not point at a commit present in the repository. On
success it prints the commit id, and the caller must use exactly that
commit. git runs with replacement refs disabled and with no inherited
GIT_* variables or user or system configuration. Validity is judged at
the verify time, not the tagger date: the signer chooses the tagger date.
`

const (
	exitVerified = 0
	exitRefused  = 1
	exitError    = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, usage)
		return exitError
	}
	var c *command
	switch args[0] {
	case "verify":
		c = newCommand("verify", stderr)
		c.fs.StringVar(&c.namespace, "namespace", "", "")
		c.fs.StringVar(&c.signature, "signature", "", "")
	case "git-tag":
		c = newCommand("git-tag", stderr)
		c.fs.StringVar(&c.repo, "repo", ".", "")
		c.fs.StringVar(&c.tag, "tag", "", "")
	case "help", "-h", "--help":
		return emit(stdout, stderr, []byte(usage), exitVerified)
	default:
		fmt.Fprintf(stderr, "sigverify: unknown command %q\n\n%s", args[0], usage)
		return exitError
	}
	if err := c.fs.Parse(args[1:]); err != nil {
		return exitError
	}
	if c.fs.NArg() != 0 {
		fmt.Fprintf(stderr, "sigverify %s: unexpected argument %q\n", args[0], c.fs.Arg(0))
		return exitError
	}

	var out report
	var err error
	if args[0] == "verify" {
		out, err = c.verify(stdin)
	} else {
		out, err = c.gitTag()
	}

	// A refusal reports its reason and nothing else. What a refused
	// signature names, such as a tag's target, is unauthenticated, and a
	// caller that misreads the exit status must find nothing to act on.
	var refusal *sigverify.Refusal
	switch {
	case errors.As(err, &refusal):
		fmt.Fprintf(stderr, "sigverify: refused: %s\n", refusal.Detail)
		return emit(stdout, stderr, []byte("refused "+string(refusal.Reason)+"\n"), exitRefused)
	case err != nil:
		fmt.Fprintf(stderr, "sigverify %s: %v\n", args[0], err)
		return exitError
	}
	var b bytes.Buffer
	b.WriteString("verified\n")
	out.write(&b)
	return emit(stdout, stderr, b.Bytes(), exitVerified)
}

// emit writes the whole of out to stdout in one write, and returns code
// only if that write succeeded. A report that did not reach its reader is
// an error, so a full disk or a closed pipe can never yield exit status 0.
func emit(stdout, stderr io.Writer, out []byte, code int) int {
	if _, err := stdout.Write(out); err != nil {
		fmt.Fprintf(stderr, "sigverify: writing the report: %v\n", err)
		return exitError
	}
	return code
}

// command holds one invocation's flags.
type command struct {
	fs *flag.FlagSet

	allowedSigners string
	principal      string
	anyPrincipal   bool
	classes        classFlag
	allowNonSK     bool
	requireUV      bool
	verifyTime     string

	namespace string // verify
	signature string

	repo string // git-tag
	tag  string
}

func newCommand(name string, stderr io.Writer) *command {
	c := &command{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	c.fs.SetOutput(stderr)
	c.fs.Usage = func() { fmt.Fprint(stderr, usage) }
	c.fs.StringVar(&c.allowedSigners, "allowed-signers", "", "")
	c.fs.StringVar(&c.principal, "principal", "", "")
	c.fs.BoolVar(&c.anyPrincipal, "any-principal", false, "")
	c.fs.Var(&c.classes, "allow-class", "")
	c.fs.BoolVar(&c.allowNonSK, "allow-non-sk", false, "")
	c.fs.BoolVar(&c.requireUV, "require-uv", false, "")
	c.fs.StringVar(&c.verifyTime, "verify-time", "", "")
	return c
}

// classFlag collects repeated --allow-class flags.
type classFlag []sigverify.Class

func (f *classFlag) String() string { return fmt.Sprint(*f) }

func (f *classFlag) Set(name string) error {
	c, err := sigverify.ParseClass(name)
	if err != nil {
		return err
	}
	*f = append(*f, c)
	return nil
}

// setup loads what both commands share: the allowed signers and the
// options.
func (c *command) setup() (*sigverify.AllowedSigners, sigverify.Options, error) {
	opts := sigverify.Options{
		Principal:    c.principal,
		AnyPrincipal: c.anyPrincipal,
		Policy: sigverify.Policy{
			Classes:    c.classes,
			AllowNonSK: c.allowNonSK,
			RequireUV:  c.requireUV,
		},
	}
	switch {
	case c.allowedSigners == "":
		return nil, opts, errors.New("--allowed-signers is required")
	case c.principal == "" && !c.anyPrincipal:
		return nil, opts, errors.New("--principal is required; pass --any-principal to accept any principal the key is listed under")
	case c.principal != "" && c.anyPrincipal:
		return nil, opts, errors.New("--principal and --any-principal cannot both be given")
	}
	if c.verifyTime != "" {
		t, err := sigverify.ParseTime(c.verifyTime)
		if err != nil {
			return nil, opts, fmt.Errorf("--verify-time: %w", err)
		}
		opts.Time = t
	}
	signers, err := sigverify.LoadAllowedSigners(c.allowedSigners)
	return signers, opts, err
}

func (c *command) verify(stdin io.Reader) (report, error) {
	if c.namespace == "" {
		return nil, errors.New("--namespace is required")
	}
	if c.signature == "" {
		return nil, errors.New("--signature is required")
	}
	signers, opts, err := c.setup()
	if err != nil {
		return nil, err
	}
	opts.Namespace = c.namespace
	signature, err := os.ReadFile(c.signature)
	if err != nil {
		return nil, err
	}
	message, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("reading the message: %w", err)
	}
	result, err := sigverify.Verify(signers, message, signature, opts)
	if err != nil {
		return nil, err
	}
	return resultLines(result), nil
}

func (c *command) gitTag() (report, error) {
	name := strings.TrimPrefix(c.tag, "refs/tags/")
	if name == "" {
		return nil, errors.New("--tag is required")
	}
	signers, opts, err := c.setup()
	if err != nil {
		return nil, err
	}

	// show-ref --verify takes only a full ref name, so refs/tags/NAME
	// cannot resolve to a branch or anything else that happens to share
	// the name.
	ref := "refs/tags/" + name
	shown, err := git(c.repo, "", "show-ref", "--verify", ref)
	if err != nil {
		return nil, fmt.Errorf("no tag %s: %w", ref, err)
	}
	id, _, _ := strings.Cut(strings.TrimSpace(string(shown)), " ")
	switch kind, err := objectType(c.repo, id); {
	case err != nil:
		return nil, err
	case kind != "tag":
		return nil, &sigverify.Refusal{
			Reason: sigverify.Unsigned,
			Detail: fmt.Sprintf("%s is a lightweight tag pointing at a %s, and carries no signature", ref, kind),
		}
	}
	object, err := git(c.repo, "", "cat-file", "tag", id)
	if err != nil {
		return nil, err
	}

	result, tag, err := sigverify.VerifyTag(signers, object, name, opts)
	if err != nil {
		return nil, err
	}

	// The signature says the tag points at a commit. The repository must
	// hold that commit, as itself and not a replacement.
	switch kind, err := objectType(c.repo, tag.Commit); {
	case err != nil:
		return nil, err
	case kind == "missing":
		return nil, &sigverify.Refusal{
			Reason: sigverify.TargetMissing, Signer: &result.Signer,
			Detail: "the signed tag points at a commit this repository does not hold",
		}
	case kind != "commit":
		return nil, &sigverify.Refusal{
			Reason: sigverify.TargetNotCommit, Signer: &result.Signer,
			Detail: fmt.Sprintf("the signed tag says it points at a commit, and the object is a %s", kind),
		}
	}
	return append(resultLines(result), [2]string{"tag", tag.Name}, [2]string{"commit", tag.Commit}), nil
}

// objectType returns the type of the object id in repo, or "missing".
func objectType(repo, id string) (string, error) {
	out, err := git(repo, id+"\n", "cat-file", "--batch-check=%(objecttype)")
	if err != nil {
		return "", err
	}
	// batch-check answers "<id> missing" for an absent object.
	kind := strings.TrimSpace(string(out))
	if strings.HasSuffix(kind, " missing") {
		return "missing", nil
	}
	return kind, nil
}

// gitEnv is the whole environment git runs in. Nothing is inherited: a
// GIT_DIR, GIT_OBJECT_DIRECTORY or GIT_CONFIG_PARAMETERS in the caller's
// environment could point git at other objects or change how it reads
// them. User and system configuration are not read, and replacement refs
// are off.
var gitEnv = []string{
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_SYSTEM=/dev/null",
	"GIT_CONFIG_GLOBAL=/dev/null",
	"GIT_NO_REPLACE_OBJECTS=1",
	"GIT_ATTR_NOSYSTEM=1",
	"GIT_TERMINAL_PROMPT=0",
	"LC_ALL=C",
}

// git runs git in repo with stdin as its input, and returns its stdout.
// Its stderr becomes the error.
func git(repo, stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command(gitProgram, append([]string{"--no-replace-objects", "-C", repo}, args...)...)
	cmd.Env = gitEnv
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git %s: %s", args[0], msg)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

// report is the "name value" lines printed after "verified".
type report [][2]string

func (r report) write(w io.Writer) {
	for _, l := range r {
		fmt.Fprintf(w, "%s %s\n", l[0], l[1])
	}
}

func resultLines(r *sigverify.Result) report {
	lines := report{
		{"namespace", r.Namespace},
		{"principal", r.Principal},
		{"class", string(r.Class)},
		{"key-type", r.KeyType},
		{"fingerprint", r.Fingerprint},
	}
	if r.Class == sigverify.ClassFIDO {
		lines = append(lines,
			[2]string{"flags", fmt.Sprintf("0x%02x", r.Flags)},
			[2]string{"user-presence", yesNo(r.UserPresent())},
			[2]string{"user-verification", yesNo(r.UserVerified())},
			[2]string{"counter", fmt.Sprint(r.Counter)},
		)
	}
	if r.SignerApp != "" {
		lines = append(lines, [2]string{"signer-app", r.SignerApp})
	}
	return lines
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

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

const usage = `usage: sigverify <command> [flags]

  verify   verify an SSH signature over the message on stdin
  git-tag  verify the SSH signature on an annotated git tag
  help     print this message

Both commands accept a signature only if all of these hold:

  - it is a valid SSHSIG over the message, in the expected namespace
  - the allowed-signers file lists its key, for the principal if one is
    given, in that namespace, at the verify time
  - the key is a FIDO security key, unless --allow-non-sk is given
  - a security key's signature has the user-presence bit set; nothing
    turns this off
  - the signature has the user-verification bit set, if --require-uv is
    given or the signer's line carries verify-required

On success stdout is "verified" followed by "name value" lines describing
the signature. On refusal it is "refused <reason>" followed by whatever is
known about the signer, and stderr says why in a sentence. The reasons are
listed in sigverify/README.md.

exit status:
  0  verified
  1  refused
  2  error: bad usage, an unreadable file, an invalid allowed-signers
     file, or a git failure

verify flags:
  --namespace NS          the namespace the signature must carry (required)
  --signature FILE        the armored or raw signature (required)
  --allowed-signers FILE  the OpenSSH allowed-signers file (required)
  --principal ID          the identity the signature must be valid for;
                          by default any principal the key is listed under
  --require-uv            require the user-verification bit
  --allow-non-sk          also accept software keys, which prove nothing
                          about presence
  --verify-time TIME      judge validity at TIME, as YYYYMMDD[HHMM[SS]][Z]
                          (default: now)

git-tag flags:
  --repo DIR              the repository (default ".")
  --tag NAME              the tag, resolved as refs/tags/NAME (required)
  --allowed-signers, --principal, --require-uv, --allow-non-sk and
  --verify-time, as for verify

git-tag reads the tag object with git, splits it into payload and
signature exactly as git verify-tag does, and verifies in the "git"
namespace. It also refuses a tag whose object was signed under a different
name than NAME, and prints the tagged object's id so the caller can use
exactly the object that was verified. Validity is judged at the verify
time, not the tagger date: the signer chooses the tagger date.
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
		fmt.Fprint(stdout, usage)
		return exitVerified
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

	var refusal *sigverify.Refusal
	switch {
	case errors.As(err, &refusal):
		fmt.Fprintf(stdout, "refused %s\n", refusal.Reason)
		if refusal.Signer != nil {
			out = append(signerLines(*refusal.Signer), out...)
		}
		out.write(stdout)
		fmt.Fprintf(stderr, "sigverify: refused: %s\n", refusal.Detail)
		return exitRefused
	case err != nil:
		fmt.Fprintf(stderr, "sigverify %s: %v\n", args[0], err)
		return exitError
	}
	fmt.Fprintln(stdout, "verified")
	out.write(stdout)
	return exitVerified
}

// command holds one invocation's flags.
type command struct {
	fs *flag.FlagSet

	allowedSigners string
	principal      string
	requireUV      bool
	allowNonSK     bool
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
	c.fs.BoolVar(&c.requireUV, "require-uv", false, "")
	c.fs.BoolVar(&c.allowNonSK, "allow-non-sk", false, "")
	c.fs.StringVar(&c.verifyTime, "verify-time", "", "")
	return c
}

// setup loads what both commands share: the allowed signers and the
// options.
func (c *command) setup() (*sigverify.AllowedSigners, sigverify.Options, error) {
	opts := sigverify.Options{
		Principal: c.principal,
		Policy:    sigverify.Policy{RequireUV: c.requireUV, AllowNonSK: c.allowNonSK},
	}
	if c.allowedSigners == "" {
		return nil, opts, errors.New("--allowed-signers is required")
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
	shown, err := git(c.repo, "show-ref", "--verify", ref)
	if err != nil {
		return nil, fmt.Errorf("no tag %s: %w", ref, err)
	}
	id, _, _ := strings.Cut(strings.TrimSpace(string(shown)), " ")
	kind, err := git(c.repo, "cat-file", "-t", id)
	if err != nil {
		return nil, err
	}
	if t := strings.TrimSpace(string(kind)); t != "tag" {
		return nil, &sigverify.Refusal{
			Reason: sigverify.Unsigned,
			Detail: fmt.Sprintf("%s is a lightweight tag pointing at a %s, and carries no signature", ref, t),
		}
	}
	object, err := git(c.repo, "cat-file", "tag", id)
	if err != nil {
		return nil, err
	}

	result, tag, err := sigverify.VerifyTag(signers, object, name, opts)
	var lines report
	if tag != nil {
		lines = report{{"tag", tag.Name}, {"object", tag.Object}, {"object-type", tag.ObjectType}}
	}
	if err != nil {
		return lines, err
	}
	return append(resultLines(result), lines...), nil
}

// git runs git in repo and returns its stdout. Its stderr becomes the
// error.
func git(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
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

// report is the "name value" lines printed after the verdict.
type report [][2]string

func (r report) write(w io.Writer) {
	for _, l := range r {
		fmt.Fprintf(w, "%s %s\n", l[0], l[1])
	}
}

func resultLines(r *sigverify.Result) report {
	return append(report{{"namespace", r.Namespace}, {"principal", r.Principal}}, signerLines(r.Signer)...)
}

func signerLines(s sigverify.Signer) report {
	lines := report{{"key-type", s.KeyType}, {"fingerprint", s.Fingerprint}}
	if !s.SecurityKey {
		return append(lines, [2]string{"security-key", "no"})
	}
	return append(lines,
		[2]string{"security-key", "yes"},
		[2]string{"flags", fmt.Sprintf("0x%02x", s.Flags)},
		[2]string{"user-presence", yesNo(s.UserPresent())},
		[2]string{"user-verification", yesNo(s.UserVerified())},
		[2]string{"counter", fmt.Sprint(s.Counter)},
	)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

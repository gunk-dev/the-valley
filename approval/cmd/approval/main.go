// Command approval writes, signs and verifies approval statements
// (the-valley/approval/v1).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"the-valley/approval"
	"the-valley/note"
)

// pivSigner is the valley-piv-signer helper this command signs on a
// YubiKey with. The package fixes it to the helper's store path at build
// time, so no PATH can change which program receives the statements.
var pivSigner = "valley-piv-signer"

const usage = `usage:
  approval statement --instance HEX --project NAME --target BRANCH --change ID
                     --version N --head git-sha1:HEX --record git-sha1:HEX
                     --principal NAME [--issued TIME]
  approval sign (--key FILE | --piv) [--slot HEX] [--reader NAME] STATEMENT...
  approval verify --signers FILE NOTE...

statement writes an approval statement in its written form to stdout.
--issued is when it is made, as 2006-01-02T15:04:05Z; it defaults to now.

sign signs each statement file as the principal the statement names, and
writes the note beside it, as STATEMENT.note. Every file is read and held
to the statement's form, and to its bound of 1024 bytes, before any key is
used. --key signs with an unencrypted OpenSSH ed25519 key. --piv signs with
the key in a YubiKey's PIV slot (--slot, default 9c), on the first reader
whose name holds --reader: one session for every statement, which asks for
the PIN once on the terminal and for a touch on every signature. Signing
stops at the first failure; the notes already written stay, and the
statements not signed are listed.

verify opens each approval note under the approval keys in --signers, an
approval-signers or approval-signers-hardware file, and prints who approved
what. It fails when any note does not verify.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "statement":
		err = statement(args[1:], stdout)
	case "sign":
		err = sign(args[1:], stderr)
	case "verify":
		err = verify(args[1:], stdout)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		err = usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
	var u usageError
	switch {
	case errors.As(err, &u):
		fmt.Fprintf(stderr, "approval: %s\n%s", u, usage)
		return 2
	case err != nil:
		fmt.Fprintf(stderr, "approval: %s\n", err)
		return 1
	}
	return 0
}

type usageError string

func (u usageError) Error() string { return string(u) }

func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func statement(args []string, stdout io.Writer) error {
	fs := flags("statement")
	var s approval.Statement
	fs.StringVar(&s.Instance, "instance", "", "")
	fs.StringVar(&s.Project, "project", "", "")
	fs.StringVar(&s.Target, "target", "", "")
	fs.StringVar(&s.Change, "change", "", "")
	fs.IntVar(&s.Version, "version", 0, "")
	fs.StringVar(&s.Head, "head", "", "")
	fs.StringVar(&s.Record, "record", "", "")
	fs.StringVar(&s.Principal, "principal", "", "")
	issued := fs.String("issued", "", "")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if fs.NArg() > 0 {
		return usageError("statement takes no arguments")
	}
	s.Issued = time.Now().UTC().Truncate(time.Second)
	if *issued != "" {
		t, err := time.Parse(approval.TimeLayout, *issued)
		if err != nil {
			return fmt.Errorf("--issued %q is not written %s", *issued, approval.TimeLayout)
		}
		s.Issued = t
	}
	text, err := s.Encode()
	if err != nil {
		return err
	}
	_, err = stdout.Write(text)
	return err
}

func sign(args []string, stderr io.Writer) error {
	fs := flags("sign")
	keyFile := fs.String("key", "", "")
	piv := fs.Bool("piv", false, "")
	slot := fs.String("slot", "", "")
	reader := fs.String("reader", "", "")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if (*keyFile == "") == !*piv {
		return usageError("sign takes one of --key and --piv")
	}
	if fs.NArg() == 0 {
		return usageError("sign takes at least one statement file")
	}
	files := fs.Args()
	statements := make([]approval.Statement, len(files))
	for i, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if statements[i], err = approval.Parse(text); err != nil {
			return fmt.Errorf("%s %w", f, err)
		}
	}

	var signer approval.Signer
	if *piv {
		p, err := approval.OpenPIV(pivSigner, *slot, *reader, stderr)
		if err != nil {
			return err
		}
		defer p.Close()
		fmt.Fprintf(stderr, "approval: signing %d statement(s) on YubiKey %s\n", len(files), p.PIVSerial())
		signer = p
	} else {
		raw, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		k, err := approval.ParseKey(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", *keyFile, err)
		}
		signer = k
	}
	for i, s := range statements {
		fmt.Fprintf(stderr, "approval: %s: change %s version %d, as %s\n", files[i], s.Change, s.Version, s.Principal)
		n, err := approval.Sign(s, signer)
		if err == nil {
			err = os.WriteFile(files[i]+".note", n, 0o644)
		}
		if err != nil {
			for _, f := range files[i+1:] {
				fmt.Fprintf(stderr, "approval: %s: not signed\n", f)
			}
			return fmt.Errorf("%s: not signed: %w", files[i], err)
		}
	}
	return nil
}

func verify(args []string, stdout io.Writer) error {
	fs := flags("verify")
	signers := fs.String("signers", "", "")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if *signers == "" || fs.NArg() == 0 {
		return usageError("verify takes --signers and at least one note")
	}
	raw, err := os.ReadFile(*signers)
	if err != nil {
		return err
	}
	keys, err := note.ParseVerifierKeys(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", *signers, err)
	}
	if len(keys) == 0 {
		return fmt.Errorf("%s lists no approval key, so no approval verifies", *signers)
	}
	failed := 0
	for _, f := range fs.Args() {
		n, err := os.ReadFile(f)
		if err == nil {
			var s approval.Statement
			if s, err = approval.Verify(n, keys); err == nil {
				fmt.Fprintf(stdout, "%s: %s approves change %s version %d (%s) for %s %s\n",
					f, s.Principal, s.Change, s.Version, s.Head, s.Project, s.Target)
				continue
			}
		}
		fmt.Fprintf(stdout, "%s: refused: %s\n", f, err)
		failed++
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d note(s) did not verify", failed, fs.NArg())
	}
	return nil
}

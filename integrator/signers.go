package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"the-valley/note"
)

// signersRef is where the host publishes what a client verifies this
// integrator's transfer statements with: a tree holding the known-signers
// file the integrator reads, as signersKeys, and the name it signs under,
// as signersName, one line. No push may write it, because the push hook
// refuses every refs/the-valley/* ref it does not name, so the host is its
// only writer.
const (
	signersRef  = "refs/the-valley/signers"
	signersKeys = "known-signers"
	signersName = "integrator-name"
)

const signersUsage = `usage: integrator publish-signers --instance-repo DIR --known-signers F [--name NAME]

Publishes, in the instance repository, the two values a client verifies
this host's transfer statements with: the known-signers file a controller
reads, and the name it signs under. Both go into one tree at
refs/the-valley/signers, so a client reads both from one ref or neither.

  --instance-repo DIR the bare repository clients read the host's
                      publications from
  --known-signers F   the file a controller is given as --known-signers
  --name NAME         the name a controller signs under, as it is given
                      to watch (default: this host's, as watch defaults)

A publication that would not change is not written. One that would is
written with a compare-and-swap on the ref, so two runs at once cannot
interleave. The file must read as verifier keys, and the name must be one
a verifier key can carry. A file that holds no key under the name is
published all the same, because it is what the controller uses, and the
run says on stderr that no client can verify a statement with it.
`

// publishSigners writes the publication, and says what it did on stdout.
func publishSigners(args []string) error {
	fs := flag.NewFlagSet("publish-signers", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, signersUsage) }
	repo := fs.String("instance-repo", "", "the instance repository")
	known := fs.String("known-signers", "", "the file a controller is given as --known-signers")
	name := fs.String("name", defaultSignerName(), "the name a controller signs under")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("publish-signers takes no argument, and was given %q", fs.Arg(0))
	}
	for _, missing := range []struct{ flag, value string }{
		{"--instance-repo", *repo},
		{"--known-signers", *known},
	} {
		if missing.value == "" {
			return fmt.Errorf("%s is required", missing.flag)
		}
	}
	keys, err := os.ReadFile(*known)
	if err != nil {
		return fmt.Errorf("reading the known-signers file: %w", err)
	}
	tree, err := signersTree(*repo, keys, *name)
	if err != nil {
		return err
	}
	old, _ := gitLine(*repo, "rev-parse", "--verify", "--quiet", signersRef)
	if old == tree {
		fmt.Printf("%s already publishes %s and the signers in %s\n", signersRef, *name, *known)
		return nil
	}
	// The value the ref is expected to hold: the one just read, or none at
	// all. An empty old value makes update-ref require that the ref does
	// not exist.
	if _, err := git(*repo, "update-ref", "-m", "publish-signers", signersRef, tree, old); err != nil {
		return err
	}
	fmt.Printf("%s now publishes %s and the signers in %s\n", signersRef, *name, *known)
	return nil
}

// signersTree writes the publication's objects into repo and returns its
// tree.
func signersTree(repo string, keys []byte, name string) (string, error) {
	if err := note.CheckName(name); err != nil {
		return "", fmt.Errorf("--name: %w", err)
	}
	parsed, err := note.ParseVerifierKeys(keys)
	if err != nil {
		return "", fmt.Errorf("the known-signers file does not read: %w", err)
	}
	if len(parsed) == 0 {
		return "", errors.New("the known-signers file holds no verifier key")
	}
	signs := false
	for _, k := range parsed {
		signs = signs || k.Name == name
	}
	if !signs {
		fmt.Fprintf(os.Stderr, "integrator: the known-signers file holds no key under %s, the name the controller signs under, so no client can verify its transfer statements with it\n", name)
	}
	keysBlob, err := hashObject(repo, keys)
	if err != nil {
		return "", err
	}
	nameBlob, err := hashObject(repo, []byte(name+"\n"))
	if err != nil {
		return "", err
	}
	entries := fmt.Sprintf("100644 blob %s\t%s\n100644 blob %s\t%s\n", nameBlob, signersName, keysBlob, signersKeys)
	out, err := gitInput(repo, []byte(entries), "mktree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func hashObject(repo string, body []byte) (string, error) {
	out, err := gitInput(repo, body, "hash-object", "-w", "--stdin")
	return string(bytes.TrimSpace(out)), err
}

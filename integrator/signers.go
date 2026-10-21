package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"the-valley/note"
)

// signersRef is where the host publishes what a client verifies this
// integrator's transfer statements with: a tree holding the known-signers
// file the integrator reads, as signersKeys, and the name it signs under,
// as signersName, one line.
//
// Only the host's own machinery writes it, never a push. The push hook
// refuses every push to a refs/the-valley/* ref it does not name. On the
// host, this publisher writes it. The integrator can write it too: the
// instance repository is group-writable, and the integrator is in the
// group because it lands code there. A client already trusts the
// integrator with what lands, so the publication is not kept from it.
//
// signersLock is the file in the instance repository that a run holds a
// lock on, from reading the signers file to moving the ref.
const (
	signersRef  = "refs/the-valley/signers"
	signersKeys = "known-signers"
	signersName = "integrator-name"
	signersLock = "valley-signers.lock"
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
written with a compare-and-swap on the ref. Two runs at once take turns:
each holds a lock in the instance repository from reading the file to
moving the ref, so a run that read an older file cannot publish it over
a newer one.

The file must read as verifier keys, and the name must be one a verifier
key can carry. A file that holds no key under the name, or no key at all,
is published all the same, because it is what the controller uses. So
when every signing key is revoked, the publication drops the old keys
too, and clients stop verifying with them. The run says on stderr that
no client can verify a statement with the file.
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
	unlock, err := lockSigners(*repo)
	if err != nil {
		return err
	}
	defer unlock()
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
	// not exist. The lock keeps other runs out, and the compare-and-swap
	// keeps this run from writing over anything else on the host that
	// moved the ref since.
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
	// A file with no key at all reads, and is published: it is what the
	// registry compiles to once every signing key is revoked.
	parsed, err := note.ParseVerifierKeys(keys)
	if err != nil {
		return "", fmt.Errorf("the known-signers file does not read: %w", err)
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

// lockSigners takes the publication's lock and returns what lets it go. A
// run that asks while another holds it waits its turn. The lock is
// flock's, on signersLock, so the kernel lets it go when the run ends
// however it ends, and a run that dies leaves nothing to clear.
func lockSigners(repo string) (func(), error) {
	path := filepath.Join(repo, signersLock)
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("could not open the publication's lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("could not lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil // close lets the flock go
}

func hashObject(repo string, body []byte) (string, error) {
	out, err := gitInput(repo, body, "hash-object", "-w", "--stdin")
	return string(bytes.TrimSpace(out)), err
}

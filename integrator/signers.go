package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"the-valley/approval"
	"the-valley/note"
)

// signersRef is where the host publishes what a client verifies this
// integrator's transfer statements with: a tree holding the known-signers
// file the integrator reads, as signersKeys, and the name it signs under,
// as signersName, one line. When the integrator is given a signer projects
// file, the tree holds that file too, as signersProjects, so a client can
// tell which signers' evidence counts in which projects.
//
// The tree also says who may approve, and in which instance: the two
// approval signers files the identity compiler writes, as
// signersApprovers and signersHardware, and the instance's identity, as
// signersInstance, 64 lowercase hex digits and a newline, once the
// instance repository has a main to derive it from. The publication
// informs clients. Whatever enforces approvals reads the host's own files,
// never this snapshot of them.
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
	signersRef      = "refs/the-valley/signers"
	signersKeys     = "known-signers"
	signersName     = "integrator-name"
	signersProjects = "signer-projects"
	signersLock     = "valley-signers.lock"

	signersApprovers = "approval-signers"
	signersHardware  = "approval-signers-hardware"
	signersInstance  = "instance-id"

	// instanceTip is the instance repository's integrated tip, the ref
	// the floor and the registry are read at by default.
	instanceTip = "refs/heads/main"
)

const signersUsage = `usage: integrator publish-signers --instance-repo DIR --known-signers F [--name NAME] [--signer-projects F]
                                  [--approval-signers F] [--approval-signers-hardware F] [--wake DIR]

Publishes, in the instance repository, the two values a client verifies
this host's transfer statements with: the known-signers file a controller
reads, and the name it signs under. Both go into one tree at
refs/the-valley/signers, so a client reads both from one ref or neither.
Given --signer-projects, the tree holds that file too, as signer-projects,
so a client can tell which signers' evidence counts in which projects.

The tree also holds who may approve, in which instance: approval-signers
and approval-signers-hardware, the approval signers files the identity
compiler writes, and instance-id, the instance's identity. The identity
is derived from the root commits of the instance repository's
refs/heads/main, as the approval statement defines it. A repository with
no refs/heads/main publishes no instance-id; a shallow one, or one
missing part of that history, publishes nothing.

  --instance-repo DIR the bare repository clients read the host's
                      publications from
  --known-signers F   the file a controller is given as --known-signers
  --name NAME         the name a controller signs under, as it is given
                      to watch (default: this host's, as watch defaults)
  --signer-projects F the file a controller is given as --signer-projects;
                      without it, the publication holds none
  --approval-signers F
                      the verifier keys of every approval key; without
                      it, the publication's approval-signers is empty
  --approval-signers-hardware F
                      the verifier keys of the hardware approval keys;
                      without it, the publication's
                      approval-signers-hardware is empty
  --wake DIR          the host check worker's wake directory: a run that
                      moves the ref creates the empty file DIR/due, so
                      the worker reads the new signers; empty marks nothing

A publication that would not change is not written. One that would is
written with a compare-and-swap on the ref. After each publication, the
run reads every input again, and publishes again from that reading until
a reading matches what it last published, so a change made up to its
last reading is published before it ends; a later one waits for the next
run. It makes at most one publication more than
the inputs that can change (the known-signers file, the instance's main,
and each other file named), and fails if the inputs are still changing
then. Two runs at once take turns: each holds a lock in the instance
repository from its first reading to its last, so a run that read an
older file cannot publish it over a newer one.

The file must read as verifier keys, the name must be one a verifier key
can carry, and a signer projects file must read as watch reads one, so
it may not name NAME. An approval signers file that is named must read
as verifier keys too; one with no key is published as it is, so the
publication drops revoked approval keys. One file never stands in for
another. A file that holds no key under the name, or no key
at all, is published all the same, because it is what the controller
uses. So when every signing key is revoked, the publication drops the
old keys too, and clients stop verifying with them. The run says on
stderr that no client can verify a statement with the file. An empty
signer projects file restricts nobody, as no file does, and is not
published.
`

// publishSigners writes the publication, and says what it did on stdout.
func publishSigners(args []string) error {
	return publishSignersWith(args, nil)
}

// publishSignersWith is publishSigners with step, when it is not nil,
// called in each round after the round's inputs are read and its tree is
// written, and before the ref moves. Tests change an input there.
func publishSignersWith(args []string, step func(round int)) error {
	fs := flag.NewFlagSet("publish-signers", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, signersUsage) }
	repo := fs.String("instance-repo", "", "the instance repository")
	known := fs.String("known-signers", "", "the file a controller is given as --known-signers")
	name := fs.String("name", defaultSignerName(), "the name a controller signs under")
	projects := fs.String("signer-projects", "", "the file a controller is given as --signer-projects")
	approvers := fs.String("approval-signers", "", "the verifier keys of every approval key")
	hardware := fs.String("approval-signers-hardware", "", "the verifier keys of the hardware approval keys")
	wake := fs.String("wake", "", "the check worker's wake directory, marked due when the publication moves")
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
	in := signersInputs{repo: *repo, known: *known, name: *name, projects: *projects, approvers: *approvers, hardware: *hardware}
	unlock, err := lockSigners(*repo)
	if err != nil {
		return err
	}
	defer unlock()

	// What wakes a run misses a change made while it runs, so a run does
	// not end on what it read at the start. After each publication it
	// reads every input again, and publishes again from that reading until
	// a reading matches what it last published. A change made up to its
	// last reading is published; one made after it waits for whatever
	// starts the next run.
	pub, err := in.read()
	if err != nil {
		return err
	}
	moved, rounds := false, in.rounds()
	var unsettled error
	for round := 1; ; round++ {
		tree, err := signersTree(*repo, pub)
		if err != nil {
			return err
		}
		if step != nil {
			step(round)
		}
		// The ref is read by its exact name. With no publication yet, old
		// is "", whatever branch is named like the ref.
		old, _, err := resolveRef(*repo, signersRef)
		if err != nil {
			return err
		}
		if old == tree {
			fmt.Printf("%s already publishes %s and the signers in %s\n", signersRef, *name, *known)
		} else {
			// The value the ref is expected to hold: the one just read, or
			// none at all. An empty old value makes update-ref require that
			// the ref does not exist. The lock keeps other runs out, and the
			// compare-and-swap keeps this run from writing over anything
			// else on the host that moved the ref since.
			if _, err := git(*repo, "update-ref", "-m", "publish-signers", signersRef, tree, old); err != nil {
				return err
			}
			moved = true
			fmt.Printf("%s now publishes %s and the signers in %s\n", signersRef, *name, *known)
		}
		again, err := in.read()
		if err != nil {
			return err
		}
		if again.equal(pub) {
			break
		}
		if round == rounds {
			unsettled = fmt.Errorf("the inputs kept changing: they had changed again after each of %d publications, so %s may not hold them as they are now", rounds, signersRef)
			break
		}
		fmt.Printf("the inputs changed while %s was published, so it is published again\n", signersRef)
		pub = again
	}
	// The check worker reads this publication for coverage, so a moved
	// one marks it due. A run that published nothing new marks nothing.
	// The ref has moved either way, so a mark that fails is said and is
	// not this run's failure.
	if moved {
		if err := markDue(*wake); err != nil {
			fmt.Printf("the check worker was not marked due: %v\n", err)
		} else if *wake != "" {
			fmt.Println("the check worker is due")
		}
	}
	return unsettled
}

// signersInputs are the files and the repository one run publishes from.
type signersInputs struct {
	repo, known, name, projects, approvers, hardware string
}

// read reads every input, as a publication.
func (in signersInputs) read() (publication, error) {
	keys, err := os.ReadFile(in.known)
	if err != nil {
		return publication{}, fmt.Errorf("reading the known-signers file: %w", err)
	}
	_, restrictions, err := readSignerProjects(in.projects, in.name)
	if err != nil {
		return publication{}, err
	}
	pub := publication{keys: keys, name: in.name, projects: restrictions}
	if pub.approvers, err = readApprovalSigners(in.approvers); err != nil {
		return publication{}, err
	}
	if pub.hardware, err = readApprovalSigners(in.hardware); err != nil {
		return publication{}, err
	}
	if pub.instance, err = instanceID(in.repo); err != nil {
		return publication{}, err
	}
	return pub, nil
}

// rounds is the most publications one run makes. Each input that can
// change while a run reads and publishes may change once and be caught by
// a round of its own: the known-signers file, the instance's main, and
// each other file named. One round more publishes the last of them. A run
// that still reads something new after that is reading inputs that are
// rewritten faster than it publishes, and it fails, so the unit shows it.
func (in signersInputs) rounds() int {
	n := 2
	for _, f := range []string{in.projects, in.approvers, in.hardware} {
		if f != "" {
			n++
		}
	}
	return n + 1
}

// publication is what one run publishes: the known-signers file, the
// signing name, the signer projects file or nothing, both approval
// signers files, and the instance identity, "" when there is none yet.
type publication struct {
	keys                []byte
	name                string
	projects            []byte
	approvers, hardware []byte
	instance            string
}

// equal reports whether two readings publish the same thing.
func (p publication) equal(q publication) bool {
	return bytes.Equal(p.keys, q.keys) && p.name == q.name && bytes.Equal(p.projects, q.projects) &&
		bytes.Equal(p.approvers, q.approvers) && bytes.Equal(p.hardware, q.hardware) && p.instance == q.instance
}

// readApprovalSigners reads an approval signers file and holds it to
// verifier keys. A file that is not named is empty: nothing is configured
// to approve, and publishing nothing is what removes the keys a run
// published before. A file that is named and cannot be read fails the
// run, which leaves the last publication in place.
func readApprovalSigners(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the approval signers file: %w", err)
	}
	if _, err := note.ParseVerifierKeys(data); err != nil {
		return nil, fmt.Errorf("the approval signers file %s does not read: %w", path, err)
	}
	return data, nil
}

// instanceID derives the instance's identity (approval.InstanceID) from
// the instance repository's integrated tip, read once by its exact name:
// its root commits and the repository's object format. It is "" when the
// repository has no tip yet. A shallow repository is refused, since the
// roots it holds need not be its history's, and so is one missing a commit
// of that history, which rev-list cannot walk.
func instanceID(repo string) (string, error) {
	tip, ok, err := resolveRef(repo, instanceTip)
	if err != nil || !ok {
		return "", err
	}
	shallow, err := gitLine(repo, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return "", err
	}
	if shallow != "false" {
		return "", fmt.Errorf("the instance repository %s is shallow, so the root commits of its %s are not known", repo, instanceTip)
	}
	format, err := gitLine(repo, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	roots, err := gitLine(repo, "rev-list", "--max-parents=0", tip)
	if err != nil {
		return "", fmt.Errorf("the history of %s in the instance repository does not read: %w", instanceTip, err)
	}
	id, err := approval.InstanceID(format, strings.Fields(roots))
	if err != nil {
		return "", fmt.Errorf("the instance identity: %w", err)
	}
	return id, nil
}

// signersTree writes the publication's objects into repo and returns its
// tree. The tree holds a signer projects file when there is one, and an
// instance identity when there is one.
func signersTree(repo string, pub publication) (string, error) {
	if err := note.CheckName(pub.name); err != nil {
		return "", fmt.Errorf("--name: %w", err)
	}
	// A file with no key at all reads, and is published: it is what the
	// registry compiles to once every signing key is revoked.
	parsed, err := note.ParseVerifierKeys(pub.keys)
	if err != nil {
		return "", fmt.Errorf("the known-signers file does not read: %w", err)
	}
	signs := false
	for _, k := range parsed {
		signs = signs || k.Name == pub.name
	}
	if !signs {
		fmt.Fprintf(os.Stderr, "integrator: the known-signers file holds no key under %s, the name the controller signs under, so no client can verify its transfer statements with it\n", pub.name)
	}
	type file struct {
		name string
		body []byte
	}
	files := []file{
		{signersName, []byte(pub.name + "\n")},
		{signersKeys, pub.keys},
		{signersApprovers, pub.approvers},
		{signersHardware, pub.hardware},
	}
	if len(pub.projects) > 0 {
		files = append(files, file{signersProjects, pub.projects})
	}
	if pub.instance != "" {
		files = append(files, file{signersInstance, []byte(pub.instance + "\n")})
	}
	var entries strings.Builder
	for _, f := range files {
		blob, err := hashObject(repo, f.body)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&entries, "100644 blob %s\t%s\n", blob, f.name)
	}
	out, err := gitInput(repo, []byte(entries.String()), "mktree")
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

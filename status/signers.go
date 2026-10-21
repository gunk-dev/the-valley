// What a transfer statement is verified with, and where each value comes
// from.
//
// A transfer statement is verified with two values: the known-signers file
// of verifier keys, and the name the integrator signs under. Each is taken
// from the first of these that gives it:
//
//  1. a flag: --known-signers <file> or --integrator-name <name>;
//  2. the environment: VALLEY_KNOWN_SIGNERS or VALLEY_INTEGRATOR_SIGNER;
//  3. the serving host's publication: a tree at refs/the-valley/signers in
//     its instance repository, holding the file as known-signers and the
//     name as integrator-name. Only the host's machinery writes it, never
//     a push: its publisher does, and its integrator, which lands code in
//     that repository, can.
//
// The instance repository is the one --all reads the project list from
// (list.go). Nothing is read until a snapshot verifies its first
// statement, so a command that verifies none reads no more than it did
// before. A value a flag or the environment gives is used whatever the
// host publishes. Before the two values are used they are checked against
// each other: the file must read as verifier keys and hold a key under the
// name. When it does not, the command says why, naming where each value
// came from, and verifies no statement.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"the-valley/note"
)

const (
	signersRef  = "refs/the-valley/signers"
	signersKeys = "known-signers"
	signersName = "integrator-name"
)

// signerVerbs are the verbs that read how far a version has landed, and
// so take --known-signers and --integrator-name.
var signerVerbs = map[string]bool{
	"status": true, "changes": true, "pending": true, "rebase": true,
	"stack": true, "brings": true, "plan": true,
}

// setting is one of the two values, and where it came from: a flag, a
// variable, or, when published, the ref and the repository it is in.
type setting struct {
	value, from string
	published   bool
}

// named is the setting as a message names it, as the kind of value it is.
// A published file is named by where it was published alone, since the
// file read is a temporary copy.
func (s setting) named(kind string) string {
	switch {
	case s.published && kind == "the known-signers file":
		return kind + " published at " + s.from
	case s.published:
		return fmt.Sprintf("%s %s published at %s", kind, s.value, s.from)
	}
	return fmt.Sprintf("%s %s (from %s)", kind, s.value, s.from)
}

// signerSource finds the two values for one run of a command.
type signerSource struct {
	g          git
	stderr     io.Writer
	keys, name setting // what the flags gave

	resolved      bool
	found, signer string // what resolve answered
	temp          string // the published file, written out for attest
}

// verification is the current run's source, which run sets up.
var verification = &signerSource{}

// newVerifier is what a snapshot checks a transfer statement with: the
// values the current run's source finds, once one is needed.
func newVerifier() verifier {
	return verifier{Attest: "attest", Resolve: verification.resolve}
}

// takeSignerFlags removes --known-signers and --integrator-name from args,
// in either the --flag value or the --flag=value form, and returns what
// is left and what they gave.
func takeSignerFlags(args []string) (rest []string, keys, name setting, err error) {
	for i := 0; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], "=")
		var into *setting
		switch flag {
		case "--known-signers":
			into = &keys
		case "--integrator-name":
			into = &name
		default:
			rest = append(rest, args[i])
			continue
		}
		if !inline && i+1 < len(args) {
			i++
			value = args[i]
		}
		if value == "" {
			return nil, keys, name, failure(flag + " needs a value")
		}
		*into = setting{value: value, from: flag}
	}
	return rest, keys, name, nil
}

// resolve is the keys file and the name, or two empty strings when a
// statement cannot be verified. It reads the publication only for a value
// neither a flag nor the environment gives, and answers the same on every
// call.
func (s *signerSource) resolve() (keys, signer string) {
	if s.resolved {
		return s.found, s.signer
	}
	s.resolved = true
	k, n := s.keys, s.name
	if k.value == "" {
		k = setting{value: os.Getenv("VALLEY_KNOWN_SIGNERS"), from: "VALLEY_KNOWN_SIGNERS"}
	}
	if n.value == "" {
		n = setting{value: os.Getenv("VALLEY_INTEGRATOR_SIGNER"), from: "VALLEY_INTEGRATOR_SIGNER"}
	}
	if k.value == "" || n.value == "" {
		p, err := readSigners(s.g)
		if err != nil {
			fmt.Fprintf(s.stderr, "valley: %v\n", err)
		}
		if p != nil && k.value == "" {
			if path, err := s.writeKeys(p.keys); err != nil {
				fmt.Fprintf(s.stderr, "valley: could not write out the known-signers file published at %s: %v\n", p.where, err)
			} else {
				k = setting{value: path, from: p.where, published: true}
			}
		}
		if p != nil && n.value == "" {
			n = setting{value: p.name, from: p.where, published: true}
		}
	}
	if k.value == "" || n.value == "" {
		return "", ""
	}
	if err := agree(k, n); err != nil {
		fmt.Fprintf(s.stderr, "valley: %v, so no transfer statement is verified\n", err)
		return "", ""
	}
	s.found, s.signer = k.value, n.value
	return s.found, s.signer
}

// agree checks the two values against each other: the name must be one a
// verifier key can carry, and the file must read as verifier keys and hold
// one under that name.
func agree(keys, name setting) error {
	file, signer := keys.named("the known-signers file"), name.named("the integrator name")
	if err := note.CheckName(name.value); err != nil {
		return fmt.Errorf("%s is no signer's name: %v", signer, err)
	}
	data, err := os.ReadFile(keys.value)
	if err != nil {
		return fmt.Errorf("%s cannot be read: %v", file, err)
	}
	parsed, err := note.ParseVerifierKeys(data)
	if err != nil {
		return fmt.Errorf("%s does not read as verifier keys: %v", file, err)
	}
	for _, v := range parsed {
		if v.Name == name.value {
			return nil
		}
	}
	return fmt.Errorf("%s holds no key under %s", file, signer)
}

// writeKeys writes the published file where attest can read it, for done
// to remove.
func (s *signerSource) writeKeys(data []byte) (string, error) {
	f, err := os.CreateTemp("", "valley-known-signers-*")
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	s.temp = f.Name()
	return s.temp, nil
}

// done removes what resolve wrote.
func (s *signerSource) done() {
	if s.temp != "" {
		os.Remove(s.temp)
		s.temp = ""
	}
}

// publishedSigners is the serving host's publication, and where it was
// read.
type publishedSigners struct {
	keys  []byte
	name  string
	where string
}

// readSigners reads the serving host's publication. It is nil, with no
// error, when there is none: origin's main names no instance repository,
// or the instance publishes nothing at refs/the-valley/signers. A
// publication that cannot be read, or does not read as one, is an error.
func readSigners(g git) (*publishedSigners, error) {
	instance, err := instanceOf(g)
	var none noInstance
	if errors.As(err, &none) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not find the instance repository to read %s from: %v", signersRef, err)
	}
	g = g.restricted()
	oid, err := remoteRef(g, instance, signersRef)
	if err != nil {
		return nil, fmt.Errorf("could not read %s in the instance repository %s", signersRef, instance)
	}
	if oid == "" {
		return nil, nil
	}
	if err := fetchFrom(g, instance, signersRef, oid); err != nil {
		return nil, err
	}
	p := &publishedSigners{where: signersRef + " in " + instance}
	listing, err := g.output(true, "ls-tree", "-z", oid)
	if err != nil {
		return nil, fmt.Errorf("%s is not a tree", p.where)
	}
	blobs := map[string]string{}
	for _, entry := range strings.Split(listing, "\x00") {
		meta, file, _ := strings.Cut(entry, "\t")
		if f := strings.Fields(meta); len(f) == 3 && f[1] == "blob" {
			blobs[file] = f[2]
		}
	}
	if blobs[signersKeys] == "" || blobs[signersName] == "" {
		return nil, fmt.Errorf("%s holds no %s or no %s", p.where, signersKeys, signersName)
	}
	keys, err := g.output(true, "cat-file", "blob", blobs[signersKeys])
	if err != nil {
		return nil, fmt.Errorf("could not read %s in %s", signersKeys, p.where)
	}
	p.keys = []byte(keys + "\n")
	if p.name, err = g.output(true, "cat-file", "blob", blobs[signersName]); err != nil {
		return nil, fmt.Errorf("could not read %s in %s", signersName, p.where)
	}
	if strings.Contains(p.name, "\n") {
		return nil, fmt.Errorf("%s in %s is more than one line", signersName, p.where)
	}
	return p, nil
}

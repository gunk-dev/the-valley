package main

// The inputs a verdict reads besides the request, the target and the
// attestation refs, and the record that remembers them.
//
// The outcome record keys a standing answer by the request's head, the
// target's tip and the evidence fingerprint. A verdict also reads the
// floor and which signers count, and either can change while those three
// stay where they are: the registry accepts a signer, or the group lands
// a floor that requires less. So the integrator remembers what it read of
// them too, in a record of its own beside the outcome record, and an
// answer stands only while both records match the pass.
//
// The inputs live in their own record, not in the outcome record, because
// the outcome record is read outside the controller (rebase/outcome.go),
// and its readers compare its evidence fingerprint with one they compute
// from the attestation refs alone. Neither the floor nor the
// known-signers file is something they can read.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// inputsPrefix is where the inputs records live, one per request, named as
// its outcome record is.
const inputsPrefix = "refs/the-valley/integration-inputs"

// unreadInputs is what a pass records as its inputs when one of them did
// not read. It is not a digest, so it never equals the inputs of a pass
// that read them all. Only a failure stands under it.
const unreadInputs = "unread"

// inputs is what one pass read of the floor and the accepted signers.
type inputs struct {
	// digest is the fingerprint the inputs record holds, or unreadInputs.
	digest string
	// floor is the floor's documents as a directory of *.cue, opened once
	// for the pass, so the verdict composes the documents digest was
	// taken over. "" when the floor did not read.
	floor string
	// err is why an input did not read. A request it would have judged
	// fails instead.
	err error
}

// inputState reads the inputs for one pass, and returns what ends the
// pass's hold on the floor. The digest is SHA-256 over these lines:
//
//   - floor <name> <sha256>, for every *.cue document of the floor, the
//     same documents the verdict composes, by name;
//   - known-signers <sha256> of the known-signers file as it reads now;
//   - signer-projects <sha256> of the signer projects file as read at
//     start, or signer-projects none when there is none;
//   - project <name> and signer <name>, which say where a signer counts
//     and which signature is the integrator's own.
//
// The floor and the known-signers file are read before any request is
// judged. The verdict reads the floor from the copy taken here. attest
// reads the known-signers file again when it verifies a note, so a file
// rewritten during the pass may be judged at its new bytes and recorded
// at its old ones. The next pass then reads a different digest and judges
// again; the reverse, an old answer recorded under new bytes, cannot
// happen.
func (in *integrator) inputState() (inputs, func()) {
	h := sha256.New()
	floor, done, err := in.policy.layer.Open(context.Background(), "valley-integrator-floor")
	if err != nil {
		return inputs{digest: unreadInputs, err: err}, done
	}
	// Open refuses a floor with no *.cue in it, so docs is never empty.
	docs, err := filepath.Glob(filepath.Join(floor, "*.cue"))
	sort.Strings(docs)
	for _, doc := range docs {
		var data []byte
		if data, err = os.ReadFile(doc); err != nil {
			break
		}
		fmt.Fprintf(h, "floor %s %x\n", filepath.Base(doc), sha256.Sum256(data))
	}
	if err != nil {
		done()
		return inputs{digest: unreadInputs, err: fmt.Errorf("reading the floor: %w", err)}, func() {}
	}
	keys, err := os.ReadFile(in.knownSigners)
	if err != nil {
		done()
		return inputs{digest: unreadInputs, err: fmt.Errorf("reading the known-signers file: %w", err)}, func() {}
	}
	fmt.Fprintf(h, "known-signers %x\n", sha256.Sum256(keys))
	if in.signerProjectsData == nil {
		fmt.Fprintf(h, "signer-projects none\n")
	} else {
		fmt.Fprintf(h, "signer-projects %x\n", sha256.Sum256(in.signerProjectsData))
	}
	fmt.Fprintf(h, "project %s\nsigner %s\n", in.project, in.signerName)
	return inputs{digest: hex.EncodeToString(h.Sum(nil)), floor: floor}, done
}

func (in *integrator) inputsRef(r request) string {
	return inputsPrefix + "/" + strings.TrimPrefix(r.ref, requestPrefix+"/")
}

// readInputs reads the inputs the request's outcome record was written
// under, or "" when that cannot be told. The inputs record names the blob
// of the outcome record it was written with. An outcome record written
// without one, as a landing's, a voiding's, or one from before inputs
// were remembered, leaves the inputs record naming another blob, and
// that reads as "".
func (in *integrator) readInputs(r request) string {
	outcome, ok, err := resolveRef(in.repo, in.memoRef(r))
	if err != nil || !ok {
		return ""
	}
	oid, ok, err := resolveRef(in.repo, in.inputsRef(r))
	if err != nil || !ok {
		return ""
	}
	out, ok := gitTry(in.repo, "cat-file", "blob", oid)
	if !ok {
		return ""
	}
	digest, bound, ok := strings.Cut(strings.TrimSpace(out), " ")
	if !ok || bound != outcome || digest == "" {
		return ""
	}
	return digest
}

// writeAnswer writes a request's outcome record and its inputs record in
// one ref transaction, so neither moves without the other.
func (in *integrator) writeAnswer(r request, value, digest string) error {
	outcome, err := in.writeBlob([]byte(value + "\n"))
	if err != nil {
		return err
	}
	record, err := in.writeBlob([]byte(digest + " " + outcome + "\n"))
	if err != nil {
		return err
	}
	stdin := fmt.Sprintf("start\nupdate %s %s\nupdate %s %s\nprepare\ncommit\n",
		in.memoRef(r), outcome, in.inputsRef(r), record)
	_, err = gitInput(in.repo, []byte(stdin), "update-ref", "--stdin")
	return err
}

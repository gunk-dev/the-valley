package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The statement of dcr-0de694f, in the shape schema/attestation.cue fixes.
// The schema is the specification; these types exist to compose a document
// that satisfies it, and every composed statement is vetted against the
// schema before anything is done with it.

const (
	statementType   = "the-valley/attestation/v1"
	predicatePure   = "the-valley/check/pure/v1"
	predicateEffect = "the-valley/check/effectful/v1"
	refPrefix       = "refs/the-valley/attestations"

	// predicateTransfer is the integrator's commit-point claim
	// (dcr-439b771): evidence produced over one tree stands for another.
	// attest never composes one — the integrator does — but the written
	// form of a statement is defined once, here, so that both programs
	// sign the same bytes.
	predicateTransfer = "the-valley/integration/transfer/v1"

	// signerSuffix completes a host's signer name. A note names its key by
	// a string and a hash, and the string a valley host uses is its fully
	// qualified name, a slash, and what it is signing: laddie.gunk.dev
	// attesting is "laddie.gunk.dev/attestations". The host part says
	// which machine to go and ask about a key; the suffix keeps a key
	// signing attestations distinct as a verifier key from the same key
	// signing anything else, because the name is inside the key hash.
	signerSuffix = "/attestations"
)

// defaultSignerName is the name this machine signs under: its own name and
// the suffix. A machine whose hostname is not its fully qualified name
// produces a name that is still unique to it and still says which machine
// it was, and --name is how a host that knows better says so.
func defaultSignerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "localhost" + signerSuffix
	}
	return host + signerSuffix
}

type statement struct {
	StatementType string          `json:"statementType"`
	Subject       subject         `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     predicate       `json:"predicate"`
	Provenance    json.RawMessage `json:"provenance,omitempty"`
}

// subject is the digest set of dcr-0de694f: the content-addressed tree
// digest under `primary`, and whatever advisory names accompany it.
type subject struct {
	Primary string            `json:"primary"`
	Digest  map[string]string `json:"digest"`
}

type predicate struct {
	Check  checkRun `json:"check"`
	Result string   `json:"result"`

	// Pure only — what a verifier re-derives against.
	Inputs     map[string]string `json:"inputs,omitempty"`
	Derivation map[string]string `json:"derivation,omitempty"`
	Output     map[string]string `json:"output,omitempty"`

	// Effectful only — what the notarization is a notarization of.
	Environment string `json:"environment,omitempty"`
	ObservedAt  string `json:"observedAt,omitempty"`
}

type checkRun struct {
	Name      string `json:"name"`
	Runner    string `json:"runner"`
	Attribute string `json:"attribute,omitempty"`
	Command   string `json:"command,omitempty"`
}

// loadProvenance reads the caller-supplied provenance for a run: the
// harness, the model, digests of the prompt and the context, and the
// delegation chain as recorded. Nothing in this repository collects any of
// it today, so it arrives from whoever runs attest or not at all.
//
// The bytes are carried into the statement without being interpreted here.
// #Provenance in schema/attestation.cue says what the field may hold, and
// vetting the composed statement is what enforces it — a second opinion in
// Go would be a second specification to keep in step.
func loadProvenance(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%s is not json", path)
	}
	return json.RawMessage(raw), nil
}

// vetStatement validates a statement, as JSON, against
// schema/attestation.cue. Nothing is signed, stored or pushed before this
// passes: a statement no verifier could read is not a thing to publish.
//
// JSON is what cue reads, and the written form of a statement carries
// exactly what its JSON does, so vetting one settles the other. Composing
// runs the gate before writing the statement out; verifying runs it after
// reading the statement back.
func vetStatement(schema string, doc []byte) error {
	dir, err := os.MkdirTemp("", "valley-attest-vet")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "statement.json"), doc, 0o644); err != nil {
		return err
	}
	// Run in the temporary directory, so cue's errors point at
	// "./statement.json" rather than at a path the reader never sees.
	cmd := exec.Command("cue", "vet", "-c", schema, "statement.json")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("statement does not satisfy %s:\n%s", schema, strings.TrimSpace(string(out)))
	}
	return nil
}

// schemaPath resolves the attestation schema: the flag, else the path the
// packaged wrapper sets, else the schema in the repository being attested.
func schemaPath(flag, repo string) (string, error) {
	for _, p := range []string{flag, os.Getenv("VALLEY_ATTEST_SCHEMA"), filepath.Join(repo, "schema", "attestation.cue")} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return filepath.Abs(p)
		}
	}
	return "", fmt.Errorf("no attestation schema found; pass --schema or set VALLEY_ATTEST_SCHEMA")
}

// attestationRef is where a note is stored: a ref keyed by the subject
// digest, then by the key hash of the signer that wrote it. The key hash
// segment is what lets several hosts publish about one subject without
// racing — each writes a ref only it writes, which an integrator can hold
// create-only — and it is the same eight hex digits a reader sees in that
// host's verifier key. It names which key wrote the ref and is not itself
// evidence of anything; two hosts colliding on four bytes lose a
// create-only push and forge nothing, because what is checked is the note.
func attestationRef(subjectDigest, keyHash string) string {
	return fmt.Sprintf("%s/%s/%s", refPrefix, subjectDigest, keyHash)
}

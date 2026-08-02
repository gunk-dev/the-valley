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
	statementType    = "the-valley/attestation/v1"
	predicatePure    = "the-valley/check/pure/v1"
	predicateEffect  = "the-valley/check/effectful/v1"
	defaultNamespace = "the-valley.attestation.v1"
	refPrefix        = "refs/the-valley/attestations"
)

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

// vetStatement validates canonical statement bytes against
// schema/attestation.cue. Nothing is signed, stored or pushed before this
// passes: a statement no verifier could read is not a thing to publish.
func vetStatement(schema string, canonical []byte) error {
	dir, err := os.MkdirTemp("", "valley-attest-vet")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "statement.json"), canonical, 0o644); err != nil {
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

// attestationRef is where a statement is stored: a ref keyed by the
// subject digest, then by the signer. The signer segment is what lets
// several parties attest to one subject side by side — each writes a ref
// only it writes, which an integrator can hold create-only.
func attestationRef(subjectDigest, signer string) string {
	return fmt.Sprintf("%s/%s/%s", refPrefix, subjectDigest, signer)
}

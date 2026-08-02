package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// cmdVerify checks an attestation: that the statement satisfies the
// schema, that its bytes are the canonical form of what it says, that an
// allowed signer signed exactly those bytes, and that the tree the
// statement names is the tree in front of the verifier.
//
// Re-running a pure check and confirming its output digest is deliberately
// not here. That is witness re-verification, and it belongs to the trust
// backstop of a later phase; what this verifies is the claim's
// authenticity and its binding to a tree.
func cmdVerify(args []string) error {
	fs := newFlagSet("verify")
	statementFile := fs.String("statement", "", "the statement")
	signatureFile := fs.String("signature", "", "its detached signature")
	allowed := fs.String("allowed-signers", "", "ssh allowed-signers file")
	principal := fs.String("principal", "", "expected signer")
	namespace := fs.String("namespace", defaultNamespace, "SSHSIG namespace")
	schemaFlag := fs.String("schema", "", "attestation schema")
	repo := fs.String("repo", "", "repository to re-check against")
	rev := fs.String("rev", "HEAD", "revision in it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for name, v := range map[string]string{"--statement": *statementFile, "--signature": *signatureFile, "--allowed-signers": *allowed} {
		if v == "" {
			return fmt.Errorf("%s is required", name)
		}
	}

	raw, err := os.ReadFile(*statementFile)
	if err != nil {
		return err
	}
	fmt.Printf("statement %s\n", *statementFile)

	root := ""
	if *repo != "" {
		if root, err = repoRoot(*repo); err != nil {
			return err
		}
	}
	schema, err := schemaPath(*schemaFlag, root)
	if err != nil {
		return err
	}
	if err := vetStatement(schema, raw); err != nil {
		return err
	}
	fmt.Printf("schema    ok (%s)\n", schema)

	// The signature covers bytes, and canonical form is what makes those
	// bytes a function of the statement. A statement stored in some other
	// rendering would verify here and fail to verify wherever it was
	// recomposed, so it is rejected.
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return fmt.Errorf("%s: %w", *statementFile, err)
	}
	canonical, err := canonicalJSON(generic)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, raw) {
		return fmt.Errorf("%s is not in canonical form; the signature would not travel", *statementFile)
	}
	fmt.Printf("canonical ok (%d bytes signed)\n", len(raw))

	who := *principal
	if who == "" {
		if who, err = findPrincipal(*allowed, *signatureFile); err != nil {
			return err
		}
	}
	said, err := verifyDetached(*allowed, who, *namespace, *signatureFile, raw)
	if err != nil {
		return fmt.Errorf("signature: %s", err)
	}
	fmt.Printf("signature %s\n", said)

	var st statement
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	recorded := st.Subject.Digest[st.Subject.Primary]
	fmt.Printf("subject   %s:%s\n", st.Subject.Primary, recorded)
	fmt.Printf("predicate %s (%s %s: %s)\n", st.PredicateType, st.Predicate.Check.Runner, st.Predicate.Check.Name, st.Predicate.Result)

	if root == "" {
		fmt.Printf("tree      not re-checked; pass --repo to bind this statement to a tree\n")
		return nil
	}
	entries, err := gitTreeEntries(root, *rev)
	if err != nil {
		return err
	}
	got := treeDigest(entries)
	fmt.Printf("tree      %s@%s %s:%s\n", root, *rev, treeScheme, got)
	if got != recorded {
		return fmt.Errorf("tree digest mismatch: the statement is about a different tree\n  recorded %s\n  found    %s", recorded, got)
	}
	fmt.Printf("          matches the recorded subject digest\n")
	return nil
}

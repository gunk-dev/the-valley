package main

import (
	"fmt"
	"strings"
)

// publish pushes the branch and the signer's attestation ref to remote in
// one atomic push, or the ref alone when branchSpec is "". notes holds
// this run's notes, one subtree per check.
//
// The remote takes an existing attestation ref only with added notes: an
// update keeps every note the ref holds, byte for byte. So when the remote
// already holds ref, the tree pushed is the one it holds with this run's
// notes beside it, and the local ref is moved to that tree. A check the
// held tree already has a note for keeps that note, and this run's note
// for it is not published. The push leases ref at the value read, so a
// writer that moved it meanwhile is never overwritten; the ref is then
// read again and the push tried once more.
func publish(root, remote, branchSpec, ref string, notes map[string]string) error {
	at := pushURL(root, remote)
	held, err := remoteRef(root, at, ref)
	if err != nil {
		return err
	}
	err = publishOnto(root, remote, at, branchSpec, ref, held, notes)
	if err == nil {
		return nil
	}
	now, rerr := remoteRef(root, at, ref)
	if rerr != nil || now == held {
		return err
	}
	fmt.Printf("moved   %s on %s while this run published; read again\n", ref, remote)
	return publishOnto(root, remote, at, branchSpec, ref, now, notes)
}

// pushURL is where a push to remote goes, so the ref is read from the
// repository it is pushed to: a configured remote's push URL, or remote
// itself when it is a URL or a path.
func pushURL(root, remote string) string {
	out, err := gitRemote(root, "remote", "get-url", "--push", remote)
	if url := strings.TrimSpace(out); err == nil && url != "" {
		return url
	}
	return remote
}

// publishOnto pushes the branch and ref, as held on the remote ("" for
// none) with notes added to it. at is the remote's push URL, which held is
// fetched from.
func publishOnto(root, remote, at, branchSpec, ref, held string, notes map[string]string) error {
	entries, added := []string{}, 0
	if held != "" {
		if _, err := gitRemote(root, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", at, held); err != nil {
			return err
		}
		listing, err := git(root, "ls-tree", held)
		if err != nil {
			return err
		}
		entries = strings.Split(strings.TrimSuffix(listing, "\n"), "\n")
	}
	has := map[string]bool{}
	for _, e := range entries {
		if _, name, ok := strings.Cut(e, "\t"); ok {
			has[name] = true
		}
	}
	for _, check := range sortedKeys(notes) {
		if has[check] {
			fmt.Printf("held    %s: %s already holds this signer's note for it; nothing added\n", check, remote)
			continue
		}
		entries = append(entries, fmt.Sprintf("040000 tree %s\t%s", notes[check], check))
		added++
	}

	args := []string{"--atomic"}
	var specs []string
	if branchSpec != "" {
		specs = append(specs, branchSpec)
	}
	if added > 0 {
		tree, err := gitInput(root, []byte(strings.Join(entries, "\n")+"\n"), "mktree")
		if err != nil {
			return err
		}
		tree = strings.TrimSpace(tree)
		if held != "" {
			if _, err := git(root, "update-ref", ref, tree); err != nil {
				return err
			}
			fmt.Printf("added   %d of %d notes to %s on %s -> %s\n", added, len(notes), ref, remote, tree)
		}
		// The tree is pushed by its id: another run may move the local
		// ref before this push reads it.
		args = append(args, "--force-with-lease="+ref+":"+held)
		specs = append(specs, tree+":"+ref)
	}
	if len(specs) == 0 {
		return nil // the remote holds every note this run made
	}
	args = append(append(args, remote), specs...)
	out, err := gitRemote(root, append([]string{"push"}, args...)...)
	if err != nil {
		return err
	}
	fmt.Printf("pushed  %s\n%s", strings.Join(args, " "), out)
	return nil
}

// remoteRef reads ref's value on remote, or "" when the remote has none.
func remoteRef(root, remote, ref string) (string, error) {
	out, err := gitRemote(root, "ls-remote", "--refs", remote, ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if id, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return id, nil
		}
	}
	return "", nil
}

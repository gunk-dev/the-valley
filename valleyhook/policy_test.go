package main

import (
	"errors"
	"strings"
	"testing"

	"the-valley/approval"
	"the-valley/change"
	"the-valley/knot"
)

// fakeRepository holds no objects.
type fakeRepository struct{}

func (f fakeRepository) symbolic([]string) (map[string]bool, error) { return nil, nil }
func (f fakeRepository) notes(string) (map[string][]byte, error) {
	return nil, errors.New("holds no objects")
}
func (f fakeRepository) record(string) (fileCommit, error) {
	return fileCommit{}, errors.New("holds no objects")
}
func (f fakeRepository) report(string) (fileCommit, error) {
	return fileCommit{}, errors.New("holds no objects")
}
func (f fakeRepository) approval(string) ([]byte, error) {
	return nil, errors.New("holds no objects")
}
func (f fakeRepository) hasRecord(string) (bool, error) {
	return false, errors.New("holds no refs")
}
func (f fakeRepository) ref(string) (string, bool, error)  { return "", false, nil }
func (f fakeRepository) kind(string) (string, error)       { return "", errors.New("holds no objects") }
func (f fakeRepository) records() (int, error)             { return 0, nil }
func (f fakeRepository) checkRequests(string) (int, error) { return 0, nil }
func (f fakeRepository) graph() (graphReader, error)       { return nil, errors.New("holds no objects") }

// Every rule that applies to a ref has to allow the write, and none of
// them can be satisfied by another. Each case here is a declaration that
// tries to stand in for a rule it is not: a writer list covering a
// namespace whose rule is a grant or a prohibition, a grant covering one
// whose rule is fixed, and a ref name standing for another ref.
func TestNoRuleStandsInForAnother(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	const some = "1111111111111111111111111111111111111111"
	p := policy{
		project: "everything",
		push: pushPolicy{
			// Every ref the project has is protected, and the writer is
			// also opened every ref by a grant.
			Refs:    []string{"refs/*"},
			Writers: []string{"writer"},
			Grants: map[string]refGrant{
				"everything": {Refs: []string{"refs/*"}, Writers: []string{"writer", "opened"}},
			},
		},
		grants:  grants{"requester": {"request": true}},
		repo:    fakeRepository{},
		symrefs: map[string]bool{"refs/heads/alias": true},
	}
	for _, c := range []struct {
		who, ref string
		allowed  bool
		says     string
	}{
		// Prohibited outright, to the writer the grant and the protection
		// both name.
		{"writer", "refs/replace/" + some, false, "refs/replace/ is closed to every push"},
		{"writer", "refs/notes/commits", false, "refs/notes/ is closed to every push"},
		{"writer", "refs/the-valley/integration-outcomes/main/c", false, "written only by the machinery"},
		{"writer", "refs/the-valley/integration-landings/main/" + some, false, "written only by the machinery"},
		// A symbolic ref is refused whoever writes it.
		{"writer", "refs/heads/alias", false, "symbolic ref"},
		// The approve grant is needed whatever else is declared, and
		// protection is needed on top of it.
		{"writer", "refs/the-valley/integration-requests/main/c", false, "approve grant (also accepted as request)"},
		{"requester", "refs/the-valley/integration-requests/main/c", false, "a protected ref"},
		// A tag takes a grant, and protection on top: being opened the
		// ref does not make a principal its writer.
		{"writer", "refs/tags/v1", true, ""},
		{"opened", "refs/tags/v1", false, "a protected ref"},
		{"requester", "refs/tags/v1", false, "opens only through a named grant"},
		// An attestation named for a digest alone is refused before
		// anything is read: it would block every ref beneath it.
		{"writer", "refs/the-valley/attestations/" + strings.Repeat("a", 64), false, "<digest>/<key hash>"},
		// The plan graph is one ref, matched exactly. A ref that only
		// begins with its name is in the closed namespace, and the graph
		// takes no write from a key that names no principal, before
		// anything is read.
		{"writer", knot.Ref + "-x", false, "written only by the machinery"},
		{"writer", knot.Ref + "/x", false, "written only by the machinery"},
		{"", knot.Ref, false, "takes writes only from a named principal"},
	} {
		got := p.decide(c.who, update{old: zero, new: some, ref: c.ref})
		if (got == "") != c.allowed || !strings.Contains(got, c.says) {
			t.Errorf("%s writing %s: got %q, want allowed=%v saying %q", c.who, c.ref, got, c.allowed, c.says)
		}
	}
}

// The plan graph is never deleted, by anyone, before anything is read.
func TestNoPushDeletesThePlanGraph(t *testing.T) {
	p := policy{project: "p", repo: fakeRepository{}}
	got := p.decide("writer", update{old: "1111111111111111111111111111111111111111", new: "0000000000000000000000000000000000000000", ref: knot.Ref})
	if !strings.Contains(got, "may not delete refs/the-valley/knots") || !strings.Contains(got, "never deleted") {
		t.Errorf("a delete of the graph: %q", got)
	}
}

// refs/the-valley/flow/data, matched exactly, opens only through a named
// grant of the project. Every neighbouring ref, case variants included,
// stays in the closed machinery namespace even under a broad grant,
// lookalikes stay refused, and holding the grant confers no other power.
func TestFlowDataRef(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	const one = "1111111111111111111111111111111111111111"
	const two = "2222222222222222222222222222222222222222"
	id := strings.Repeat("k", 32)

	// An exact grant on a project whose main is protected with no writers.
	exact := policy{
		project: "app",
		push: pushPolicy{
			Refs: []string{"refs/heads/main"},
			Grants: map[string]refGrant{
				"flow": {Refs: []string{flowDataRef}, Writers: []string{"agent"}},
			},
		},
		repo: fakeRepository{},
	}
	// The same project with no grants at all.
	noGrant := policy{
		project: "app",
		push:    pushPolicy{Refs: []string{"refs/heads/main"}},
		repo:    fakeRepository{},
	}
	// A project where the ref is symbolic.
	symref := policy{
		project: "app",
		push:    exact.push,
		repo:    fakeRepository{},
		symrefs: map[string]bool{flowDataRef: true},
	}
	// Protection covering refs/the-valley/flow/data adds a writer check on top.
	protected := policy{
		project: "app",
		push: pushPolicy{
			Refs:    []string{"refs/*"},
			Writers: []string{"writer"},
			Grants: map[string]refGrant{
				"flow": {Refs: []string{flowDataRef}, Writers: []string{"agent", "writer"}},
			},
		},
		repo: fakeRepository{},
	}

	for _, c := range []struct {
		name    string
		p       policy
		who     string
		u       update
		allowed bool
		says    string
	}{
		// Granted principal creates, updates (including non-fast-forward), and deletes the exact ref.
		{"exact grant create", exact, "agent", update{zero, one, flowDataRef}, true, ""},
		{"exact grant update", exact, "agent", update{one, two, flowDataRef}, true, ""},
		{"exact grant delete", exact, "agent", update{one, zero, flowDataRef}, true, ""},

		// Symbolic ref is still refused even for a grant holder.
		{"symbolic ref refused", symref, "agent", update{one, two, flowDataRef}, false, "symbolic ref"},

		// Ungranted and untagged principals are refused with the named-grant wording.
		{"ungranted principal with grant present", exact, "other", update{zero, one, flowDataRef}, false,
			"refs/the-valley/flow/ opens only through a named grant, and the grant flow of app opens this ref to agent only"},
		{"untagged key with grant present", exact, "", update{zero, one, flowDataRef}, false,
			"<untagged key> may not write refs/the-valley/flow/data — refs/the-valley/flow/ opens only through a named grant, and the grant flow of app opens this ref to agent only"},
		{"named principal with no grant", noGrant, "agent", update{zero, one, flowDataRef}, false,
			"refs/the-valley/flow/ opens only through a named grant, and no grant of app opens this ref"},
		{"untagged key with no grant", noGrant, "", update{zero, one, flowDataRef}, false,
			"<untagged key> may not write refs/the-valley/flow/data — refs/the-valley/flow/ opens only through a named grant, and no grant of app opens this ref"},

		// Holding the flow grant confers nothing else.
		{"flow grant does not open protected main", exact, "agent", update{zero, one, "refs/heads/main"}, false, "a protected ref of app"},
		{"flow grant does not open requests", exact, "agent", update{zero, one, requestPrefix + "main/c"}, false, approveGrant},
		{"flow grant does not open approvals", exact, "agent", update{zero, one, approval.RefPrefix + "main/c-" + id}, false, approveGrant},
		{"flow grant does not open restack reports", exact, "agent", update{zero, one, change.RestackPrefix + "main/" + id}, false, "restack grant"},
		{"flow grant does not delete change records", exact, "agent", update{one, zero, change.RefPrefix + "main/" + id}, false, approveGrant},
		{"flow grant does not open tags", exact, "agent", update{zero, one, "refs/tags/v1"}, false, "refs/tags/ opens only through a named grant"},

		// Protection adds on top of the grant.
		{"protected flow ref refuses non-writer grant holder", protected, "agent", update{zero, one, flowDataRef}, false, "a protected ref of app"},
		{"protected flow ref allows writer grant holder", protected, "writer", update{zero, one, flowDataRef}, true, ""},
	} {
		got := c.p.decide(c.who, c.u)
		if (got == "") != c.allowed || !strings.Contains(got, c.says) {
			t.Errorf("%s (%s on %s): got %q, want allowed=%v saying %q", c.name, c.who, c.u.ref, got, c.allowed, c.says)
		}
	}

	// A broad grant opens the exact ref and no other ref under
	// refs/the-valley/. refs/* is the broad pattern a host can declare;
	// the schema refuses refs/the-valley/*, and the hook holds without it.
	const machinery = "written only by the machinery on the host, and no push may write it"
	for _, pattern := range []string{"refs/*", "refs/the-valley/*"} {
		broad := policy{
			project: "app",
			push: pushPolicy{
				Refs: []string{"refs/heads/main"},
				Grants: map[string]refGrant{
					"broad": {Refs: []string{pattern}, Writers: []string{"broad"}},
				},
			},
			repo: fakeRepository{},
		}
		for _, c := range []struct {
			name    string
			u       update
			allowed bool
			says    string
		}{
			{"opens exact ref", update{zero, one, flowDataRef}, true, ""},
			{"refused on prefix slash", update{zero, one, "refs/the-valley/flow/"}, false, machinery},
			{"refused on prefix bare", update{zero, one, "refs/the-valley/flow"}, false, machinery},
			{"refused on sibling", update{zero, one, "refs/the-valley/flow/other"}, false, machinery},
			{"refused on child", update{zero, one, "refs/the-valley/flow/data/x"}, false, machinery},
			{"refused on suffixed name", update{zero, one, "refs/the-valley/flow/data2"}, false, machinery},
			{"refused on dashed suffix", update{zero, one, "refs/the-valley/flow/data-x"}, false, machinery},
			{"refused on case variant of flow", update{zero, one, "refs/the-valley/Flow/data"}, false, machinery},
			{"refused on case variant of data", update{zero, one, "refs/the-valley/flow/Data"}, false, machinery},
			{"refused on outcomes", update{zero, one, "refs/the-valley/integration-outcomes/main/c"}, false, machinery},
			{"refused on signers", update{zero, one, "refs/the-valley/signers"}, false, machinery},

			// Nor does it bypass the other fixed namespaces under refs/the-valley/.
			{"does not open requests", update{zero, one, requestPrefix + "main/c"}, false, approveGrant},
			{"does not open approvals", update{zero, one, approval.RefPrefix + "main/c-" + id}, false, approveGrant},
			{"does not open restack reports", update{zero, one, change.RestackPrefix + "main/" + id}, false, "restack grant"},
			{"does not delete knots", update{one, zero, knot.Ref}, false, "never deleted"},

			// Lookalikes of refs/the-valley/flow/data stay refused on create and update.
			{"lookalike under refs/heads/", update{zero, one, "refs/heads/" + flowDataRef}, false, "no push may create or update a ref named refs/heads/ followed by a full ref name"},
			{"lookalike under refs/tags/", update{zero, one, "refs/tags/" + flowDataRef}, false, "no push may create or update a ref named refs/tags/ followed by a full ref name"},
			{"lookalike under refs/", update{zero, one, "refs/" + flowDataRef}, false, "no push may create or update a ref named refs/ followed by a full ref name"},
			{"lookalike under refs/remotes/", update{zero, one, "refs/remotes/" + flowDataRef}, false, "no push may create or update a ref named refs/remotes/ followed by a full ref name"},
			{"lookalike under refs/remotes/ with /HEAD", update{zero, one, "refs/remotes/" + flowDataRef + "/HEAD"}, false, "no push may create or update a ref named refs/remotes/ followed by a full ref name"},
		} {
			got := broad.decide("broad", c.u)
			if (got == "") != c.allowed || !strings.Contains(got, c.says) {
				t.Errorf("%s grant %s (on %s): got %q, want allowed=%v saying %q", pattern, c.name, c.u.ref, got, c.allowed, c.says)
			}
		}
	}
}

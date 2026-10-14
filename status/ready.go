// ready, unready and withdraw write a change's record on origin.
//
// `valley ready [--head <sha>] [--change <id>] [--stamp] [<branch>]` marks
// a head of a branch ready, as a new version of its change. The branch is
// the current one unless named. The head is --head when given, and
// otherwise the branch's head on origin; either way it must be on origin
// already. ready creates the change's record at v1, or adds the next
// version to the record there, and pushes the record ref with a lease on
// the value it read. A change that is a draft or withdrawn, readied at the
// head of its active version, is ready again at that version, and gains no
// new one.
//
// `valley unready <change>` makes a ready change a draft: a pause, which
// voids nothing. `valley withdraw <change>` withdraws a ready change or a
// draft, which voids everything outstanding on it. Either is undone by
// readying the change again. Until the integrator reads records, neither
// stops a pending integration request from landing the change, so both
// warn when one is pending.
//
// The record names the principal that pushes it as the uploader of the
// version it adds, and as the owner of a change it creates. The host
// derives that principal from the ssh key a push authenticates with, and
// nothing on this side can ask the host for it. So the principal is read
// from git config valley.principal, and the hook refuses a record that
// names any other.

package main

import (
	"fmt"
	"io"
	"strings"

	"the-valley/change"
)

const readyUsage = "usage: valley ready [--head <sha>] [--change <id>] [--stamp] [<branch>]"

type readyArgs struct {
	head, id, branch string
	stamp            bool
}

func parseReadyArgs(args []string) (readyArgs, error) {
	var a readyArgs
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func(flag string) (string, error) {
			if v, ok := strings.CutPrefix(arg, flag+"="); ok {
				return v, nil
			}
			if i+1 >= len(args) {
				return "", failure(flag + " needs a value; " + readyUsage)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case arg == "--head" || strings.HasPrefix(arg, "--head="):
			a.head, err = value("--head")
		case arg == "--change" || strings.HasPrefix(arg, "--change="):
			a.id, err = value("--change")
		case arg == "--stamp":
			a.stamp = true
		case strings.HasPrefix(arg, "-"):
			err = failure("unknown option " + arg + "; " + readyUsage)
		default:
			rest = append(rest, arg)
		}
		if err != nil {
			return a, err
		}
	}
	switch {
	case len(rest) > 1:
		return a, failure(readyUsage)
	case len(rest) == 1:
		a.branch = strings.TrimPrefix(rest[0], "origin/")
	}
	return a, nil
}

func runReady(g git, args []string, w io.Writer) error {
	a, err := parseReadyArgs(args)
	if err != nil {
		return err
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	principal, err := g.output(true, "config", "valley.principal")
	if err != nil || !change.ValidPrincipal(principal) {
		return failure("ready records the principal your key pushes as, and git config valley.principal names none; set it with git config valley.principal <name>")
	}
	if a.branch == "" {
		if a.branch, err = g.output(true, "symbolic-ref", "--quiet", "--short", "HEAD"); err != nil {
			return failure("HEAD is on no branch; name one: " + readyUsage)
		}
	}
	if !change.ValidBranch(a.branch) {
		return failure("not a branch name: " + a.branch)
	}
	if a.stamp && a.head != "" {
		return failure("--stamp amends the branch's own head, so it takes no --head")
	}

	o, err := listOrigin(g, append([]string{"refs/heads/" + recordTarget, "refs/heads/" + a.branch}, recordPatterns...)...)
	if err != nil {
		return err
	}
	tip, remote := o.refs["refs/heads/"+recordTarget], o.refs["refs/heads/"+a.branch]
	if tip == "" {
		return failure("origin has no " + recordTarget + " branch, so a change has nothing to target")
	}
	if err := fetch(g, tip, remote); err != nil {
		return err
	}

	var head string
	switch {
	case a.stamp:
		if head, err = g.output(true, "rev-parse", "--verify", "--quiet", "refs/heads/"+a.branch+"^{commit}"); err != nil {
			return failure("--stamp amends the head of your local " + a.branch + ", and there is no such branch")
		}
	case a.head != "":
		if !isHexPrefix(a.head) {
			return failure("--head takes a commit's object ID, and " + a.head + " is not one")
		}
		head, err = g.output(true, "rev-parse", "--verify", "--quiet", a.head+"^{commit}")
		if err != nil || remote == "" || !g.succeeds(true, "merge-base", "--is-ancestor", head, remote) {
			return failure(a.head + " is not on origin's " + a.branch + "; push it there first")
		}
	default:
		if remote == "" {
			return failure(a.branch + " is not on origin; push it there first")
		}
		head = remote
	}
	if err := checkRange(g, tip, head); err != nil {
		return err
	}
	id, generated, err := changeID(g, head, a.id)
	if err != nil {
		return err
	}

	ref := change.Ref(recordTarget, id)
	old := o.refs[ref]
	var before *change.Record
	if old != "" {
		if err := fetch(g, old); err != nil {
			return err
		}
		if before, err = readRecord(g, old); err != nil {
			return err
		}
	}
	handle := o.handle(id)

	if a.stamp {
		// Who owns a change is the host's to say, from the key a push
		// authenticates with, and valley.principal is only what this
		// checkout claims. So --stamp, which rewrites a branch before the
		// record is pushed, starts new changes alone and never asks whose
		// an existing one is.
		if before != nil {
			return failure(fmt.Sprintf("--stamp marks only a new change's first version, and %s has a record on origin already; ready this head with --change %s instead, without --stamp", handle, id))
		}
		if head, err = stamp(g, a.branch, head, remote, tip, id); err != nil {
			return err
		}
	}

	subject, err := g.output(true, "log", "-1", "--format=%s", head)
	if err != nil {
		return failure("could not read the subject of " + short(head))
	}
	version := change.Version{
		Head: change.HeadPrefix + head, Title: change.Title(subject), Branch: a.branch, Uploader: principal,
	}
	var after change.Record
	var parents []string
	if before == nil {
		version.N = 1
		after = change.Record{
			Schema: change.Schema, ChangeID: id, Target: recordTarget, Owner: principal,
			State: change.Ready, Seq: 1, Versions: []change.Version{version}, Active: 1,
		}
		parents = []string{head}
	} else {
		active := before.ActiveVersion()
		same := active.Head == version.Head
		switch {
		case same && before.State == change.Ready:
			return failure(fmt.Sprintf("%s v%d is at %s already; a new version needs a new head", handle, active.N, short(head)))
		case !same && len(before.Versions) >= change.MaxVersions:
			return failure(fmt.Sprintf("%s has %d versions, the most a change may have", handle, len(before.Versions)))
		case before.Seq >= change.MaxSeq:
			return failure(fmt.Sprintf("%s's record has been updated %d times, the most a record may be", handle, before.Seq))
		}
		after = *before
		if same {
			// A draft or a withdrawn change readied at its active head is
			// ready again at the version it had, and the record changes
			// nothing else.
			version = active
			parents = []string{old}
		} else {
			version.N = before.Last().N + 1
			after.Versions = append(append([]change.Version(nil), before.Versions...), version)
			after.Active = version.N
			parents = []string{old, head}
		}
		after.State, after.Seq = change.Ready, before.Seq+1
	}
	message := fmt.Sprintf("valley ready: %s v%d\n\nChange %s, version %d, at %s.", handle, version.N, id, version.N, head)
	commit, err := writeRecord(g, &after, message, parents...)
	if err != nil {
		return err
	}
	if err := pushRecord(g, id, old, commit); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s %s v%d ready\n", project(g), handle, version.N)
	if generated {
		fmt.Fprintf(w, "The change ID %s is new. Ready a later head of this change with --change %s, or carry the ID in the head with --stamp.\n", id, id)
	}
	return nil
}

func runUnready(g git, args []string, stdout, stderr io.Writer) error {
	return setState(g, args, stdout, stderr, "unready", change.Draft)
}

func runWithdraw(g git, args []string, stdout, stderr io.Writer) error {
	return setState(g, args, stdout, stderr, "withdraw", change.Withdrawn)
}

// setState writes a record that moves a change to the state to, and
// changes nothing else but the seq. unready moves a ready change to draft,
// and withdraw moves a ready change or a draft to withdrawn.
func setState(g git, args []string, stdout, stderr io.Writer, verb, to string) error {
	if len(args) != 1 {
		return failure("usage: valley " + verb + " <change>")
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	name := args[0]
	o, err := listOrigin(g, append([]string{"refs/heads/" + strings.TrimPrefix(name, "origin/"), requestPrefix + "*"},
		recordPatterns...)...)
	if err != nil {
		return err
	}
	n, found, err := lookup(g, o, name)
	if err != nil {
		return err
	}
	if !found {
		if n, err = onBranch(g, o, strings.TrimPrefix(name, "origin/")); err != nil {
			return err
		}
	}
	handle := o.handle(n.id)
	switch from := n.record.State; {
	case from == change.Withdrawn && to == change.Withdrawn:
		return failure(fmt.Sprintf("%s is withdrawn already; ready it to bring it back", handle))
	case from == change.Withdrawn:
		return failure(fmt.Sprintf("%s is withdrawn, and a withdrawn change is never made a draft; ready it to bring it back", handle))
	case from == change.Draft && to == change.Draft:
		return failure(fmt.Sprintf("%s is a draft already; ready it to resume it", handle))
	case n.record.Seq >= change.MaxSeq:
		return failure(fmt.Sprintf("%s's record has been updated %d times, the most a record may be", handle, n.record.Seq))
	}
	after := *n.record
	after.State, after.Seq = to, n.record.Seq+1
	message := fmt.Sprintf("valley %s: %s\n\nChange %s, %s at version %d.", verb, handle, n.id, to, after.Active)
	commit, err := writeRecord(g, &after, message, n.commit)
	if err != nil {
		return err
	}
	if err := pushRecord(g, n.id, n.commit, commit); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s %s v%d %s\n", project(g), handle, after.Active, to)
	warnPending(stderr, o, n, handle)
	return nil
}

// warnPending warns of an integration request for the change that origin
// held when the verb read it. The integrator does not read records yet, so
// a draft or a withdrawn change still lands by a request filed before it
// was one. The request is named either way approve names a change's: by a
// branch, the way approve names one today, or as c-<change ID>. A request
// filed for an earlier version is named by that version's branch, so the
// branch of every version is looked for, each once.
func warnPending(w io.Writer, o origin, n recorded, handle string) {
	var refs []string
	seen := map[string]bool{}
	add := func(ref string) {
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	for _, v := range n.record.Versions {
		if v.Branch != "" {
			add(requestPrefix + changeName(v.Branch))
		}
	}
	add(requestPrefix + "c-" + n.id)
	warned := false
	for _, ref := range refs {
		if oid := o.refs[ref]; oid != "" {
			fmt.Fprintf(w, "valley: warning: the integration request %s for %s is pending on origin, at %s\n", ref, handle, short(oid))
			warned = true
		}
	}
	if warned {
		fmt.Fprintln(w, "valley: the integrator does not read change records yet, so a pending request can still land the change. Only a holder of the request grant may touch a request ref, so the operator must drop it")
	}
}

// onBranch is the change whose active version is on branch, for a change
// named by its branch. Every record on origin is read to find it.
func onBranch(g git, o origin, branch string) (recorded, error) {
	all, err := readAll(g, o)
	if err != nil {
		return recorded{}, err
	}
	found := activeOn(all, branch)
	switch len(found) {
	case 0:
		return recorded{}, failure(fmt.Sprintf("no change on origin is named %s or has its active version on that branch", branch))
	case 1:
		return found[0], nil
	}
	var handles []string
	for _, n := range found {
		handles = append(handles, o.handle(n.id))
	}
	return recorded{}, failure(fmt.Sprintf("the changes %s all have their active version on %s; name one by its handle",
		strings.Join(handles, ", "), branch))
}

// checkRange refuses a change whose commits are not one line: the range
// from the merge base of origin's main and the head, to the head, must hold
// no merge commit, and the merge base must be one commit.
func checkRange(g git, tip, head string) error {
	out, err := g.output(true, "merge-base", "--all", tip, head)
	bases := strings.Fields(out)
	switch {
	case err != nil && len(bases) == 0:
		return failure(fmt.Sprintf("%s shares no history with origin's %s, so it is no change to it", short(head), recordTarget))
	case len(bases) > 1:
		return failure(fmt.Sprintf("%s has %d merge bases with origin's %s, so the commits of the change are ambiguous", short(head), len(bases), recordTarget))
	}
	merges, err := g.output(true, "rev-list", "--min-parents=2", bases[0]+".."+head)
	if err != nil {
		return failure("could not read the commits from origin's " + recordTarget + " to " + short(head))
	}
	if merges != "" {
		first := strings.Fields(merges)[0]
		return failure(fmt.Sprintf("the commits from origin's %s to %s include the merge %s, and a change is a line of commits", recordTarget, short(head), short(first)))
	}
	return nil
}

// changeID is the change's ID: --change when given, then the head
// commit's change-id header, as jj writes it, then a Change-Id trailer in
// its message, and otherwise a new ID. Both the header and the trailer are
// read whatever the source, and either one malformed, or the two
// disagreeing, is refused. IDs on the other commits of the change are
// their own provenance and are not read.
func changeID(g git, head, flag string) (id string, generated bool, err error) {
	header, trailer, err := headIDs(g, head)
	if err != nil {
		return "", false, err
	}
	switch {
	case header != "" && trailer != "" && header != trailer:
		return "", false, failure(fmt.Sprintf("%s has the change-id header %s and the Change-Id trailer %s, and they must agree", short(head), header, trailer))
	case flag != "":
		if !change.ValidID(flag) {
			return "", false, failure("--change takes a change ID, 32 letters from k to z, and " + flag + " is not one")
		}
		return flag, false, nil
	case header != "":
		return header, false, nil
	case trailer != "":
		return trailer, false, nil
	}
	id, err = change.NewID()
	if err != nil {
		return "", false, failure("could not make a change ID: " + err.Error())
	}
	return id, true, nil
}

// headIDs reads the change IDs a commit carries: the change-id header in
// the commit object itself, never text in its message, and the Change-Id
// trailer, as git interpret-trailers parses the message.
func headIDs(g git, head string) (header, trailer string, err error) {
	raw, err := g.output(true, "cat-file", "commit", head)
	if err != nil {
		return "", "", failure("could not read the commit " + short(head))
	}
	headers, message, _ := strings.Cut(raw, "\n\n")
	var found []string
	for _, line := range strings.Split(headers, "\n") {
		// A line opening with a space continues the header above it.
		if v, ok := strings.CutPrefix(line, "change-id "); ok {
			found = append(found, v)
		}
	}
	switch {
	case len(found) > 1:
		return "", "", failure(short(head) + " has more than one change-id header")
	case len(found) == 1 && !change.ValidID(found[0]):
		return "", "", failure(fmt.Sprintf("%s has the change-id header %q, which is not a change ID", short(head), found[0]))
	case len(found) == 1:
		header = found[0]
	}

	parsed, err := g.input(true, strings.NewReader(message), "interpret-trailers", "--parse")
	if err != nil {
		return "", "", failure("could not read the trailers of " + short(head))
	}
	found = nil
	for _, line := range strings.Split(parsed, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Change-Id") {
			found = append(found, strings.TrimSpace(value))
		}
	}
	switch {
	case len(found) > 1:
		return "", "", failure(short(head) + " has more than one Change-Id trailer")
	case len(found) == 1 && !change.ValidID(found[0]):
		return "", "", failure(fmt.Sprintf("%s has the Change-Id trailer %q, which is not a change ID", short(head), found[0]))
	case len(found) == 1:
		trailer = found[0]
	}
	return header, trailer, nil
}

// stamp amends the head of the local branch to carry the change ID as a
// Change-Id trailer, and pushes the branch so that the amended head is on
// origin. It returns the amended head, or the head as it was when it
// carries the trailer already.
//
// Amending rewrites a commit, which is harmless only while nobody else can
// have it. So stamp refuses a head that is on origin already. It refuses a
// local branch that is behind origin's or has diverged from it too, since
// pushing the amended head would drop commits origin holds. Its caller
// refuses a change that has a record already.
func stamp(g git, branch, head, remote, tip, id string) (string, error) {
	_, trailer, err := headIDs(g, head)
	if err != nil {
		return "", err
	}
	if trailer == id {
		return head, nil
	}
	if trailer != "" {
		return "", failure(fmt.Sprintf("%s carries the Change-Id trailer %s already, and --stamp would add %s", short(head), trailer, id))
	}
	onBranch := remote != "" && g.succeeds(true, "merge-base", "--is-ancestor", head, remote)
	inTarget := g.succeeds(true, "merge-base", "--is-ancestor", head, tip)
	switch {
	case onBranch && head != remote:
		return "", failure(fmt.Sprintf("origin's %s is ahead of yours; bring yours up to date before stamping its head", branch))
	case remote != "" && !onBranch && !g.succeeds(true, "merge-base", "--is-ancestor", remote, head):
		return "", failure(fmt.Sprintf("your %s and origin's have diverged; --stamp would drop what origin holds", branch))
	case onBranch || inTarget:
		return "", failure(fmt.Sprintf("%s is on origin already, so --stamp will not rewrite it; ready it with --change %s instead", short(head), id))
	}

	raw, err := g.output(true, "cat-file", "commit", head)
	if err != nil {
		return "", failure("could not read the commit " + short(head))
	}
	_, message, _ := strings.Cut(raw, "\n\n")
	stamped, err := g.input(true, strings.NewReader(message), "interpret-trailers", "--trailer", "Change-Id: "+id)
	if err != nil {
		return "", failure("could not add the Change-Id trailer to " + short(head))
	}
	// The amended commit keeps the head's tree, parents and author, as
	// git commit --amend does. The committer is whoever stamps it.
	who, err := g.output(true, "log", "-1", "--date=raw", "--format=%an%x00%ae%x00%ad%x00%T%x00%P", head)
	if err != nil {
		return "", failure("could not read the commit " + short(head))
	}
	f := strings.Split(who, "\x00")
	if len(f) != 5 {
		return "", failure("could not read the commit " + short(head))
	}
	args := []string{"commit-tree", f[3]}
	for _, p := range strings.Fields(f[4]) {
		args = append(args, "-p", p)
	}
	amend := g
	amend.env = append(append([]string(nil), g.env...),
		"GIT_AUTHOR_NAME="+f[0], "GIT_AUTHOR_EMAIL="+f[1], "GIT_AUTHOR_DATE=@"+f[2])
	amended, err := amend.input(false, strings.NewReader(stamped+"\n"), append(args, "-F", "-")...)
	if err != nil {
		return "", failure("could not write the amended commit")
	}
	if _, err := g.output(false, "update-ref", "-m", "valley ready --stamp", "refs/heads/"+branch, amended, head); err != nil {
		return "", failure("could not move your " + branch + " to the amended head")
	}
	_, err = g.output(false, "push", "--quiet", "--force-with-lease=refs/heads/"+branch+":"+remote,
		"origin", amended+":refs/heads/"+branch)
	if err != nil {
		return "", failure(fmt.Sprintf("your %s carries the stamped head %s, and origin did not take it", branch, short(amended)))
	}
	return amended, nil
}

func isHexPrefix(s string) bool {
	if len(s) < 4 || len(s) > 40 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

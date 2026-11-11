package main

// coverage says which of a head's required nix checks already have evidence
// on origin that approval can reuse, so that approve (approve.go) runs only
// the rest. A check worker publishes such evidence, and so does an earlier
// approval.
//
// Approve skips a check only where the integrator will accept the same
// evidence. So coverage judges each check by the note the integrator
// judges it by, read the way the integrator reads it:
//
//   - Only what origin holds counts. coverage lists origin's attestation
//     refs for the head's tree, fetches the objects they name by ID, and
//     reads every note in them, byte for byte. It never reads a local ref,
//     so a note stored here and never published is not mistaken for
//     evidence the integrator will see. A listing or a fetch that fails is
//     an error, never an empty answer.
//   - Of the notes about a check, the one judged is change.SelectNotes's
//     choice, in the order the integrator reads them: the first that
//     verifies, whatever it records.
//   - It verifies when attest verify accepts it under the known-signers
//     file the serving host publishes (signers.go), which is the host's
//     current authorization as far as a client can read it. With no
//     publication, nothing is reused.
//   - And one of the signers attest verify accepted must count in this
//     project, as the integrator's classify decides it: the signer
//     projects file in the same publication (note/signerprojects.go) lets
//     it count here, and it is not the publication's integrator name,
//     since the integrator's signature on a note is its countersignature.
//     A note no such signer signed is a stranger's to the integrator, so a
//     later note may replace it. A publication whose signer projects file
//     is not a file, or does not read, is not used, so nothing is reused.
//   - The project is named as the other verbs name it, after origin's URL
//     (changes.go). The host's controllers name it after their
//     repository's directory, so the two agree when origin's URL ends in
//     that directory's name.
//
// The note judged is reused only when it also meets every one of these:
//
//   - it is pure evidence, from the nix runner, for this check's name and
//     this check's flake attribute, and it records the check as passed;
//   - when VALLEY_KNOWN_SIGNERS is set, attest verify accepts it under that
//     file too, by a signer that counts in this project. An override can
//     narrow what is reused, never widen it.
//
// The integrator applies the same runner and attribute test to every
// note it judges (integrator/verdict/verdict.go). A command check is
// never covered: approve runs it again. The integrator judges every note again
// when it decides the request, so coverage decides only what approve
// builds, never what lands.
//
// The answer is one line per fact, fields separated by single spaces:
//
//	digest <valley-tree-v1 digest of the head's tree>
//	stored <ref> <object ID>   one per attestation ref origin holds for it
//	covered <check> <ref> <signer>+<key hash>
//	failed <check> <ref> <signer>+<key hash>
//	missing <check>
//
// with a covered, a failed or a missing line for every check asked about,
// in the order asked. A check is failed when the note judged verifies, is
// signed by a signer who counts in this project, and records something
// other than a pass of this check, as valley pending --checks reads it
// (versionchecks.go): the integrator judges the check by that note, so
// building the check again does not change the verdict on this tree.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"the-valley/change"
	"the-valley/note"
)

const predicatePure = "the-valley/check/pure/v1"

// nixCheck is one required check the nix runner runs: its name, and the
// flake attribute the policy builds it from.
type nixCheck struct{ name, attribute string }

// evidence is what coverage reads of a head's tree on origin: the tree's
// digest, every attestation ref origin holds for it, in order, with the
// object each names, and each check covered or failed, mapped to
// "<ref> <signer>".
type evidence struct {
	digest  string
	stored  []string
	listed  map[string]string
	covered map[string]string
	failed  map[string]string
}

// coverageOf reads the evidence origin holds for a head's tree, and which
// of the checks asked about it covers. The check worker (worker.go) calls
// it as coverage does.
func coverageOf(g git, head string, checks []nixCheck, stderr io.Writer) (evidence, error) {
	var keys hostKeys
	defer keys.done()
	return coverageIn(g, head, nil, checks, &keys, stderr)
}

// coverageIn is coverageOf for a reader that judges many trees, as the
// main walk does (mainstate.go): listed is origin's attestation refs,
// listed once for every tree, or nil to list this tree's alone, and keys
// reads the host's signers once for every tree judged under it.
func coverageIn(g git, head string, listed map[string]string, checks []nixCheck, keys *hostKeys, stderr io.Writer) (evidence, error) {
	oid, err := g.output(true, "rev-parse", "--verify", "--quiet", "--end-of-options", head+"^{commit}")
	if err != nil || !isObjectID(oid) {
		return evidence{}, failure("no such commit: " + head)
	}
	head = oid
	repo := g.dir
	if repo == "" {
		repo = "."
	}

	digest, err := treeDigest(g, repo, head)
	if err != nil {
		return evidence{}, err
	}

	// Every attestation ref origin holds for the tree, signer by signer.
	if listed == nil {
		o, err := listOrigin(g, attestationRefs+digest+"/*")
		if err != nil {
			return evidence{}, failure("could not list the evidence origin holds for this tree")
		}
		listed = o.refs
	}
	e := evidence{digest: digest, listed: map[string]string{}, stored: storedFor(digest, listed)}
	for _, ref := range e.stored {
		e.listed[ref] = listed[ref]
	}
	e.covered, e.failed, err = cover(g, repo, head, e.stored, e.listed, checks, keys, stderr)
	return e, err
}

// treeDigest is the valley-tree-v1 digest of a commit's tree, as attest
// digests it, without its scheme.
func treeDigest(g git, repo, head string) (string, error) {
	out, err := attestTool(g, "digest", "--repo", repo, "--rev", head)
	digest, ok := strings.CutPrefix(strings.TrimSpace(out), "valley-tree-v1:")
	if err != nil || !ok || digest == "" {
		return "", failure("could not digest the tree of " + head)
	}
	return digest, nil
}

// storedFor is every attestation ref listed for a tree, in byte order.
func storedFor(digest string, listed map[string]string) []string {
	prefix := attestationRefs + digest + "/"
	var stored []string
	for ref := range listed {
		if rest, ok := strings.CutPrefix(ref, prefix); ok && rest != "" {
			stored = append(stored, ref)
		}
	}
	sort.Strings(stored)
	return stored
}

// cover is the checks the stored refs hold reusable evidence for, and the
// checks whose judged note records a failure, each mapped to
// "<ref> <signer>". It fetches the refs' objects only when there are
// checks to look for.
func cover(g git, repo, head string, stored []string, listed map[string]string, checks []nixCheck, signers *hostKeys, stderr io.Writer) (covered, failed map[string]string, err error) {
	covered, failed = map[string]string{}, map[string]string{}
	if len(stored) == 0 || len(checks) == 0 {
		return covered, failed, nil
	}
	var oids []string
	for _, ref := range stored {
		oids = append(oids, listed[ref])
	}
	if err := fetch(g, oids...); err != nil {
		return nil, nil, failure("could not fetch the evidence origin holds for this tree")
	}
	keys, p, why := signers.read(g)
	if why != "" {
		fmt.Fprintf(stderr, "valley: %s, so none of the evidence on origin is reused\n", why)
		return covered, failed, nil
	}
	here := counting{project: project(g), integrator: p.name, projects: p.projects}

	judged, why, err := judge(g, repo, head, keys, os.Getenv("VALLEY_KNOWN_SIGNERS"), here, stored, listed, checks)
	if err != nil {
		return nil, nil, err
	}
	if why != "" {
		fmt.Fprintf(stderr, "valley: %s, so the integrator cannot judge this tree's evidence, and none of it is reused\n", why)
		return covered, failed, nil
	}
	for _, c := range checks {
		j, ok := judged[c.name]
		if !ok {
			continue // origin holds no note about this check
		}
		if j.why != "" {
			fmt.Fprintf(stderr, "valley: not reusing %s: the integrator judges it by %s:%s, and %s\n", c.name, j.note.ref, j.note.path, j.why)
			if j.note.refusal == "" && mismatch(j.note, c) != "" {
				failed[c.name] = j.note.ref + " " + j.note.signer
			}
			continue
		}
		covered[c.name] = j.note.ref + " " + j.note.signer
	}
	return covered, failed, nil
}

// judgment is the note the integrator judges a check by, and why approve
// may not skip the check on it, or "" when it may.
type judgment struct {
	note storedNote
	why  string
}

// judge reads the notes the stored refs hold about the checks asked, whose
// objects have been fetched, and verifies each under keys, the host's
// known-signers file. It returns the note the integrator judges each check
// by, for each check origin holds a note about, with why it is not
// reusable. local is a known-signers file that narrows what is reusable,
// or "" for none. unjudged says why the integrator cannot judge the tree's
// evidence at all.
func judge(g git, repo, head, keys, local string, here counting, stored []string, listed map[string]string, checks []nixCheck) (judged map[string]judgment, unjudged string, err error) {
	asked := map[string]bool{}
	for _, c := range checks {
		asked[c.name] = true
	}
	notes, why, err := readNotes(g, stored, listed, asked)
	if err != nil || why != "" {
		return nil, why, err
	}
	for i := range notes {
		signers, refusal, err := verify(g, repo, head, keys, notes[i].note)
		if err != nil {
			return nil, "", err
		}
		if refusal != "" {
			notes[i].refusal = "attest verify refuses it under the host's signers: " + refusal
			continue
		}
		notes[i].signer, notes[i].refusal = here.signer(signers)
	}
	picked := change.SelectNotes(notes,
		func(n storedNote) string { return n.check },
		func(n storedNote) bool { return n.refusal == "" })

	judged = map[string]judgment{}
	for _, c := range checks {
		n, ok := picked[c.name]
		if !ok {
			continue
		}
		why, err := reusable(g, repo, head, local, here, c, n)
		if err != nil {
			return nil, "", err
		}
		judged[c.name] = judgment{note: n, why: why}
	}
	return judged, "", nil
}

// storedNote is one note an attestation ref on origin holds, as read: where
// it is, its bytes, the check its text says it is about, and whether the
// integrator would admit it under the host's signers.
type storedNote struct {
	ref, path string
	note      []byte
	check     string
	signer    string // the first verified signer who counts here, when one does
	refusal   string // why the integrator would not admit it; "" when it would
}

// counting is who counts as a check's signer in a project, by the rule the
// integrator's classify applies (integrator/evidence.go): a signer the
// signer projects file lets count in the project, other than the host's
// integrator.
type counting struct {
	project, integrator string
	projects            note.SignerProjects
}

// signer is the first of the verified signers, each name+hash in the order
// attest verify reported them, who counts in the project, or why none
// does.
func (c counting) signer(verified []string) (signer, why string) {
	var not []string
	for _, s := range verified {
		name, _, _ := strings.Cut(s, "+")
		switch {
		case name == c.integrator:
			not = append(not, name+" is the host's integrator, whose signature is a countersignature")
		case !c.projects.Counts(name, c.project):
			not = append(not, fmt.Sprintf("the published %s lets %s count %s", signersProjects, name, c.only(name)))
		default:
			return s, ""
		}
	}
	return "", fmt.Sprintf("none of its signers counts in project %s: %s", c.project, strings.Join(not, "; "))
}

// only is where the signer projects file lets a restricted signer count.
func (c counting) only(name string) string {
	var in []string
	for p := range c.projects[name] {
		in = append(in, p)
	}
	if len(in) == 0 {
		return "in no project"
	}
	sort.Strings(in)
	return "only in " + strings.Join(in, ", ")
}

// readNotes reads every note the stored refs hold about a check asked
// about, in the order the integrator reads them (integrator/evidence.go):
// the refs in byte order, and each ref's notes in the order git ls-tree -r
// lists them. A note is any file named statement.note, at any depth. The
// integrator reads every note under the tree, and a note it cannot split
// into a text and signatures stops it judging the tree's evidence at all,
// so why names the first such note.
func readNotes(g git, stored []string, listed map[string]string, asked map[string]bool) (notes []storedNote, why string, err error) {
	type file struct{ ref, path string }
	var files []file
	var objects []string
	for _, ref := range stored {
		listing, err := g.output(true, "ls-tree", "-r", "--name-only", listed[ref])
		if err != nil {
			return nil, "", fmt.Errorf("could not list %s", ref)
		}
		for _, p := range strings.Fields(listing) {
			if path.Base(p) == "statement.note" {
				files = append(files, file{ref, p})
				objects = append(objects, listed[ref]+":"+p)
			}
		}
	}
	read, err := blobs(g, objects)
	if err != nil {
		return nil, "", err
	}
	for i, f := range files {
		note := read[i]
		if note == nil {
			return nil, "", fmt.Errorf("could not read %s:%s", f.ref, f.path)
		}
		split := bytes.LastIndex(note, []byte("\n\n"))
		if split < 0 {
			return nil, fmt.Sprintf("%s:%s is not a note: no blank line separates its text from its signatures", f.ref, f.path), nil
		}
		check := statementValue(string(note[:split+1]), "predicate.check.name")
		if check == "" {
			check = "(unnamed)"
		}
		if asked[check] {
			notes = append(notes, storedNote{ref: f.ref, path: f.path, note: note, check: check})
		}
	}
	return notes, "", nil
}

// reusable says why the note a check is judged by is not evidence approve
// may skip the check on, or "" when it is.
func reusable(g git, repo, head, local string, here counting, c nixCheck, n storedNote) (string, error) {
	if n.refusal != "" {
		return n.refusal, nil
	}
	if why := mismatch(n, c); why != "" {
		return why, nil
	}
	if local != "" {
		signers, refusal, err := verify(g, repo, head, local, n.note)
		if err != nil {
			return "", err
		}
		if refusal != "" {
			return "attest verify refuses it under VALLEY_KNOWN_SIGNERS: " + refusal, nil
		}
		if _, why := here.signer(signers); why != "" {
			return "under VALLEY_KNOWN_SIGNERS, " + why, nil
		}
	}
	return "", nil
}

// mismatch says why a note is not a pass of this check, or "" when it
// is one: pure evidence, from the nix runner, for the check's attribute,
// recording that it passed.
func mismatch(n storedNote, c nixCheck) string {
	text := string(n.note[:bytes.LastIndex(n.note, []byte("\n\n"))+1])
	for _, want := range [][2]string{
		{"predicateType", predicatePure},
		{"predicate.check.runner", "nix"},
		{"predicate.check.attribute", c.attribute},
		{"predicate.result", "passed"},
	} {
		if got := statementValue(text, want[0]); got != want[1] {
			return fmt.Sprintf("its %s is %q, not %q", want[0], got, want[1])
		}
	}
	return ""
}

// verify runs attest verify over a note's exact bytes, under a
// known-signers file. It returns every signer whose signature checked
// out, in the order attest reported them, or the sentence attest refused
// the note with.
func verify(g git, repo, head, keys string, note []byte) (signers []string, refusal string, err error) {
	file, err := os.CreateTemp("", "valley-coverage-*.note")
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(note)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, "", err
	}
	out, err := attestTool(g, "verify", "--note", file.Name(), "--known-keys", keys, "--repo", repo, "--rev", head)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, "", fmt.Errorf("could not run attest verify: %v", err)
		}
		return nil, refusalOf(out), nil
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "signature" && f[1] == "ok" {
			signers = append(signers, f[2])
		}
	}
	if len(signers) == 0 {
		return nil, "attest verify named no signer", nil
	}
	return signers, "", nil
}

// hostKeys is the host's known-signers file, read by the first tree that
// needs it and kept for every later one, until done removes it.
type hostKeys struct {
	keys, why string
	p         *publishedSigners
	once      bool
}

// read is hostSigners, read once.
func (h *hostKeys) read(g git) (keys string, p *publishedSigners, why string) {
	if !h.once {
		h.keys, h.p, h.why = hostSigners(g)
		h.once = true
	}
	return h.keys, h.p, h.why
}

// done removes the file read wrote, if it wrote one.
func (h *hostKeys) done() {
	if h.keys != "" {
		os.Remove(h.keys)
	}
}

// hostSigners writes out the known-signers file the serving host
// publishes, for attest verify to read, and returns its path and the
// publication it came from. why says why there is none.
func hostSigners(g git) (keys string, p *publishedSigners, why string) {
	p, err := readSigners(g)
	switch {
	case err != nil:
		return "", nil, err.Error()
	case p == nil:
		return "", nil, "the serving host publishes no signers at " + signersRef
	}
	if _, err := note.ParseVerifierKeys(p.keys); err != nil {
		return "", nil, fmt.Sprintf("the known-signers file published at %s does not read as verifier keys: %v", p.where, err)
	}
	f, err := os.CreateTemp("", "valley-host-signers-*")
	if err != nil {
		return "", nil, err.Error()
	}
	_, err = f.Write(p.keys)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", nil, err.Error()
	}
	return f.Name(), p, ""
}

// blobs are the contents of the objects named, each exactly as git
// stores it, read by one git cat-file --batch rather than a process each.
// An object that is no blob is nil. g.output trims trailing newlines, and
// a note's last newline is part of its envelope (note.Split), so a note is
// never read through it.
func blobs(g git, objects []string) ([][]byte, error) {
	out := make([][]byte, len(objects))
	if len(objects) == 0 {
		return out, nil
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Env = g.env
	cmd.Dir = g.dir
	cmd.Stdin = strings.NewReader(strings.Join(objects, "\n") + "\n")
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("could not read the notes origin holds: %v", err)
	}
	for i := range objects {
		header, rest, ok := bytes.Cut(stdout, []byte("\n"))
		if !ok {
			return nil, errors.New("git cat-file --batch ended early")
		}
		f := strings.Fields(string(header))
		stdout = rest
		if len(f) != 3 {
			continue // "<object> missing" or "ambiguous"
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size < 0 || len(stdout) < size+1 {
			return nil, fmt.Errorf("git cat-file --batch gave an unreadable answer for %s", objects[i])
		}
		if f[1] == "blob" {
			out[i] = stdout[:size:size]
		}
		stdout = stdout[size+1:]
	}
	return out, nil
}

// attestTool runs the attest helper in g's repository and environment,
// and returns what it printed on stdout and stderr together.
func attestTool(g git, args ...string) (string, error) {
	cmd := exec.Command("attest", args...)
	cmd.Env = g.env
	cmd.Dir = g.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// statementValue reads one key's value from statement text, where each
// line is a key, one space and a value.
func statementValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			return v
		}
	}
	return ""
}

// refusalOf is the sentence attest refused with: the line it prefixes
// with its own name, or else the last line it printed.
func refusalOf(out string) string {
	last := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if why, ok := strings.CutPrefix(line, "attest: "); ok {
			return strings.TrimSpace(why)
		}
		if strings.TrimSpace(line) != "" {
			last = strings.TrimSpace(line)
		}
	}
	return last
}

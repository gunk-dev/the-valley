package main

// coverage says which of a head's required nix checks already have evidence
// on origin that approval can reuse, so that approve (bin/valley) runs only
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
// The attribute test is stricter than the integrator's rule for an
// unmoved tree, which compares no attribute. A command check is never
// covered: approve runs it again. The integrator judges every note again
// when it decides the request, so coverage decides only what approve
// builds, never what lands.
//
// The answer is one line per fact, fields separated by single spaces:
//
//	digest <valley-tree-v1 digest of the head's tree>
//	stored <ref> <object ID>   one per attestation ref origin holds for it
//	covered <check> <ref> <signer>+<key hash>
//	missing <check>
//
// with a covered or a missing line for every check asked about, in the
// order asked.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"

	"the-valley/change"
	"the-valley/note"
)

const coverageUsage = "usage: valley-go coverage --head <sha> [--check <name>=<attribute>]..."

const predicatePure = "the-valley/check/pure/v1"

// checkName is a check's name or flake attribute as the policy schema
// admits one (#Name in schema/verification.cue).
var checkName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// nixCheck is one required check the nix runner runs: its name, and the
// flake attribute the policy builds it from.
type nixCheck struct{ name, attribute string }

func runCoverage(g git, args []string, w, stderr io.Writer) error {
	var head string
	var checks []nixCheck
	for i := 0; i < len(args); i++ {
		opt, value, inline := strings.Cut(args[i], "=")
		if opt != "--head" && opt != "--check" {
			return failure(coverageUsage)
		}
		if !inline {
			if i+1 == len(args) {
				return failure(coverageUsage)
			}
			i++
			value = args[i]
		}
		if opt == "--head" {
			head = value
			continue
		}
		name, attribute, ok := strings.Cut(value, "=")
		if !ok || !checkName.MatchString(name) || !checkName.MatchString(attribute) {
			return failure(fmt.Sprintf("--check %q: expected <name>=<attribute>", value))
		}
		checks = append(checks, nixCheck{name, attribute})
	}
	if head == "" {
		return failure(coverageUsage)
	}
	oid, err := g.output(true, "rev-parse", "--verify", "--quiet", "--end-of-options", head+"^{commit}")
	if err != nil || !isObjectID(oid) {
		return failure("no such commit: " + head)
	}
	head = oid
	repo := g.dir
	if repo == "" {
		repo = "."
	}

	out, err := attestTool(g, "digest", "--repo", repo, "--rev", head)
	digest, ok := strings.CutPrefix(strings.TrimSpace(out), "valley-tree-v1:")
	if err != nil || !ok || digest == "" {
		return failure("could not digest the tree of " + head)
	}

	// Every attestation ref origin holds for the tree, signer by signer.
	prefix := attestationRefs + digest + "/"
	o, err := listOrigin(g, prefix+"*")
	if err != nil {
		return failure("could not list the evidence origin holds for this tree")
	}
	listed := o.refs
	var stored []string
	for ref := range listed {
		if rest, ok := strings.CutPrefix(ref, prefix); ok && rest != "" {
			stored = append(stored, ref)
		}
	}
	sort.Strings(stored)

	var lines []string
	lines = append(lines, "digest "+digest)
	for _, ref := range stored {
		lines = append(lines, "stored "+ref+" "+listed[ref])
	}
	covered, err := cover(g, repo, head, stored, listed, checks, stderr)
	if err != nil {
		return err
	}
	for _, c := range checks {
		if by, ok := covered[c.name]; ok {
			lines = append(lines, "covered "+c.name+" "+by)
		} else {
			lines = append(lines, "missing "+c.name)
		}
	}
	_, err = io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}

// cover is the checks the stored refs hold reusable evidence for, each
// mapped to "<ref> <signer>". It fetches the refs' objects only when there
// are checks to look for.
func cover(g git, repo, head string, stored []string, listed map[string]string, checks []nixCheck, stderr io.Writer) (map[string]string, error) {
	covered := map[string]string{}
	if len(stored) == 0 || len(checks) == 0 {
		return covered, nil
	}
	var oids []string
	for _, ref := range stored {
		oids = append(oids, listed[ref])
	}
	if err := fetch(g, oids...); err != nil {
		return nil, failure("could not fetch the evidence origin holds for this tree")
	}
	keys, p, why := hostSigners(g)
	if why != "" {
		fmt.Fprintf(stderr, "valley: %s, so none of the evidence on origin is reused\n", why)
		return covered, nil
	}
	defer os.Remove(keys)
	here := counting{project: project(g), integrator: p.name, projects: p.projects}

	asked := map[string]bool{}
	for _, c := range checks {
		asked[c.name] = true
	}
	notes, why, err := readNotes(g, stored, listed, asked)
	if err != nil {
		return nil, err
	}
	if why != "" {
		fmt.Fprintf(stderr, "valley: %s, so the integrator cannot judge this tree's evidence, and none of it is reused\n", why)
		return covered, nil
	}
	for i := range notes {
		signers, refusal, err := verify(g, repo, head, keys, notes[i].note)
		if err != nil {
			return nil, err
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

	local := os.Getenv("VALLEY_KNOWN_SIGNERS")
	for _, c := range checks {
		n, ok := picked[c.name]
		if !ok {
			continue // origin holds no note about this check
		}
		why, err := reusable(g, repo, head, local, here, c, n)
		if err != nil {
			return nil, err
		}
		if why != "" {
			fmt.Fprintf(stderr, "valley: not reusing %s: the integrator judges it by %s:%s, and %s\n", c.name, n.ref, n.path, why)
			continue
		}
		covered[c.name] = n.ref + " " + n.signer
	}
	return covered, nil
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
	for _, ref := range stored {
		listing, err := g.output(true, "ls-tree", "-r", "--name-only", listed[ref])
		if err != nil {
			return nil, "", fmt.Errorf("could not list %s", ref)
		}
		for _, file := range strings.Fields(listing) {
			if path.Base(file) != "statement.note" {
				continue
			}
			note, err := blob(g, listed[ref]+":"+file)
			if err != nil {
				return nil, "", fmt.Errorf("could not read %s:%s", ref, file)
			}
			split := bytes.LastIndex(note, []byte("\n\n"))
			if split < 0 {
				return nil, fmt.Sprintf("%s:%s is not a note: no blank line separates its text from its signatures", ref, file), nil
			}
			check := statementValue(string(note[:split+1]), "predicate.check.name")
			if check == "" {
				check = "(unnamed)"
			}
			if asked[check] {
				notes = append(notes, storedNote{ref: ref, path: file, note: note, check: check})
			}
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
	text := string(n.note[:bytes.LastIndex(n.note, []byte("\n\n"))+1])
	for _, want := range [][2]string{
		{"predicateType", predicatePure},
		{"predicate.check.runner", "nix"},
		{"predicate.check.attribute", c.attribute},
		{"predicate.result", "passed"},
	} {
		if got := statementValue(text, want[0]); got != want[1] {
			return fmt.Sprintf("its %s is %q, not %q", want[0], got, want[1]), nil
		}
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

// blob is an object's content exactly as git stores it. g.output trims
// trailing newlines, and a note's last newline is part of its envelope
// (note.Split), so a note is never read through it.
func blob(g git, object string) ([]byte, error) {
	cmd := exec.Command("git", "cat-file", "blob", object)
	cmd.Env = g.env
	cmd.Dir = g.dir
	return cmd.Output()
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

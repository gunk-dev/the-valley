// valley pending: what is waiting to land, for a person to read.
//
// It lists the ready changes whose active version has not landed, by the
// first two landing rules (inventory.go), one line each:
//
//	nxvp v2 | the active version's title | includes xunx | uncertain
//
// A change includes another when its active head strictly contains the
// other's active head, and the other has not landed either. That is
// ancestry alone: approving the change lands the other's content too.
// Only the nearest is named. uncertain is shown when the active version
// may have landed without its evidence being stored. With --owes, each
// line ends with how many checks the change owes, by cost.
//
// Two sections follow. branches lists each branch on origin, other than
// main, that main does not reach and that is no change's active head.
// drafts lists the changes whose record says they are drafts, at the head
// of each active version.
//
// With --json it prints valley-pending/v1: valley-changes/v1, holding the
// same changes this lists, the drafts among them, and the same branches.

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	"the-valley/change"
)

// pendingChange is a ready change whose active version has not landed.
func pendingChange(s *snapshot, n recorded) bool {
	return n.record.State == change.Ready && !s.isLanded(n.id, n.record.ActiveVersion())
}

// unlanded is a change in any state whose active version has not landed:
// the changes a pending change may include.
func unlanded(s *snapshot, n recorded) bool {
	return !s.isLanded(n.id, n.record.ActiveVersion())
}

// listedByPending is what pending lists: the pending changes and the
// drafts.
func listedByPending(s *snapshot, n recorded) bool {
	return n.record.State == change.Draft || pendingChange(s, n)
}

func runPending(g git, args []string, stdout, stderr io.Writer) error {
	l, err := parseListing("pending", args, true)
	if err != nil {
		return err
	}
	list, host, err := sources(g, l, stderr)
	if err != nil {
		return err
	}
	repos := readSources(list, verifierFromEnv())
	h := handles(repos)
	defer warnUnverified(repos, stderr)
	if l.asJSON {
		doc := jsonChanges{Schema: pendingSchema, Host: host, Repos: []jsonRepo{}}
		for _, r := range repos {
			view := r.repoView(h, listedByPending, unlanded)
			utf8Branches(&view, stderr)
			doc.Repos = append(doc.Repos, view)
		}
		return writeDocument(stdout, doc)
	}
	for i, r := range repos {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		if err := writePending(r, h, l, stdout, len(repos) > 1 || l.all || l.repo != ""); err != nil {
			return err
		}
	}
	return nil
}

// writePending prints one repository's pending changes, then its
// unrecorded branches and its drafts. Several repositories are each
// headed by their name.
func writePending(r listed, h map[string]string, l listing, w io.Writer, headed bool) error {
	p := func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
	heading := func(section string) {
		switch {
		case headed && section == "":
			p("%s\n", r.name)
		case headed:
			p("\n%s %s\n", r.name, section)
		case section != "":
			p("\n%s\n", section)
		}
	}
	if r.err != nil {
		p("%s: %v\n", r.name, r.err)
		return nil
	}
	view := r.repoView(h, listedByPending, unlanded)
	heading("")
	shown := 0
	for _, c := range view.Changes {
		if c.State != change.Ready {
			continue
		}
		shown++
		v := c.Versions[c.Active-1]
		p("%s v%d | %s", c.Handle, v.N, v.Title)
		if len(c.Includes) > 0 {
			var named []string
			for _, id := range c.Includes {
				named = append(named, h[id])
			}
			p(" | includes %s", strings.Join(named, ", "))
		}
		if v.Landing == uncertain {
			p(" | uncertain")
		}
		if l.owes {
			p(" | %s", r.snap.owes(v.Head))
		}
		p("\n")
	}
	if shown == 0 {
		p("no change pending\n")
	}
	if len(view.Branches) > 0 {
		heading("branches")
		for _, b := range view.Branches {
			p("%s\n", r.snap.pendingLine(b.Branch, b.Head))
		}
	}
	drafted := false
	for _, c := range view.Changes {
		if c.State != change.Draft {
			continue
		}
		if !drafted {
			heading("drafts")
			drafted = true
		}
		v := c.Versions[c.Active-1]
		label := fmt.Sprintf("%s v%d", c.Handle, v.N)
		if v.Branch != "" {
			label += " on " + v.Branch
		}
		p("%s\n", r.snap.pendingLine(label, v.Head))
	}
	return nil
}

// pendingLine is one line of pending for a commit: the label, then the
// commit's subject, age and author, how far it is ahead of main, and its
// diffstat against the merge base.
func (s *snapshot) pendingLine(label, oid string) string {
	out, _ := s.g.output(true, "log", "-1", "--format=%s%x00%cr%x00%an", oid)
	fields := strings.SplitN(out, "\x00", 3)
	for len(fields) < 3 {
		fields = append(fields, "")
	}
	subject := fields[0]
	if utf8.RuneCountInString(subject) > 60 {
		subject = string([]rune(subject)[:57]) + "..."
	}
	ahead, stat := "?", ""
	if s.tip != "" {
		ahead, _ = s.g.output(true, "rev-list", "--count", s.tip+".."+oid)
		stat, _ = s.g.output(true, "diff", "--shortstat", s.tip+"..."+oid)
	}
	stat = strings.TrimPrefix(stat, " ")
	if stat == "" {
		stat = "no file changes"
	}
	return fmt.Sprintf("%s | %s | %s | %s | ahead %s | %s", label, subject, fields[1], fields[2], ahead, stat)
}

// owes says how many checks a head owes under the policy, by cost, as
// valley checks derives them against the merge base with main. It runs
// the valley command bin/valley names in VALLEY_COMMAND, inside the
// repository the change is in.
func (s *snapshot) owes(head string) string {
	valley := os.Getenv("VALLEY_COMMAND")
	if valley == "" {
		valley = "valley"
	}
	base, err := s.g.output(true, "merge-base", s.tip, head)
	if err != nil {
		return "owes ? (no merge base with main)"
	}
	cmd := exec.Command(valley, "checks", base, head)
	cmd.Dir = s.g.dir
	cmd.Env = s.g.env
	out, err := cmd.Output()
	if err != nil {
		return "owes ? (the policy did not derive)"
	}
	count := func(prefix string) int {
		for _, line := range strings.Split(string(out), "\n") {
			if rest, ok := strings.CutPrefix(line, prefix); ok && rest != "none" {
				return len(strings.Split(rest, ", "))
			}
		}
		return 0
	}
	quick, slow := count("quick: "), count("slow: ")
	if quick+slow == 0 {
		return "owes nothing"
	}
	return fmt.Sprintf("owes %d quick, %d slow", quick, slow)
}

package knot

import (
	"fmt"
	"slices"
	"strings"
)

// check holds one knot to every rule about it alone, in the order a
// reader would want to hear them: what it is, then each field.
func (k *Knot) check(path string) *Error {
	t, ok := TypeNamed(k.Type)
	switch {
	case k.Type == "":
		return refuse(RuleRequired, path, "a knot has a type, one of "+strings.Join(TypeNames(), ", "))
	case !ok:
		return refuse(RuleType, path, fmt.Sprintf("its type is %q, and a knot's type is one of %s", k.Type, strings.Join(TypeNames(), ", ")))
	}
	switch {
	case k.ID == "":
		return refuse(RuleRequired, path, "a knot has an id")
	case !IsLocalID(k.ID) || !strings.HasPrefix(k.ID, t.Prefix+"-"):
		return refuse(RuleIDShape, path, fmt.Sprintf("its id is %q, and a%s %s's id is %s- and 7 hex digits", k.ID, article(t.Name), t.Name, t.Prefix))
	case k.Status == "":
		return refuse(RuleRequired, path, fmt.Sprintf("a knot has a status; a%s %s's is one of %s", article(t.Name), t.Name, strings.Join(t.Statuses(), ", ")))
	case !slices.Contains(t.Statuses(), k.Status):
		return refuse(RuleStatus, path, fmt.Sprintf("its status is %q, and a%s %s's status is one of %s", k.Status, article(t.Name), t.Name, strings.Join(t.Statuses(), ", ")))
	case k.Title == "":
		return refuse(RuleRequired, path, "a knot has a title")
	case k.Created == "":
		return refuse(RuleRequired, path, "a knot has a created date")
	case !date.MatchString(k.Created):
		return refuse(RuleFieldShape, path, fmt.Sprintf("created is %q, and a date is YYYY-MM-DD", k.Created))
	}
	if err := k.checkFieldsOnType(path, t); err != nil {
		return err
	}
	if err := k.checkShapes(path); err != nil {
		return err
	}
	switch {
	case t.Name == "outcome" && k.BlockedBy == nil:
		return refuse(RuleRequired, path, "an outcome has a blocked_by list, [] when it waits on nothing")
	case t.Name == "idea" && k.Status == "graduated" && k.GraduatedInto == "":
		return refuse(RuleGraduated, path, "a graduated idea names the outcome or question it graduated into, in graduated_into")
	case k.GraduatedInto != "" && k.Status != "graduated":
		return refuse(RuleGraduated, path, fmt.Sprintf("it names graduated_into, and its status is %s: only a graduated idea names one", k.Status))
	}
	return k.checkLive(path)
}

// checkFieldsOnType refuses a field the knot's type does not carry.
func (k *Knot) checkFieldsOnType(path string, t Type) *Error {
	for _, f := range Fields {
		if !f.carries(t.Name) && k.holds(f.Name) {
			return refuse(RuleFieldOnType, path, fmt.Sprintf("a%s %s does not carry %s; it carries %s", article(t.Name), t.Name, f.Name, strings.Join(FieldNames(t.Name), ", ")))
		}
	}
	return nil
}

// holds reports whether the knot holds field, empty or not for a list.
func (k *Knot) holds(field string) bool {
	switch field {
	case "blocked_by":
		return k.BlockedBy != nil
	case "changes":
		return k.Changes != nil
	}
	return k.Get(field) != ""
}

// checkShapes holds each field that is there to its shape.
func (k *Knot) checkShapes(path string) *Error {
	shape := func(field, value, what string) *Error {
		return refuse(RuleFieldShape, path, fmt.Sprintf("%s is %q, and it is %s", field, value, what))
	}
	const aName = "a name of letters, digits, '.', '_' and '-', opening with a letter or digit"
	switch {
	case k.CreatedBy != "" && !name.MatchString(k.CreatedBy):
		return shape("created_by", k.CreatedBy, "a principal's name: "+aName)
	case k.Owner != "" && !name.MatchString(k.Owner):
		return shape("owner", k.Owner, "a principal's name: "+aName)
	case k.Parent != "" && !IsLocalID(k.Parent):
		return shape("parent", k.Parent, "an outcome's id in this graph, oc- and 7 hex digits")
	case k.Parent != "" && !strings.HasPrefix(k.Parent, "oc-"):
		return refuse(RuleParentOutcome, path, fmt.Sprintf("parent is %s, and a parent is an outcome, oc- and 7 hex digits", k.Parent))
	case k.ClaimedBy != "" && !name.MatchString(k.ClaimedBy):
		return shape("claimed_by", k.ClaimedBy, "a run's ID: "+aName)
	case k.ClaimedAt != "" && !IsTimestamp(k.ClaimedAt):
		return shape("claimed_at", k.ClaimedAt, "a UTC time, YYYY-MM-DDTHH:MM:SSZ")
	case k.Target != "" && !name.MatchString(k.Target) && !qualifiedID.MatchString(k.Target):
		return shape("target", k.Target, "a project's name, or <project>:<id>")
	case k.GraduatedInto != "" && (!IsLocalID(k.GraduatedInto) || !strings.HasPrefix(k.GraduatedInto, "oc-") && !strings.HasPrefix(k.GraduatedInto, "qn-")):
		return shape("graduated_into", k.GraduatedInto, "an outcome's or a question's id in this graph, oc- or qn- and 7 hex digits")
	}
	for _, ref := range k.BlockedBy {
		if !IsLocalID(ref) && !qualifiedID.MatchString(ref) {
			return shape("an entry of blocked_by", ref, "a knot's id, or <project>:<id> for another project's")
		}
	}
	for _, link := range k.Changes {
		if !valleyChange.MatchString(link) && !githubPR.MatchString(link) {
			return shape("an entry of changes", link, "valley:<project>/<change ID>, or https://github.com/<owner>/<repo>/pull/<n>")
		}
	}
	return nil
}

// checkLive holds a knot to the rules the live graph adds to the
// archive's: who created it, its limits, its claim, and the cutoff.
func (k *Knot) checkLive(path string) *Error {
	switch {
	case k.CreatedBy == "":
		return refuse(RuleProvenance, path, "a knot names the principal that created it, in created_by")
	case k.Created <= IDRuleCutoff:
		return refuse(RuleCutoff, path, fmt.Sprintf("created is %s, and a knot is created after %s", k.Created, IDRuleCutoff))
	case (k.ClaimedBy == "") != (k.ClaimedAt == ""):
		return refuse(RuleClaim, path, "claimed_by and claimed_at come together: a claim names its run and when it was made")
	case strings.ContainsAny(k.Title, "\n\t") || checkText(path, []byte(k.Title)) != nil:
		return refuse(RuleText, path, "its title is one line, with no tab or other control character, written or escaped")
	case strings.ContainsAny(k.Source, "\n\t") || checkText(path, []byte(k.Source)) != nil:
		return refuse(RuleText, path, "its source is one line, with no tab or other control character, written or escaped")
	case len(k.Title) > MaxTitle:
		return refuse(RuleLimit, path, fmt.Sprintf("its title is %d bytes, more than the %d a title may be", len(k.Title), MaxTitle))
	case len(k.Source) > MaxTitle:
		return refuse(RuleLimit, path, fmt.Sprintf("its source is %d bytes, more than the %d a source may be", len(k.Source), MaxTitle))
	case len(k.BlockedBy) > MaxListItems || len(k.Changes) > MaxListItems:
		return refuse(RuleLimit, path, fmt.Sprintf("a list holds more than the %d entries one may", MaxListItems))
	}
	for field, value := range map[string]string{"created_by": k.CreatedBy, "owner": k.Owner, "claimed_by": k.ClaimedBy, "target": k.Target} {
		if len(value) > MaxName+12 || (field != "target" && len(value) > MaxName) {
			return refuse(RuleLimit, path, fmt.Sprintf("%s is %d bytes, more than the %d a name may be", field, len(value), MaxName))
		}
	}
	for _, ref := range k.BlockedBy {
		if len(ref) > MaxName+12 {
			return refuse(RuleLimit, path, fmt.Sprintf("blocked_by names %q, longer than a reference may be", ref))
		}
	}
	for _, link := range k.Changes {
		if len(link) > MaxLink {
			return refuse(RuleLimit, path, fmt.Sprintf("changes holds a link of %d bytes, more than the %d one may be", len(link), MaxLink))
		}
	}
	return nil
}

func article(word string) string {
	if strings.ContainsRune("aeiou", rune(word[0])) {
		return "n"
	}
	return ""
}

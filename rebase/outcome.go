package rebase

import "strings"

// Outcome is an integrator outcome record, as the integrator writes it
// (integrator/refs.go): the head it judged, the target tip it judged that
// head against, the fingerprint of the evidence it saw, and its decision.
// A failed pass adds when it failed. The first three are what the
// integrator compares to decide whether to judge a request again.
type Outcome struct {
	Head, Tip, Evidence string
	Decision            string // land, stale, reject, refused-not-ready, refused-merge, voided-withdrawn, failed, or unknown
	FailedAt            string // set for failed alone
}

// ParseOutcome reads an outcome record. Anything it does not recognise is
// unknown, never a decision: a damaged record, or one in a format newer
// than this reader, must not read as a landing or as any other answer the
// integrator did not give. Fields are separated by spaces or tabs, and
// trailing newlines are dropped, as bin/valley read it with read(1); a
// second line, a NUL, or a sixth field makes the record unknown.
func ParseOutcome(record string) Outcome {
	line := strings.TrimRight(record, "\n")
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	field := func(i int) string {
		if i < len(fields) {
			return fields[i]
		}
		return ""
	}
	m := Outcome{Head: field(0), Tip: field(1), Evidence: field(2), Decision: field(3), FailedAt: field(4)}
	if strings.ContainsAny(line, "\n\x00") || len(fields) > 5 ||
		!IsObjectID(m.Head) || !IsObjectID(m.Tip) || !isHex(m.Evidence, 16) {
		m.Decision = "unknown"
	}
	switch m.Decision {
	case "land", "stale", "reject", "refused-not-ready", "refused-merge", "voided-withdrawn":
		if m.FailedAt != "" {
			m.Decision = "unknown"
		}
	case "failed":
		if m.FailedAt == "" {
			m.Decision = "unknown"
		}
	default:
		m.Decision = "unknown"
	}
	if m.Decision != "failed" {
		m.FailedAt = ""
	}
	return m
}

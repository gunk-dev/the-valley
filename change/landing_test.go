package change

import (
	"strings"
	"testing"
)

// A landing record reads back as written, and reads as nothing else: a
// record that does not parse binds no head to a landing.
func TestALandingRecordReadsOnlyAsWritten(t *testing.T) {
	l := Landing{Landed: strings.Repeat("a", 40), Request: "c-" + strings.Repeat("k", 32), Head: strings.Repeat("b", 40)}
	got, err := ParseLanding(l.Format())
	if err != nil || got != l {
		t.Fatalf("ParseLanding(Format()) = %+v, %v; want %+v", got, err, l)
	}
	good := string(l.Format())
	for what, text := range map[string]string{
		"no final newline":   strings.TrimSuffix(good, "\n"),
		"a fourth line":      good + "extra x\n",
		"lines out of order": "request r\nlanded " + l.Landed + "\nhead " + l.Head + "\n",
		"a short commit":     strings.Replace(good, l.Landed, l.Landed[:39], 1),
		"an uppercase head":  strings.Replace(good, l.Head, strings.ToUpper(l.Head), 1),
		"a space in a name":  strings.Replace(good, l.Request, "c- x", 1),
		"an empty name":      strings.Replace(good, "request "+l.Request, "request ", 1),
	} {
		if got, err := ParseLanding([]byte(text)); err == nil {
			t.Errorf("%s: read as %+v", what, got)
		}
	}
}

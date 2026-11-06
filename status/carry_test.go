package main

import "testing"

// An answer carries over landings only as far as the change's carry mode
// allows: same-paths over any, same-class over those that touched none
// of its classes, exact over none.
func TestCarryAllows(t *testing.T) {
	for _, c := range []struct {
		mode   string
		shared bool
		want   bool
	}{
		{"same-paths", false, true},
		{"same-paths", true, true},
		{"same-class", false, true},
		{"same-class", true, false},
		{"exact", false, false},
		{"exact", true, false},
		{"unknown", false, false},
	} {
		if got := carryAllows(c.mode, c.shared); got != c.want {
			t.Errorf("carryAllows(%q, %v) = %v, want %v", c.mode, c.shared, got, c.want)
		}
	}
}

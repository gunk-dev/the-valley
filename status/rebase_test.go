package main

import "testing"

// The manifest lines up the layers' names, says for each change whether a
// version was added, and gives the command that finishes each change whose
// branch may hold its rebased head with no version recording it: one
// pushed and left without a version, or one whose push may have reached
// origin. A plain branch is never unfinished, and neither is a change
// whose branch did not move.
func TestManifest(t *testing.T) {
	const (
		o1, n1 = "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "1111111bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		o2, n2 = "2222222aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		o3, n3 = "3333333aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "3333333bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		id2    = "qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
		id3    = "pppppppppppppppppppppppppppppppp"
	)
	stack := func(pushed string, added2, added3 int) []*layer {
		return []*layer{
			{name: "topic/one", branch: "topic/one", head: o1, rebased: n1, pushed: pushed},
			{name: "qqqq", id: id2, branch: "two", head: o2, rebased: n2, pushed: pushed, added: added2},
			{name: "pppp", id: id3, branch: "my branch", head: o3, rebased: n3, pushed: pushed, added: added3},
		}
	}
	for _, c := range []struct {
		name   string
		layers []*layer
		want   string
	}{
		{"finished", stack(pushDone, 2, 4), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one pushed
  qqqq       2222222 -> 2222222  origin/two pushed, v2 added
  pppp       3333333 -> 3333333  origin/my branch pushed, v4 added
`},
		{"one change left without a version", stack(pushDone, 0, 4), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one pushed
  qqqq       2222222 -> 2222222  origin/two pushed, no version added
  pppp       3333333 -> 3333333  origin/my branch pushed, v4 added
to finish qqqq: valley ready --head ` + n2 + ` --change ` + id2 + ` two
`},
		{"refused", stack(pushNot, 0, 0), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one not pushed
  qqqq       2222222 -> 2222222  origin/two not pushed, no version added
  pppp       3333333 -> 3333333  origin/my branch not pushed, no version added
`},
		{"not known", stack(pushUnknown, 0, 0), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one may have moved
  qqqq       2222222 -> 2222222  origin/two may have moved, no version added
  pppp       3333333 -> 3333333  origin/my branch may have moved, no version added
to finish qqqq: valley ready --head ` + n2 + ` --change ` + id2 + ` two
to finish pppp: valley ready --head ` + n3 + ` --change ` + id3 + ` my\ branch
`},
	} {
		if got := manifest("pppp", c.layers); got != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.name, got, c.want)
		}
	}
}

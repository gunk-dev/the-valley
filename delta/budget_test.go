package delta

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hangEnv makes this test binary stand in for a git that never answers:
// run with it set, the binary sleeps instead of running the tests.
const hangEnv = "DELTA_TEST_GIT_HANGS"

func TestMain(m *testing.M) {
	if os.Getenv(hangEnv) == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// plenty is a budget no vector comes near.
func plenty() Budget {
	return Budget{Bytes: 64 << 20, Entries: 100000, Deadline: time.Now().Add(time.Minute)}
}

// Within a budget it does not cross, BetweenWithin gives every vector the
// delta Between gives it, byte for byte, and refuses the trees Between
// refuses.
func TestBetweenWithinAgreesWithBetween(t *testing.T) {
	r := newRepo(t)
	specs, err := filepath.Glob("testdata/valid/*.json")
	if err != nil || len(specs) == 0 {
		t.Fatalf("no vectors: %v", err)
	}
	for _, spec := range specs {
		pre, post := readVector(t, spec)
		a, b := r.commit(r.tree(pre)), r.commit(r.tree(post))
		want, err := Between(r.repo(), a, b)
		if err != nil {
			t.Fatal(err)
		}
		got, err := BetweenWithin(r.repo(), a, b, plenty())
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if !bytes.Equal(got.Encode(), want.Encode()) {
			t.Fatalf("%s: BetweenWithin gives\n%q\nand Between\n%q", spec, got.Encode(), want.Encode())
		}
	}
	blob := r.blob([]byte("x\n"))
	twice := r.rawTree(rawEntry{"100644", "a", blob}, rawEntry{"100644", "a", blob})
	good := r.tree([]file{{"ok", ModeFile, []byte("x\n")}})
	_, err = BetweenWithin(r.repo(), good, twice, plenty())
	if err == nil || errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "named twice") {
		t.Fatalf("a tree Between refuses: %v", err)
	}
}

// A budget with a bound left unset is refused, so no caller reads
// unbounded by mistake.
func TestABudgetBoundsEverything(t *testing.T) {
	r := newRepo(t)
	tree := r.tree([]file{{"a", ModeFile, []byte("a\n")}})
	for _, b := range []Budget{
		{Entries: 10, Deadline: time.Now().Add(time.Minute)},
		{Bytes: 10, Deadline: time.Now().Add(time.Minute)},
		{Bytes: 10, Entries: 10},
	} {
		if _, err := BetweenWithin(r.repo(), tree, tree, b); err == nil {
			t.Fatalf("the budget %+v was taken", b)
		}
	}
}

// A few small trees, each naming the next many times, describe more paths
// than any machine can walk: four levels of 64 names make 16 million. Each
// time the comparison walks a subtree counts, so the walk stops at the
// entries bound, long before it would finish.
func TestSharedSubtreesSpendEntries(t *testing.T) {
	r := newRepo(t)
	tree := r.tree([]file{{"leaf", ModeFile, []byte("x\n")}})
	for level := 0; level < 4; level++ {
		var entries []rawEntry
		for i := 0; i < 64; i++ {
			entries = append(entries, rawEntry{"40000", string(rune('A'+i/26)) + string(rune('a'+i%26)), tree})
		}
		tree = r.rawTree(entries...)
	}
	empty := r.git(nil, "mktree")
	start := time.Now()
	b := plenty()
	b.Entries = 10000
	_, err := BetweenWithin(r.repo(), empty, tree, b)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "10000 entries") {
		t.Fatalf("the hostile tree: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("stopping at the bound took %s", elapsed)
	}
}

// A blob larger than what is left of the bytes bound is refused once git
// has said its size, and its content is never read.
func TestALargeBlobSpendsBytes(t *testing.T) {
	r := newRepo(t)
	pre := r.tree([]file{{"big", ModeFile, []byte("small\n")}})
	post := r.tree([]file{{"big", ModeFile, bytes.Repeat([]byte("0123456789abcdef"), 1<<18)}})
	b := plenty()
	b.Bytes = 1 << 20
	_, err := BetweenWithin(r.repo(), pre, post, b)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "1048576 bytes") {
		t.Fatalf("a blob of 4 MiB within 1 MiB: %v", err)
	}
	b.Bytes = 8 << 20
	if _, err := BetweenWithin(r.repo(), pre, post, b); err != nil {
		t.Fatalf("a blob of 4 MiB within 8 MiB: %v", err)
	}
}

// Every path the reading builds is spent from the bytes bound: a chain of
// directories with long names builds paths whose lengths add up to many
// times the trees' own size.
func TestDeepPathsSpendBytes(t *testing.T) {
	r := newRepo(t)
	tree := r.tree([]file{{"leaf", ModeFile, []byte("x\n")}})
	name := strings.Repeat("d", 250)
	for level := 0; level < 300; level++ {
		tree = r.rawTree(rawEntry{"40000", name, tree})
	}
	empty := r.git(nil, "mktree")
	b := plenty()
	b.Bytes = 1 << 20
	_, err := BetweenWithin(r.repo(), empty, tree, b)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("a chain of 300 directories: %v", err)
	}
	if _, err := Between(r.repo(), empty, tree); err != nil {
		t.Fatalf("Between, unbounded: %v", err)
	}
}

// git is killed at the deadline, and the reading fails then, whether git
// never answers or the deadline has passed before it starts.
func TestTheDeadlineEndsTheReading(t *testing.T) {
	r := newRepo(t)
	tree := r.tree([]file{{"a", ModeFile, []byte("a\n")}})
	hung := Repo{Git: os.Args[0], Dir: r.dir, Env: append(os.Environ(), hangEnv+"=1")}
	start := time.Now()
	_, err := BetweenWithin(hung, tree, tree, Budget{Bytes: 1 << 20, Entries: 100, Deadline: start.Add(300 * time.Millisecond)})
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("a git that never answers: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the reading ended %s after it began, with a deadline of 300ms", elapsed)
	}
	_, err = BetweenWithin(r.repo(), tree, tree, Budget{Bytes: 1 << 20, Entries: 100, Deadline: time.Now().Add(-time.Second)})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("a deadline already passed: %v", err)
	}
}

// A deadline that passes after git's last answer, while the delta is
// sorted, fails the reading too: no delta comes back late.
func TestTheDeadlineHoldsAfterTheLastRead(t *testing.T) {
	r := newRepo(t)
	pre := r.tree([]file{{"a", ModeFile, []byte("a\n")}})
	post := r.tree([]file{{"a", ModeFile, []byte("b\n")}})
	b := plenty()
	b.Deadline = time.Now().Add(time.Second)
	reached := false
	sorted = func() {
		reached = true
		time.Sleep(time.Until(b.Deadline) + 10*time.Millisecond)
	}
	defer func() { sorted = func() {} }()
	_, err := BetweenWithin(r.repo(), pre, post, b)
	if !reached {
		t.Fatalf("the reading did not reach the sort within a second: %v", err)
	}
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("a deadline passed while sorting: %v", err)
	}
}

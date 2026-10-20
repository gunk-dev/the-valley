package rebase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrInterrupted is a rebase whose git was ended by a signal, as by an
// interrupt from the terminal. The worktree is cleaned up all the same.
var ErrInterrupted = errors.New("the rebase was interrupted; nothing changed")

// keepPrefix is where a prepared family's heads are kept reachable in the
// repository it was prepared in, one ref per layer, until it is released.
// The refs are local and are never pushed.
const keepPrefix = "refs/valley-rebase/"

// Prepared is a family rebased and not yet published: each layer's new
// head, kept reachable in the repository until Release.
type Prepared struct {
	Family *Family
	Heads  map[*Layer]string // each layer's prepared head
	keep   []string
}

// Prepare rebases every layer of a checked family in one throwaway
// worktree, the caller's checkout untouched: the bottom onto main, and
// each layer above onto the prepared layer below it, so a shared lower
// layer is prepared once and every layer on it sits on the same new head.
// Each layer's own commits are rebased, so every layer keeps its own
// delta. A conflict or a layer left empty anywhere ends it with nothing
// prepared and nothing pushed.
//
// The worktree is removed however Prepare ends, and a Prepare that
// returns no Prepared leaves no ref behind. A cancelled context ends the
// git it is running, and Prepare returns the context's error; git ended
// by a signal gives ErrInterrupted.
func Prepare(ctx context.Context, g Git, f *Family) (*Prepared, error) {
	if err := f.Check(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "valley-rebase.")
	if err != nil {
		return nil, &Refusal{Kind: NoWorktree}
	}
	wt := g.in(dir)
	p := &Prepared{Family: f, Heads: map[*Layer]string{}}
	// A step killed part way may have done some of its work. A worktree
	// add may have registered the worktree and not checked it out, and an
	// update-ref may have created the refs before it returned. So each
	// step's cleanup is armed before the step starts. The cleanup runs
	// under a context of its own, since it must run when the caller's is
	// cancelled, and that context is bounded (cleanupContext).
	adding, added, prepared := false, false, false
	defer func() {
		done, cancel := cleanupContext(ctx)
		defer cancel()
		if !prepared {
			p.Release(done, g)
		}
		// git runs in dir only once it is a worktree: a directory that is
		// not one would leave git to find the repository above it.
		if added {
			wt.succeeds(done, true, "rebase", "--abort")
		}
		// worktree remove refuses a worktree whose directory holds no .git
		// file, as a killed worktree add can leave it, unless the
		// directory is gone. Forced twice, it also removes a worktree that
		// is still locked: worktree add locks one until it is checked out.
		os.RemoveAll(dir)
		if adding {
			g.succeeds(done, true, "worktree", "remove", "--force", "--force", dir)
		}
	}()
	stopped := func(err error) error {
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &exit) && exit.ExitCode() == -1:
			return ErrInterrupted
		}
		return nil
	}

	bottom := f.Bottom()
	adding = true
	if _, err := g.output(ctx, false, "worktree", "add", "--quiet", "--detach", dir, bottom.Head); err != nil {
		if err := stopped(err); err != nil {
			return nil, err
		}
		return nil, &Refusal{Kind: NoCheckout, Layer: bottom}
	}
	added = true
	for _, l := range f.Layers {
		onto, below := f.Tip, Target
		if l.Below != nil {
			onto, below = p.Heads[l.Below], l.Below.Name+" as rebased"
		}
		// rebase.updateRefs would move the person's own branches that
		// point into the commits rebased.
		args := []string{"-c", "rebase.updateRefs=false", "rebase", "--quiet"}
		if l == bottom && len(f.Cut) == 0 {
			args = append(args, onto, l.Head)
		} else {
			args = append(args, "--onto", onto, l.Base, l.Head)
		}
		if err := wt.passing(ctx, args...); err != nil {
			if err := stopped(err); err != nil {
				return nil, err
			}
			return nil, &Refusal{Kind: Conflict, Layer: l, Below: below, Paths: conflicted(ctx, wt)}
		}
		head, err := wt.output(ctx, true, "rev-parse", "HEAD")
		if err != nil {
			if err := stopped(err); err != nil {
				return nil, err
			}
			return nil, &Refusal{Kind: NoNewHead, Layer: l}
		}
		if head == onto {
			return nil, &Refusal{Kind: EmptyLayer, Layer: l, Below: below}
		}
		p.Heads[l] = head
	}
	if err := p.hold(ctx, g); err != nil {
		return nil, err
	}
	prepared = true
	return p, nil
}

// conflicted is every path a stopped rebase left unmerged, as git names
// it, whatever bytes the name holds. None is left out: a caller that
// shows them within a bound, as a restack report does, cuts its own view.
func conflicted(ctx context.Context, wt Git) []string {
	out, err := wt.output(ctx, true, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// hold keeps every prepared head reachable, under refs of its own in the
// repository, so the objects outlive the worktree until Release. Each ref
// is in p.keep before update-ref runs, so Release deletes any it created
// however it ended.
func (p *Prepared) hold(ctx context.Context, g Git) error {
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	var stdin strings.Builder
	for k, l := range p.Family.Layers {
		ref := fmt.Sprintf("%s%s/%d", keepPrefix, hex.EncodeToString(token[:]), k)
		fmt.Fprintf(&stdin, "create %s %s\n", ref, p.Heads[l])
		p.keep = append(p.keep, ref)
	}
	if _, err := g.input(ctx, true, strings.NewReader(stdin.String()), "update-ref", "--stdin"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not keep the rebased heads; nothing pushed")
	}
	return nil
}

// Release lets the prepared heads go: their refs are deleted, and git may
// collect the objects no other ref reaches. It runs under a cancelled or
// expired context too, as cleanup does, and for at most cleanupBound.
func (p *Prepared) Release(ctx context.Context, g Git) error {
	if len(p.keep) == 0 {
		return nil
	}
	var stdin strings.Builder
	for _, ref := range p.keep {
		fmt.Fprintf(&stdin, "delete %s\n", ref)
	}
	done, cancel := cleanupContext(ctx)
	defer cancel()
	if _, err := g.input(done, true, strings.NewReader(stdin.String()), "update-ref", "--stdin"); err != nil {
		return errors.New("could not delete the refs that kept the rebased heads")
	}
	p.keep = nil
	return nil
}

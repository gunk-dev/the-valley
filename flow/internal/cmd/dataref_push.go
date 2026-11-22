package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"

	"the-valley/flow/internal/event"
	"the-valley/flow/internal/git"
	"the-valley/flow/internal/run"
)

// dataRefNotInHistory is a failure's message when origin took a history
// that lacks the commit finalize wrote for the run.
const dataRefNotInHistory = "the run's commit is not in the history that was pushed"

// recordDataRefPush pushes the data ref in root to origin when push is set,
// then records on state, and in one event, what happened at finalize. It
// records published only when the history origin took holds commit. A
// failed push changes nothing else: the run keeps its outcome and the local
// commit stays. Only the class and the remote's own lines of a failure are
// kept, since git's other output can echo origin's URL with its credentials.
func recordDataRefPush(ctx context.Context, store run.StateStore, gitClient git.Client, root, dataRef, commit string, push bool, state *run.State) {
	rec := &run.DataRefPush{Ref: dataRef, Commit: commit, Status: run.DataRefNotAttempted}
	if push {
		pushed, err := gitClient.PushDataRef(ctx, root, dataRef)
		switch {
		case err != nil:
			pushErr := &git.PushError{Class: git.PushOther}
			errors.As(err, &pushErr)
			rec.Status, rec.Class, rec.Message = run.DataRefFailed, pushErr.Class, pushErr.Message
		case !git.IsAncestor(ctx, root, commit, pushed):
			rec.Status, rec.Class, rec.Message = run.DataRefFailed, git.PushOther, dataRefNotInHistory
		default:
			rec.Status = run.DataRefPublished
		}
		if rec.Status == run.DataRefFailed {
			fmt.Fprintf(os.Stderr, "warning: at finalize: %s\n  %s\n", dataRefFailure(rec), dataRefHint(rec))
		}
	}
	state.DataRefPush = rec
	if err := store.Save(state); err != nil {
		slog.Warn("failed to save state after the data ref push", "id", state.ID, "err", err)
	}
	hds, ok := store.(*run.HomeDirStore)
	if !ok {
		return
	}
	data := map[string]interface{}{"ref": rec.Ref, "commit": rec.Commit, "status": rec.Status}
	if rec.Status != run.DataRefFailed {
		emitRunEvent(hds.BaseDir(), state, event.AgentDataRefPush, data)
		return
	}
	data["class"], data["message"] = rec.Class, rec.Message
	emitRunEvent(hds.BaseDir(), state, event.AgentDataRefPushFailed, data)
}

// dataRefFailure says on one line how finalize's push of rec failed. Where
// nothing else says so, callers label it as what happened at finalize.
func dataRefFailure(rec *run.DataRefPush) string {
	s := fmt.Sprintf("publishing %s failed (%s)", oneLine(rec.Ref), git.PushClassText(rec.Class))
	if m := oneLine(rec.Message); m != "" {
		s += ": " + m
	}
	return s
}

// dataRefHint says what is known of a conversation finalize could not
// publish, and how it reaches origin. The data ref is one history per clone,
// so any later push of it from that clone carries every earlier run's commit.
func dataRefHint(rec *run.DataRefPush) string {
	ref := oneLine(rec.Ref)
	switch {
	case rec.Class == git.PushNotFastForward:
		// A later run's push from the same clone also leaves origin ahead of
		// this one, and that push carried this run's commit.
		return fmt.Sprintf("origin's %s has moved: it may already hold this run's commit, carried by a later run's push, or hold commits this clone lacks", ref)
	case rec.Class == git.PushUnconfirmed:
		return fmt.Sprintf("origin did not report the push's outcome, so it may hold this run's commit; to be sure, push by hand in the run's clone: git push origin %s", ref)
	case rec.Class == git.PushOther && rec.Message == dataRefNotInHistory:
		return fmt.Sprintf("the clone's %s did not hold this run's commit when it was pushed, so pushing the ref does not publish the run", ref)
	}
	return fmt.Sprintf("the conversation stays in the run's clone; the next run there that publishes carries it, or push by hand there: git push origin %s", ref)
}

// failedDataRefPushes returns the runs whose data ref push failed at
// finalize, in states' order.
func failedDataRefPushes(states []*run.State) []*run.State {
	var failed []*run.State
	for _, s := range states {
		if s.DataRefPush != nil && s.DataRefPush.Status == run.DataRefFailed {
			failed = append(failed, s)
		}
	}
	return failed
}

// oneLine joins the lines of a stored push message for a one-line display,
// dropping control characters, which a hand-edited record could carry.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsControl(r) }), "; ")
}

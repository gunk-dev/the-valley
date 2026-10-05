package main

// Publishing outcomes. The mechanism is the post-receive hook's, exactly:
// one `nats pub` per event, on valley.git.<repo>.<event>, with a payload
// satisfying schema/events.cue. Failure to publish is logged and never
// fatal — git is the source of truth and the bus is the replaceable
// component, so a bus problem costs a log line and one `valley replay`.
// Each publish is cut off after busDeadline, so a bus that hangs costs at
// most that long, and a landing publishes only after its own record is
// complete (refs.go).
//
// Nothing here consumes. Bus authentication (bd-d853d9c) gates automated
// consumers, and the integrator level-triggers over the request refs
// instead, so it needs no subscription.

import (
	"the-valley/integrator/verdict"

	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func (in *integrator) publishLanded(ch verdict.Change, v verdict.Verdict, old, new string) {
	transferred := []string{}
	for _, cv := range v.Checks {
		if cv.Transferred {
			transferred = append(transferred, cv.Name)
		}
	}
	in.publish("integration-succeeded", map[string]any{
		"event":       "integration-succeeded",
		"repo":        in.project,
		"change":      ch.ID,
		"target":      ch.Target,
		"old":         old,
		"new":         new,
		"transferred": transferred,
	})
}

func (in *integrator) publishStale(ch verdict.Change, v verdict.Verdict, tip string) {
	checks := v.Invalidated
	if checks == nil {
		checks = []string{}
	}
	in.publish("request-stale", map[string]any{
		"event":  "request-stale",
		"repo":   in.project,
		"change": ch.ID,
		"target": ch.Target,
		"tip":    tip,
		"reason": v.StaleReason,
		"checks": checks,
	})
}

// publishRefUpdated publishes the ref-updated event for a landing's move.
// A push gets the event from the post-receive hook. The integrator moves
// refs with update-ref, which runs no hook, so it publishes the event
// itself, once the landing is recorded, and never behind a mirror push.
// The fields are the host publisher's, in its order (nix/valley-host.nix,
// and `valley replay` in bin/valley); change one only with the others.
func (in *integrator) publishRefUpdated(ref, old, new string) {
	in.publish("ref-updated", struct {
		Event string `json:"event"`
		Repo  string `json:"repo"`
		Ref   string `json:"ref"`
		Old   string `json:"old"`
		New   string `json:"new"`
	}{"ref-updated", in.project, ref, old, new})
}

// queueRefUpdate hands one ref move to the host, which pushes it to the
// project's mirrors the way it pushes a push's moves. A push queues its
// moves from the post-receive hook. The integrator moves refs with
// update-ref, which runs no hook, so its landings reach the mirrors only if
// it says what it moved.
//
// The integrator does not push the mirrors itself. The mirror credentials
// belong to the git user, and this process runs as its own user. A unit
// running as the git user watches the queue and drains it, and it is the
// only thing that pushes the mirrors (nix/valley-host.nix).
//
// One file per move, holding the line post-receive reads: old, new, ref.
// The file is written beside the queue and renamed into it, so the drain
// never reads a partial one, and names sort in the order the moves were
// made. Like the bus, this is best-effort: a move that cannot be queued is
// reported and the landing stands, because git is the source of truth.
func (in *integrator) queueRefUpdate(ref, old, new string) {
	if in.publishQueue == "" {
		return
	}
	if err := writeRefUpdate(in.publishQueue, ref, old, new); err != nil {
		fmt.Fprintf(in.out, "  publish  %s not queued: %v\n", ref, err)
		return
	}
	fmt.Fprintf(in.out, "  publish  %s queued\n", ref)
}

func writeRefUpdate(queue, ref, old, new string) error {
	if info, err := os.Stat(queue); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory the host drains", queue)
	}
	tmp, err := os.CreateTemp(filepath.Dir(queue), ".valley-publish-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// The drain runs as the git user, which reads the file through the
	// group, and CreateTemp makes it readable by its owner alone.
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return err
	}
	if _, err := fmt.Fprintf(tmp, "%s %s %s\n", old, new, ref); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s", time.Now().UnixNano(), new)
	return os.Rename(tmp.Name(), filepath.Join(queue, name))
}

// publish writes one event. The payload is validated against the event
// vocabulary before it is sent when a schema is configured: an event no
// consumer could read is not a thing to publish, and the discipline that
// keeps the vocabulary one schema'd event at a time is worth nothing if the
// publisher can sidestep it.
// busDeadline bounds one `nats pub`. A local server answers in
// milliseconds; anything near this long is a bus that is not answering.
const busDeadline = 10 * time.Second

func (in *integrator) publish(kind string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(in.out, "  bus      %s not published: %v\n", kind, err)
		return
	}
	if err := in.vetEvent(kind, body); err != nil {
		fmt.Fprintf(in.out, "  bus      %s not published: %v\n", kind, err)
		return
	}
	fmt.Fprintf(in.out, "  event    %s %s\n", kind, body)
	if in.bus == "" {
		return
	}
	subject := fmt.Sprintf("valley.git.%s.%s", in.project, kind)
	ctx, cancel := context.WithTimeout(context.Background(), busDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, in.nats, "--server", in.bus, "pub", subject, string(body))
	// The deadline kills nats itself. WaitDelay stops the wait for its
	// output too, should anything it started still hold the pipe.
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("no answer within %s", busDeadline)
		}
		fmt.Fprintf(in.out, "  bus      publish of %s FAILED: %v: %s\n", subject, err, firstLine(string(out)))
		return
	}
	fmt.Fprintf(in.out, "  bus      %s\n", subject)
}

// eventDefinition names the schema definition a payload is vetted against.
var eventDefinition = map[string]string{
	"integration-succeeded": "#IntegrationSucceeded",
	"ref-updated":           "#RefUpdated",
	"request-stale":         "#RequestStale",
}

func (in *integrator) vetEvent(kind string, body []byte) error {
	if in.eventSchema == "" {
		return nil
	}
	def, ok := eventDefinition[kind]
	if !ok {
		return fmt.Errorf("%s is not in the event vocabulary", kind)
	}
	file, err := writeTemp("valley-integrator-event", body)
	if err != nil {
		return err
	}
	defer removeTemp(file)
	cmd := exec.Command(in.cue, "vet", "-d", def, in.eventSchema, file)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("the payload does not satisfy %s: %s", def, firstLine(string(out)))
	}
	return nil
}

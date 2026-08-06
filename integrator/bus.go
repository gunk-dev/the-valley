package main

// Publishing outcomes. The mechanism is the post-receive hook's, exactly:
// one `nats pub` per event, on valley.git.<repo>.<event>, with a payload
// satisfying schema/events.cue. Failure to publish is logged and never
// fatal — git is the source of truth and the bus is the replaceable
// component, so a bus problem costs a log line and one `valley replay`.
//
// Nothing here consumes. Bus authentication (bd-d853d9c) gates automated
// consumers, and the integrator level-triggers over the request refs
// instead, so it needs no subscription.

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

func (in *integrator) publishLanded(ch Change, v Verdict, old, new string) {
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

func (in *integrator) publishStale(ch Change, v Verdict, tip string) {
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

// publish writes one event. The payload is validated against the event
// vocabulary before it is sent when a schema is configured: an event no
// consumer could read is not a thing to publish, and the discipline that
// keeps the vocabulary one schema'd event at a time is worth nothing if the
// publisher can sidestep it.
func (in *integrator) publish(kind string, payload map[string]any) {
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
	cmd := exec.Command(in.nats, "--server", in.bus, "pub", subject, string(body))
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(in.out, "  bus      publish of %s FAILED: %v: %s\n", subject, err, firstLine(string(out)))
		return
	}
	fmt.Fprintf(in.out, "  bus      %s\n", subject)
}

// eventDefinition names the schema definition a payload is vetted against.
var eventDefinition = map[string]string{
	"integration-succeeded": "#IntegrationSucceeded",
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

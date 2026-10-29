package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"the-valley/readaccess"
	"the-valley/web/export"
)

// The policy path's state, beside what state.go lists:
//
//	authorization.lock      held while a policy check is read, its
//	                        sequence allocated and its authorization signed
//	authorization-sequence  the greatest sequence an authorization was
//	                        signed with
//	authorization           the last authorization the receiver said it
//	                        stored, as a signed note
const (
	authorizationLockFile     = "authorization.lock"
	authorizationSequenceFile = "authorization-sequence"
	authorizationFile         = "authorization"
)

// authorizeAttempts is how many times the policy path signs the current
// policy again when the receiver holds a newer authorization than this
// publisher's counter.
const authorizeAttempts = 3

// errIncomplete is the error of a policy check that could not read every
// project. Its authorization was still signed and sent.
var errIncomplete = errors.New("the policy check is incomplete")

// authorize is the policy path: the only writer of authorizations. Under
// the authorization lock it reads the policy, allocates the next sequence
// from the durable counter, and signs the authorization. It then sends it
// to the receiver, outside the lock. With --out it sends nothing, and the
// signed authorization is written with the export.
//
// A receiver that holds a newer authorization refuses this one as
// replayed, and one that holds another than its basis refuses it as
// stale-basis. Either says the publisher's record of the receiver is out
// of date, as after its state is restored from an older backup. The
// policy path then checks the policy again, signs the current result above
// the receiver's sequence, and keeps no access it could not check: it
// knows nothing it can trust of what the receiver serves. It never sends
// an authorization it signed earlier.
//
// Once the receiver stores an authorization, it is recorded as the last
// one acknowledged, which the next incomplete check may keep access from.
//
// The reply is nil with --out. An incomplete check is sent, and authorize
// then returns an error that wraps errIncomplete.
func (p *publisher) authorize(ctx context.Context) (*export.AuthorizationReply, error) {
	var floor int64
	trusted := true
	for attempt := 1; ; attempt++ {
		c, err := p.signPolicy(ctx, floor, trusted)
		if err != nil {
			return nil, err
		}
		a := c.authorization
		if p.cfg.out != "" {
			p.signedAuthorization = c.signed
			return nil, c.incomplete
		}
		reply, err := p.sendAuthorization(ctx, a, c.signed)
		var refusal *export.Error
		if errors.As(err, &refusal) && (refusal.Code == export.CodeReplayed || refusal.Code == export.CodeStaleBasis) && attempt < authorizeAttempts {
			p.warn("the receiver holds authorization %d, and refused %d as %s; rechecking the policy, keeping no access it cannot check, and signing above %d",
				refusal.HighWater, a.Sequence, refusal.Code, refusal.HighWater)
			floor, trusted = refusal.HighWater, false
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := p.recordAuthorization(a, c.signed); err != nil {
			return nil, err
		}
		p.say("the receiver stored authorization %d: %s", a.Sequence, describe(a))
		return reply, c.incomplete
	}
}

// recordAuthorization records an authorization the receiver said it
// stored, under the authorization lock, unless a greater one is recorded
// already.
func (p *publisher) recordAuthorization(a *export.Authorization, signed []byte) error {
	unlock, err := lockFile(filepath.Join(p.cfg.state, authorizationLockFile))
	if err != nil {
		return err
	}
	defer unlock()
	last, err := p.lastAcknowledged()
	if err != nil {
		return err
	}
	if last != nil && last.Sequence >= a.Sequence {
		return nil
	}
	return writeDurable(filepath.Join(p.cfg.state, authorizationFile), signed, 0o600)
}

// describe is what an authorization lets anyone read, for messages.
func describe(a *export.Authorization) string {
	var names []string
	for _, proj := range a.Projects {
		names = append(names, proj.Name)
	}
	return "anyone may read " + listNames(names)
}

// policyCheck is what one check of the policy found: the projects it
// authorizes, and, when some project's policy could not be read, an error
// that wraps errIncomplete. A signed check also has its authorization and
// the signed note.
type policyCheck struct {
	projects   []export.AuthorizedProject
	incomplete error
	// kept is set when a project keeps access it could not check.
	kept          bool
	authorization *export.Authorization
	signed        []byte
}

// signPolicy reads the policy and signs an authorization for it, holding
// the authorization lock throughout, so of two authorizations the one with
// the greater sequence always reflects the later check. The sequence is
// greater than floor and than every sequence signed before. When trusted
// is set, a project whose policy cannot be read may keep the access the
// last acknowledged authorization gave it, and the authorization then
// names that one as its basis.
func (p *publisher) signPolicy(ctx context.Context, floor int64, trusted bool) (policyCheck, error) {
	unlock, err := lockFile(filepath.Join(p.cfg.state, authorizationLockFile))
	if err != nil {
		return policyCheck{}, err
	}
	defer unlock()
	if p.key, err = p.loadKey(); err != nil {
		return policyCheck{}, err
	}
	prev, err := p.lastAcknowledged()
	if err != nil {
		return policyCheck{}, err
	}
	keepFrom := prev
	if !trusted {
		keepFrom = nil
	}
	c, err := p.checkPolicy(ctx, keepFrom)
	if err != nil {
		return policyCheck{}, err
	}
	// A check that did not read every project leaves the time of the last
	// one that did, or the epoch when none has.
	checked := time.Unix(0, 0).UTC()
	switch {
	case c.incomplete == nil:
		checked = p.now().UTC().Truncate(time.Second)
	case prev != nil:
		checked = prev.Checked
	}
	seq, err := p.nextSequence(floor)
	if err != nil {
		return policyCheck{}, err
	}
	c.authorization = &export.Authorization{Instance: p.cfg.instance, Destination: p.cfg.destination,
		Sequence: seq, Checked: checked, Projects: c.projects}
	if c.kept {
		c.authorization.Basis = keepFrom.Sequence
	}
	if c.signed, err = c.authorization.Sign(p.signer, p.key); err != nil {
		return policyCheck{}, err
	}
	return c, nil
}

// checkPolicy reads who may read each project at the destination: the
// instance's read grants, and each selected project's own policy/read. It
// reads no plan, no knot and no listing of the CLI, so no project's graph
// can fail it. It reads into caches of its own, under policy/ in the
// publisher's cache, so a content pass running beside it is not disturbed.
//
// A project is authorized for anyone when a pass would publish it: a read
// grant to anyone covers it, the host declaration places it in a group,
// and its policy/read lets anyone read it. A project whose policy cannot
// be read keeps what prev gave it, as long as the grants still cover it,
// and never more; the check is then incomplete. prev is the authorization
// the receiver last said it stored, or nil when that is not known, and
// then nothing is kept. An instance or registry that cannot be read fails
// the check, and nothing is signed.
func (p *publisher) checkPolicy(ctx context.Context, prev *export.Authorization) (policyCheck, error) {
	q := *p
	q.origin = filepath.Join(p.cache, "policy", "origin.git")
	q.mirrors = filepath.Join(p.cache, "policy", "repos")
	if err := q.access.CheckSchemas(ctx); err != nil {
		return policyCheck{}, err
	}
	inst, err := q.fetchInstance(ctx)
	if err != nil {
		return policyCheck{}, err
	}
	grants, err := q.readGrants(ctx, inst)
	if err != nil {
		return policyCheck{}, err
	}
	var c policyCheck
	var failed []error
	for _, name := range inst.projects {
		if !q.selects(grants, name) {
			continue
		}
		read, err := q.readableByAnyone(ctx, name, q.projectURL(name), grants)
		if err != nil {
			failed = append(failed, err)
			if old, ok := prev.Project(name); ok && grants.Covers(readaccess.Anyone, name, old.Identity) {
				p.warn("%s: could not read its policy; keeping the access from authorization %d, if the receiver still holds it", name, prev.Sequence)
				c.projects = append(c.projects, export.AuthorizedProject{Name: name, Identity: old.Identity, Readers: []string{export.Anyone}})
				c.kept = true
			}
			continue
		}
		if read.public {
			c.projects = append(c.projects, export.AuthorizedProject{Name: name, Identity: read.consent.Identity, Readers: []string{export.Anyone}})
		}
	}
	if len(failed) > 0 {
		c.incomplete = fmt.Errorf("%w: %w", errIncomplete, errors.Join(failed...))
	}
	return c, nil
}

// lastAcknowledged is the authorization the receiver last said it stored,
// or nil for none.
func (p *publisher) lastAcknowledged() (*export.Authorization, error) {
	data, err := os.ReadFile(filepath.Join(p.cfg.state, authorizationFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a, err := export.ReadStoredAuthorization(data)
	if err != nil {
		p.warn("%s is not a valid authorization; ignoring it: %v", filepath.Join(p.cfg.state, authorizationFile), err)
		return nil, nil
	}
	return a, nil
}

// nextSequence allocates an authorization's sequence: one more than the
// greater of floor and the counter in the state directory, which it writes
// durably before it returns. The counter reads no clock, so no clock
// stepping back can make a later authorization's sequence smaller. A
// counter that cannot be read fails, and is never taken for zero.
func (p *publisher) nextSequence(floor int64) (int64, error) {
	path := filepath.Join(p.cfg.state, authorizationSequenceFile)
	var last int64
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return 0, err
	default:
		last, err = strconv.ParseInt(strings.TrimSuffix(string(data), "\n"), 10, 64)
		if err != nil || last < 0 {
			return 0, fmt.Errorf("%s holds %q, which is not a valid sequence; restore it from a backup, or remove it so the receiver's reply can advance the sequence", path, data)
		}
	}
	last = max(last, floor)
	if last == math.MaxInt64 {
		return 0, fmt.Errorf("authorization sequence %d has reached the maximum value", last)
	}
	next := last + 1
	if err := writeDurable(path, []byte(strconv.FormatInt(next, 10)+"\n"), 0o600); err != nil {
		return 0, err
	}
	return next, nil
}

// writeDurable is writeFile, with the directory synced after the rename,
// so the file survives a crash once it returns.
func writeDurable(path string, data []byte, mode os.FileMode) error {
	if err := writeFile(path, data, mode); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return syncFile(d)
}

// sendAuthorization posts the signed authorization, and retries, with the
// same authorization, a request that failed in transit, that the receiver
// answered with a status and no refusal, or that it refused as unreadable,
// after a pause that doubles each time. A receiver that has no
// authorization path is older than this publisher, and the error says so.
func (p *publisher) sendAuthorization(ctx context.Context, a *export.Authorization, signed []byte) (*export.AuthorizationReply, error) {
	rc := receiver{base: strings.TrimSuffix(p.cfg.receiver, "/"), token: p.token, client: p.client}
	backoff := firstBackoff
	for attempt := 1; ; attempt++ {
		reply, err := rc.postAuthorization(ctx, signed)
		if err == nil && reply.Sequence != a.Sequence {
			err = fmt.Errorf("the receiver stored authorization %d, not %d", reply.Sequence, a.Sequence)
		}
		if err == nil {
			return &reply, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var status statusError
		if errors.As(err, &status) && status.code == 404 {
			return nil, fmt.Errorf("the receiver answered 404 to %s because its valley-web-receive is older than this publisher; upgrade the edge before the valley host", export.AuthorizationPath)
		}
		var refusal *export.Error
		if errors.As(err, &refusal) && refusal.Code != export.CodeUnreadable {
			return nil, fmt.Errorf("the receiver refused authorization %d: %w", a.Sequence, err)
		}
		if attempt == uploadAttempts {
			return nil, fmt.Errorf("sending authorization %d failed %d times: %w", a.Sequence, attempt, err)
		}
		p.warn("%v; trying again", err)
		if err := p.sleep(ctx, backoff); err != nil {
			return nil, err
		}
		backoff *= 2
	}
}

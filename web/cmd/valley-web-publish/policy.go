package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
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
//	links-delivery          the greatest credential file revision signed
//	                        from, written under the links lock beside the
//	                        credential file, and the revision the last
//	                        stored authorization carried the links of
//	                        (link.go)
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

// errOverflow is the error of a policy check whose authorization is too
// large to sign in full. Its bounded fallback was signed and sent in its
// place, and the access only the full one gives is pending.
var errOverflow = errors.New("the authorization is too large to send")

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
// Growth never blocks a restriction. A check whose authorization would be
// larger than the limit, less export.FallbackRoom, signs and sends its
// bounded fallback instead (fallBack): the last authorization the
// receiver acknowledged, less every access this check no longer gives,
// and restricted further when that is over the limit too, so every
// withdrawal and revocation still reaches the edge.
//
// The reply is nil with --out. An incomplete check is sent, and authorize
// then returns an error that wraps errIncomplete. So is a fallback, with
// an error that wraps errOverflow, and an authorization signed with no
// bindings because the links lock could not be taken or the credential
// file could not be read, with that error. A send that fails returns its
// error joined with those. Every attempt that signs a fallback prints what
// is pending and what the fallback dropped, whether or not it was sent or
// stored. When the receiver refuses a fallback and the policy path tries
// again, that fallback's overflow is joined into every failure a later
// attempt returns, a check that cannot be read included, so the pass's
// error still names the access it left pending and dropped.
func (p *publisher) authorize(ctx context.Context) (*export.AuthorizationReply, error) {
	var floor int64
	trusted := true
	// refused is the overflow of each fallback the receiver refused.
	var refused []error
	carry := func(err error) error {
		if err == nil {
			return nil
		}
		return errors.Join(append([]error{err}, refused...)...)
	}
	for attempt := 1; ; attempt++ {
		c, err := p.signPolicy(ctx, floor, trusted)
		if err != nil {
			return nil, carry(err)
		}
		a := c.authorization
		failed := errors.Join(c.incomplete, c.overflow, c.links.err)
		if p.cfg.out != "" {
			p.signedAuthorization = c.signed
			p.sayOverflow(c)
			return nil, failed
		}
		reply, err := p.sendAuthorization(ctx, a, c.signed)
		var refusal *export.Error
		if errors.As(err, &refusal) && (refusal.Code == export.CodeReplayed || refusal.Code == export.CodeStaleBasis) && attempt < authorizeAttempts {
			p.sayOverflow(c)
			if c.overflow != nil {
				refused = append(refused, fmt.Errorf("the receiver refused authorization %d as %s: %w", a.Sequence, refusal.Code, c.overflow))
			}
			p.warn("the receiver holds authorization %d, and refused %d as %s; rechecking the policy, keeping no access it cannot check, and signing above %d",
				refusal.HighWater, a.Sequence, refusal.Code, refusal.HighWater)
			floor, trusted = refusal.HighWater, false
			continue
		}
		if err != nil {
			p.sayOverflow(c)
			return nil, carry(errors.Join(err, failed))
		}
		if err := p.recordAuthorization(a, c.signed, c.revision); err != nil {
			p.sayOverflow(c)
			return nil, carry(errors.Join(err, failed))
		}
		p.say("the receiver stored authorization %d: %s", a.Sequence, describe(a))
		p.sayOverflow(c)
		return reply, carry(failed)
	}
}

// sayOverflow prints, when the check signed its bounded fallback, the
// access that is pending and, when the fallback was itself over the
// limit, the access it dropped to fit.
func (p *publisher) sayOverflow(c policyCheck) {
	if c.overflow == nil {
		return
	}
	p.say("pending: %s", c.pending)
	if c.dropped != "" {
		p.say("dropped: %s", c.dropped)
	}
}

// recordAuthorization records an authorization the receiver said it
// stored, and the credential file revision it carried the read links of,
// under the authorization lock, unless a greater one is recorded already.
func (p *publisher) recordAuthorization(a *export.Authorization, signed []byte, revision int64) error {
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
	if err := writeDurable(filepath.Join(p.cfg.state, authorizationFile), signed, 0o600); err != nil {
		return err
	}
	return p.recordDelivery(a, revision)
}

// describe is who an authorization lets read each project, for messages:
// "app to anyone, operator; site to alice".
func describe(a *export.Authorization) string {
	var each []string
	for _, proj := range a.Projects {
		each = append(each, proj.Name+" to "+strings.Join(proj.Readers, ", "))
	}
	s := "no project may be read"
	if len(each) > 0 {
		s = strings.Join(each, "; ")
	}
	if len(a.Bindings) > 0 {
		s += fmt.Sprintf("; %d read links", len(a.Bindings))
	}
	return s
}

// policyCheck is what one check of the policy found: the projects it
// authorizes, and, when some project's policy could not be read, an error
// that wraps errIncomplete. A signed check also has its authorization and
// the signed note.
type policyCheck struct {
	projects   []export.AuthorizedProject
	incomplete error
	// kept names the projects whose policy the check could not read, and
	// that keep access on the authorization's basis.
	kept []string
	// links is what the check read of the read links.
	links linkRead
	// overflow, which wraps errOverflow, is set when the authorization is
	// the bounded fallback. pending then says what access the full one
	// gives and the fallback, narrowed from the last acknowledged one,
	// does not. dropped, when that fallback was over the limit too, says
	// what access the restriction to fit took from it.
	overflow      error
	pending       string
	dropped       string
	authorization *export.Authorization
	signed        []byte
	// revision is the credential file revision the authorization carries
	// the links of, or 0 for none read from the file.
	revision int64
}

// signPolicy reads the policy and signs an authorization for it, holding
// the authorization lock throughout, so of two authorizations the one with
// the greater sequence always reflects the later check. The sequence is
// greater than floor and than every sequence signed before. When trusted
// is set, a project whose policy cannot be read may keep the access the
// last acknowledged authorization gave it, and the authorization then
// names that one as its basis. The read links are read last, after the
// check, under the links lock, which is held until the authorization is
// signed, and every authorization it signs carries them, one that keeps
// access included. Every change to the credential file holds the links
// lock, so none lands between that read and the signature. The lock order
// is the authorization lock, then the links lock.
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
	if len(c.kept) > 0 {
		c.authorization.Basis = keepFrom.Sequence
	}
	unlockLinks, links := p.readBindings()
	c.links, c.revision = links, links.revision
	c.authorization.Bindings = c.links.bindings
	if size := c.authorization.SignedSize(p.signer); size > p.limit-export.FallbackRoom {
		p.fallBack(&c, prev, size)
	}
	c.signed, err = c.authorization.Sign(p.signer, p.key)
	unlockLinks()
	if err != nil {
		return policyCheck{}, err
	}
	return c, nil
}

// fallBack puts in place of the check's authorization, which would be
// size bytes signed, its bounded fallback: prev, the authorization the
// receiver last said it stored, less every access the check no longer
// gives (export.Narrow). It keeps the authorization's sequence, which
// nothing was signed with, its checked time and its basis, so the
// receiver admits it as it would the full one, and what it keeps of prev
// is within what the check gives.
//
// The fallback is measured, never assumed to fit. One narrowed from an
// authorization this publisher signed in full fits, as that left
// export.FallbackRoom. One narrowed from an authorization the receiver
// admitted without that room, whose sequence has grown a digit or that
// adds a basis, can be over the limit. It is then restricted further
// until it fits (export.Fit): first the projects kept on the basis, then
// readers, the largest project first, then read links, and at last it
// grants nothing. Every step only removes access, so the restriction is
// always safe, and the access it drops is reported beside what is
// pending.
//
// The fallback carries only the read links prev and the check both carry.
// Its delivery is recorded as the revision prev carried the links of, so a
// link issued since stays pending; a revocation since is recorded pending
// too, though the fallback delivers it. A fallback that drops a read link
// to fit is recorded as carrying none from the file, so every link is
// pending until an authorization fits.
func (p *publisher) fallBack(c *policyCheck, prev *export.Authorization, size int) {
	full := c.authorization
	narrowed := export.Narrow(prev, full)
	c.authorization = export.Fit(narrowed, c.kept, p.signer, p.limit)
	c.revision = 0
	if d := c.links.delivered; prev != nil && c.links.err == nil && d.Sequence == prev.Sequence &&
		len(c.authorization.Bindings) == len(narrowed.Bindings) {
		c.revision = min(d.Revision, c.links.revision)
	}
	c.pending = withheld(full, narrowed)
	from := "no authorization, as the receiver has acknowledged none"
	if prev != nil {
		from = fmt.Sprintf("authorization %d, the last the receiver acknowledged", prev.Sequence)
	}
	msg := fmt.Sprintf("authorization %d would be %d bytes signed, more than the %d a pass signs in full: a receiver reads at most %d, and a pass leaves %d of room; "+
		"its bounded fallback was signed in its place, from %s, less every access this check no longer gives, adding none",
		full.Sequence, size, p.limit-export.FallbackRoom, p.limit, export.FallbackRoom, from)
	if c.authorization != narrowed {
		c.dropped = withheld(narrowed, c.authorization)
		msg += fmt.Sprintf("; that fallback would be %d bytes signed, over the %d a receiver reads too, so to fit it also drops %s",
			narrowed.SignedSize(p.signer), p.limit, c.dropped)
	}
	c.overflow = fmt.Errorf("%w: %s; pending until an authorization fits: %s", errOverflow, msg, c.pending)
}

// withheld is what full gives and sent does not, for messages: "app to
// bob; site to 1200 readers; 3 read links".
func withheld(full, sent *export.Authorization) string {
	var each []string
	for _, proj := range full.Projects {
		var readers []string
		for _, r := range proj.Readers {
			if !sent.Allows(r, proj.Name, proj.Identity) {
				readers = append(readers, r)
			}
		}
		switch {
		case len(readers) == 0:
		case len(readers) > 4:
			each = append(each, fmt.Sprintf("%s to %d readers", proj.Name, len(readers)))
		default:
			each = append(each, proj.Name+" to "+strings.Join(readers, ", "))
		}
	}
	links := 0
	for _, b := range full.Bindings {
		if !slices.Contains(sent.Bindings, b) {
			links++
		}
	}
	if links > 0 {
		each = append(each, fmt.Sprintf("%d read links", links))
	}
	if len(each) == 0 {
		return "no access"
	}
	return strings.Join(each, "; ")
}

// checkPolicy reads who may read each project at the destination: the
// instance's read grants, and each selected project's own policy/read. It
// reads no plan, no knot and no listing of the CLI, so no project's graph
// can fail it. It reads into caches of its own, under policy/ in the
// publisher's cache, so a content pass running beside it is not disturbed.
//
// A project is authorized when a pass would export it: the host
// declaration places it in a group, and it has an effective reader, a
// principal its policy/read names and a read grant covers. The
// authorization names each of its effective readers, anyone among them
// only when both consents let anyone read it. A project whose policy
// cannot be read keeps those of the readers prev gave it that the grants
// still cover, under prev's publication identity, and never another; the
// check is then incomplete. prev is the authorization the receiver last
// said it stored, or nil when that is not known, and then nothing is
// kept. An instance or registry that cannot be read fails the check, and
// nothing is signed.
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
		read, err := q.readConsent(ctx, name, q.projectURL(name), grants)
		if err != nil {
			failed = append(failed, err)
			if kept, ok := stillCovered(prev, grants, name); ok {
				p.warn("%s: could not read its policy; keeping the access authorization %d gave %s, if the receiver still holds it",
					name, prev.Sequence, strings.Join(kept.Readers, ", "))
				c.projects = append(c.projects, kept)
				c.kept = append(c.kept, name)
			}
			continue
		}
		if len(read.readers) > 0 {
			c.projects = append(c.projects, export.AuthorizedProject{Name: name, Identity: read.consent.Identity, Readers: read.readers})
		}
	}
	if len(failed) > 0 {
		c.incomplete = fmt.Errorf("%w: %w", errIncomplete, errors.Join(failed...))
	}
	return c, nil
}

// stillCovered is what prev authorized of the project that the grants
// still cover: the project under prev's publication identity, with those
// of prev's readers a grant covers it for. It is false when prev does not
// name the project, or the grants cover none of its readers.
func stillCovered(prev *export.Authorization, grants readaccess.Grants, name string) (export.AuthorizedProject, bool) {
	old, ok := prev.Project(name)
	if !ok {
		return export.AuthorizedProject{}, false
	}
	kept := export.AuthorizedProject{Name: name, Identity: old.Identity}
	for _, r := range old.Readers {
		if grants.Covers(r, name, old.Identity) {
			kept.Readers = append(kept.Readers, r)
		}
	}
	return kept, len(kept.Readers) > 0
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
	return writeDurableGroup(path, data, mode, -1)
}

// writeDurableGroup is writeDurable, with the file's group set to gid
// unless gid is -1.
func writeDurableGroup(path string, data []byte, mode os.FileMode, gid int) error {
	if err := writeFileGroup(path, data, mode, gid); err != nil {
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

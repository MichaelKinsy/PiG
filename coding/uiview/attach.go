package uiview

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
)

// pig additive (D614): attachments. Every client that draws mounts holds one attachment. The registry resolves each
// mount for each client (the alternative its claims select, or the fallback when the content does not fit it) and
// delivers full snapshots, never deltas.
//
// The contract of a delivery stream:
//   - Order. Deliveries to one attachment are made one at a time, in the order the registry accepted the operations
//     that caused them, and are numbered 1, 2, 3 and so on without a hole.
//   - Gap. A sink that returns false refused the delivery. The attachment is then in a gap: it is sent nothing more,
//     and no number is used, until Resync returns every current snapshot and the number of the last delivery made.
//     A client that sees a hole in the numbers it received calls Resync as well.
//   - A sink runs under the registry lock. It must not block and must not call the registry or its attachment; to stop
//     the stream it returns false.
//   - End. Detach waits for a delivery in progress, and after it returns no delivery is made or running. An intent
//     raised under an ended attachment is stale without reaching the plugin; one in flight when the attachment ends is
//     settled stale, and its handler is cancelled if the intent is cancellable.
//   - Errors. UpdateAbilities, Resync and Report on an ended attachment fail with CodeStale.

// Delivery is one snapshot sent to one client.
type Delivery struct {
	Attachment uint64   `json:"attachment"`
	Seq        uint64   `json:"seq"`
	Snapshot   Snapshot `json:"snapshot"`
}

// Sink takes the deliveries of an attachment. It returns false when it could not take one.
type Sink func(Delivery) bool

// Resync is every mount a client draws, resolved for it. Seq is the number of the last delivery made to the attachment,
// so the next one is Seq+1.
type Resync struct {
	Attachment uint64     `json:"attachment"`
	Seq        uint64     `json:"seq"`
	Snapshots  []Snapshot `json:"snapshots"`
}

// Attachment is one client's hold on the registry.
type Attachment struct {
	r         *Registry
	gen       uint64
	abilities ClientAbilities
	sink      Sink
	seq       uint64
	gap       bool
	ended     bool
	// sent is the identity of the last snapshot of each mount this client was sent, so a change of its abilities
	// redelivers only the mounts that now resolve differently.
	sent map[ownerKey]string
	ctx  context.Context
	end  context.CancelFunc
}

// Attach attaches a client and returns its attachment with every current mount resolved for it. A nil sink takes no
// deliveries (the client reads Resync when it wants to draw).
func (r *Registry) Attach(a ClientAbilities, sink Sink) (*Attachment, Resync, *Error) {
	if err := a.check(); err != nil {
		return nil, Resync{}, err
	}
	ctx, end := context.WithCancel(context.Background())
	r.mu.Lock()
	r.gen++
	t := &Attachment{r: r, gen: r.gen, abilities: cloneAbilities(a), sink: sink, sent: map[ownerKey]string{}, ctx: ctx, end: end}
	r.atts = append(r.atts, t)
	r.merged = mergeAbilities(r.atts)
	rs := r.resync(t)
	fn := r.onAbility
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return t, rs, nil
}

// Generation is the attachment's number; every delivery carries it.
func (t *Attachment) Generation() uint64 { return t.gen }

// InGap reports whether a refused delivery stopped the stream.
func (t *Attachment) InGap() bool {
	t.r.mu.Lock()
	defer t.r.mu.Unlock()
	return t.gap
}

// ordered returns the mounts oldest first, the order of every bulk delivery.
func (r *Registry) ordered() []ownerKey {
	keys := make([]ownerKey, 0, len(r.mounts))
	for k := range r.mounts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b ownerKey) int {
		return int(int64(r.mounts[a].snap.Mount.Epoch) - int64(r.mounts[b].snap.Mount.Epoch))
	})
	return keys
}

func (r *Registry) resync(t *Attachment) Resync {
	rs := Resync{Attachment: t.gen, Snapshots: []Snapshot{}}
	clear(t.sent)
	for _, k := range r.ordered() {
		if s, ok := r.resolve(t, r.mounts[k]); ok {
			rs.Snapshots = append(rs.Snapshots, s)
			t.sent[k] = identity(&s)
		}
	}
	t.gap = false
	rs.Seq = t.seq
	return rs
}

// Resync ends a gap: it returns every current mount resolved for the client and the number of the last delivery made.
func (t *Attachment) Resync() (Resync, *Error) {
	t.r.mu.Lock()
	defer t.r.mu.Unlock()
	if t.ended {
		return Resync{}, fail(CodeStale, "attachment %d ended", t.gen)
	}
	return t.r.resync(t), nil
}

// UpdateAbilities replaces the client's abilities and redelivers the mounts that resolve differently for them. It walks
// every mount once: per mount at most maxAlternatives predicates and a comparison of cached identities.
func (t *Attachment) UpdateAbilities(a ClientAbilities) *Error {
	if err := a.check(); err != nil {
		return err
	}
	r := t.r
	r.mu.Lock()
	if t.ended {
		r.mu.Unlock()
		return fail(CodeStale, "attachment %d ended", t.gen)
	}
	t.abilities = cloneAbilities(a)
	r.merged = mergeAbilities(r.atts)
	for _, k := range r.ordered() {
		if t.gap {
			break
		}
		s, ok := r.resolve(t, r.mounts[k])
		if !ok {
			// The client no longer draws the slot and has dropped the mount; if it draws the slot again it is sent anew.
			delete(t.sent, k)
		} else if t.sent[k] != identity(&s) {
			t.deliver(k, s)
		}
	}
	fn := r.onAbility
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

// Detach ends the attachment. It waits for a delivery in progress; afterwards no delivery is made.
func (t *Attachment) Detach() {
	r := t.r
	r.mu.Lock()
	if t.ended {
		r.mu.Unlock()
		return
	}
	t.ended = true
	r.atts = slices.DeleteFunc(r.atts, func(o *Attachment) bool { return o == t })
	r.merged = mergeAbilities(r.atts)
	if r.solo == t {
		r.solo = nil
	}
	fn := r.onAbility
	r.mu.Unlock()
	t.end()
	if fn != nil {
		fn()
	}
}

// Invoke raises an intent for this client, as [Registry.Invoke] does. Under an ended attachment the result is stale and
// the plugin is not reached; an intent in flight when the attachment ends is settled stale.
func (t *Attachment) Invoke(ctx context.Context, plugin string, ref Ref, nodeKey, intent string, arg json.RawMessage, inv Invoker) Result {
	if t.ctx.Err() != nil {
		return Result{Outcome: OutcomeStale, Error: "attachment ended"}
	}
	ictx, stop := context.WithCancel(ctx)
	defer stop()
	defer context.AfterFunc(t.ctx, stop)()
	res := t.r.Invoke(ictx, plugin, ref, nodeKey, intent, arg, inv)
	if res.Outcome == OutcomeCancelled && ctx.Err() == nil {
		return Result{Ticket: res.Ticket, Outcome: OutcomeStale, Error: "attachment ended"}
	}
	return res
}

// Report records that this client could not draw part of a mount. Code is undrawn, unsupported or limit.
func (t *Attachment) Report(plugin string, ref Ref, n Notice) *Error {
	if n.Code != NoticeUndrawn && n.Code != string(CodeUnsupported) && n.Code != string(CodeLimit) {
		return fail(CodeInvalid, "a client reports undrawn, unsupported or limit, not %q", n.Code)
	}
	if n.Severity != SeverityInfo && n.Severity != SeverityWarning && n.Severity != SeverityError {
		return fail(CodeInvalid, "unknown severity %q", n.Severity)
	}
	r := t.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.ended {
		return fail(CodeStale, "attachment %d ended", t.gen)
	}
	m := r.mounts[ownerKey{plugin, ref.Key}]
	if m == nil {
		return fail(CodeNotFound, "no mount under key %q", ref.Key)
	}
	if m.snap.Mount.Epoch != ref.Epoch {
		return fail(CodeStale, "epoch %d is not current (%d)", ref.Epoch, m.snap.Mount.Epoch)
	}
	m.note(Notice{Code: n.Code, Severity: n.Severity, Message: n.Message, Path: n.Path, Claim: n.Claim, Measured: n.Measured, Limit: n.Limit})
	return nil
}

// deliver sends one snapshot. The caller holds the lock and has checked the gap.
func (t *Attachment) deliver(k ownerKey, s Snapshot) {
	t.seq++
	t.sent[k] = identity(&s)
	if t.sink != nil && !t.sink(Delivery{Attachment: t.gen, Seq: t.seq, Snapshot: s}) {
		t.gap = true
	}
}

// publish delivers a changed mount to every client that draws its slot and is not in a gap.
func (r *Registry) publish(k ownerKey, m *mount) {
	for _, t := range r.atts {
		if t.gap {
			continue
		}
		if s, ok := r.resolve(t, m); ok {
			t.deliver(k, s)
		}
	}
}

// identity names what a client was sent: the revision, the state and which content.
func identity(s *Snapshot) string {
	b := make([]byte, 0, 96)
	b = strconv.AppendUint(b, s.Mount.Epoch, 10)
	b = append(b, '.')
	b = strconv.AppendUint(b, s.Mount.Rev, 10)
	b = append(b, '.')
	b = append(b, s.State...)
	b = append(b, '.')
	b = append(b, s.Reason...)
	b = append(b, '.')
	b = strconv.AppendInt(b, int64(s.Alternative), 10)
	b = strconv.AppendBool(b, s.Degraded)
	b = append(b, s.Digest...)
	return string(b)
}

func fallbackFits(a *ClientAbilities, m measured) bool {
	return m.bytes <= a.Limits.SnapshotBytes && m.nodes <= a.Limits.Nodes && m.depth+1 <= a.Limits.Depth && m.text <= a.Limits.TextBytes
}

// resolve returns the snapshot one client draws for a mount: the alternative its claims select or the view, with the
// a11y and focus entries of the nodes that view has. A client that does not draw the slot is not sent the mount. A
// client the content does not fit is sent the fallback (or no view when that does not fit either), marked degraded, and
// the plugin gets a notice. It reads the measurements made when the content was admitted and never walks a view.
func (r *Registry) resolve(t *Attachment, m *mount) (Snapshot, bool) {
	if !slices.Contains(t.abilities.Slots, m.snap.Slot) {
		return Snapshot{}, false
	}
	c := &m.snap.Content
	i, view, d := candidate(&t.abilities, m.snap.Slot, c)
	s := m.snap
	out := Content{Intents: c.Intents, Assets: c.Assets, View: view, Drawing: d}
	ids := m.cands[i].ids
	if bad := fits(&t.abilities, c, m.cands[i], d); bad != nil {
		n := Notice{Code: string(bad.code), Severity: SeverityWarning, Path: "delivery", Message: bad.message}
		if bad.code == CodeLimit {
			n.Measured, n.Limit = &bad.measured, &bad.over
		}
		m.note(n)
		i, ids = -1, nil
		out.View, out.Drawing = nil, nil
		s.Degraded = true
		if m.fb != nil && fallbackFits(&t.abilities, *m.fb) {
			out.View = append(append(json.RawMessage(`{"root":`), c.Fallback...), '}')
			ids = m.fb.ids
		}
	}
	for _, e := range c.A11y {
		if _, ok := ids[e.Node]; !ok {
			continue
		}
		e.Relations = slices.DeleteFunc(slices.Clone(e.Relations), func(rel Relation) bool { _, ok := ids[rel.Target]; return !ok })
		out.A11y = append(out.A11y, e)
	}
	for _, f := range c.Focus {
		if _, ok := ids[f.Node]; ok {
			out.Focus = append(out.Focus, f)
		}
	}
	if i > 0 {
		s.Alternative = i
	}
	s.Content = out
	s.Digest = m.digest(i, &out)
	return s, true
}

// digest returns the canonical hash of a resolved content, computed once per revision and candidate. A content RFC 8785
// cannot canonicalize (a lone surrogate, a number outside a double) has no digest, and a client redraws it.
func (m *mount) digest(i int, c *Content) string {
	if d, ok := m.digests[i]; ok {
		return d
	}
	var d string
	if raw, err := json.Marshal(c); err == nil {
		d, _ = Digest(raw)
	}
	if m.digests == nil {
		m.digests = map[int]string{}
	}
	m.digests[i] = d
	return d
}

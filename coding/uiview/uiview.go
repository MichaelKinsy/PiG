// Package uiview is the host side of D614, the one data-only UI protocol for plugins: a plugin mounts a D107 view in a
// client slot, updates it, and receives the user's intents as typed requests. The registry owns the mount lifecycle,
// stale-write rejection, intent tickets and the replicated client abilities. It knows nothing about a client or a
// plugin transport: a client subscribes to snapshots and a transport supplies an [Invoker].
//
// pig additive (D614). Closed records, no version field: an unknown field or kind rejects the operation atomically.
package uiview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Slot names where a client draws a mount. The set is closed.
type Slot string

const (
	SlotNotify      Slot = "notify"
	SlotStatus      Slot = "status"
	SlotWidgetAbove Slot = "widget-above"
	SlotWidgetBelow Slot = "widget-below"
	SlotHeader      Slot = "header"
	SlotFooter      Slot = "footer"
	SlotDialog      Slot = "dialog"
	SlotOverlay     Slot = "overlay"
	SlotPanel       Slot = "panel"
	SlotToolCard    Slot = "tool-card"
	SlotMessage     Slot = "message"
	SlotEntry       Slot = "entry"
)

var slots = []Slot{SlotNotify, SlotStatus, SlotWidgetAbove, SlotWidgetBelow, SlotHeader, SlotFooter, SlotDialog, SlotOverlay, SlotPanel, SlotToolCard, SlotMessage, SlotEntry}

// State is where a mount is in its lifecycle. finished, removed and broken are terminal and the first one wins.
type State string

const (
	StateLive     State = "live"
	StateHidden   State = "hidden"
	StateFinished State = "finished"
	StateRemoved  State = "removed"
	StateBroken   State = "broken"
)

func (s State) terminal() bool { return s == StateFinished || s == StateRemoved || s == StateBroken }

// Reason says why the host broke a mount. It is empty in every other state.
type Reason string

const (
	ReasonSessionReplaced Reason = "session_replaced"
	ReasonOwnerGone       Reason = "owner_gone"
	ReasonShutdown        Reason = "shutdown"
)

// Code classifies an operation error.
type Code string

const (
	CodeInvalid     Code = "invalid"     // malformed or out-of-vocabulary record
	CodeUnsupported Code = "unsupported" // the attached client has no such slot
	CodeLimit       Code = "limit"       // a client limit would be exceeded
	CodeStale       Code = "stale"       // epoch or rev is not the mount's current one
	CodeTerminal    Code = "terminal"    // the mount already reached a terminal state
	CodeNotFound    Code = "not_found"   // no mount under that key
)

// Error is a failed operation. Code is stable; Message is for people.
type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func fail(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Ref names one revision of one mount incarnation. Epoch increases for every mount; Rev for every accepted update.
type Ref struct {
	Key   string `json:"key"`
	Epoch uint64 `json:"epoch"`
	Rev   uint64 `json:"rev"`
}

// Target ties a tool-card, message or entry mount to the thing it decorates.
type Target struct {
	ToolCallID  string `json:"toolCallId,omitempty"`
	MessageType string `json:"messageType,omitempty"`
	EntryType   string `json:"entryType,omitempty"`
}

// Asset is a bounded binary a view refers to by id.
type Asset struct {
	ID       string `json:"id"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Delivery string `json:"delivery"`
}

// IntentDecl declares one intent a view's nodes may raise.
type IntentDecl struct {
	Name        string `json:"name"`
	NodeKey     string `json:"nodeKey,omitempty"`
	Arg         Shape  `json:"arg"`
	Answer      *Shape `json:"answer,omitempty"`
	Cancellable bool   `json:"cancellable,omitempty"`
	TimeoutMs   int    `json:"timeoutMs,omitempty"`
}

// Content is what a mount shows: the D107 view, the intents its nodes raise, its assets and a degrade node.
type Content struct {
	View     json.RawMessage `json:"view"`
	Intents  []IntentDecl    `json:"intents,omitempty"`
	Assets   []Asset         `json:"assets,omitempty"`
	Fallback json.RawMessage `json:"fallback,omitempty"`
}

// MountArgs is the ui.view.mount record.
type MountArgs struct {
	Key    string  `json:"key"`
	Slot   Slot    `json:"slot"`
	Target *Target `json:"target,omitempty"`
	Content
}

// UpdateArgs is the ui.view.update record: a full replacement of the content of the named revision.
type UpdateArgs struct {
	Mount Ref `json:"mount"`
	Content
}

// RefArgs is the record of hide, show, finish and remove. Rev is not compared.
type RefArgs struct {
	Mount Ref `json:"mount"`
}

// StateArgs is the ui.view.state record.
type StateArgs struct {
	Key string `json:"key"`
}

// Status is the reply of every operation that names a mount.
type Status struct {
	Mount  Ref    `json:"mount"`
	State  State  `json:"state"`
	Reason Reason `json:"reason,omitempty"`
}

// Snapshot is the whole of a mount, what a client draws.
type Snapshot struct {
	Plugin string  `json:"plugin"`
	Slot   Slot    `json:"slot"`
	Target *Target `json:"target,omitempty"`
	Status
	Content
}

// Event reports a change of a mount to a subscribed client. Snapshot is a full snapshot, never a delta.
type Event struct {
	Snapshot Snapshot
}

// DecodeMount, DecodeUpdate, DecodeRef and DecodeState decode a record strictly: unknown fields, trailing data and a
// missing required member reject it.
func DecodeMount(raw []byte) (MountArgs, *Error) {
	var a MountArgs
	if err := strict(raw, &a); err != nil {
		return a, err
	}
	if n := len(a.Key); n < 1 || n > 128 {
		return a, fail(CodeInvalid, "key must be 1..128 bytes")
	}
	if !slices.Contains(slots, a.Slot) {
		return a, fail(CodeInvalid, "unknown slot %q", a.Slot)
	}
	if a.Target != nil && !validTarget(a.Slot, *a.Target) {
		return a, fail(CodeInvalid, "target does not fit slot %q", a.Slot)
	}
	if a.Target == nil && (a.Slot == SlotToolCard || a.Slot == SlotMessage || a.Slot == SlotEntry) {
		return a, fail(CodeInvalid, "slot %q needs a target", a.Slot)
	}
	return a, a.check()
}

func DecodeUpdate(raw []byte) (UpdateArgs, *Error) {
	var a UpdateArgs
	if err := strict(raw, &a); err != nil {
		return a, err
	}
	if a.Mount.Key == "" {
		return a, fail(CodeInvalid, "mount.key is required")
	}
	return a, a.check()
}

func DecodeRef(raw []byte) (RefArgs, *Error) {
	var a RefArgs
	if err := strict(raw, &a); err != nil {
		return a, err
	}
	if a.Mount.Key == "" {
		return a, fail(CodeInvalid, "mount.key is required")
	}
	return a, nil
}

func DecodeState(raw []byte) (StateArgs, *Error) {
	var a StateArgs
	if err := strict(raw, &a); err != nil {
		return a, err
	}
	if a.Key == "" {
		return a, fail(CodeInvalid, "key is required")
	}
	return a, nil
}

func validTarget(slot Slot, t Target) bool {
	set := 0
	for _, s := range []string{t.ToolCallID, t.MessageType, t.EntryType} {
		if s != "" {
			set++
		}
	}
	if set != 1 {
		return false
	}
	switch slot {
	case SlotToolCard:
		return t.ToolCallID != ""
	case SlotMessage:
		return t.MessageType != ""
	case SlotEntry:
		return t.EntryType != ""
	}
	return false
}

func strict(raw []byte, into any) *Error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fail(CodeInvalid, "%v", err)
	}
	if dec.More() {
		return fail(CodeInvalid, "trailing data")
	}
	return nil
}

func (c *Content) check() *Error {
	var view struct {
		Root json.RawMessage `json:"root"`
	}
	if len(c.View) == 0 || json.Unmarshal(c.View, &view) != nil || len(view.Root) == 0 {
		return fail(CodeInvalid, "view must be an object with a root")
	}
	if len(c.Fallback) > 0 && !bytes.HasPrefix(bytes.TrimSpace(c.Fallback), []byte("{")) {
		return fail(CodeInvalid, "fallback must be a node object")
	}
	seen := map[string]struct{}{}
	for i := range c.Intents {
		d := &c.Intents[i]
		if d.Name == "" {
			return fail(CodeInvalid, "intent name is required")
		}
		if _, dup := seen[d.Name]; dup {
			return fail(CodeInvalid, "intent %q declared twice", d.Name)
		}
		seen[d.Name] = struct{}{}
		if d.TimeoutMs == 0 {
			d.TimeoutMs = defaultIntentTimeoutMs
		}
		// pig additive (D614): an intent timeout is 100..300000 ms.
		if d.TimeoutMs < 100 || d.TimeoutMs > 300000 {
			return fail(CodeInvalid, "intent %q timeoutMs must be 100..300000", d.Name)
		}
		if err := d.Arg.check(); err != nil {
			return fail(CodeInvalid, "intent %q arg: %v", d.Name, err)
		}
		if d.Answer != nil {
			if err := d.Answer.check(); err != nil {
				return fail(CodeInvalid, "intent %q answer: %v", d.Name, err)
			}
		}
	}
	for _, a := range c.Assets {
		if a.ID == "" || a.Size < 0 || len(a.SHA256) != 64 || (a.Delivery != "inline" && a.Delivery != "managed") {
			return fail(CodeInvalid, "asset %q is malformed", a.ID)
		}
		if !slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, a.MIME) {
			return fail(CodeInvalid, "asset %q has unsupported mime %q", a.ID, a.MIME)
		}
	}
	return nil
}

// pig additive (D614): an intent without a timeout waits 30 s.
const defaultIntentTimeoutMs = 30000

// Limits bound what one client draws. A zero value allows nothing.
type Limits struct {
	Nodes          int `json:"nodes"`
	Depth          int `json:"depth"`
	SnapshotBytes  int `json:"snapshotBytes"`
	TextBytes      int `json:"textBytes"`
	Mounts         int `json:"mounts"`
	IntentArgBytes int `json:"intentArgBytes"`
}

// Input says which input a client delivers to intents.
type Input struct {
	Keyboard bool `json:"keyboard"`
	Pointer  bool `json:"pointer"`
	Text     bool `json:"text"`
}

// ClientAbilities is what the attached client draws. The host replicates it to every extension, which reads it before it
// mounts. Slots lists the slots the client draws; Nodes the view node kinds it draws; the rest bound a mount.
type ClientAbilities struct {
	Slots        []Slot   `json:"slots"`
	Nodes        []string `json:"nodes"`
	Markdown     bool     `json:"markdown"`
	ImageFormats []string `json:"imageFormats"`
	Limits       Limits   `json:"limits"`
	Input        Input    `json:"input"`
}

// Outcome is how an intent ended.
type Outcome string

const (
	OutcomeDone      Outcome = "done"
	OutcomeRefused   Outcome = "refused"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeFailed    Outcome = "failed"
	OutcomeTimeout   Outcome = "timeout"
	OutcomeStale     Outcome = "stale"
)

// Invocation is one intent the user raised, as the plugin receives it.
type Invocation struct {
	Mount   Ref             `json:"mount"`
	Rev     uint64          `json:"rev"`
	NodeKey string          `json:"nodeKey,omitempty"`
	Intent  string          `json:"intent"`
	Arg     json.RawMessage `json:"arg"`
	Ticket  string          `json:"ticket"`
}

// Result is the plugin's reply and the final IntentResult. A plugin answers done, refused or failed; the host decides
// cancelled, timeout and stale.
type Result struct {
	Ticket  string          `json:"ticket"`
	Outcome Outcome         `json:"outcome"`
	Answer  json.RawMessage `json:"answer,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// Invoker carries intents to the plugin that owns a mount. Invoke returns when the plugin answers or ctx ends. Ending
// ctx cancels the plugin's handler through the transport's own request cancellation, so there is no second cancel path.
type Invoker interface {
	Invoke(ctx context.Context, inv Invocation) (Result, error)
}

type mount struct {
	snap Snapshot
}

type ownerKey struct{ plugin, key string }

// Registry holds every plugin's mounts. The zero value is not usable; use [New].
type Registry struct {
	mu        sync.Mutex
	epoch     uint64
	ticket    uint64
	mounts    map[ownerKey]*mount
	abilities *ClientAbilities
	subs      []func(Event)
	onAbility func()
	kindOK    func(string) bool
}

// Options configure a registry.
type Options struct {
	// KnownKind reports whether a view node kind is in the host's vocabulary. Nil accepts any kind.
	KnownKind func(kind string) bool
}

// New returns an empty registry with no client attached: nothing can be mounted until [Registry.SetAbilities].
func New(o Options) *Registry {
	return &Registry{mounts: map[ownerKey]*mount{}, kindOK: o.KnownKind}
}

// Subscribe registers fn for every mount change, in order. fn runs under the registry lock and must not call back.
func (r *Registry) Subscribe(fn func(Event)) {
	r.mu.Lock()
	r.subs = append(r.subs, fn)
	r.mu.Unlock()
}

// OnAbilities registers fn, called after the abilities changed and outside the lock, so a transport can replicate them.
func (r *Registry) OnAbilities(fn func()) {
	r.mu.Lock()
	r.onAbility = fn
	r.mu.Unlock()
}

// SetAbilities attaches a client with the given abilities, or detaches it with nil. Detaching keeps live mounts; they
// stay until their owner ends them, and no new one can be made.
func (r *Registry) SetAbilities(a *ClientAbilities) {
	var c *ClientAbilities
	if a != nil {
		cp := *a
		cp.Slots = slices.Clone(a.Slots)
		cp.Nodes = slices.Clone(a.Nodes)
		cp.ImageFormats = slices.Clone(a.ImageFormats)
		c = &cp
	}
	r.mu.Lock()
	r.abilities = c
	fn := r.onAbility
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Abilities returns a copy of the attached client's abilities, or nil.
func (r *Registry) Abilities() *ClientAbilities {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.abilities == nil {
		return nil
	}
	cp := *r.abilities
	cp.Slots = slices.Clone(cp.Slots)
	cp.Nodes = slices.Clone(cp.Nodes)
	cp.ImageFormats = slices.Clone(cp.ImageFormats)
	return &cp
}

func (r *Registry) publish(m *mount) {
	ev := Event{Snapshot: m.snap}
	for _, fn := range r.subs {
		fn(ev)
	}
}

// measure counts the nodes (objects with a string "kind") and the nesting depth of a view and reports the first node
// kind outside the host's vocabulary.
func measure(raw json.RawMessage, known func(string) bool) (nodes, depth, text int, badKind string) {
	var walk func(v any, d int)
	walk = func(v any, d int) {
		if d > depth {
			depth = d
		}
		switch t := v.(type) {
		case map[string]any:
			if k, ok := t["kind"].(string); ok {
				nodes++
				if known != nil && !known(k) && badKind == "" {
					badKind = k
				}
			}
			for _, c := range t {
				walk(c, d+1)
			}
		case []any:
			for _, c := range t {
				walk(c, d+1)
			}
		case string:
			if len(t) > text {
				text = len(t)
			}
		}
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		walk(v, 0)
	}
	return
}

func (r *Registry) admit(slot Slot, c Content) *Error {
	a := r.abilities
	if a == nil {
		return fail(CodeUnsupported, "no client is attached")
	}
	if slot != "" && !slices.Contains(a.Slots, slot) {
		return fail(CodeUnsupported, "the client does not draw slot %q", slot)
	}
	size := len(c.View) + len(c.Fallback)
	if size > a.Limits.SnapshotBytes {
		return fail(CodeLimit, "snapshot is %d bytes, the client takes %d", size, a.Limits.SnapshotBytes)
	}
	nodes, depth, text, bad := measure(c.View, r.kindOK)
	if bad != "" {
		return fail(CodeInvalid, "unknown node kind %q", bad)
	}
	if nodes > a.Limits.Nodes || depth > a.Limits.Depth || text > a.Limits.TextBytes {
		return fail(CodeLimit, "view has %d nodes, depth %d, longest text %d; the client takes %d, %d, %d", nodes, depth, text, a.Limits.Nodes, a.Limits.Depth, a.Limits.TextBytes)
	}
	for _, d := range c.Intents {
		if !a.Input.Keyboard && !a.Input.Pointer && !a.Input.Text {
			return fail(CodeUnsupported, "the client delivers no input, so intent %q cannot be raised", d.Name)
		}
	}
	return nil
}

// Mount makes a new mount for plugin. A key that is live or hidden is taken; a key whose mount ended can be mounted again
// under a new epoch.
func (r *Registry) Mount(plugin string, a MountArgs) (Status, *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := ownerKey{plugin, a.Key}
	if old := r.mounts[k]; old != nil && !old.snap.State.terminal() {
		return Status{}, fail(CodeInvalid, "key %q is already mounted", a.Key)
	}
	live := 0
	for ok, m := range r.mounts {
		if ok.plugin == plugin && !m.snap.State.terminal() {
			live++
		}
	}
	if r.abilities != nil && live >= r.abilities.Limits.Mounts {
		return Status{}, fail(CodeLimit, "%d live mounts is the client's limit", r.abilities.Limits.Mounts)
	}
	if err := r.admit(a.Slot, a.Content); err != nil {
		return Status{}, err
	}
	r.epoch++
	m := &mount{}
	m.snap = Snapshot{Plugin: plugin, Slot: a.Slot, Target: a.Target, Content: a.Content,
		Status: Status{Mount: Ref{Key: a.Key, Epoch: r.epoch, Rev: 1}, State: StateLive}}
	r.mounts[k] = m
	r.publish(m)
	return m.snap.Status, nil
}

func (r *Registry) find(plugin, key string, epoch uint64) (*mount, *Error) {
	m := r.mounts[ownerKey{plugin, key}]
	if m == nil {
		return nil, fail(CodeNotFound, "no mount under key %q", key)
	}
	if m.snap.Mount.Epoch != epoch {
		return nil, fail(CodeStale, "epoch %d is not current (%d)", epoch, m.snap.Mount.Epoch)
	}
	return m, nil
}

// Update replaces the content of a live or hidden mount. It is rejected as stale unless the epoch and rev are current,
// so an update computed from an old snapshot never overwrites a newer one.
func (r *Registry) Update(plugin string, a UpdateArgs) (Status, *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.find(plugin, a.Mount.Key, a.Mount.Epoch)
	if err != nil {
		return Status{}, err
	}
	if m.snap.State.terminal() {
		return m.snap.Status, fail(CodeTerminal, "mount is %s", m.snap.State)
	}
	if m.snap.Mount.Rev != a.Mount.Rev {
		return m.snap.Status, fail(CodeStale, "rev %d is not current (%d)", a.Mount.Rev, m.snap.Mount.Rev)
	}
	if err := r.admit("", a.Content); err != nil {
		return m.snap.Status, err
	}
	m.snap.Content = a.Content
	m.snap.Mount.Rev++
	r.publish(m)
	return m.snap.Status, nil
}

// Transition applies hide, show, finish or remove. A terminal state is final: a later operation on the mount is rejected
// with CodeTerminal and the state the first one reached.
func (r *Registry) Transition(plugin string, op string, a RefArgs) (Status, *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.find(plugin, a.Mount.Key, a.Mount.Epoch)
	if err != nil {
		return Status{}, err
	}
	if m.snap.State.terminal() {
		return m.snap.Status, fail(CodeTerminal, "mount is already %s", m.snap.State)
	}
	var to State
	switch op {
	case "hide":
		to = StateHidden
	case "show":
		to = StateLive
	case "finish":
		to = StateFinished
	case "remove":
		to = StateRemoved
	default:
		return m.snap.Status, fail(CodeInvalid, "unknown operation %q", op)
	}
	m.snap.State = to
	r.publish(m)
	return m.snap.Status, nil
}

// State reports a mount of plugin under key.
func (r *Registry) State(plugin string, a StateArgs) (Status, *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.mounts[ownerKey{plugin, a.Key}]
	if m == nil {
		return Status{}, fail(CodeNotFound, "no mount under key %q", a.Key)
	}
	return m.snap.Status, nil
}

// Snapshots returns every mount, for a client that attaches late.
func (r *Registry) Snapshots() []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Snapshot, 0, len(r.mounts))
	for _, m := range r.mounts {
		out = append(out, m.snap)
	}
	slices.SortFunc(out, func(a, b Snapshot) int {
		if a.Mount.Epoch < b.Mount.Epoch {
			return -1
		}
		if a.Mount.Epoch > b.Mount.Epoch {
			return 1
		}
		return 0
	})
	return out
}

// Break ends every non-terminal mount of plugin as broken for reason. An empty plugin breaks every plugin's mounts.
func (r *Registry) Break(plugin string, reason Reason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, m := range r.mounts {
		if (plugin != "" && k.plugin != plugin) || m.snap.State.terminal() {
			continue
		}
		m.snap.State, m.snap.Reason = StateBroken, reason
		r.publish(m)
	}
}

// Invoke raises an intent on a mount and returns its final result. The intent is checked against the mount's declaration
// first: an unknown intent or an argument outside its shape is refused without reaching the plugin. The result is the
// first of the plugin's answer, the timeout, and ctx ending. A cancellable intent's handler is cancelled when the
// timeout or ctx ends it; an answer that arrives afterwards is dropped. An answer for a mount that changed in the
// meantime is stale.
func (r *Registry) Invoke(ctx context.Context, plugin string, ref Ref, nodeKey, intent string, arg json.RawMessage, inv Invoker) Result {
	r.mu.Lock()
	m := r.mounts[ownerKey{plugin, ref.Key}]
	if m == nil || m.snap.Mount.Epoch != ref.Epoch || m.snap.Mount.Rev != ref.Rev {
		r.mu.Unlock()
		return Result{Outcome: OutcomeStale, Error: "mount changed"}
	}
	if m.snap.State != StateLive {
		r.mu.Unlock()
		return Result{Outcome: OutcomeRefused, Error: "mount is " + string(m.snap.State)}
	}
	var decl *IntentDecl
	for i := range m.snap.Intents {
		if m.snap.Intents[i].Name == intent {
			decl = &m.snap.Intents[i]
		}
	}
	limit := 0
	if r.abilities != nil {
		limit = r.abilities.Limits.IntentArgBytes
	}
	if decl == nil || (decl.NodeKey != "" && decl.NodeKey != nodeKey) {
		r.mu.Unlock()
		return Result{Outcome: OutcomeRefused, Error: "unknown intent " + strconv.Quote(intent)}
	}
	if len(arg) > limit {
		r.mu.Unlock()
		return Result{Outcome: OutcomeRefused, Error: fmt.Sprintf("argument is %d bytes, the limit is %d", len(arg), limit)}
	}
	if err := decl.Arg.Validate(arg); err != nil {
		r.mu.Unlock()
		return Result{Outcome: OutcomeRefused, Error: err.Error()}
	}
	d := *decl
	r.ticket++
	ticket := "t" + strconv.FormatUint(r.ticket, 10)
	snapRef := m.snap.Mount
	r.mu.Unlock()

	cctx, stop := context.WithTimeout(ctx, time.Duration(d.TimeoutMs)*time.Millisecond)
	defer stop()
	// Only a cancellable intent has its handler cancelled when the deadline or the caller ends it; any other runs on
	// and its late answer is dropped.
	handlerCtx := context.WithoutCancel(ctx)
	if d.Cancellable {
		handlerCtx = cctx
	}
	type answer struct {
		res Result
		err error
	}
	done := make(chan answer, 1)
	go func() {
		res, err := inv.Invoke(handlerCtx, Invocation{Mount: Ref{Key: snapRef.Key, Epoch: snapRef.Epoch}, Rev: snapRef.Rev, NodeKey: nodeKey, Intent: intent, Arg: arg, Ticket: ticket})
		done <- answer{res, err}
	}()
	ended := func() Result {
		if ctx.Err() != nil {
			return Result{Ticket: ticket, Outcome: OutcomeCancelled}
		}
		return Result{Ticket: ticket, Outcome: OutcomeTimeout}
	}
	select {
	case a := <-done:
		// An answer that lost the race with the deadline or the caller's cancel is a late answer: it is dropped.
		if cctx.Err() != nil {
			return ended()
		}
		res := a.res
		res.Ticket = ticket
		if a.err != nil {
			return Result{Ticket: ticket, Outcome: OutcomeFailed, Error: a.err.Error()}
		}
		if res.Outcome != OutcomeDone && res.Outcome != OutcomeRefused && res.Outcome != OutcomeFailed {
			return Result{Ticket: ticket, Outcome: OutcomeFailed, Error: "plugin answered with outcome " + strconv.Quote(string(res.Outcome))}
		}
		r.mu.Lock()
		cur := r.mounts[ownerKey{plugin, ref.Key}]
		changed := cur == nil || cur.snap.Mount != snapRef
		r.mu.Unlock()
		if changed {
			return Result{Ticket: ticket, Outcome: OutcomeStale, Error: "mount changed while the plugin answered"}
		}
		if res.Outcome == OutcomeDone && d.Answer != nil {
			if err := d.Answer.Validate(res.Answer); err != nil {
				return Result{Ticket: ticket, Outcome: OutcomeFailed, Error: "answer: " + err.Error()}
			}
		}
		return res
	case <-cctx.Done():
		return ended()
	}
}

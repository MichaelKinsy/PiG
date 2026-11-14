// Package uiview is the host side of D614, the one data-only UI protocol for plugins: a plugin mounts a D107 view in a
// client slot, updates it, and receives the user's intents as typed requests. The registry owns the mount lifecycle,
// stale-write rejection, intent tickets, the notices of a mount and the replicated client abilities. It knows nothing
// about a client or a plugin transport: a client holds an [Attachment] and a transport supplies an [Invoker].
//
// pig additive (D614). Closed records, no version field: an unknown field or kind rejects the operation atomically.
package uiview

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/sortx"
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
	// WhenHidden lets a client raise the intent while the mount is hidden: it is how the user opens a view its plugin
	// hid, and the plugin's handler shows the mount. At most one intent of a mount has it; it names no node and its
	// argument shape accepts the empty object, which is what a client sends.
	WhenHidden bool `json:"whenHidden,omitempty"`
	// Dismiss names the intent a client raises, with the empty object, when the user dismisses a dialog or overlay
	// mount: the plugin's handler closes the mount. At most one intent of a mount has it; it names no node and its
	// argument shape accepts the empty object. A client ignores it on a mount of another slot.
	Dismiss bool `json:"dismiss,omitempty"`
}

// Content is what a mount shows: the D107 view, the intents its nodes raise, its assets and a degrade node; and beside
// them the alternatives a client's claims select from, a drawing, the a11y and focus records of the view's nodes, and
// the bindings of keys and gestures to its intents.
// Title is the short text a client labels the mount with, in a tab or a list of views.
type Content struct {
	Title string          `json:"title,omitempty"`
	View  json.RawMessage `json:"view"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Intents []IntentDecl `json:"intents,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Assets   []Asset         `json:"assets,omitempty"`
	Fallback json.RawMessage `json:"fallback,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Alternatives []Alternative `json:"alternatives,omitempty"`
	Drawing      *Drawing      `json:"drawing,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	A11y []A11y `json:"a11y,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Focus []Focus `json:"focus,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Bindings []Binding `json:"bindings,omitempty"`
}

// MountArgs is the ui.view.mount record. Answering is not a member of the record: a transport sets it to the ticket of
// the intent whose handler made the call (see [Registry.Invoke]). A record is held to the closed shape once: by
// [DecodeMount] when it came off the wire, by [Registry.Mount] when it was built in Go.
type MountArgs struct {
	Key    string  `json:"key"`
	Slot   Slot    `json:"slot"`
	Target *Target `json:"target,omitempty"`
	Content
	Answering string `json:"-"`
	checked   bool
}

// UpdateArgs is the ui.view.update record: a full replacement of the content of the named revision. Answering is not a
// member of the record: a transport sets it to the ticket of the intent whose handler made the call. A record is held to
// the closed shape once: by [DecodeUpdate] when it came off the wire, by [Registry.Update] when it was built in Go.
type UpdateArgs struct {
	Mount Ref `json:"mount"`
	Content
	Answering string `json:"-"`
	checked   bool
}

// RefArgs is the record of hide, show and remove. Rev is not compared.
type RefArgs struct {
	Mount Ref `json:"mount"`
}

// pig additive (D614): a finish result is at most 65536 bytes.
const maxResultBytes = 65536

// FinishArgs is the ui.view.finish record. Result is the plugin's last word on the mount, any JSON value.
type FinishArgs struct {
	Mount  Ref             `json:"mount"`
	Result json.RawMessage `json:"result,omitempty"`
}

// StateArgs is the ui.view.state record.
type StateArgs struct {
	Key string `json:"key"`
}

// Status is the reply of every operation that names a mount. Result is the result a finished mount was given.
type Status struct {
	Mount  Ref             `json:"mount"`
	State  State           `json:"state"`
	Reason Reason          `json:"reason,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Snapshot is the whole of a mount. [Registry.Snapshots] returns it as the plugin sent it. A client is sent it resolved:
// Content holds the one view that client draws, Alternative says which (0 the view, n alternative n-1), Degraded that
// the content did not fit the client and the fallback (or nothing) stands in, and Digest is the canonical hash of the
// resolved content.
type Snapshot struct {
	Plugin string  `json:"plugin"`
	Slot   Slot    `json:"slot"`
	Target *Target `json:"target,omitempty"`
	Status
	Content
	Alternative int    `json:"alternative,omitempty"`
	Degraded    bool   `json:"degraded,omitempty"`
	Digest      string `json:"digest,omitempty"`
}

// DecodeMount, DecodeUpdate, DecodeRef and DecodeState decode a record strictly: unknown fields, trailing data and a
// missing required member reject it.
func DecodeMount(raw []byte) (MountArgs, *Error) {
	var a MountArgs
	dec := strictDecoder(raw)
	if err := closed(dec, dec.Decode(&a)); err != nil {
		return a, err
	}
	err := a.Check()
	return a, err
}

// Check verifies a mount record that was not decoded by DecodeMount, which calls it: the key is 1..128 bytes, the slot
// is known, the target fits the slot and is there when the slot needs one, and the content is well formed (the title,
// the view's root, the fallback, the intents, assets, alternatives, drawing, accessibility and focus members). It sets
// the default timeout of an intent that names none, as decoding does.
// MountArgs must keep its own check: without it a.check() is the embedded content's check (no key, slot, target, mark).
func (a *MountArgs) Check() *Error { return a.check() }

// check holds a mount record to the closed shape and marks the record that passed, so that it is checked once whichever
// way it reaches the registry.
func (a *MountArgs) check() *Error {
	if a.checked {
		return nil
	}
	if n := len(a.Key); n < 1 || n > 128 {
		return fail(CodeInvalid, "key must be 1..128 bytes")
	}
	if offLabel(a.Key) {
		return fail(CodeInvalid, "key holds %s", labelFault)
	}
	if !slices.Contains(slots, a.Slot) {
		return fail(CodeInvalid, "unknown slot %q", a.Slot)
	}
	if a.Target != nil && !validTarget(a.Slot, *a.Target) {
		return fail(CodeInvalid, "target does not fit slot %q", a.Slot)
	}
	if a.Target == nil && (a.Slot == SlotToolCard || a.Slot == SlotMessage || a.Slot == SlotEntry) {
		return fail(CodeInvalid, "slot %q needs a target", a.Slot)
	}
	if err := a.Content.check(); err != nil {
		return err
	}
	a.checked = true
	return nil
}

func DecodeUpdate(raw []byte) (UpdateArgs, *Error) {
	var a UpdateArgs
	dec := strictDecoder(raw)
	if err := closed(dec, dec.Decode(&a)); err != nil {
		return a, err
	}
	err := a.Check()
	return a, err
}

// Check verifies an update record that was not decoded by DecodeUpdate, which calls it: the mount's key is named and
// the content is well formed, as MountArgs.Check verifies it. It sets the default timeout of an intent that names none,
// as decoding does.
// UpdateArgs must keep its own check: without it a.check() is the embedded content's check (no mount key, no mark).
func (a *UpdateArgs) Check() *Error { return a.check() }

// check holds an update record to the closed shape and marks the record that passed, as [MountArgs.check] does.
func (a *UpdateArgs) check() *Error {
	if a.checked {
		return nil
	}
	if a.Mount.Key == "" {
		return fail(CodeInvalid, "mount.key is required")
	}
	if err := a.Content.check(); err != nil {
		return err
	}
	a.checked = true
	return nil
}

func DecodeRef(raw []byte) (RefArgs, *Error) {
	var a RefArgs
	dec := strictDecoder(raw)
	if err := closed(dec, dec.Decode(&a)); err != nil {
		return a, err
	}
	if a.Mount.Key == "" {
		return a, fail(CodeInvalid, "mount.key is required")
	}
	return a, nil
}

func DecodeFinish(raw []byte) (FinishArgs, *Error) {
	var a FinishArgs
	dec := strictDecoder(raw)
	if err := closed(dec, dec.Decode(&a)); err != nil {
		return a, err
	}
	if a.Mount.Key == "" {
		return a, fail(CodeInvalid, "mount.key is required")
	}
	if len(a.Result) > maxResultBytes {
		return a, fail(CodeInvalid, "result is %d bytes, at most %d", len(a.Result), maxResultBytes)
	}
	return a, nil
}

func DecodeState(raw []byte) (StateArgs, *Error) {
	var a StateArgs
	dec := strictDecoder(raw)
	if err := closed(dec, dec.Decode(&a)); err != nil {
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

// strictDecoder reads one closed record: an unknown member is an error. The caller decodes into its own record type and
// passes the result to closed.
func strictDecoder(raw []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec
}

// closed turns the result of a strict decode into the operation error; data after the record is an error too.
func closed(dec *json.Decoder, err error) *Error {
	if err != nil {
		return fail(CodeInvalid, "%v", err)
	}
	if dec.More() {
		return fail(CodeInvalid, "trailing data")
	}
	return nil
}

func hasRoot(raw json.RawMessage) bool {
	var view struct {
		Root json.RawMessage `json:"root"`
	}
	return len(raw) > 0 && json.Unmarshal(raw, &view) == nil && len(view.Root) > 0
}

// pig additive (D614): a mount's title is at most 64 bytes and a label (offLabel). An empty title is no title.
const maxTitle = 64

// labelFault says what a label must not hold.
const labelFault = "a control character, a line separator or a control that reorders text"

// pig additive (D614): a label is text a client writes on one line among its own: a mount's title, a mount's key, which
// labels a mount that has no title, and a binding's name. offLabel reports whether s cannot be one. A label holds no C0
// or C1 control character (ESC and the line breaks are among them), neither U+2028 nor U+2029, and none of the controls
// that reorder or hide the text around them, with which one label could pass for another: the embeddings and overrides
// U+202A to U+202E and the isolates U+2066 to U+2069. The direction marks U+200E, U+200F and U+061C are not among them:
// Arabic and Hebrew text beside Latin holds them, and they reorder nothing on their own.
func offLabel(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
	})
}

func (c *Content) check() *Error {
	if len(c.Title) > maxTitle {
		return fail(CodeInvalid, "title is %d bytes, at most %d", len(c.Title), maxTitle)
	}
	if offLabel(c.Title) {
		return fail(CodeInvalid, "title holds %s", labelFault)
	}
	if !hasRoot(c.View) {
		return fail(CodeInvalid, "view must be an object with a root")
	}
	if len(c.Fallback) > 0 && !bytes.HasPrefix(bytes.TrimSpace(c.Fallback), []byte("{")) {
		return fail(CodeInvalid, "fallback must be a node object")
	}
	seen := map[string]struct{}{}
	whenHidden, dismiss := 0, 0
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
		if d.WhenHidden {
			if whenHidden++; whenHidden > 1 {
				return fail(CodeInvalid, "intent %q: at most one intent of a mount may be whenHidden", d.Name)
			}
			if d.NodeKey != "" {
				return fail(CodeInvalid, "intent %q is whenHidden and names a node; a hidden mount draws none", d.Name)
			}
			if err := d.Arg.Validate(json.RawMessage(`{}`)); err != nil {
				return fail(CodeInvalid, "intent %q is whenHidden and its arg does not accept {}: %v", d.Name, err)
			}
		}
		// pig additive (D614): the intent a client raises when the user dismisses a dialog or overlay mount.
		if d.Dismiss {
			if dismiss++; dismiss > 1 {
				return fail(CodeInvalid, "intent %q: at most one intent of a mount may be dismiss", d.Name)
			}
			if d.NodeKey != "" {
				return fail(CodeInvalid, "intent %q is dismiss and names a node; the user dismisses the mount, not a node", d.Name)
			}
			if err := d.Arg.Validate(json.RawMessage(`{}`)); err != nil {
				return fail(CodeInvalid, "intent %q is dismiss and its arg does not accept {}: %v", d.Name, err)
			}
		}
	}
	assets := make(map[string]struct{}, len(c.Assets))
	for _, a := range c.Assets {
		if a.ID == "" || a.Size < 0 || len(a.SHA256) != 64 || (a.Delivery != "inline" && a.Delivery != "managed") {
			return fail(CodeInvalid, "asset %q is malformed", a.ID)
		}
		if !slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, a.MIME) {
			return fail(CodeInvalid, "asset %q has unsupported mime %q", a.ID, a.MIME)
		}
		assets[a.ID] = struct{}{}
	}
	if len(c.Alternatives) > maxAlternatives {
		return fail(CodeInvalid, "%d alternatives, at most %d", len(c.Alternatives), maxAlternatives)
	}
	for i := range c.Alternatives {
		alt := &c.Alternatives[i]
		if err := alt.When.check(); err != nil {
			return fail(CodeInvalid, "alternative %d: %v", i, err)
		}
		if !hasRoot(alt.View) {
			return fail(CodeInvalid, "alternative %d: view must be an object with a root", i)
		}
		if alt.Drawing != nil {
			if err := alt.Drawing.check(seen, assets); err != nil {
				return fail(CodeInvalid, "alternative %d drawing: %v", i, err)
			}
		}
	}
	if c.Drawing != nil {
		if err := c.Drawing.check(seen, assets); err != nil {
			return fail(CodeInvalid, "drawing: %v", err)
		}
	}
	if err := checkA11y(c.A11y); err != nil {
		return fail(CodeInvalid, "%v", err)
	}
	if err := checkFocus(c.Focus); err != nil {
		return fail(CodeInvalid, "%v", err)
	}
	if err := checkBindings(c.Bindings, c.Intents); err != nil {
		return fail(CodeInvalid, "%v", err)
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
// mounts. Slots lists the slots the client draws; Nodes the view node kinds it draws; Limits and Input bound a mount.
// Claims are further facts the client states about itself, SlotClaims those that differ for one slot, and Drawing the
// drawing profiles it draws.
type ClientAbilities struct {
	Slots        []Slot   `json:"slots"`
	Nodes        []string `json:"nodes"`
	Markdown     bool     `json:"markdown"`
	ImageFormats []string `json:"imageFormats"`
	Limits       Limits   `json:"limits"`
	Input        Input    `json:"input"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Claims []Claim `json:"claims,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	SlotClaims []SlotClaims      `json:"slotClaims,omitempty"`
	Drawing    *DrawingAbilities `json:"drawing,omitempty"`
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

type measured struct {
	nodes, depth, text, bytes int
	ids                       map[string]struct{}
	// kinds are the node kinds the view holds, each once, in the order they first appear.
	kinds []string
}

type mount struct {
	snap Snapshot
	// cands[0] measures the view and cands[i] alternative i-1; fb measures the fallback. They are computed once when the
	// content is admitted, so a delivery never walks a view again.
	cands   []measured
	fb      *measured
	digests map[int]string
	notices []Notice
}

type ownerKey struct{ plugin, key string }

// flight is one intent whose answer is awaited. ref is the mount ref the answer is valid for: the ref the intent was
// raised on, moved forward by each change of the ref that the answering handler itself made.
type flight struct {
	k   ownerKey
	ref Ref
}

// Registry holds every plugin's mounts. The zero value is not usable; use [New].
type Registry struct {
	mu        sync.Mutex
	epoch     uint64
	ticket    uint64
	gen       uint64
	mounts    map[ownerKey]*mount
	flights   map[string]*flight
	atts      []*Attachment
	solo      *Attachment
	merged    *ClientAbilities
	onAbility []abilityWatcher
	watchID   uint64
	kindOK    func(string) bool
}

type abilityWatcher struct {
	id uint64
	fn func()
}

// Options configure a registry.
type Options struct {
	// KnownKind reports whether a view node kind is in the host's vocabulary. Nil accepts any kind.
	KnownKind func(kind string) bool
}

// New returns an empty registry with no client attached: nothing can be mounted until a client attaches.
func New(o Options) *Registry {
	return &Registry{mounts: map[ownerKey]*mount{}, flights: map[string]*flight{}, kindOK: o.KnownKind}
}

// OnAbilities registers fn, called after the abilities changed and outside the lock: a transport replicates them, and
// host code that mounts for itself mounts when a client arrives. Every registered function is called, in registration
// order, on the goroutine that changed the abilities. The returned function removes fn.
func (r *Registry) OnAbilities(fn func()) (remove func()) {
	r.mu.Lock()
	r.watchID++
	id := r.watchID
	r.onAbility = append(r.onAbility, abilityWatcher{id, fn})
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		r.onAbility = slices.DeleteFunc(r.onAbility, func(w abilityWatcher) bool { return w.id == id })
		r.mu.Unlock()
	}
}

// abilityWatchers returns one call of every function registered with OnAbilities now, or nil when there is none. The
// caller holds the lock and makes the call after it released it.
func (r *Registry) abilityWatchers() func() {
	if len(r.onAbility) == 0 {
		return nil
	}
	watchers := slices.Clone(r.onAbility)
	return func() {
		for _, w := range watchers {
			w.fn()
		}
	}
}

// SetAbilities is the one-client form of [Registry.Attach]: it attaches a client that takes no deliveries, replaces its
// abilities, or detaches it with nil. Detaching keeps live mounts; they stay until their owner ends them, and no new one
// can be made while no client is attached.
func (r *Registry) SetAbilities(a *ClientAbilities) *Error {
	r.mu.Lock()
	solo := r.solo
	r.mu.Unlock()
	switch {
	case a == nil:
		if solo != nil {
			solo.Detach()
		}
		r.mu.Lock()
		r.solo = nil
		fn := r.abilityWatchers()
		r.mu.Unlock()
		if solo == nil && fn != nil {
			fn()
		}
		return nil
	case solo != nil:
		return solo.UpdateAbilities(*a)
	}
	t, _, err := r.Attach(*a, nil)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.solo = t
	r.mu.Unlock()
	return nil
}

// Abilities returns a copy of what the attached clients draw, or nil while none is attached. With one client it is that
// client's record; with several it is their merge (see [mergeAbilities]).
func (r *Registry) Abilities() *ClientAbilities {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.merged == nil {
		return nil
	}
	cp := cloneAbilities(*r.merged)
	return &cp
}

// measure counts the nodes (objects with a string "kind") and the nesting depth of a view, collects the node ids, finds
// the longest string value, and reports the first node kind outside the host's vocabulary. It reads the view token by
// token, so it builds no tree of the view.
func measure(raw json.RawMessage, known func(string) bool) (m measured, badKind string) {
	m.ids = map[string]struct{}{}
	m.bytes = len(raw)
	// frame is one open object or array. An object remembers the member it is reading, whether it is a node, and its id.
	// images is the view's own list of image bytes, and image one of its records.
	type frame struct {
		object, wantName, node bool
		images, image          bool
		name, id               string
	}
	var open []frame
	dec := jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return m, badKind
		}
		kind := tok.Kind()
		top := len(open) - 1
		if kind == '}' || kind == ']' {
			if f := open[top]; f.node {
				m.nodes++
				if f.id != "" {
					m.ids[f.id] = struct{}{}
				}
			}
			open = open[:top]
			if top > 0 && open[top-1].object {
				open[top-1].wantName = true
			}
			continue
		}
		if top >= 0 && open[top].object && open[top].wantName {
			open[top].name, open[top].wantName = tok.String(), false
			continue
		}
		// A value sits one level below the container that holds it.
		if len(open) > m.depth {
			m.depth = len(open)
		}
		text, isText := "", kind == '"'
		if isText {
			text = tok.String()
			// The bytes of an image are not text: they count in the snapshot's bytes and not as its longest text.
			imageBytes := top >= 0 && open[top].image && open[top].name == "data"
			if !imageBytes && len(text) > m.text {
				m.text = len(text)
			}
		}
		if top >= 0 && open[top].object {
			switch open[top].name {
			case "kind":
				open[top].node = isText
				if isText && !slices.Contains(m.kinds, text) {
					m.kinds = append(m.kinds, text)
				}
				if isText && known != nil && !known(text) && badKind == "" {
					badKind = text
				}
			case "id":
				open[top].id = text
			}
			open[top].wantName = kind != '{' && kind != '['
		}
		switch kind {
		case '{':
			open = append(open, frame{object: true, wantName: true, image: top >= 0 && open[top].images})
		case '[':
			open = append(open, frame{images: top == 0 && open[top].object && open[top].name == "images"})
		}
	}
}

// prepare measures the view, every alternative and the fallback once, and rejects a node kind the host does not know
// and an a11y, focus or drawing reference to a node no candidate has.
func (r *Registry) prepare(c *Content) ([]measured, *measured, *Error) {
	cands := make([]measured, 0, 1+len(c.Alternatives))
	all := map[string]struct{}{}
	add := func(view json.RawMessage, d *Drawing) *Error {
		m, bad := measure(view, r.kindOK)
		if bad != "" {
			return fail(CodeInvalid, "unknown node kind %q", bad)
		}
		if d != nil {
			m.bytes += d.bytes
			if d.Node != "" {
				if _, ok := m.ids[d.Node]; !ok {
					return fail(CodeInvalid, "drawing names node %q, which the view does not have", d.Node)
				}
			}
			if d.text > m.text {
				m.text = d.text
			}
		}
		for id := range m.ids {
			all[id] = struct{}{}
		}
		cands = append(cands, m)
		return nil
	}
	if err := add(c.View, c.Drawing); err != nil {
		return nil, nil, err
	}
	for i := range c.Alternatives {
		if err := add(c.Alternatives[i].View, c.Alternatives[i].Drawing); err != nil {
			return nil, nil, err
		}
	}
	var fb *measured
	if len(c.Fallback) > 0 {
		m, bad := measure(c.Fallback, r.kindOK)
		if bad != "" {
			return nil, nil, fail(CodeInvalid, "unknown node kind %q", bad)
		}
		fb = &m
		cands[0].bytes += len(c.Fallback)
	}
	for i := range c.A11y {
		if _, ok := all[c.A11y[i].Node]; !ok {
			return nil, nil, fail(CodeInvalid, "a11y names node %q, which no view has", c.A11y[i].Node)
		}
		for _, rel := range c.A11y[i].Relations {
			if _, ok := all[rel.Target]; !ok {
				return nil, nil, fail(CodeInvalid, "a11y relation names node %q, which no view has", rel.Target)
			}
		}
	}
	for i := range c.Focus {
		if _, ok := all[c.Focus[i].Node]; !ok {
			return nil, nil, fail(CodeInvalid, "focus names node %q, which no view has", c.Focus[i].Node)
		}
	}
	return cands, fb, nil
}

// misfit says why a client cannot draw a candidate: the code, and for a limit what was measured against it.
type misfit struct {
	code           Code
	message        string
	measured, over int64
}

func (f *misfit) err() *Error { return &Error{Code: f.code, Message: f.message} }

func overLimit(what string, got, limit int) *misfit {
	return &misfit{code: CodeLimit, message: fmt.Sprintf("%s is %d, the client takes %d", what, got, limit), measured: int64(got), over: int64(limit)}
}

// fits checks one candidate of a content against one client. It reads only what prepare measured.
func fits(a *ClientAbilities, c *Content, m measured, d *Drawing) *misfit {
	if kind, ok := undrawnKind(a, m); ok {
		return &misfit{code: CodeUnsupported, message: fmt.Sprintf("the client does not draw node kind %q", kind)}
	}
	if m.bytes > a.Limits.SnapshotBytes {
		return overLimit("snapshot bytes", m.bytes, a.Limits.SnapshotBytes)
	}
	if m.nodes > a.Limits.Nodes {
		return overLimit("node count", m.nodes, a.Limits.Nodes)
	}
	if m.depth > a.Limits.Depth {
		return overLimit("depth", m.depth, a.Limits.Depth)
	}
	if m.text > a.Limits.TextBytes {
		return overLimit("longest text", m.text, a.Limits.TextBytes)
	}
	if len(c.Intents) > 0 && !a.Input.Keyboard && !a.Input.Pointer && !a.Input.Text {
		return &misfit{code: CodeUnsupported, message: fmt.Sprintf("the client delivers no input, so intent %q cannot be raised", c.Intents[0].Name)}
	}
	for i := range c.Bindings {
		if n := len(c.Bindings[i].Arg); n > a.Limits.IntentArgBytes {
			return overLimit("binding argument bytes", n, a.Limits.IntentArgBytes)
		}
	}
	if d == nil {
		return nil
	}
	if a.Drawing == nil || !slices.Contains(a.Drawing.Profiles, d.Profile) {
		return &misfit{code: CodeUnsupported, message: fmt.Sprintf("the client does not draw profile %q", d.Profile)}
	}
	if d.shapes > a.Drawing.Shapes {
		return overLimit("shape count", d.shapes, a.Drawing.Shapes)
	}
	if d.Pixels != nil && d.Pixels.Width*d.Pixels.Height > a.Drawing.Pixels {
		return overLimit("pixel count", d.Pixels.Width*d.Pixels.Height, a.Drawing.Pixels)
	}
	if len(d.Regions) > 0 && !a.Input.Keyboard && !a.Input.Pointer {
		return &misfit{code: CodeUnsupported, message: "the client delivers no pointer or keyboard input, so a region cannot be used"}
	}
	return nil
}

// undrawnKind returns the first node kind of m that the client's nodes list does not name. A client draws only the kinds it
// declares: an empty list draws none, as an empty slots list draws no slot.
func undrawnKind(a *ClientAbilities, m measured) (string, bool) {
	for _, kind := range m.kinds {
		if !slices.Contains(a.Nodes, kind) {
			return kind, true
		}
	}
	return "", false
}

// candidate returns the index a client draws (0 the view, i alternative i-1) with its view and drawing.
func candidate(a *ClientAbilities, slot Slot, c *Content) (int, json.RawMessage, *Drawing) {
	if len(c.Alternatives) > 0 {
		claims := a.claimsFor(slot)
		for i := range c.Alternatives {
			if c.Alternatives[i].When.holds(claims) {
				return i + 1, c.Alternatives[i].View, c.Alternatives[i].Drawing
			}
		}
	}
	return 0, c.View, c.Drawing
}

// admit accepts a content when at least one attached client that draws the slot can draw what it would be sent. With
// one client that is D614's rule unchanged. An update (mounting false) skips the slot test: it keeps its mount's slot.
func (r *Registry) admit(slot Slot, mounting bool, c *Content, cands []measured) *Error {
	if len(r.atts) == 0 {
		return fail(CodeUnsupported, "no client is attached")
	}
	var first *Error
	for _, t := range r.atts {
		if mounting && !slices.Contains(t.abilities.Slots, slot) {
			if first == nil {
				first = fail(CodeUnsupported, "the client does not draw slot %q", slot)
			}
			continue
		}
		i, _, d := candidate(&t.abilities, slot, c)
		bad := fits(&t.abilities, c, cands[i], d)
		if bad == nil {
			return nil
		}
		if first == nil || first.Code == CodeUnsupported {
			first = bad.err()
		}
	}
	return first
}

// Mount makes a new mount for plugin. A key that is live or hidden is taken; a key whose mount ended can be mounted again
// under a new epoch.
func (r *Registry) Mount(plugin string, a MountArgs) (Status, *Error) {
	if err := a.check(); err != nil {
		return Status{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := ownerKey{plugin, a.Key}
	old := r.mounts[k]
	if old != nil && !old.snap.State.terminal() {
		return Status{}, fail(CodeInvalid, "key %q is already mounted", a.Key)
	}
	live := 0
	for ok, m := range r.mounts {
		if ok.plugin == plugin && !m.snap.State.terminal() {
			live++
		}
	}
	if r.merged != nil && live >= r.merged.Limits.Mounts {
		return Status{}, fail(CodeLimit, "%d live mounts is the client's limit", r.merged.Limits.Mounts)
	}
	cands, fb, err := r.prepare(&a.Content)
	if err != nil {
		return Status{}, err
	}
	if err := r.admit(a.Slot, true, &a.Content, cands); err != nil {
		return Status{}, err
	}
	r.epoch++
	m := &mount{cands: cands, fb: fb}
	m.snap = Snapshot{Plugin: plugin, Slot: a.Slot, Target: a.Target, Content: a.Content,
		Mount: Ref{Key: a.Key, Epoch: r.epoch, Rev: 1}, State: StateLive}
	r.mounts[k] = m
	if old != nil {
		r.answering(a.Answering, k, old.snap.Mount, m.snap.Mount)
	}
	r.publish(k, m)
	return m.snap.Status, nil
}

// answering moves the ref the answer of the intent ticket is valid for from one ref of its mount to the next, when that
// intent is in flight on k and the mount was still at the ref the answer was valid for. A ticket that names no intent in
// flight (none, or one that settled) moves nothing. The caller holds the lock.
func (r *Registry) answering(ticket string, k ownerKey, from, to Ref) {
	if f := r.flights[ticket]; f != nil && f.k == k && f.ref == from {
		f.ref = to
	}
}

func (r *Registry) find(plugin, key string, epoch uint64) (*mount, *Error) {
	m := r.mounts[ownerKey{plugin, key}]
	if m == nil {
		return nil, fail(CodeNotFound, "no mount under key %q", key)
	}
	if m.snap.Mount.Epoch != epoch {
		m.note(Notice{Code: string(CodeStale), Severity: SeverityWarning, Message: "an operation named an earlier epoch"})
		return nil, fail(CodeStale, "epoch %d is not current (%d)", epoch, m.snap.Mount.Epoch)
	}
	return m, nil
}

// rejected records the notice of a refused operation and returns its error.
func (m *mount) rejected(path string, err *Error) *Error {
	m.note(Notice{Code: string(err.Code), Severity: SeverityError, Path: path, Message: err.Message})
	return err
}

// Update replaces the content of a live or hidden mount. It is rejected as stale unless the epoch and rev are current,
// so an update computed from an old snapshot never overwrites a newer one.
func (r *Registry) Update(plugin string, a UpdateArgs) (Status, *Error) {
	if err := a.check(); err != nil {
		r.refused(plugin, "update", a.Mount, err)
		return Status{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.find(plugin, a.Mount.Key, a.Mount.Epoch)
	if err != nil {
		return Status{}, err
	}
	if m.snap.State.terminal() {
		return m.snap.Status, m.rejected("update", fail(CodeTerminal, "mount is %s", m.snap.State))
	}
	if m.snap.Mount.Rev != a.Mount.Rev {
		return m.snap.Status, m.rejected("update", fail(CodeStale, "rev %d is not current (%d)", a.Mount.Rev, m.snap.Mount.Rev))
	}
	cands, fb, err := r.prepare(&a.Content)
	if err != nil {
		return m.snap.Status, m.rejected("update", err)
	}
	if err := r.admit(m.snap.Slot, false, &a.Content, cands); err != nil {
		return m.snap.Status, m.rejected("update", err)
	}
	m.snap.Content, m.cands, m.fb, m.digests = a.Content, cands, fb, nil
	from := m.snap.Mount
	m.snap.Mount.Rev++
	k := ownerKey{plugin, a.Mount.Key}
	r.answering(a.Answering, k, from, m.snap.Mount)
	r.publish(k, m)
	return m.snap.Status, nil
}

// Transition applies hide, show, finish or remove. A terminal state is final: a later operation on the mount is rejected
// with CodeTerminal and the state the first one reached.
func (r *Registry) Transition(plugin string, op string, a RefArgs) (Status, *Error) {
	return r.transition(plugin, op, a.Mount, nil)
}

// Finish ends a mount as finished and keeps the plugin's result, which the clients' snapshots and [Registry.State] carry.
func (r *Registry) Finish(plugin string, a FinishArgs) (Status, *Error) {
	return r.transition(plugin, "finish", a.Mount, a.Result)
}

func (r *Registry) transition(plugin string, op string, ref Ref, result json.RawMessage) (Status, *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.find(plugin, ref.Key, ref.Epoch)
	if err != nil {
		return Status{}, err
	}
	if m.snap.State.terminal() {
		return m.snap.Status, m.rejected(op, fail(CodeTerminal, "mount is already %s", m.snap.State))
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
	m.snap.Result = result
	r.publish(ownerKey{plugin, ref.Key}, m)
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

// Snapshots returns every mount with its whole content, as its plugin sent it. A client reads what it draws from its
// attachment instead.
func (r *Registry) Snapshots() []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Snapshot, 0, len(r.mounts))
	for _, m := range r.mounts {
		out = append(out, m.snap)
	}
	sortSnapshots(out)
	return out
}

func sortSnapshots(out []Snapshot) {
	sortx.Func(out, func(a, b Snapshot) int {
		if a.Mount.Epoch < b.Mount.Epoch {
			return -1
		}
		if a.Mount.Epoch > b.Mount.Epoch {
			return 1
		}
		return 0
	})
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
		r.publish(k, m)
	}
}

// noteIntent records how the host ended an intent on the mount incarnation it was raised on. The caller holds the lock.
func (r *Registry) noteIntent(k ownerKey, epoch uint64, intent string, res Result) Result {
	sev := SeverityWarning
	if res.Outcome == OutcomeFailed {
		sev = SeverityError
	}
	if m := r.mounts[k]; m != nil && m.snap.Mount.Epoch == epoch {
		m.note(Notice{Code: string(res.Outcome), Severity: sev, Intent: intent, Message: res.Error})
	}
	return res
}

// Invoke raises an intent on a mount and returns its final result. The intent is checked against the mount's declaration
// first: an unknown intent or an argument outside its shape is refused without reaching the plugin, and so is every
// intent on a mount that is not live, except the intent a hidden mount declares whenHidden. The result is the
// first of the plugin's answer, the timeout, and ctx ending. A cancellable intent's handler is cancelled when the
// timeout or ctx ends it; an answer that arrives afterwards is dropped. An answer is stale when the mount's ref is not
// the one the answer is valid for. That is the ref the intent was raised on, moved forward by each update, and each
// mount of the key again, that the answering handler itself made: a transport marks such a call with the intent's ticket
// ([UpdateArgs] and [MountArgs], Answering). A change of the ref by any other hand, the plugin's own background work and
// another intent's handler included, makes the answer stale. Hide, show, finish, remove and a break change no ref, so
// they make no answer stale. Every result but done and cancelled leaves a notice on the mount.
func (r *Registry) Invoke(ctx context.Context, plugin string, ref Ref, nodeKey, intent string, arg json.RawMessage, inv Invoker) Result {
	k := ownerKey{plugin, ref.Key}
	r.mu.Lock()
	m := r.mounts[k]
	if m == nil || m.snap.Mount.Epoch != ref.Epoch || m.snap.Mount.Rev != ref.Rev {
		res := Result{Outcome: OutcomeStale, Error: "mount changed"}
		if m != nil {
			m.note(Notice{Code: string(OutcomeStale), Severity: SeverityWarning, Intent: intent, Message: res.Error})
		}
		r.mu.Unlock()
		return res
	}
	refuse := func(msg string) Result {
		res := r.noteIntent(k, ref.Epoch, intent, Result{Outcome: OutcomeRefused, Error: msg})
		r.mu.Unlock()
		return res
	}
	var decl *IntentDecl
	for i := range m.snap.Intents {
		if m.snap.Intents[i].Name == intent {
			decl = &m.snap.Intents[i]
		}
	}
	// A mount that is not live takes no intent, but for the one a hidden mount declares whenHidden.
	if m.snap.State != StateLive && (m.snap.State != StateHidden || decl == nil || !decl.WhenHidden) {
		return refuse("mount is " + string(m.snap.State))
	}
	limit := 0
	if r.merged != nil {
		limit = r.merged.Limits.IntentArgBytes
	}
	if decl == nil || (decl.NodeKey != "" && decl.NodeKey != nodeKey) {
		return refuse("unknown intent " + strconv.Quote(intent))
	}
	if len(arg) > limit {
		return refuse(fmt.Sprintf("argument is %d bytes, the limit is %d", len(arg), limit))
	}
	if err := decl.Arg.Validate(arg); err != nil {
		return refuse(err.Error())
	}
	d := *decl
	r.ticket++
	ticket := "t" + strconv.FormatUint(r.ticket, 10)
	snapRef := m.snap.Mount
	f := &flight{k: k, ref: snapRef}
	r.flights[ticket] = f
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.flights, ticket)
		r.mu.Unlock()
	}()

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
	noted := func(res Result) Result {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.noteIntent(k, f.ref.Epoch, intent, res)
	}
	ended := func() Result {
		if ctx.Err() != nil {
			return Result{Ticket: ticket, Outcome: OutcomeCancelled}
		}
		return noted(Result{Ticket: ticket, Outcome: OutcomeTimeout})
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
			return noted(Result{Ticket: ticket, Outcome: OutcomeFailed, Error: a.err.Error()})
		}
		if res.Outcome != OutcomeDone && res.Outcome != OutcomeRefused && res.Outcome != OutcomeFailed {
			return noted(Result{Ticket: ticket, Outcome: OutcomeFailed, Error: "plugin answered with outcome " + strconv.Quote(string(res.Outcome))})
		}
		r.mu.Lock()
		cur := r.mounts[k]
		changed := cur == nil || cur.snap.Mount != f.ref
		r.mu.Unlock()
		if changed {
			return noted(Result{Ticket: ticket, Outcome: OutcomeStale, Error: "mount changed while the plugin answered"})
		}
		if res.Outcome == OutcomeDone && d.Answer != nil {
			if err := d.Answer.Validate(res.Answer); err != nil {
				return noted(Result{Ticket: ticket, Outcome: OutcomeFailed, Error: "answer: " + err.Error()})
			}
		}
		if res.Outcome != OutcomeDone {
			return noted(res)
		}
		return res
	case <-cctx.Done():
		return ended()
	}
}

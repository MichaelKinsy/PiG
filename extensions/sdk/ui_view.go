package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/internal/kitwire"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// pig additive (D614): plugin UI mounts. An extension mounts a [kit.View] in a slot the attached client draws, updates it,
// and answers the user's intents. The host replicates what the client draws ([Context.ViewAbilities]); with no client
// attached it is nil and a mount fails with ViewErrorUnsupported.

// ViewSlot names where a client draws a mount.
type ViewSlot string

const (
	ViewSlotNotify      ViewSlot = "notify"
	ViewSlotStatus      ViewSlot = "status"
	ViewSlotWidgetAbove ViewSlot = "widget-above"
	ViewSlotWidgetBelow ViewSlot = "widget-below"
	ViewSlotHeader      ViewSlot = "header"
	ViewSlotFooter      ViewSlot = "footer"
	ViewSlotDialog      ViewSlot = "dialog"
	ViewSlotOverlay     ViewSlot = "overlay"
	ViewSlotPanel       ViewSlot = "panel"
	ViewSlotToolCard    ViewSlot = "tool-card"
	ViewSlotMessage     ViewSlot = "message"
	ViewSlotEntry       ViewSlot = "entry"
)

// ViewState is where a mount is in its lifecycle: live, hidden, finished, removed or broken. The last three are terminal
// and the first one reached wins.
type ViewState string

const (
	ViewStateLive     ViewState = "live"
	ViewStateHidden   ViewState = "hidden"
	ViewStateFinished ViewState = "finished"
	ViewStateRemoved  ViewState = "removed"
	ViewStateBroken   ViewState = "broken"
)

// ViewBrokenReason says why the host broke a mount: "session_replaced", "owner_gone" or "shutdown".
type ViewBrokenReason string

// ViewErrorCode classifies a failed mount operation.
type ViewErrorCode string

const (
	ViewErrorInvalid     ViewErrorCode = "invalid"
	ViewErrorUnsupported ViewErrorCode = "unsupported"
	ViewErrorLimit       ViewErrorCode = "limit"
	ViewErrorStale       ViewErrorCode = "stale"
	ViewErrorTerminal    ViewErrorCode = "terminal"
	ViewErrorNotFound    ViewErrorCode = "not_found"
)

// ViewError is the error of a failed mount operation; match it with errors.As.
type ViewError struct {
	Code    ViewErrorCode
	Message string
}

func (e *ViewError) Error() string { return string(e.Code) + ": " + e.Message }

// ViewRef names one revision of one mount incarnation. Pass the ref the last operation returned to the next update.
type ViewRef struct {
	Key   string `json:"key"`
	Epoch uint64 `json:"epoch"`
	Rev   uint64 `json:"rev"`
}

// ViewStatus is the reply of every mount operation.
type ViewStatus struct {
	Mount  ViewRef          `json:"mount"`
	State  ViewState        `json:"state"`
	Reason ViewBrokenReason `json:"reason,omitempty"`
}

// ViewTarget ties a tool-card, message or entry mount to what it decorates; set exactly one field.
type ViewTarget struct {
	ToolCallID  string `json:"toolCallId,omitempty"`
	MessageType string `json:"messageType,omitempty"`
	EntryType   string `json:"entryType,omitempty"`
}

// ViewShape is the closed JSON subset an intent argument or answer is checked against: type "boolean", "string",
// "integer", "number", "enum" (Enum, at most 32), "object" (Properties at most 32, Required) or "array" (Items,
// MaxItems at most 64), nested to depth 4.
type ViewShape struct {
	Type       string               `json:"type"`
	Enum       []string             `json:"enum,omitempty"`
	Properties map[string]ViewShape `json:"properties,omitempty"`
	Required   []string             `json:"required,omitempty"`
	Items      *ViewShape           `json:"items,omitempty"`
	MaxItems   int                  `json:"maxItems,omitempty"`
}

// ViewIntent declares one intent the view's nodes may raise. Name is "<extension>.<verb>". TimeoutMs defaults to 30000
// and is 100..300000.
type ViewIntent struct {
	Name        string     `json:"name"`
	NodeKey     string     `json:"nodeKey,omitempty"`
	Arg         ViewShape  `json:"arg"`
	Answer      *ViewShape `json:"answer,omitempty"`
	Cancellable bool       `json:"cancellable,omitempty"`
	TimeoutMs   int        `json:"timeoutMs,omitempty"`
}

// ViewAsset is a bounded image a view refers to by id.
type ViewAsset struct {
	ID       string `json:"id"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Delivery string `json:"delivery"`
}

// ViewContent is what a mount shows.
type ViewContent struct {
	View     kit.View
	Intents  []ViewIntent
	Assets   []ViewAsset
	Fallback kit.Node
}

// ViewMount is the argument of [Context.MountView].
type ViewMount struct {
	Key    string
	Slot   ViewSlot
	Target *ViewTarget
	ViewContent
}

// ViewUpdate is the argument of [Context.UpdateView]: a full replacement of the content of the revision Mount names.
type ViewUpdate struct {
	Mount ViewRef
	ViewContent
}

// ViewLimits bound one mount on the attached client.
type ViewLimits struct {
	Nodes          int `json:"nodes"`
	Depth          int `json:"depth"`
	SnapshotBytes  int `json:"snapshotBytes"`
	TextBytes      int `json:"textBytes"`
	Mounts         int `json:"mounts"`
	IntentArgBytes int `json:"intentArgBytes"`
}

// ViewInput says which input the client delivers to intents.
type ViewInput struct {
	Keyboard bool `json:"keyboard"`
	Pointer  bool `json:"pointer"`
	Text     bool `json:"text"`
}

// ViewAbilities is what the attached client draws.
type ViewAbilities struct {
	Slots        []ViewSlot `json:"slots"`
	Nodes        []string   `json:"nodes"`
	Markdown     bool       `json:"markdown"`
	ImageFormats []string   `json:"imageFormats"`
	Limits       ViewLimits `json:"limits"`
	Input        ViewInput  `json:"input"`
}

// ViewIntentInvocation is one intent the user raised.
type ViewIntentInvocation struct {
	Mount   ViewRef
	Rev     uint64
	NodeKey string
	Intent  string
	Arg     json.RawMessage
	Ticket  string
	// Context is the handler's context: it is cancelled when the host cancels a cancellable intent (timeout, or the
	// user withdrew it). A cancelled handler's answer is dropped by the host.
	Context Context
}

// ViewIntentResult is a handler's answer: Outcome is "done" (Answer), "refused" or "failed" (Error).
type ViewIntentResult struct {
	Outcome string          `json:"outcome"`
	Answer  json.RawMessage `json:"answer,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ViewIntentHandler answers the intents of this extension's mounts.
type ViewIntentHandler func(in ViewIntentInvocation) ViewIntentResult

type viewState struct {
	mu        sync.RWMutex
	abilities *ViewAbilities
	handler   ViewIntentHandler
}

func (s *viewState) applyAbilities(raw json.RawMessage) {
	var a *ViewAbilities
	if len(raw) > 0 && string(raw) != "null" {
		var v ViewAbilities
		if json.Unmarshal(raw, &v) == nil {
			a = &v
		}
	}
	s.mu.Lock()
	s.abilities = a
	s.mu.Unlock()
}

// OnViewIntent registers the handler that answers the intents of this extension's mounts. A later call replaces it.
func (e *Extension) OnViewIntent(handler ViewIntentHandler) {
	if handler == nil {
		panic("sdk: OnViewIntent: handler must not be nil")
	}
	e.views.mu.Lock()
	e.views.handler = handler
	e.views.mu.Unlock()
}

// ViewAbilities returns what the attached client draws, or nil while none is attached.
func (c Context) ViewAbilities() *ViewAbilities {
	c.assertActive()
	c.ext.views.mu.RLock()
	defer c.ext.views.mu.RUnlock()
	if c.ext.views.abilities == nil {
		return nil
	}
	a := *c.ext.views.abilities
	a.Slots = append([]ViewSlot(nil), a.Slots...)
	a.Nodes = append([]string(nil), a.Nodes...)
	a.ImageFormats = append([]string(nil), a.ImageFormats...)
	return &a
}

func (c Context) encodeViewContent(v ViewContent) (map[string]any, error) {
	enc, err := kitwire.Encode(v.View, c.ext.frontendAttached())
	if err != nil {
		return nil, err
	}
	view, err := enc.Payload(enc.Images)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"view": view}
	if len(v.Intents) > 0 {
		out["intents"] = v.Intents
	}
	if len(v.Assets) > 0 {
		out["assets"] = v.Assets
	}
	if v.Fallback != nil {
		fb, err := kitwire.Encode(kit.View{Root: v.Fallback}, c.ext.frontendAttached())
		if err != nil {
			return nil, err
		}
		var root struct {
			Root json.RawMessage `json:"root"`
		}
		if err := json.Unmarshal(fb.View, &root); err != nil {
			return nil, err
		}
		out["fallback"] = root.Root
	}
	return out, nil
}

func (c Context) viewCall(method string, args any) (ViewStatus, error) {
	result, err := c.callHost(method, args)
	if err != nil {
		return ViewStatus{}, err
	}
	if result != nil && result.Error != nil {
		return ViewStatus{}, &ViewError{Code: ViewErrorCode(result.Error.Code), Message: result.Error.Message}
	}
	var st ViewStatus
	if result == nil || json.Unmarshal(result.Result, &st) != nil {
		return ViewStatus{}, errors.New("sdk: " + method + ": malformed reply")
	}
	return st, nil
}

// MountView mounts a view in a slot the attached client draws. The key names the mount among this extension's.
func (c Context) MountView(m ViewMount) (ViewStatus, error) {
	c.assertActive()
	args, err := c.encodeViewContent(m.ViewContent)
	if err != nil {
		return ViewStatus{}, err
	}
	args["key"], args["slot"] = m.Key, m.Slot
	if m.Target != nil {
		args["target"] = m.Target
	}
	return c.viewCall("ui.view.mount", args)
}

// UpdateView replaces the content of a mount. It fails with ViewErrorStale unless u.Mount carries the epoch and rev the
// last operation returned.
func (c Context) UpdateView(u ViewUpdate) (ViewStatus, error) {
	c.assertActive()
	args, err := c.encodeViewContent(u.ViewContent)
	if err != nil {
		return ViewStatus{}, err
	}
	args["mount"] = u.Mount
	return c.viewCall("ui.view.update", args)
}

func (c Context) viewTransition(op string, ref ViewRef) (ViewStatus, error) {
	c.assertActive()
	return c.viewCall("ui.view."+op, map[string]any{"mount": ref})
}

// HideView hides a live mount; ShowView shows it again.
func (c Context) HideView(ref ViewRef) (ViewStatus, error) { return c.viewTransition("hide", ref) }
func (c Context) ShowView(ref ViewRef) (ViewStatus, error) { return c.viewTransition("show", ref) }

// FinishView ends a mount as completed; RemoveView ends it as withdrawn. Either is terminal: a later operation on the
// mount fails with ViewErrorTerminal.
func (c Context) FinishView(ref ViewRef) (ViewStatus, error) { return c.viewTransition("finish", ref) }
func (c Context) RemoveView(ref ViewRef) (ViewStatus, error) { return c.viewTransition("remove", ref) }

// ViewState reports the mount under key.
func (c Context) ViewState(key string) (ViewStatus, error) {
	c.assertActive()
	return c.viewCall("ui.view.state", map[string]string{"key": key})
}

// handleViewIntent answers a ui.view.intent request.
func (e *Extension) handleViewIntent(ctx Context, args json.RawMessage) (any, error) {
	var in ViewIntentInvocation
	var wire struct {
		Mount   ViewRef         `json:"mount"`
		Rev     uint64          `json:"rev"`
		NodeKey string          `json:"nodeKey"`
		Intent  string          `json:"intent"`
		Arg     json.RawMessage `json:"arg"`
		Ticket  string          `json:"ticket"`
	}
	if err := json.Unmarshal(args, &wire); err != nil {
		return nil, fmt.Errorf("ui.view.intent: %w", err)
	}
	in = ViewIntentInvocation{Mount: wire.Mount, Rev: wire.Rev, NodeKey: wire.NodeKey, Intent: wire.Intent, Arg: wire.Arg, Ticket: wire.Ticket, Context: ctx}
	e.views.mu.RLock()
	handler := e.views.handler
	e.views.mu.RUnlock()
	if handler == nil {
		return ViewIntentResult{Outcome: "refused", Error: "no intent handler"}, nil
	}
	res := handler(in)
	if res.Outcome == "" {
		res.Outcome = "done"
	}
	if strings.TrimSpace(string(res.Answer)) == "" {
		res.Answer = nil
	}
	return res, nil
}

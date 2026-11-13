package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// ViewStatus is the reply of every mount operation. Result is the result a finished mount was given by
// [Context.FinishViewWith]; it is nil otherwise.
type ViewStatus struct {
	Mount  ViewRef          `json:"mount"`
	State  ViewState        `json:"state"`
	Reason ViewBrokenReason `json:"reason,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
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
// and is 100..300000. WhenHidden lets a client raise the intent while the mount is hidden: it is how the user opens a
// view the extension hid, and the handler shows the mount. At most one intent of a mount has it; it names no node and
// its Arg accepts the empty object. Dismiss names the intent a client raises, with the empty object, when the user
// dismisses a dialog or overlay mount: the handler closes the mount. The same three rules hold for it. Without one,
// Escape leaves the mount and it stays open until the extension closes it.
type ViewIntent struct {
	Name        string     `json:"name"`
	NodeKey     string     `json:"nodeKey,omitempty"`
	Arg         ViewShape  `json:"arg"`
	Answer      *ViewShape `json:"answer,omitempty"`
	Cancellable bool       `json:"cancellable,omitempty"`
	TimeoutMs   int        `json:"timeoutMs,omitempty"`
	WhenHidden  bool       `json:"whenHidden,omitempty"`
	Dismiss     bool       `json:"dismiss,omitempty"`
}

// ViewAsset is a bounded image a view refers to by id.
type ViewAsset struct {
	ID       string `json:"id"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Delivery string `json:"delivery"`
}

// ViewClaim is one fact the attached client states about itself, named "<area>.<fact>". Value is a bool, an int64, a
// string or a []string.
type ViewClaim struct {
	Name  string
	Value any
}

// UnmarshalJSON reads the closed claim value subset.
func (c *ViewClaim) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Name = raw.Name
	v := bytes.TrimSpace(raw.Value)
	switch {
	case len(v) == 0:
		return errors.New("sdk: claim " + raw.Name + " has no value")
	case v[0] == '[':
		list := []string{}
		if err := json.Unmarshal(v, &list); err != nil {
			return err
		}
		c.Value = list
	case v[0] == '"':
		var text string
		if err := json.Unmarshal(v, &text); err != nil {
			return err
		}
		c.Value = text
	case v[0] == 't' || v[0] == 'f':
		c.Value = v[0] == 't'
	default:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return err
		}
		c.Value = n
	}
	return nil
}

// ViewSlotClaims are the claims that replace the same-named ones for mounts of one slot.
type ViewSlotClaims struct {
	Slot   ViewSlot    `json:"slot"`
	Claims []ViewClaim `json:"claims"`
}

// ViewDrawingAbilities says which drawing profiles ("scene", "pixels") the client draws, and how many shapes and pixels.
type ViewDrawingAbilities struct {
	Profiles []string `json:"profiles"`
	Shapes   int      `json:"shapes"`
	Pixels   int      `json:"pixels"`
}

// ViewClause tests one claim. Op is "present", "equals" (Value a bool, an integer or a string), "contains" (Value a
// []string the claimed list must hold) or "atLeast" (Value an integer).
type ViewClause struct {
	Claim string `json:"claim"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// ViewWhenAll holds when all its clauses hold; ViewWhen when any of its groups does. At most 8 groups of 8 clauses.
type ViewWhenAll struct {
	All []ViewClause `json:"all"`
}
type ViewWhen struct {
	Any []ViewWhenAll `json:"any"`
}

// ViewAlternative is a view a client is sent in place of the mount's view when When holds for that client's claims.
// The first alternative that holds wins; a mount carries at most 8.
type ViewAlternative struct {
	When    ViewWhen
	View    kit.View
	Drawing *ViewDrawing
}

// ViewViewport is the rectangle of drawing coordinates the client fits ("contain", "cover", "stretch") into its space.
type ViewViewport struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Fit    string  `json:"fit"`
}

// ViewStroke outlines a shape; ViewPoint is a polyline vertex; ViewPathCommand a path step with Op "move", "line",
// "quad" (X1, Y1), "cubic" (X1, Y1, X2, Y2) or "close".
type ViewStroke struct {
	Color string  `json:"color"`
	Width float64 `json:"width"`
}
type ViewPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type ViewPathCommand struct {
	Op string  `json:"op"`
	X  float64 `json:"x,omitempty"`
	Y  float64 `json:"y,omitempty"`
	X1 float64 `json:"x1,omitempty"`
	Y1 float64 `json:"y1,omitempty"`
	X2 float64 `json:"x2,omitempty"`
	Y2 float64 `json:"y2,omitempty"`
}

// ViewDrawShape is one shape of a scene. Kind is "group" (Children), "rect" (X, Y, Width, Height, Radius), "ellipse"
// (CX, CY, RX, RY), "line" (X1, Y1, X2, Y2), "polyline" (Points, Closed), "path" (Commands), "text" (X, Y, Text, Size,
// Align) or "image" (X, Y, Width, Height, Asset). A member of another kind rejects the drawing. A color is "#rrggbb",
// "#rrggbbaa" or a theme color token. At most 4096 shapes nested 8 deep, 1024 points or commands in one.
type ViewDrawShape struct {
	Kind      string            `json:"kind"`
	Key       string            `json:"key,omitempty"`
	Fill      string            `json:"fill,omitempty"`
	Stroke    *ViewStroke       `json:"stroke,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	Transform []float64         `json:"transform,omitempty"`
	Children  []ViewDrawShape   `json:"children,omitempty"`
	X         float64           `json:"x,omitempty"`
	Y         float64           `json:"y,omitempty"`
	Width     float64           `json:"width,omitempty"`
	Height    float64           `json:"height,omitempty"`
	Radius    float64           `json:"radius,omitempty"`
	CX        float64           `json:"cx,omitempty"`
	CY        float64           `json:"cy,omitempty"`
	RX        float64           `json:"rx,omitempty"`
	RY        float64           `json:"ry,omitempty"`
	X1        float64           `json:"x1,omitempty"`
	Y1        float64           `json:"y1,omitempty"`
	X2        float64           `json:"x2,omitempty"`
	Y2        float64           `json:"y2,omitempty"`
	Points    []ViewPoint       `json:"points,omitempty"`
	Closed    bool              `json:"closed,omitempty"`
	Commands  []ViewPathCommand `json:"commands,omitempty"`
	Text      string            `json:"text,omitempty"`
	Size      float64           `json:"size,omitempty"`
	Align     string            `json:"align,omitempty"`
	Asset     string            `json:"asset,omitempty"`
}

// ViewPixels is a pixel grid over an image asset; Scaling is "nearest" or "smooth".
type ViewPixels struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Asset   string `json:"asset"`
	Scaling string `json:"scaling"`
}

// ViewRegion turns a gesture ("activate", "press", "release", "drag") inside Shape (a rect, an ellipse or a closed
// polyline) into the declared intent Intent. The client hit-tests. At most 64 regions.
type ViewRegion struct {
	Key      string        `json:"key"`
	Shape    ViewDrawShape `json:"shape"`
	Gestures []string      `json:"gestures"`
	Intent   string        `json:"intent"`
	Name     string        `json:"name"`
	Role     string        `json:"role,omitempty"`
}

// ViewDrawing is the drawing of a view: Profile "scene" with Shapes, or "pixels" with Pixels. Node names the view node
// the drawing fills. A client that does not draw the profile makes the mount fail with ViewErrorUnsupported.
type ViewDrawing struct {
	Profile  string          `json:"profile"`
	Node     string          `json:"node,omitempty"`
	Viewport *ViewViewport   `json:"viewport,omitempty"`
	Shapes   []ViewDrawShape `json:"shapes,omitempty"`
	Pixels   *ViewPixels     `json:"pixels,omitempty"`
	Regions  []ViewRegion    `json:"regions,omitempty"`
}

// ViewRelation ties a node to another: Kind is "labelledBy", "describedBy", "controls" or "owns".
type ViewRelation struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// ViewA11y describes the view node with id Node to assistive technology. At most 256 entries, 16 relations each.
type ViewA11y struct {
	Node        string         `json:"node"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Role        string         `json:"role,omitempty"`
	Selected    *bool          `json:"selected,omitempty"`
	Checked     *bool          `json:"checked,omitempty"`
	Expanded    *bool          `json:"expanded,omitempty"`
	Busy        *bool          `json:"busy,omitempty"`
	Disabled    *bool          `json:"disabled,omitempty"`
	ReadOnly    *bool          `json:"readOnly,omitempty"`
	Required    *bool          `json:"required,omitempty"`
	Invalid     *bool          `json:"invalid,omitempty"`
	Relations   []ViewRelation `json:"relations,omitempty"`
}

// ViewFocus makes the node with id Node focusable; the order of the entries is the focus order. Request "mount" asks
// for the focus when the mount is first drawn and "update" when the revision that carries it is first drawn (at most
// one entry may ask); a client that does not give it leaves an "unfocused" notice. Restore is "previous" or "self".
type ViewFocus struct {
	Node    string `json:"node"`
	Request string `json:"request,omitempty"`
	Restore string `json:"restore,omitempty"`
}

// ViewBinding ties a key or a gesture to an intent of the same content that names no node; a client fires it only while
// the mount holds the focus and the focused node did not take the key. Exactly one of Key and Gesture is set. Key is a
// key id as RegisterShortcut writes it ("d", "shift+left", "ctrl+shift+p"); Gesture is "swipe-left", "swipe-right",
// "swipe-up" or "swipe-down". Arg is the constant argument the intent is raised with ({} when nil) and Name what a client
// shows for the binding. At most 32; the host checks the record and answers "invalid".
type ViewBinding struct {
	Key     string `json:"key,omitempty"`
	Gesture string `json:"gesture,omitempty"`
	Intent  string `json:"intent"`
	Arg     any    `json:"arg,omitzero"`
	Name    string `json:"name,omitempty"`
}

// ViewNotice is one diagnostic of a mount: an operation the host rejected (Code "invalid", "unsupported", "limit",
// "stale", "terminal"), an intent it ended ("refused", "failed", "timeout", "stale"), a part a client could not draw
// ("undrawn") or a focus request a client did not grant ("unfocused"). Severity is "info", "warning" or "error"; Count
// how often it happened.
type ViewNotice struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message,omitempty"`
	Path     string `json:"path,omitempty"`
	Claim    string `json:"claim,omitempty"`
	Intent   string `json:"intent,omitempty"`
	Measured *int64 `json:"measured,omitempty"`
	Limit    *int64 `json:"limit,omitempty"`
	Epoch    uint64 `json:"epoch"`
	Rev      uint64 `json:"rev,omitempty"`
	Count    int    `json:"count"`
}

// ViewContent is what a mount shows: the view, the intents its nodes raise, its assets and a degrade node; and beside
// them the alternatives the client's claims select from, a drawing, the a11y and focus records of the view's nodes, and
// the bindings of keys and gestures to its intents.
// Title is the short text a client labels the mount with, in a tab or a list of views: at most 64 bytes on one line.
type ViewContent struct {
	Title        string
	View         kit.View
	Intents      []ViewIntent
	Assets       []ViewAsset
	Fallback     kit.Node
	Alternatives []ViewAlternative
	Drawing      *ViewDrawing
	A11y         []ViewA11y
	Focus        []ViewFocus
	Bindings     []ViewBinding
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

// ViewAbilities is what the attached client draws. Claims are further facts it states about itself, SlotClaims those
// that differ for one slot, and Drawing the drawing profiles it draws (nil: none). With several clients attached the
// host reports their merge: what some client draws, and the claims all of them share.
type ViewAbilities struct {
	Slots        []ViewSlot            `json:"slots"`
	Nodes        []string              `json:"nodes"`
	Markdown     bool                  `json:"markdown"`
	ImageFormats []string              `json:"imageFormats"`
	Limits       ViewLimits            `json:"limits"`
	Input        ViewInput             `json:"input"`
	Claims       []ViewClaim           `json:"claims,omitempty"`
	SlotClaims   []ViewSlotClaims      `json:"slotClaims,omitempty"`
	Drawing      *ViewDrawingAbilities `json:"drawing,omitempty"`
}

func cloneViewClaims(in []ViewClaim) []ViewClaim {
	out := append([]ViewClaim(nil), in...)
	for i := range out {
		if list, ok := out[i].Value.([]string); ok {
			out[i].Value = append([]string(nil), list...)
		}
	}
	return out
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

// ViewAbilitiesHandler receives what the attached client draws after it changed: the first client attached, the
// abilities changed, or the last client left (abilities is nil).
type ViewAbilitiesHandler func(ctx Context, abilities *ViewAbilities)

type viewState struct {
	mu        sync.RWMutex
	abilities *ViewAbilities
	// raw is the uiAbilities member abilities was decoded from (nil: none), and known reports that the host sent a
	// state, so a later different member is a change (viewAbilitiesFeed).
	raw     []byte
	known   bool
	handler ViewIntentHandler
}

// applyAbilities replaces the replicated abilities with the uiAbilities member of a state snapshot and reports whether
// they differ from the ones the snapshot before carried. The first snapshot is the state the extension starts in, not
// a change.
func (s *viewState) applyAbilities(raw json.RawMessage) (changed bool, abilities *ViewAbilities) {
	var a *ViewAbilities
	if len(raw) > 0 && string(raw) != "null" {
		var v ViewAbilities
		if json.Unmarshal(raw, &v) == nil {
			a = &v
		}
	}
	if a == nil {
		raw = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed = s.known && !bytes.Equal(s.raw, raw)
	s.abilities, s.raw, s.known = a, bytes.Clone(raw), true
	return changed, a
}

// clone returns a copy that shares no slice with a; nil stays nil.
func (a *ViewAbilities) clone() *ViewAbilities {
	if a == nil {
		return nil
	}
	c := *a
	c.Slots = append([]ViewSlot(nil), c.Slots...)
	c.Nodes = append([]string(nil), c.Nodes...)
	c.ImageFormats = append([]string(nil), c.ImageFormats...)
	c.Claims = cloneViewClaims(c.Claims)
	c.SlotClaims = append([]ViewSlotClaims(nil), c.SlotClaims...)
	for i := range c.SlotClaims {
		c.SlotClaims[i].Claims = cloneViewClaims(c.SlotClaims[i].Claims)
	}
	if c.Drawing != nil {
		d := *c.Drawing
		d.Profiles = append([]string(nil), d.Profiles...)
		c.Drawing = &d
	}
	return &c
}

// OnViewAbilities registers a handler for changes of what the attached client draws: it runs when the first client
// attaches, when the abilities change, and when the last client leaves (abilities is nil; a client that draws no slot
// is a record with no slots). It is where an extension mounts its views for a client that arrives while the session is
// idle, and stops work no client can see. The handler receives the abilities of the change it is delivered for, and
// [Context.ViewAbilities] already reports them when it runs, so a mount it makes is checked against that client. The
// state an extension starts in is not a change: an extension loaded while a client is attached reads
// [Context.ViewAbilities] at session_start. A session no client attaches to never runs the handler.
//
// Handlers run in registration order on a goroutine of their own, one change at a time in the order the changes
// arrived, never on the message loop, so a handler can make a blocking host call. The host does not wait for them. A
// handler that panics is recovered and does not stop the others.
func (e *Extension) OnViewAbilities(handler ViewAbilitiesHandler) {
	if handler == nil {
		panic("sdk: OnViewAbilities: handler must not be nil")
	}
	e.viewAbilitiesFeed.subscribe(func(ctx Context, abilities *ViewAbilities) { handler(ctx, abilities.clone()) })
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
	return c.ext.views.abilities.clone()
}

func (c Context) encodeView(v kit.View) (any, error) {
	enc, err := kitwire.Encode(v, c.ext.frontendAttached())
	if err != nil {
		return nil, err
	}
	return enc.Payload(enc.Images)
}

func (c Context) encodeViewContent(v ViewContent) (map[string]any, error) {
	view, err := c.encodeView(v.View)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"view": view}
	if v.Title != "" {
		out["title"] = v.Title
	}
	if len(v.Alternatives) > 0 {
		alts := make([]map[string]any, len(v.Alternatives))
		for i, alt := range v.Alternatives {
			altView, err := c.encodeView(alt.View)
			if err != nil {
				return nil, err
			}
			alts[i] = map[string]any{"when": alt.When, "view": altView}
			if alt.Drawing != nil {
				alts[i]["drawing"] = alt.Drawing
			}
		}
		out["alternatives"] = alts
	}
	if v.Drawing != nil {
		out["drawing"] = v.Drawing
	}
	if len(v.A11y) > 0 {
		out["a11y"] = v.A11y
	}
	if len(v.Focus) > 0 {
		out["focus"] = v.Focus
	}
	if len(v.Bindings) > 0 {
		out["bindings"] = v.Bindings
	}
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

// FinishViewWith ends a mount as completed and leaves result, any JSON value of at most 65536 encoded bytes, as the
// extension's last word on it. The clients' snapshots and [Context.ViewState] carry it.
func (c Context) FinishViewWith(ref ViewRef, result any) (ViewStatus, error) {
	c.assertActive()
	raw, err := json.Marshal(result)
	if err != nil {
		return ViewStatus{}, err
	}
	return c.viewCall("ui.view.finish", map[string]any{"mount": ref, "result": json.RawMessage(raw)})
}

// ViewDiagnostics returns the notices of the mount under key, oldest first: what the host rejected or ended and what a
// client could not draw. The host keeps at most 32 and counts repeats.
func (c Context) ViewDiagnostics(key string) ([]ViewNotice, error) {
	c.assertActive()
	result, err := c.callHost("ui.view.diagnostics", map[string]string{"key": key})
	if err != nil {
		return nil, err
	}
	if result != nil && result.Error != nil {
		return nil, &ViewError{Code: ViewErrorCode(result.Error.Code), Message: result.Error.Message}
	}
	var reply struct {
		Notices []ViewNotice `json:"notices"`
	}
	if result == nil || json.Unmarshal(result.Result, &reply) != nil || reply.Notices == nil {
		return nil, errors.New("sdk: ui.view.diagnostics: malformed reply")
	}
	return reply.Notices, nil
}

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

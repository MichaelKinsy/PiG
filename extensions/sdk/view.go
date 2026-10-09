package sdk

import (
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/internal/kitwire"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// pig additive (D107): the extension component kit. A surface may describe
// itself as Pi's tui components, a [kit.View], instead of drawing lines. The
// host renders the view with its tui ports at the width it lays the surface
// out at, so a view frame carries no lines. See
// docs/plan/extension-component-kit.md.

const (
	notifyUIViewEvent   = "ui.view.event"
	notifyUIViewEvicted = "ui.view.evicted"
)

// ViewComponent is a focused component that describes itself as a
// [kit.View], the kit form of an upstream custom component built from Pi's
// components. Pass one to [Context.Custom]; it need not implement Render.
// The host renders the view and owns the state of its interactive nodes:
// keys the focused list binds stay on the host and come back as
// [ViewEventHandler] events, and every other key reaches HandleInput as it
// does for a [RemoteComponent].
type ViewComponent interface {
	View(width int) kit.View
	HandleInput(data string) (RemoteComponentResult, error)
}

// ViewEventHandler is implemented by a [ViewComponent] that takes the
// callbacks of its interactive nodes: upstream SelectList onSelect, onCancel
// and onSelectionChange, and SettingsList onChange and onCancel. Events run on
// the same queue as HandleInput, in arrival order, never concurrently with it,
// and a result means what HandleInput's does. A component without one ignores
// events.
type ViewEventHandler interface {
	HandleViewEvent(event kit.Event) (RemoteComponentResult, error)
}

// MessageViewRendererFunc renders a custom message as a [kit.View]; the host
// renders it at the width it lays the message out at.
type MessageViewRendererFunc func(ctx Context, message map[string]any, options MessageRenderOptions, width int) (kit.View, error)

// EntryViewRendererFunc renders a custom session entry as a [kit.View]; the
// host renders it at the width it lays the entry out at.
type EntryViewRendererFunc func(ctx Context, entry map[string]any, options EntryRenderOptions, width int) (kit.View, error)

// sentImages is a connection's set of image refs whose bytes a frame carried.
// A ref counts as sent once its frame is written, and the host's
// ui.view.evicted forgets it, so the next frame that references it carries
// its bytes again.
type sentImages struct {
	mu   sync.Mutex
	refs map[string]struct{}
}

// unsent returns the images whose bytes the connection has not sent.
func (s *sentImages) unsent(images []kitwire.Image) []kitwire.Image {
	if len(images) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []kitwire.Image
	for _, image := range images {
		if _, ok := s.refs[image.Ref]; !ok {
			out = append(out, image)
		}
	}
	return out
}

func (s *sentImages) markSent(images []kitwire.Image) {
	if len(images) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs == nil {
		s.refs = make(map[string]struct{}, len(images))
	}
	for _, image := range images {
		s.refs[image.Ref] = struct{}{}
	}
}

func (s *sentImages) forget(refs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ref := range refs {
		delete(s.refs, ref)
	}
}

// sendView writes one frame of the encoded view on conn through write, with
// the bytes of each image conn has not sent. Those count as sent once the
// frame is written.
func sendView(conn *conn, encoded kitwire.Encoded, write func(view json.RawMessage) error) error {
	unsent := conn.images.unsent(encoded.Images)
	payload, err := encoded.Payload(unsent)
	if err != nil {
		return err
	}
	if err := write(payload); err != nil {
		return err
	}
	conn.images.markSent(unsent)
	return nil
}

func (e *Extension) frontendAttached() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.frontend
}

// encodeView encodes view for this extension's host, with frontend-only
// annotations only while a frontend draws.
func (e *Extension) encodeView(view kit.View) (kitwire.Encoded, error) {
	return kitwire.Encode(view, e.frontendAttached())
}

// respondView answers a renderer request with a view, or with err.
func (e *Extension) respondView(id string, view kit.View, err error) {
	if err != nil {
		_ = e.conn.respond(id, nil, err)
		return
	}
	encoded, err := e.encodeView(view)
	if err != nil {
		_ = e.conn.respond(id, nil, err)
		return
	}
	_ = sendView(e.conn, encoded, func(payload json.RawMessage) error {
		return e.conn.respond(id, map[string]json.RawMessage{"view": payload}, nil)
	})
}

// viewSurface is the latest view of a widget, the header or the footer. The
// SDK keeps it to send it again when the host evicts one of its images.
type viewSurface struct {
	encoded kitwire.Encoded
	// pushed is set for a widget_push widget, which goes out on the
	// extension's connection rather than the Context's host connection.
	pushed bool
	send   func(c Context, view json.RawMessage) error

	// mu serializes a send with stop, so a superseded view never overwrites
	// the one that replaced it.
	mu      sync.Mutex
	stopped bool
}

func (s *viewSurface) push(c Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	conn := c.hostConnection()
	if s.pushed {
		conn = c.ext.conn
	}
	return sendView(conn, s.encoded, func(view json.RawMessage) error { return s.send(c, view) })
}

func (s *viewSurface) stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

func widgetViewSurfaceID(key string) string { return "widget:" + key }

// replaceViewSurface retires the view installed as id and installs next (nil
// for none).
func (e *Extension) replaceViewSurface(id string, next *viewSurface) {
	e.surfaceMu.Lock()
	previous := e.viewSurfaces[id]
	if next == nil {
		delete(e.viewSurfaces, id)
	} else {
		if e.viewSurfaces == nil {
			e.viewSurfaces = map[string]*viewSurface{}
		}
		e.viewSurfaces[id] = next
	}
	e.surfaceMu.Unlock()
	if previous != nil {
		previous.stop()
	}
}

func (c Context) setWidgetView(key string, view kit.View, options []WidgetOptions) error {
	encoded, err := c.ext.encodeView(view)
	if err != nil {
		return err
	}
	surface := &viewSurface{encoded: encoded}
	if len(options) == 0 {
		surface.pushed = true
		surface.send = func(c Context, view json.RawMessage) error { return c.ext.conn.pushWidgetView(key, view) }
	} else {
		opts := options[0]
		surface.send = func(c Context, view json.RawMessage) error {
			result, err := c.callHost("ui.setWidget", map[string]any{"key": key, "view": view, "options": opts})
			return callResultError(result, err)
		}
	}
	c.ext.replaceViewSurface(widgetViewSurfaceID(key), surface)
	return surface.push(c)
}

func (c Context) setSurfaceView(method string, view kit.View) error {
	encoded, err := c.ext.encodeView(view)
	if err != nil {
		return err
	}
	c.ext.replaceSurface(method, nil)
	surface := &viewSurface{encoded: encoded, send: func(c Context, view json.RawMessage) error {
		result, err := c.callHost(method, map[string]json.RawMessage{"view": view})
		return callResultError(result, err)
	}}
	c.ext.replaceViewSurface(method, surface)
	return surface.push(c)
}

// SetHeaderView replaces the default header with view, the kit form of the
// component factory Pi's ctx.ui.setHeader takes. The host renders it at the
// width it lays the header out at, every frame, so it needs no re-send after a
// resize. [Context.SetHeader] with nil restores the built-in header.
func (c Context) SetHeaderView(view kit.View) error {
	return c.setSurfaceView(headerMethod, view)
}

// SetFooterView replaces the default footer with view; it follows the
// contract of [Context.SetHeaderView]. [Context.SetFooter] with nil restores
// the built-in footer.
func (c Context) SetFooterView(view kit.View) error {
	return c.setSurfaceView(footerMethod, view)
}

// MessageViewRenderer registers a custom message renderer that returns a
// [kit.View]. It replaces a [Extension.MessageRenderer] for customType, and
// the host sees the same registration.
func (e *Extension) MessageViewRenderer(customType string, handler MessageViewRendererFunc) {
	e.renderers = append(e.renderers, rendererDef{CustomType: customType})
	delete(e.rendererFuncs, customType)
	e.messageViewRendererFuncs[customType] = handler
}

// EntryViewRenderer registers a custom session-entry renderer that returns a
// [kit.View]. It replaces an [Extension.EntryRenderer] for customType, and the
// host sees the same registration.
func (e *Extension) EntryViewRenderer(customType string, handler EntryViewRendererFunc) {
	e.entryRenderers = append(e.entryRenderers, rendererDef{CustomType: customType})
	delete(e.entryRendererFuncs, customType)
	e.entryViewRendererFuncs[customType] = handler
}

// viewImagesEvicted forgets refs the host dropped and sends again every live
// view that references one, so its next frame carries the bytes.
func (e *Extension) viewImagesEvicted(raw json.RawMessage) {
	var payload struct {
		Refs []string `json:"refs"`
	}
	if json.Unmarshal(raw, &payload) != nil || len(payload.Refs) == 0 {
		return
	}
	e.conn.images.forget(payload.Refs)
	evicted := make(map[string]struct{}, len(payload.Refs))
	for _, ref := range payload.Refs {
		evicted[ref] = struct{}{}
	}
	e.overlaysMu.RLock()
	overlays := make([]*remoteOverlay, 0, len(e.overlays))
	for _, overlay := range e.overlays {
		overlays = append(overlays, overlay)
	}
	e.overlaysMu.RUnlock()
	for _, overlay := range overlays {
		overlay.resendIfReferences(evicted)
	}
	e.surfaceMu.Lock()
	var surfaces []*viewSurface
	for _, surface := range e.viewSurfaces {
		if surface.encoded.References(evicted) {
			surfaces = append(surfaces, surface)
		}
	}
	e.surfaceMu.Unlock()
	c := Context{ext: e}
	for _, surface := range surfaces {
		// A send may wait for the host's reply, so it never blocks the
		// message loop.
		go func() { _ = surface.push(c) }()
	}
}

// viewEvent routes a ui.view.event to its overlay's input queue. Events for
// an overlay that has closed are dropped.
func (e *Extension) viewEvent(raw json.RawMessage) {
	var payload struct {
		Key   string `json:"key"`
		Node  string `json:"node"`
		Type  string `json:"type"`
		Index int    `json:"index"`
		Item  *struct {
			Value       string `json:"value"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"item"`
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Key == "" {
		return
	}
	e.overlaysMu.RLock()
	overlay := e.overlays[payload.Key]
	e.overlaysMu.RUnlock()
	if overlay == nil {
		return
	}
	event := &kit.Event{Node: payload.Node, Type: payload.Type, Index: payload.Index, ID: payload.ID, Value: payload.Value}
	if payload.Item != nil {
		event.Item = &kit.SelectItem{Value: payload.Item.Value, Label: payload.Item.Label, Description: payload.Item.Description}
	}
	overlay.enqueue(e.conn, payload.Key, overlayMessage{event: event})
}

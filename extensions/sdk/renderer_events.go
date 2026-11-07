package sdk

import (
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// notifyRenderRelease (host→ext) reports that a message or entry the extension rendered is no longer shown: the extension drops the state it kept
// under the key.
const notifyRenderRelease = "render_release"

// MessageViewEventFunc handles an event of a node of a message renderer's view, as the closure of a Pi component that the renderer returned handles
// the pointer: the click on a [kit.MouseRegion] of the view arrives as a [kit.EventMouse] event of that node, with coordinates local to the region.
// The host answers the terminal from the region's description at once and reports the events its handler ran for; the extension changes the
// message's [MessageRenderOptions.State] here, and the host draws the message again with the next request, which runs after every event the host had
// taken. message and options are the ones of the message's last render.
type MessageViewEventFunc func(ctx Context, message map[string]any, options MessageRenderOptions, event kit.Event) error

// EntryViewEventFunc handles an event of a node of an entry renderer's view; see [MessageViewEventFunc].
type EntryViewEventFunc func(ctx Context, entry map[string]any, options EntryRenderOptions, event kit.Event) error

// MessageViewEvent registers the handler of the events of customType's [Extension.MessageViewRenderer] views. A later registration replaces an
// earlier one.
//
// pig additive (D276)
func (e *Extension) MessageViewEvent(customType string, handler MessageViewEventFunc) {
	e.toolMu.Lock()
	defer e.toolMu.Unlock()
	if e.messageViewEventFuncs == nil {
		e.messageViewEventFuncs = make(map[string]MessageViewEventFunc)
	}
	e.messageViewEventFuncs[customType] = handler
}

// EntryViewEvent registers the handler of the events of customType's [Extension.EntryViewRenderer] views. A later registration replaces an earlier
// one.
//
// pig additive (D276)
func (e *Extension) EntryViewEvent(customType string, handler EntryViewEventFunc) {
	e.toolMu.Lock()
	defer e.toolMu.Unlock()
	if e.entryViewEventFuncs == nil {
		e.entryViewEventFuncs = make(map[string]EntryViewEventFunc)
	}
	e.entryViewEventFuncs[customType] = handler
}

// renderCard is the renderer state of one displayed message or entry, which the host names with a key. Calls for one card run one at a time, as
// Pi draws a message on one thread. subject is the message or entry as its first request decoded it: every call for the card receives that one map,
// as Pi's renderer receives one message object.
type renderCard struct {
	mu      sync.Mutex
	state   map[string]any
	subject map[string]any
}

// renderOptions are the options a render request carries; Entry requests carry no outputPad.
type renderOptions struct {
	Expanded  bool `json:"expanded"`
	OutputPad int  `json:"outputPad"`
}

// renderOutput is a renderer's lines, or its view when view is set.
type renderOutput struct {
	lines []string
	view  *kit.View
}

// renderRequest is a render_message or render_entry argument (the host's RenderComponentPayload) with the message or entry left undecoded.
type renderRequest struct {
	Key     string          `json:"key"`
	Events  []viewEventWire `json:"events"`
	Options renderOptions   `json:"options"`
	Width   int             `json:"width"`
}

// viewEventWire is a ui.view.event argument, and an element of a render request's events.
type viewEventWire struct {
	Key   string `json:"key"`
	Node  string `json:"node"`
	Type  string `json:"type"`
	Index int    `json:"index"`
	Item  *struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"item"`
	ID     string      `json:"id"`
	Value  string      `json:"value"`
	Cursor *kit.Cursor `json:"cursor"`
	Mouse  *kit.Mouse  `json:"mouse"`
}

// event is the kit event the wire form describes.
func (w viewEventWire) event() kit.Event {
	event := kit.Event{Node: w.Node, Type: w.Type, Index: w.Index, ID: w.ID, Value: w.Value, Cursor: w.Cursor, Mouse: w.Mouse}
	if w.Item != nil {
		event.Item = &kit.SelectItem{Value: w.Item.Value, Label: w.Item.Label, Description: w.Item.Description}
	}
	return event
}

// renderTranscript answers a render_message or render_entry request: the events the host's view took go to handle in order, then render draws the
// subject. subjectKey is the member of the arguments that holds it. A request without a key is not for a displayed message: it keeps nothing and
// takes no events.
func (e *Extension) renderTranscript(
	id string,
	req *requestMsg,
	subjectKey string,
	render func(card *renderCard, subject map[string]any, options renderOptions, width int) (renderOutput, error),
	handle func(card *renderCard, subject map[string]any, options renderOptions, event kit.Event) error,
) {
	var request renderRequest
	var raw map[string]json.RawMessage
	if len(req.Args) > 0 {
		_ = json.Unmarshal(req.Args, &request)
		_ = json.Unmarshal(req.Args, &raw)
	}
	card := e.renderCard(request.Key)
	card.mu.Lock()
	defer card.mu.Unlock()
	if card.subject == nil {
		subject := map[string]any{}
		if encoded, ok := raw[subjectKey]; ok {
			_ = json.Unmarshal(encoded, &subject)
		}
		card.subject = subject
	}
	var failure error
	for _, event := range request.Events {
		if err := handle(card, card.subject, request.Options, event.event()); err != nil && failure == nil {
			failure = err
		}
	}
	output, err := render(card, card.subject, request.Options, request.Width)
	if err == nil {
		err = failure
	}
	if output.view != nil {
		e.respondView(id, *output.view, err)
		return
	}
	_ = e.conn.respond(id, map[string]any{"lines": output.lines}, err)
}

// renderCard returns the card of key, creating it on first use. An empty key gets a card of its own that nothing keeps.
func (e *Extension) renderCard(key string) *renderCard {
	if key == "" {
		return &renderCard{state: map[string]any{}}
	}
	e.renderMu.Lock()
	defer e.renderMu.Unlock()
	card := e.renderCards[key]
	if card == nil {
		card = &renderCard{state: map[string]any{}}
		if e.renderCards == nil {
			e.renderCards = make(map[string]*renderCard)
		}
		e.renderCards[key] = card
	}
	return card
}

// releaseRenderCard drops the state of a message or entry the host no longer shows.
func (e *Extension) releaseRenderCard(raw json.RawMessage) {
	var payload struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	e.renderMu.Lock()
	delete(e.renderCards, payload.Key)
	e.renderMu.Unlock()
}

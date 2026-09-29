package subprocess

import (
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Wire of an extension editor component (ctx.ui.setEditorComponent): Pi's
// factory runs in the extension process and the editor it returns stays
// there. Every message is a notify, so the connection's frame order is the
// order the host and the editor see each other's events. Autocomplete uses the shared provider bridge.
const (
	// Extension → host.
	NotifyEditorInstall   = "ui.editor.install"
	NotifyEditorClear     = "ui.editor.clear"
	NotifyEditorRender    = "ui.editor.render"
	NotifyEditorChange    = "ui.editor.change"
	NotifyEditorSubmit    = "ui.editor.submit"
	NotifyEditorAction    = "ui.editor.action"
	NotifyEditorInputDone = "ui.editor.inputDone"
	NotifyEditorShortcut  = "ui.editor.shortcut"
	NotifyTerminalWrite   = "ui.terminal.write"
	NotifyHardwareCursor  = "ui.setShowHardwareCursor"

	// Host → extension.
	NotifyEditorInput        = "ui.editor.input"
	NotifyEditorMouse        = "ui.editor.mouse"
	NotifyEditorSetText      = "ui.editor.setText"
	NotifyEditorInsertText   = "ui.editor.insertText"
	NotifyEditorAddToHistory = "ui.editor.addToHistory"
	NotifyEditorConfigure    = "ui.editor.configure"
	NotifyEditorSubmitted    = "ui.editor.submitted"
	NotifyEditorClosed       = "ui.editor.closed"
)

// EditorPayload is the argument of the editor notifies; each carries the
// fields its method uses.
type EditorPayload struct {
	Key             string   `json:"key"`
	Data            string   `json:"data,omitempty"`
	Text            string   `json:"text,omitempty"`
	Expanded        string   `json:"expanded,omitempty"`
	Lines           []string `json:"lines,omitempty"`
	Width           int      `json:"width,omitempty"`
	Seq             uint64   `json:"seq,omitempty"`
	WantsKeyRelease bool     `json:"wantsKeyRelease,omitempty"`
	ID              uint64   `json:"id,omitempty"`
	Action          string   `json:"action,omitempty"`
	Local           bool     `json:"local,omitempty"`
	Enabled         bool     `json:"enabled,omitempty"`
}

// editorProxy is the host's handle on one extension editor component. It
// sends the host's calls to the editor and hands the editor's events to the
// bound host, holding events that arrive before the host binds.
type editorProxy struct {
	ext  string
	conn *Conn
	key  string

	mu       sync.Mutex
	host     extension.RemoteEditorHost
	pending  []func(extension.RemoteEditorHost)
	lastSeq  uint64
	closed   bool
	detached bool
}

var _ extension.RemoteEditor = (*editorProxy)(nil)

func (p *editorProxy) send(method string, payload any) {
	if p.conn == nil {
		return
	}
	args, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = p.conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: method, Args: args}})
}

func (p *editorProxy) live() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && !p.detached
}

func (p *editorProxy) Input(data string) {
	if p.live() {
		p.send(NotifyEditorInput, EditorPayload{Key: p.key, Data: data})
	}
}

func (p *editorProxy) Mouse(event extension.RemoteEditorMouseEvent) {
	if p.live() {
		p.send(NotifyEditorMouse, map[string]any{"key": p.key, "event": event})
	}
}

func (p *editorProxy) SetText(text string) {
	if p.live() {
		p.send(NotifyEditorSetText, map[string]any{"key": p.key, "text": text})
	}
}

func (p *editorProxy) InsertTextAtCursor(text string) {
	if p.live() {
		p.send(NotifyEditorInsertText, map[string]any{"key": p.key, "text": text})
	}
}

func (p *editorProxy) AddToHistory(text string) {
	if p.live() {
		p.send(NotifyEditorAddToHistory, map[string]any{"key": p.key, "text": text})
	}
}

func (p *editorProxy) Configure(config extension.RemoteEditorConfig) {
	if !p.live() {
		return
	}
	if config.Shortcuts == nil {
		config.Shortcuts = []string{}
	}
	p.send(NotifyEditorConfigure, struct {
		Key string `json:"key"`
		extension.RemoteEditorConfig
	}{p.key, config})
}

// Bind attaches host and replays the events that arrived before it.
func (p *editorProxy) Bind(host extension.RemoteEditorHost) {
	p.mu.Lock()
	p.host = host
	pending := p.pending
	p.pending = nil
	closed := p.closed
	p.mu.Unlock()
	for _, event := range pending {
		event(host)
	}
	if closed && host != nil {
		host.EditorClosed()
	}
}

// Close detaches the host from the editor, which the host replaced or
// dropped; the extension disposes of it.
func (p *editorProxy) Close() {
	p.mu.Lock()
	if p.detached {
		p.mu.Unlock()
		return
	}
	p.detached = true
	p.host = nil
	p.pending = nil
	closed := p.closed
	p.mu.Unlock()
	if !closed {
		p.send(NotifyEditorClosed, map[string]any{"key": p.key})
	}
}

// deliver runs event on the bound host, or holds it until Bind.
func (p *editorProxy) deliver(event func(extension.RemoteEditorHost)) {
	p.mu.Lock()
	if p.closed || p.detached {
		p.mu.Unlock()
		return
	}
	host := p.host
	if host == nil {
		p.pending = append(p.pending, event)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	event(host)
}

// connClosed reports the extension's process gone.
func (p *editorProxy) connClosed() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	host := p.host
	detached := p.detached
	p.mu.Unlock()
	if host != nil && !detached {
		host.EditorClosed()
	}
}

func (p *editorProxy) handle(method string, args json.RawMessage) {
	var payload EditorPayload
	if json.Unmarshal(args, &payload) != nil {
		return
	}
	switch method {
	case NotifyEditorRender:
		p.mu.Lock()
		if payload.Seq > 0 && payload.Seq <= p.lastSeq {
			p.mu.Unlock()
			return
		}
		p.lastSeq = payload.Seq
		p.mu.Unlock()
		lines := payload.Lines
		if lines == nil {
			lines = []string{}
		}
		p.deliver(func(host extension.RemoteEditorHost) {
			host.EditorFrame(lines, payload.Width, payload.WantsKeyRelease)
		})
	case NotifyEditorChange:
		p.deliver(func(host extension.RemoteEditorHost) { host.EditorChanged(payload.Text, payload.Expanded) })
	case NotifyEditorSubmit:
		id := payload.ID
		p.deliver(func(host extension.RemoteEditorHost) {
			host.EditorSubmit(payload.Text, func() {
				p.send(NotifyEditorSubmitted, map[string]any{"key": p.key, "id": id})
			})
		})
	case NotifyEditorAction:
		p.deliver(func(host extension.RemoteEditorHost) {
			host.EditorAction(extension.RemoteEditorAction{Action: payload.Action, Text: payload.Text, Expanded: payload.Expanded, Local: payload.Local})
		})
	case NotifyEditorInputDone:
		p.deliver(func(host extension.RemoteEditorHost) { host.EditorInputDone() })
	case NotifyEditorShortcut:
		p.deliver(func(host extension.RemoteEditorHost) { host.EditorShortcut(payload.Data) })
	case NotifyTerminalWrite:
		p.deliver(func(host extension.RemoteEditorHost) { host.TerminalWrite(payload.Data) })
	case NotifyHardwareCursor:
		p.deliver(func(host extension.RemoteEditorHost) { host.SetShowHardwareCursor(payload.Enabled) })
	}
}

// handleEditorNotify routes the editor notifies of one connection.
// Installing a new editor replaces the previous one, as Pi's
// setEditorComponent does. It runs on the connection's serial read loop.
func (b *UIBridge) handleEditorNotify(extName string, owner *Conn, n *NotifyPayload) bool {
	switch n.Method {
	case NotifyEditorInstall, NotifyEditorClear, NotifyEditorRender, NotifyEditorChange, NotifyEditorSubmit,
		NotifyEditorAction, NotifyEditorInputDone, NotifyEditorShortcut, NotifyTerminalWrite, NotifyHardwareCursor:
	default:
		return false
	}
	var payload EditorPayload
	if json.Unmarshal(n.Args, &payload) != nil {
		return true
	}
	switch n.Method {
	case NotifyEditorInstall:
		proxy := &editorProxy{ext: extName, conn: owner, key: payload.Key}
		b.mu.Lock()
		previous := b.editor
		b.editor = proxy
		b.editorsByConn[customOverlayOwnerPrefix(extName, owner)] = proxy
		ui, ready := b.uiCtx, b.uiReady
		b.mu.Unlock()
		if previous != nil {
			previous.Close()
		}
		if ready {
			ui.SetEditorComponent(proxy)
		}
	case NotifyEditorClear:
		b.mu.Lock()
		current := b.editor
		clear := current != nil && current.conn == owner && current.ext == extName && current.key == payload.Key
		if clear {
			b.editor = nil
		}
		ui, ready := b.uiCtx, b.uiReady
		b.mu.Unlock()
		if clear {
			current.Close()
			if ready {
				ui.SetEditorComponent(nil)
			}
		}
	default:
		b.mu.RLock()
		proxy := b.editorsByConn[customOverlayOwnerPrefix(extName, owner)]
		b.mu.RUnlock()
		if proxy == nil {
			return true
		}
		if n.Method != NotifyTerminalWrite && n.Method != NotifyHardwareCursor && proxy.key != payload.Key {
			return true
		}
		proxy.handle(n.Method, n.Args)
	}
	return true
}

// clearEditorConn drops the editor of a connection that closed, restoring the
// host's editor when it was the active one.
func (b *UIBridge) clearEditorConn(extName string, owner *Conn, ownerOnly bool) {
	prefix := customOverlayOwnerPrefix(extName, owner)
	b.mu.Lock()
	var proxies []*editorProxy
	for key, proxy := range b.editorsByConn {
		if key == prefix || (!ownerOnly && proxy.ext == extName) {
			proxies = append(proxies, proxy)
			delete(b.editorsByConn, key)
		}
	}
	for _, proxy := range proxies {
		if b.editor == proxy {
			b.editor = nil
		}
	}
	b.mu.Unlock()
	for _, proxy := range proxies {
		proxy.connClosed()
	}
}

// activeEditor is the installed extension editor, for a UI context that
// becomes ready after the extension installed it.
func (b *UIBridge) activeEditor() extension.RemoteEditor {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.editor == nil {
		return nil
	}
	return b.editor
}

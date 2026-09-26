package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// toolRenderSession is one tool card's renderer session in an extension
// process: upstream's per-card renderer state and last components live there.
// The card's call and result proxies share the session. When neither is
// reachable any more, the extension is told to release the card.
type toolRenderSession struct {
	card       string
	conn       *Conn
	invalidate atomic.Pointer[func()]
}

// toolRenderSessions routes an extension's invalidate notifications to live
// card sessions. Entries are weak, so a card that leaves the transcript
// releases its session.
type toolRenderSessions struct {
	mu     sync.Mutex
	byCard map[string]weak.Pointer[toolRenderSession]
}

var toolRenderCardSeq atomic.Uint64

// session returns the live session of card on conn, creating it when card has
// none. An empty card gets a session of its own.
func (r *toolRenderSessions) session(conn *Conn, card string) *toolRenderSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if card != "" {
		if existing, ok := r.byCard[card]; ok {
			if session := existing.Value(); session != nil && session.conn == conn {
				return session
			}
		}
	} else {
		card = fmt.Sprintf("host-%d", toolRenderCardSeq.Add(1))
	}
	session := &toolRenderSession{card: card, conn: conn}
	if r.byCard == nil {
		r.byCard = make(map[string]weak.Pointer[toolRenderSession])
	}
	r.byCard[card] = weak.Make(session)
	runtime.AddCleanup(session, r.release, toolRenderRelease{card: card, conn: conn})
	return session
}

type toolRenderRelease struct {
	card string
	conn *Conn
}

// release drops a collected session and tells its extension to free the
// card's renderer state.
func (r *toolRenderSessions) release(released toolRenderRelease) {
	r.mu.Lock()
	if existing, ok := r.byCard[released.card]; ok && existing.Value() == nil {
		delete(r.byCard, released.card)
	}
	r.mu.Unlock()
	if released.conn == nil {
		return
	}
	args, err := json.Marshal(ToolRenderCardPayload{Card: released.card})
	if err != nil {
		return
	}
	_ = released.conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyToolRenderRelease, Args: args}})
}

// invalidate runs the invalidate callback of card's latest render context.
func (r *toolRenderSessions) invalidate(conn *Conn, card string) {
	r.mu.Lock()
	existing, ok := r.byCard[card]
	r.mu.Unlock()
	if !ok {
		return
	}
	session := existing.Value()
	if session == nil || session.conn != conn {
		return
	}
	if invalidate := session.invalidate.Load(); invalidate != nil && *invalidate != nil {
		(*invalidate)()
	}
}

// toolRenderProxy is the host-side component a subprocess tool's renderCall
// or renderResult returns. Render never performs IPC: it returns the last
// frame the extension rendered and starts at most one owned background
// request loop when the renderer inputs or the width change.
type toolRenderProxy struct {
	session    *toolRenderSession
	tool       string
	phase      string
	inactivity time.Duration
	repaint    func()

	mu         sync.Mutex
	payload    RenderToolPayload
	rerender   bool
	lines      []string
	linesWidth int
	failed     bool
	fallback   func(width int) []string
	width      int
	generation uint64
	requesting bool
	dirty      bool
}

// makeToolRenderCall returns the renderCall the host registers for a
// subprocess tool that defines one.
func (h *Host) makeToolRenderCall(me *managedExt, toolName string) extension.ToolRenderCallFunc {
	return func(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		return h.toolRenderProxyFor(me, toolName, "call", context, RenderToolPayload{Args: args})
	}
}

// makeToolRenderResult returns the renderResult the host registers for a
// subprocess tool that defines one.
func (h *Host) makeToolRenderResult(me *managedExt, toolName string) extension.ToolRenderResultFunc {
	return func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		args, _ := context.Args.(json.RawMessage)
		return h.toolRenderProxyFor(me, toolName, "result", context, RenderToolPayload{
			Args:    args,
			Result:  renderToolResultPayload(result),
			Options: &options,
		})
	}
}

// toolRenderProxyFor reuses the card's last proxy for phase, as upstream
// hands a renderer its last component, and schedules the renderer to run with
// the new inputs.
func (h *Host) toolRenderProxyFor(me *managedExt, toolName, phase string, context extension.ToolRenderContext, payload RenderToolPayload) *toolRenderProxy {
	proxy, _ := context.LastComponent.(*toolRenderProxy)
	if proxy == nil || proxy.phase != phase || proxy.tool != toolName || proxy.session.conn != me.conn || (context.Card != "" && proxy.session.card != context.Card) {
		proxy = &toolRenderProxy{
			session:    me.toolRenders.session(me.conn, context.Card),
			tool:       toolName,
			phase:      phase,
			inactivity: rendererInactivity,
			repaint: func() {
				if h.uiBridge != nil {
					h.uiBridge.Invalidate()
				}
			},
		}
	}
	invalidate := context.Invalidate
	proxy.session.invalidate.Store(&invalidate)
	payload.Card = proxy.session.card
	payload.Phase = phase
	payload.Context = RenderToolContext{
		ToolCallID:       context.ToolCallID,
		Cwd:              context.Cwd,
		ExecutionStarted: context.ExecutionStarted,
		ArgsComplete:     context.ArgsComplete,
		IsPartial:        context.IsPartial,
		Expanded:         context.Expanded,
		ShowImages:       context.ShowImages,
		IsError:          context.IsError,
	}
	proxy.update(payload)
	return proxy
}

// renderToolResultPayload converts the host's tool result to upstream's
// {content, details} shape.
func renderToolResultPayload(result extension.AgentToolResult) *RenderToolResult {
	out := &RenderToolResult{Content: []RenderToolContent{}}
	var toolResult agent.AgentToolResult
	switch value := result.(type) {
	case agent.AgentToolResult:
		toolResult = value
	case *agent.AgentToolResult:
		if value != nil {
			toolResult = *value
		}
	default:
		return out
	}
	if toolResult.Content != "" {
		out.Content = append(out.Content, RenderToolContent{Type: "text", Text: toolResult.Content})
	}
	for _, image := range toolResult.Images {
		out.Content = append(out.Content, RenderToolContent{Type: "image", Data: image.Data, MimeType: image.MimeType})
	}
	if toolResult.Details != nil {
		if details, err := json.Marshal(toolResult.Details); err == nil {
			out.Details = details
		}
	}
	return out
}

// SetRendererFallback supplies what the card draws in place of a renderer
// that throws, as upstream catches a renderer error and shows its fallback.
func (p *toolRenderProxy) SetRendererFallback(fallback func(width int) []string) {
	p.mu.Lock()
	p.fallback = fallback
	p.mu.Unlock()
}

// IsDirty reports a frame that arrived after the last Render.
func (p *toolRenderProxy) IsDirty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dirty
}

func (p *toolRenderProxy) update(payload RenderToolPayload) {
	p.mu.Lock()
	p.payload = payload
	p.rerender = true
	start := false
	if p.width > 0 {
		p.generation++
		if !p.requesting {
			p.requesting = true
			start = true
		}
	}
	p.mu.Unlock()
	if start {
		go p.requestLoop()
	}
}

func (p *toolRenderProxy) Render(width int) []string {
	p.mu.Lock()
	p.dirty = false
	start := false
	if width > 0 && width != p.width {
		p.width = width
		p.generation++
		if !p.requesting {
			p.requesting = true
			start = true
		}
	}
	failed, fallback := p.failed, p.fallback
	lines := append([]string(nil), widthx.FrameAt(p.lines, p.linesWidth, width)...)
	p.mu.Unlock()
	if start {
		go p.requestLoop()
	}
	if failed && fallback != nil {
		return fallback(width)
	}
	return lines
}

// RenderNow runs the renderer in the extension at width and waits for its
// frame, for a caller off the TUI loop that needs the component's lines at
// once, as HTML export renders upstream's synchronous renderers. answered is
// false when no frame arrived within the renderer inactivity boundary;
// failed reports a renderer that threw.
func (p *toolRenderProxy) RenderNow(width int) (lines []string, failed, answered bool) {
	p.mu.Lock()
	payload := p.payload
	payload.Width = width
	payload.Rerender = p.rerender
	p.rerender = false
	p.mu.Unlock()
	lines, failed, answered = p.request(payload)
	p.mu.Lock()
	if answered {
		p.lines, p.linesWidth, p.failed = lines, width, failed
	} else if payload.Rerender {
		p.rerender = true
	}
	p.mu.Unlock()
	return lines, failed, answered
}

// Invalidate is a no-op: the card runs its renderers again on
// invalidation, which schedules a new generation through update.
func (p *toolRenderProxy) Invalidate() {}

// requestLoop renders the newest generation. A request that a newer
// generation overtook is discarded and the loop renders again.
func (p *toolRenderProxy) requestLoop() {
	for {
		p.mu.Lock()
		generation := p.generation
		payload := p.payload
		payload.Width = p.width
		payload.Rerender = p.rerender
		p.rerender = false
		p.mu.Unlock()

		lines, failed, ok := p.request(payload)

		p.mu.Lock()
		if !ok && payload.Rerender {
			p.rerender = true
		}
		current := generation == p.generation
		if current && ok {
			p.lines = lines
			p.linesWidth = payload.Width
			p.failed = failed
			p.dirty = true
		}
		if current {
			p.requesting = false
			p.mu.Unlock()
			if ok && p.repaint != nil {
				p.repaint()
			}
			return
		}
		p.mu.Unlock()
	}
}

// request runs one render in the extension. failed reports a renderer that
// threw; ok is false when no answer arrived.
func (p *toolRenderProxy) request(payload RenderToolPayload) (lines []string, failed, ok bool) {
	args, err := json.Marshal(payload)
	if err != nil {
		return nil, false, false
	}
	// pig divergence (D56): a tool renderer generation has the renderer
	// inactivity boundary off the TUI loop and keeps the last frame.
	resp, err := p.session.conn.requestWithInactivity(context.Background(), &Envelope{
		Type:    MsgRequest,
		Request: &RequestPayload{Method: RequestRenderTool, Tool: p.tool, Args: args},
	}, p.inactivity, RequestRenderTool+" "+p.tool)
	if err != nil || resp == nil || resp.Response == nil {
		return nil, false, false
	}
	if resp.Response.Error != nil {
		return nil, true, true
	}
	var result RenderResult
	if err := json.Unmarshal(resp.Response.Result, &result); err != nil {
		return nil, true, true
	}
	return result.Lines, false, true
}

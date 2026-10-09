package subprocess

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// renderProxyComponent keeps subprocess-rendered lines in a host-side cache.
// pig divergence (D56): renderer inactivity cancels one generation off-loop.
// Render never performs IPC or JSON work; it returns the current generation and
// starts at most one owned background request loop when width/options change.
type renderProxyComponent struct {
	// changed is set when a response replaces the painted lines; a parent that caches this component reads it through IsDirty and NeedsRedraw.
	changed    atomic.Bool
	extName    string
	customType string
	method     string // "render_message" | "render_entry"
	payloadKey string // "message" | "entry"
	payload    any    // extension.CustomMessage | extension.CustomEntry
	conn       *Conn
	inactivity time.Duration
	invalidate func()
	fallback   []string

	mu         sync.Mutex
	lines      []string
	linesWidth int // width the cached lines were rendered at
	width      int
	options    extension.MessageRenderOptions
	generation uint64
	requesting bool
	// view is the view of the renderer's results (D107).
	view rendererView
}

func newRenderProxyComponent(
	extName string,
	customType string,
	message extension.CustomMessage,
	options extension.MessageRenderOptions,
	conn *Conn,
	inactivity time.Duration,
	invalidate func(),
) *renderProxyComponent {
	return &renderProxyComponent{
		extName:    extName,
		customType: customType,
		method:     "render_message",
		payloadKey: "message",
		payload:    message,
		conn:       conn,
		inactivity: inactivity,
		invalidate: invalidate,
		fallback:   []string{fmt.Sprintf("[%s]", customType)},
		options:    options,
	}
}

// newEntryRenderProxyComponent builds the host-side proxy for a subprocess
// custom-entry renderer. It shares renderProxyComponent's proven single-flight,
// generation-guarded, off-loop IPC core with the message renderer; only the RPC
// method and payload key differ. The host-owned transcript Spacer(1) upstream
// CustomEntryComponent adds is contributed by the caller (addCustomEntryToChat),
// so this proxy stays a pure content renderer identical to the message proxy.
func newEntryRenderProxyComponent(
	extName string,
	customType string,
	entry extension.CustomEntry,
	options extension.EntryRenderOptions,
	conn *Conn,
	inactivity time.Duration,
	invalidate func(),
) *renderProxyComponent {
	return &renderProxyComponent{
		extName:    extName,
		customType: customType,
		method:     "render_entry",
		payloadKey: "entry",
		payload:    entry,
		conn:       conn,
		inactivity: inactivity,
		invalidate: invalidate,
		fallback:   []string{fmt.Sprintf("[%s]", customType)},
		options:    extension.MessageRenderOptions{Expanded: options.Expanded},
	}
}

func (c *renderProxyComponent) Render(width int) []string {
	c.mu.Lock()
	start := false
	if width > 0 && width != c.width {
		c.width = width
		c.generation++
		if !c.requesting && c.conn != nil {
			c.requesting = true
			start = true
		}
	}
	lines := c.lines
	if len(lines) == 0 {
		lines = c.fallback
	} else {
		// A response for an earlier width is never painted (whatever its rows)
		// while the request at the current width is in flight.
		lines = widthx.FrameAt(lines, c.linesWidth, c.width)
		if live, ok := c.view.live(c.width); ok && c.linesWidth == c.width {
			lines = live
		}
	}
	out := append([]string(nil), lines...)
	c.mu.Unlock()

	if start {
		go c.requestLoop()
	}
	return out
}

// IsDirty reports whether a response replaced the painted lines since the last NeedsRedraw, so a caching parent re-renders the proxy.
func (c *renderProxyComponent) IsDirty() bool { return c.changed.Load() }

// NeedsRedraw consumes the flag IsDirty reads.
func (c *renderProxyComponent) NeedsRedraw() bool { return c.changed.Swap(false) }

func (c *renderProxyComponent) Invalidate() {
	c.mu.Lock()
	if c.width <= 0 || c.conn == nil {
		c.mu.Unlock()
		return
	}
	c.generation++
	start := !c.requesting
	c.requesting = true
	c.mu.Unlock()
	if start {
		go c.requestLoop()
	}
}

// SetExpanded updates the renderer options and refreshes the cached generation.
func (c *renderProxyComponent) SetExpanded(expanded bool) {
	c.mu.Lock()
	if c.options.Expanded == expanded {
		c.mu.Unlock()
		return
	}
	c.options.Expanded = expanded
	if c.width <= 0 || c.conn == nil {
		c.mu.Unlock()
		return
	}
	c.generation++
	start := !c.requesting
	c.requesting = true
	c.mu.Unlock()
	if start {
		go c.requestLoop()
	}
}

// SetOutputPad refreshes custom-message rendering without changing entry options.
func (c *renderProxyComponent) SetOutputPad(padding int) {
	c.mu.Lock()
	if c.method != "render_message" || c.options.OutputPad == padding {
		c.mu.Unlock()
		return
	}
	c.options.OutputPad = padding
	if c.width <= 0 || c.conn == nil {
		c.mu.Unlock()
		return
	}
	c.generation++
	start := !c.requesting
	c.requesting = true
	c.mu.Unlock()
	if start {
		go c.requestLoop()
	}
}

func (c *renderProxyComponent) requestLoop() {
	for {
		c.mu.Lock()
		generation := c.generation
		width := c.width
		options := c.options
		c.mu.Unlock()

		lines, ok := c.request(width, options)

		c.mu.Lock()
		current := generation == c.generation
		if current && ok {
			c.lines = append([]string(nil), lines...)
			c.linesWidth = width
			c.changed.Store(true)
		}
		if current {
			c.requesting = false
			c.mu.Unlock()
			if ok && c.invalidate != nil {
				c.invalidate()
			}
			return
		}
		c.mu.Unlock()
	}
}

func (c *renderProxyComponent) request(width int, options extension.MessageRenderOptions) ([]string, bool) {
	ctx := context.Background()
	var wireOptions any = options
	if c.method == "render_entry" {
		wireOptions = extension.EntryRenderOptions{Expanded: options.Expanded}
	}
	args, err := json.Marshal(map[string]any{
		c.payloadKey: c.payload,
		"options":    wireOptions,
		"width":      width,
	})
	if err != nil {
		return nil, false
	}
	resp, err := c.conn.requestWithInactivity(ctx, &Envelope{
		Type: MsgRequest,
		Request: &RequestPayload{
			Method: c.method,
			Tool:   c.customType,
			Args:   args,
		},
	}, c.inactivity, c.method+" "+c.customType)
	if err != nil || resp == nil || resp.Response == nil || resp.Response.Error != nil {
		return nil, false
	}
	var result RenderResult
	if err := json.Unmarshal(resp.Response.Result, &result); err != nil {
		return nil, false
	}
	return c.view.apply(c.conn, c.invalidate, result, width)
}

// FrontendView reports the structure of the renderer's result to a D91
// frontend (D107) while the result shown is the one laid out at width.
func (c *renderProxyComponent) FrontendView(width int) *frontend.View {
	c.mu.Lock()
	current := len(c.lines) > 0 && c.linesWidth == width
	c.mu.Unlock()
	if !current {
		return nil
	}
	return c.view.frontendView(width)
}

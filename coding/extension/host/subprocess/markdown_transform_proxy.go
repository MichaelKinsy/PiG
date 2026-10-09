package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// RequestMarkdownTransform runs an extension's registered display rewrite. A null answer keeps the input, as Pi does for a throwing or non-string transformer.
const RequestMarkdownTransform = "markdown_transform"

// MarkdownTransformPayload is the RequestMarkdownTransform argument.
type MarkdownTransformPayload struct {
	Markdown string                             `json:"markdown"`
	Context  extension.MarkdownTransformContext `json:"context"`
}

// markdownTransformProxy waits on the caller-owned off-loop generation. It does not cache across messages: two identical messages can have different results from a stateful transformer.
type markdownTransformProxy struct {
	conn       func() *Conn
	inactivity time.Duration

	mu sync.Mutex
	// stalled closes when the body of a request the host abandoned returns, or its connection ends. While it is open the transformer is disabled.
	stalled <-chan struct{}
}

// disabled reports whether a body the host abandoned is still running. Pi's chain finishes one transformer body before it starts the next
// (markdown-transform.ts:18-29), so an abandoned body keeps the transformer out of later chains, which keep their markdown as for a throwing transformer (D56: the stalled generation is disabled).
func (p *markdownTransformProxy) disabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stalled == nil {
		return false
	}
	select {
	case <-p.stalled:
		p.stalled = nil
		return false
	default:
		return true
	}
}

func (p *markdownTransformProxy) abandon(settled <-chan struct{}) {
	if settled == nil {
		return
	}
	p.mu.Lock()
	p.stalled = settled
	p.mu.Unlock()
}

func (p *markdownTransformProxy) transform(markdown string, transformContext extension.MarkdownTransformContext) string {
	conn := p.conn()
	// pig divergence (D56): while a body the renderer inactivity boundary abandoned still runs, later chains skip this transformer instead of waiting for it.
	if conn == nil || p.disabled() {
		return markdown
	}
	ctx := transformContext.Context
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := json.Marshal(MarkdownTransformPayload{Markdown: markdown, Context: transformContext})
	if err != nil {
		return markdown
	}
	resp, err := conn.requestWithInactivity(ctx, &Envelope{
		Type:    MsgRequest,
		Request: &RequestPayload{Method: RequestMarkdownTransform, Args: args},
	}, p.inactivity, RequestMarkdownTransform)
	if stalled, ok := errors.AsType[*HandlerStalledError](err); ok {
		p.abandon(stalled.Settled)
	}
	if err != nil || resp == nil || resp.Response == nil || resp.Response.Error != nil {
		return markdown
	}
	var transformed *string
	if err := json.Unmarshal(resp.Response.Result, &transformed); err != nil || transformed == nil {
		return markdown
	}
	return *transformed
}

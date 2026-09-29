package subprocess

import (
	"context"
	"encoding/json"
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
}

func (p *markdownTransformProxy) transform(markdown string, transformContext extension.MarkdownTransformContext) string {
	conn := p.conn()
	if conn == nil {
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
	if err != nil || resp == nil || resp.Response == nil || resp.Response.Error != nil {
		return markdown
	}
	var transformed *string
	if err := json.Unmarshal(resp.Response.Result, &transformed); err != nil || transformed == nil {
		return markdown
	}
	return *transformed
}

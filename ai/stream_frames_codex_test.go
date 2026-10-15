//go:build !pig_strip_mistral_conversations && !pig_strip_openai_codex_responses

package ai

import (
	"context"
	"fmt"
	"strings"
)

// The Codex Responses provider streams the Responses wire format over SSE through its own provider configuration; its frames are gated like the other providers'.
func init() {
	streamFrameShapes = append(streamFrameShapes, streamFrameShape{
		name: "openai-codex-responses-text", api: APIOpenAICodexResponses, providerID: "openai-codex", model: "gpt-bench",
		provider: func(url string) Provider {
			return codexSSEProvider{NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{BaseURL: url, APIKey: codexToken(), Model: "gpt-bench", ProviderID: "openai-codex"})}
		},
		body: func(frames int) string {
			var body strings.Builder
			body.WriteString(sseRecords(
				`data: {"type":"response.created","response":{"id":"resp_1"}}`,
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
				`data: {"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
			))
			for i := range frames {
				fmt.Fprintf(&body, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"item_id\":\"msg_1\",\"delta\":\"t%d \"}\n\n", i)
			}
			body.WriteString(sseRecords(
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
				`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12}}}`,
			))
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	})
}

// codexSSEProvider streams over SSE only. The default transport, auto, first attempts a WebSocket upgrade, which the frame server answers
// with an SSE body; the request then falls back to SSE, so every Stream would reach the server twice and the shape would measure the
// failed upgrade along with the frames.
type codexSSEProvider struct{ Provider }

func (p codexSSEProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	opts.Transport = TransportSSE
	return p.Provider.Stream(ctx, transcript, opts)
}

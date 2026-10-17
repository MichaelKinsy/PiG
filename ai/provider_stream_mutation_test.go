//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// Every streaming parser must settle on any corrupted, truncated or spliced stream: no panic, no hang, and a stop reason other than
// pending. The seeds are valid streams of each API; mutateSSE damages them with a fixed pseudo-random sequence.
func mutateSSE(r *rand.Rand, s string) string {
	b := []byte(s)
	for range 1 + r.Intn(4) {
		if len(b) == 0 {
			break
		}
		switch r.Intn(5) {
		case 0:
			b = b[:r.Intn(len(b))]
		case 1:
			i := r.Intn(len(b))
			b[i] = byte(r.Intn(256))
		case 2:
			i := r.Intn(len(b))
			b = append(b[:i], b[min(len(b), i+1+r.Intn(8)):]...)
		case 3:
			i := r.Intn(len(b))
			j := r.Intn(len(b))
			b = append(b[:i], append([]byte(string(b[j:min(len(b), j+r.Intn(30))])), b[i:]...)...)
		case 4:
			b = []byte(strings.Replace(string(b), `"`, `\"`, 1+r.Intn(2)))
		}
	}
	return string(b)
}

func fuzzParse(t *testing.T, name, seed string, seedNum int64, n int, run func(in string) *AssistantMessage) {
	r := rand.New(rand.NewSource(seedNum))
	for i := range n {
		in := mutateSSE(r, seed)
		done := make(chan string, 1)
		go func() {
			defer func() {
				if e := recover(); e != nil {
					done <- fmt.Sprint("PANIC ", e)
				}
			}()
			m := run(in)
			if m.StopReason == "" || m.StopReason == StopReasonPending {
				done <- "PENDING " + string(m.StopReason)
				return
			}
			done <- ""
		}()
		select {
		case res := <-done:
			if res != "" {
				t.Errorf("%s iter %d: %s\n%q", name, i, res, in)
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s iter %d HANG %q", name, i, in)
		}
	}
}

func TestStreamingParsersSettleOnMutatedStreams(t *testing.T) {
	const iterations = 3000
	openAISeed := `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"hi","reasoning_content":"r"}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"a\":"}}]}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}` + "\n\ndata: [DONE]\n\n"
	fuzzParse(t, "openai-completions", openAISeed, 1, iterations, func(in string) *AssistantMessage {
		p := &openAIProvider{cfg: OpenAIConfig{ProviderID: "openai"}}
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "m")
		p.parseSSE(context.Background(), strings.NewReader(in), builder, nil)
		return builder.stream.Result()
	})
	anthropicSeed := anthropicFixtureEvent("message_start", `{"message":{"id":"m","usage":{"input_tokens":5,"output_tokens":0}}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"thinking","thinking":""}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"signature_delta","signature":"sig"}}`) +
		anthropicFixtureEvent("content_block_stop", `{"index":0}`) +
		anthropicFixtureEvent("content_block_start", `{"index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":1,"delta":{"type":"input_json_delta","partial_json":"1}"}}`) +
		anthropicFixtureEvent("content_block_stop", `{"index":1}`) +
		anthropicFixtureEnd("tool_use")
	fuzzParse(t, "anthropic-messages", anthropicSeed, 2, iterations, func(in string) *AssistantMessage {
		p := &anthropicProvider{cfg: AnthropicConfig{Model: "claude-haiku-4-5", ProviderID: "anthropic"}}
		builder := newAssistantStreamBuilder(context.Background(), APIAnthropicMessages, "anthropic", "claude-haiku-4-5")
		p.parseAnthropicSSE(context.Background(), strings.NewReader(in), builder, anthropicStreamNames{})
		return builder.stream.Result()
	})
	resp := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}` + "\n\n" +
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hi"}` + "\n\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}` + "\n\n" +
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}` + "\n\n" +
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"a\":1}"}` + "\n\n" +
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"a\":1}"}}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}` + "\n\n"
	fuzzParse(t, "openai-responses", resp, 3, iterations, func(in string) *AssistantMessage {
		p := &openAIResponsesProvider{}
		bld := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "m")
		p.parseResponsesSSE(context.Background(), strings.NewReader(in), bld, nil)
		return bld.stream.Result()
	})
	gem := `data: {"candidates":[{"content":{"parts":[{"text":"hi","thought":true},{"text":"yo"}]}}],"usageMetadata":{"promptTokenCount":3}}` + "\r\n\r\n" +
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"a":1}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":7}}` + "\r\n\r\n"
	fuzzParse(t, "google-generative-ai", gem, 4, iterations, func(in string) *AssistantMessage {
		p := &googleProvider{}
		bld := newAssistantStreamBuilder(context.Background(), APIGoogleGenerativeAI, "google", "m")
		p.parseGeminiSSE(context.Background(), strings.NewReader(in), bld)
		return bld.stream.Result()
	})
	mistral := `data: {"id":"r","model":"m","choices":[{"index":0,"finish_reason":null,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"hm"}]}]}}]}` + "\r\n\r\n" +
		`data: {"id":"r","choices":[{"index":0,"finish_reason":null,"delta":{"tool_calls":[{"id":"abc123456","index":0,"function":{"name":"read","arguments":"{\"a\":"}}]}}]}` + "\r\n\r\n" +
		`data: {"id":"r","choices":[{"index":0,"finish_reason":null,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}` + "\r\n\r\n" + mistralUpstreamTerminal
	fuzzParse(t, "mistral-conversations", mistral, 5, iterations, func(in string) *AssistantMessage {
		p := &mistralProvider{}
		builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "m")
		p.consumeStream(context.Background(), io.NopCloser(strings.NewReader(in)), builder)
		return builder.stream.Result()
	})
}

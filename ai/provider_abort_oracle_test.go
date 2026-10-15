package ai_test

import (
	"context"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/*.ts stream (options.signal)

// A request aborted while its response stream is open: the events delivered before the abort and the final message (stop reason "aborted", the content
// so far, the error message) read from the pinned pi-ai (AbortSignal) and from PiG (context cancellation). The server delivers a prefix of a stream at an
// event boundary and then holds the connection open.
func TestProviderAbortMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(157))
	type target struct {
		module, name, provider string
		api                    ai.API
		stream                 func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
		events                 []string
	}
	targets := []target{
		{"openai-completions.js", "openai-completions", "openai", ai.APIOpenAICompletions, ai.StreamOpenAICompletions, []string{
			`{"id":"1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"hello "},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}`,
			`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`,
		}},
		{"anthropic-messages.js", "anthropic-messages", "anthropic", ai.APIAnthropicMessages, ai.StreamAnthropic, []string{
			`{"type":"message_start","message":{"id":"m","usage":{"input_tokens":4,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi "}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"hm"}}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`,
			`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		}},
		{"openai-responses.js", "openai-responses", "openai", ai.APIOpenAIResponses, ai.StreamOpenAIResponses, []string{
			`{"type":"response.created","response":{"id":"r"}}`,
			`{"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
			`{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`,
			`{"type":"response.output_text.delta","delta":"hello "}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","delta":"{\"p\":"}`,
		}},
		{"google-generative-ai.js", "google-generative-ai", "google", ai.APIGoogleGenerativeAI, ai.StreamGoogle, []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello "}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"hm","thought":true}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","args":{"a":1}}}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"more"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
		}},
		{"mistral-conversations.js", "mistral-conversations", "mistral", ai.APIMistralConversations, ai.StreamMistral, []string{
			`{"data":{"choices":[{"index":0,"delta":{"role":"assistant"}}]}}`,
			`{"data":{"choices":[{"index":0,"delta":{"content":"hello "}}]}}`,
			`{"data":{"choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"hm"}]}]}}]}}`,
			`{"data":{"choices":[{"index":0,"delta":{"toolCalls":[{"id":"call_1","function":{"name":"read","arguments":"{\"a\":"}}]}}]}}`,
		}},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			var probes []responseProbe
			for range 20 {
				var sse strings.Builder
				for _, event := range target.events[:rng.Intn(len(target.events)+1)] {
					sse.WriteString("data: " + event + "\n\n")
				}
				probes = append(probes, responseProbe{Provider: target.provider, SSE: sse.String(), AbortAfterMs: 150})
			}
			checkResponsesAllowEmpty(t, target.module, target.name, target.api, target.stream, probes)
		})
	}
}

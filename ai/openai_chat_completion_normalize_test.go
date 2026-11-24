package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestOpenAICompletionsNormalizeFinishReasonOnProviderEvent pins openai@7.59.0's
// normalizeChatCompletionStream (lib/chat-completions/streaming.mjs:1-22), called by
// chat.completions.create({stream: true}) (resources/chat/completions/completions.mjs:79-85)
// before Pi passes each chunk to onProviderStreamEvent (openai-completions.ts:544-545).
func TestOpenAICompletionsNormalizeFinishReasonOnProviderEvent(t *testing.T) {
	cases := []struct {
		name      string
		wire      string
		wantEvent string
	}{
		{
			name:      "missing_finish_reason_gets_null",
			wire:      `{"id":"1","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			wantEvent: `{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		},
		{
			name:      "existing_null_untouched",
			wire:      `{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
			wantEvent: `{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		},
		{
			name:      "existing_stop_untouched",
			wire:      `{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			wantEvent: `{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		},
		{
			name:      "existing_empty_string_untouched",
			wire:      `{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":""}]}`,
			wantEvent: `{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":""}]}`,
		},
		{
			name:      "existing_false_and_zero_untouched",
			wire:      `{"id":"1","choices":[{"index":0,"finish_reason":false},{"index":1,"finish_reason":0}]}`,
			wantEvent: `{"id":"1","choices":[{"index":0,"finish_reason":false},{"index":1,"finish_reason":0}]}`,
		},
		{
			name:      "empty_choices_untouched",
			wire:      `{"id":"1","choices":[]}`,
			wantEvent: `{"id":"1","choices":[]}`,
		},
		{
			name:      "no_choices_untouched",
			wire:      `{"id":"1","usage":{"prompt_tokens":1,"completion_tokens":1}}`,
			wantEvent: `{"id":"1","usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		},
		{
			name:      "non_array_choices_untouched",
			wire:      `{"id":"1","choices":{}}`,
			wantEvent: `{"id":"1","choices":{}}`,
		},
		{
			name:      "multiple_choices_only_missing_gets_null",
			wire:      `{"id":"1","choices":[{"index":0,"finish_reason":"stop"},{"index":1,"delta":{"content":"x"}}]}`,
			wantEvent: `{"id":"1","choices":[{"index":0,"finish_reason":"stop"},{"index":1,"delta":{"content":"x"},"finish_reason":null}]}`,
		},
		{
			name:      "primitive_chunk_untouched",
			wire:      `42`,
			wantEvent: `42`,
		},
		{
			name:      "extra_nested_metadata_with_missing_finish_reason",
			wire:      `{"id":"1","openrouter_metadata":{"strategy":"direct"},"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			wantEvent: `{"id":"1","openrouter_metadata":{"strategy":"direct"},"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var observed []any
			builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "openai", "gpt-4o")
			builder.setProviderEventObserver(StreamOptions{
				OnProviderStreamEvent: func(_ context.Context, data any, _ *Model) error {
					observed = append(observed, data)
					return nil
				},
			}, &Model{ID: "gpt-4o"})
			body := "data: " + tc.wire + "\n\ndata: [DONE]\n\n"
			(&openAIProvider{cfg: OpenAIConfig{Model: "gpt-4o", ProviderID: "openai"}}).parseSSE(t.Context(), strings.NewReader(body), builder, nil)
			_ = builder.stream.Result()

			var want any
			if err := json.Unmarshal([]byte(tc.wantEvent), &want); err != nil {
				t.Fatal(err)
			}
			if len(observed) != 1 {
				t.Fatalf("observed %d events (%#v), want 1", len(observed), observed)
			}
			if !reflect.DeepEqual(observed[0], want) {
				t.Fatalf("observed = %#v\nwant     = %#v", observed[0], want)
			}
		})
	}
}

// TestNormalizeOpenAIChatCompletionChunkEventZeroAllocFastPath checks that
// normalizeOpenAIChatCompletionChunkEvent allocates 0 times when every choice already carries
// finish_reason, when choices is empty/absent/non-array, or when the chunk is not an object.
func TestNormalizeOpenAIChatCompletionChunkEventZeroAllocFastPath(t *testing.T) {
	alreadyNormalized := []any{
		decodedJSON(t, `{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`),
		decodedJSON(t, `{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		decodedJSON(t, `{"id":"1","choices":[]}`),
		decodedJSON(t, `{"id":"1","usage":{}}`),
		decodedJSON(t, `{"id":"1","choices":{}}`),
		decodedJSON(t, `42`),
	}
	for i, ev := range alreadyNormalized {
		allocs := testing.AllocsPerRun(100, func() {
			normalizeOpenAIChatCompletionChunkEvent(ev)
		})
		if allocs != 0 {
			t.Errorf("case %d: normalizeOpenAIChatCompletionChunkEvent allocated %.1f times, want 0", i, allocs)
		}
	}

	// Verify the fast decoder sets allChoicesHaveFinishReason accurately without allocations.
	var d openAIChunkDecoder
	_, ok := d.decode(`{"id":"1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
	if !ok || !d.allChoicesHaveFinishReason {
		t.Fatalf("with finish_reason: ok=%v allChoicesHaveFinishReason=%v, want true/true", ok, d.allChoicesHaveFinishReason)
	}
	_, ok = d.decode(`{"id":"1","choices":[{"index":0,"delta":{"content":"hi"}}]}`)
	if !ok || d.allChoicesHaveFinishReason {
		t.Fatalf("without finish_reason: ok=%v allChoicesHaveFinishReason=%v, want true/false", ok, d.allChoicesHaveFinishReason)
	}
	_, ok = d.decode(`{"id":"1","choices":[]}`)
	if !ok || !d.allChoicesHaveFinishReason {
		t.Fatalf("empty choices: ok=%v allChoicesHaveFinishReason=%v, want true/true", ok, d.allChoicesHaveFinishReason)
	}
	_, ok = d.decode(`{"id":"1","choices":[{"index":0,"finish_reason":"stop"},{"index":1,"delta":{"content":"x"}}]}`)
	if !ok || d.allChoicesHaveFinishReason {
		t.Fatalf("mixed choices: ok=%v allChoicesHaveFinishReason=%v, want true/false", ok, d.allChoicesHaveFinishReason)
	}
}

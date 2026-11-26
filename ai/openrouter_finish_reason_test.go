package ai

import (
	"fmt"
	"strings"
	"testing"
)

// Port of Pi main ddf5868d2866394b6b90109ea7151812d85019d5 and 9b911b409c8fcc6e6232ab37a3ffc512331852f1 (#11931, #12407).
func TestOpenRouterNativeFinishReason(t *testing.T) {
	check := func(t *testing.T, newProvider func() Provider, chunks []string, assertFn func(t *testing.T, msg *AssistantMessage)) {
		t.Helper()
		var sse strings.Builder
		for _, chunk := range chunks {
			fmt.Fprintf(&sse, "data: %s\n\n", chunk)
		}
		for _, wire := range []bool{false, true} {
			t.Run(fmt.Sprintf("wire=%v", wire), func(t *testing.T) {
				msg := metadataStreamResult(t, newProvider(), sse.String(), wire)
				assertFn(t, msg)
			})
		}
	}

	openRouter := func() Provider {
		return NewOpenAIProvider(OpenAIConfig{
			Model:      "google/gemini-4-neon",
			ProviderID: "openrouter",
			BaseURL:    "https://openrouter.ai/api/v1",
			APIKey:     "test",
		})
	}

	t.Run("records native reason hidden behind successful finish_reason without changing turn with text", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-1","choices":[{"index":0,"delta":{"content":"Done."},"finish_reason":null}]}`,
			`{"id":"gen-1","choices":[{"index":0,"delta":{},"finish_reason":"stop","native_finish_reason":"MALFORMED_RESPONSE"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.StopReason != StopReasonStop || msg.RawStopReason != "stop.MALFORMED_RESPONSE" || msg.ErrorMessage != "" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("retries a stop after reasoning only when the native reason is not a clean stop", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-8","choices":[{"index":0,"delta":{"reasoning":"Let me think."},"finish_reason":null}]}`,
			`{"id":"gen-8","choices":[{"index":0,"delta":{},"finish_reason":"stop","native_finish_reason":"MALFORMED_RESPONSE"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			wantErr := "OpenRouter returned an empty response (native_finish_reason: MALFORMED_RESPONSE)"
			if msg.StopReason != StopReasonError || msg.RawStopReason != "stop.MALFORMED_RESPONSE" || msg.ErrorMessage != wantErr {
				t.Fatalf("message = %#v", msg)
			}
			if !IsRetryableAssistantError(*msg) {
				t.Fatalf("expected retryable error, got false for %q", msg.ErrorMessage)
			}
		})
	})

	t.Run("retries an empty stop whose native reason is OTHER", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-9","choices":[{"index":0,"delta":{"content":"  "},"finish_reason":null}]}`,
			`{"id":"gen-9","choices":[{"index":0,"delta":{},"finish_reason":"stop","native_finish_reason":"OTHER"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.StopReason != StopReasonError || !IsRetryableAssistantError(*msg) {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("keeps an empty stop whose native reason is a clean stop", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-10","choices":[{"index":0,"delta":{},"finish_reason":"stop","native_finish_reason":"end_turn"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.StopReason != StopReasonStop || msg.RawStopReason != "stop.end_turn" || msg.ErrorMessage != "" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("matches clean stop reasons case-insensitively", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-11","choices":[{"index":0,"delta":{},"finish_reason":"stop","native_finish_reason":"End_Turn"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.StopReason != StopReasonStop || msg.ErrorMessage != "" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("records the native reason of a tool call", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":"{}"}}]},"finish_reason":null}]}`,
			`{"id":"gen-2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls","native_finish_reason":"STOP"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.StopReason != StopReasonToolUse || msg.RawStopReason != "tool_calls.STOP" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("adds the native reason to an error message and keeps it retryable", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-3","choices":[{"index":0,"delta":{},"finish_reason":"error","native_finish_reason":"MALFORMED_FUNCTION_CALL"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			wantErr := "Provider finish_reason: error (native_finish_reason: MALFORMED_FUNCTION_CALL)"
			if msg.StopReason != StopReasonError || msg.RawStopReason != "error.MALFORMED_FUNCTION_CALL" || msg.ErrorMessage != wantErr {
				t.Fatalf("message = %#v", msg)
			}
			if !IsRetryableAssistantError(*msg) {
				t.Fatalf("expected retryable error, got false for %q", msg.ErrorMessage)
			}
		})
	})

	t.Run("keeps the native reason when a trailing chunk repeats finish_reason without it", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-4","choices":[{"index":0,"delta":{},"finish_reason":"error","native_finish_reason":"MALFORMED_FUNCTION_CALL"}]}`,
			`{"id":"gen-4","choices":[{"index":0,"delta":{},"finish_reason":"error"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			wantErr := "Provider finish_reason: error (native_finish_reason: MALFORMED_FUNCTION_CALL)"
			if msg.RawStopReason != "error.MALFORMED_FUNCTION_CALL" || msg.ErrorMessage != wantErr {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("does not repeat a native reason equal to finish_reason", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-5","choices":[{"index":0,"delta":{},"finish_reason":"stop","native_finish_reason":"stop"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.RawStopReason != "stop" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("ignores native_finish_reason from providers other than OpenRouter", func(t *testing.T) {
		nonOR := func() Provider {
			return NewOpenAIProvider(OpenAIConfig{Model: "test-model", ProviderID: "openai", APIKey: "test"})
		}
		check(t, nonOR, []string{
			`{"id":"chatcmpl-6","choices":[{"index":0,"delta":{},"finish_reason":"stop","native_finish_reason":"MALFORMED_RESPONSE"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.RawStopReason != "stop" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("ignores non-string native_finish_reason without failing JSON decoding", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-12","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop","native_finish_reason":123}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.StopReason != StopReasonStop || msg.RawStopReason != "stop" || msg.ErrorMessage != "" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})

	t.Run("does not overwrite RawStopReason when a later chunk has only a different native reason", func(t *testing.T) {
		check(t, openRouter, []string{
			`{"id":"gen-13","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop","native_finish_reason":"end_turn"}]}`,
			`{"id":"gen-13","choices":[{"index":0,"delta":{},"finish_reason":null,"native_finish_reason":"OTHER"}]}`,
		}, func(t *testing.T, msg *AssistantMessage) {
			if msg.StopReason != StopReasonStop || msg.RawStopReason != "stop.end_turn" {
				t.Fatalf("message = %#v", msg)
			}
		})
	})
}

package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Pi anthropic-sse-parsing.test.ts's serving-model transformation case and anthropic-messages.ts:603-605,752-754,801-813: the latest array replaces prior metadata; successful output exposes only type/path/reason with nullish members omitted.
func TestAnthropicInputTransformationsUpstream(t *testing.T) {
	const initial = `[{"type":"thinking_dropped","path":"messages.1.content.0","reason":"prefix_binding_mismatch"}]`
	const final = `[{"type":"thinking_dropped","path":"messages.3.content.0","reason":"model_binding_mismatch"}]`
	for _, tc := range []struct {
		name, initial, final, want string
		failure                    string
	}{
		{"final replaces initial", initial, final, final, ""},
		{"initial retained", initial, "", initial, ""},
		{"empty final clears initial", initial, `[]`, "", ""},
		{"non-array final ignored", initial, `{"ignored":true}`, initial, ""},
		{"null final ignored", initial, `null`, initial, ""},
		{"non-array initial ignored", `false`, "", "", ""},
		{"projection drops extra and nullish fields", `[{"type":"thinking_dropped","path":null,"reason":"","private":"not forwarded"},{}]`, "", `[{"type":"thinking_dropped","reason":""},{}]`, ""},
		{"primitive entries project to empty objects", `[false,1,"text",[]]`, "", `[{},{},{},{}]`, ""},
		{"null entry fails", `[null]`, "", "", "null"},
		{"failure does not publish transformations", initial, final, "", "refusal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				startField, deltaField := "", ""
				if tc.initial != "" {
					startField = `,"input_transformations":` + tc.initial
				}
				if tc.final != "" {
					deltaField = `,"input_transformations":` + tc.final
				}
				stop := "end_turn"
				wantStop := StopReasonStop
				if tc.failure == "refusal" {
					stop = "refusal"
				}
				if tc.failure != "" {
					wantStop = StopReasonError
				}
				sse := fmt.Sprintf("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_transformations\",\"model\":\"claude-fable-5-1\",\"usage\":{\"input_tokens\":12,\"output_tokens\":0}%s}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"input_tokens\":12,\"output_tokens\":5}%s}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", startField, stop, deltaField)
				provider := NewAnthropicProvider(AnthropicConfig{ProviderID: "anthropic", Model: "claude-fable-5-1"}).(*anthropicProvider)
				defer func() {
					if err := provider.Close(); err != nil {
						t.Error(err)
					}
				}()
				builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "anthropic", "claude-fable-5-1")
				wantTimestamp := time.Now().UnixMilli()
				provider.parseAnthropicSSE(t.Context(), strings.NewReader(sse), builder, anthropicStreamNames{})
				result := builder.stream.Result()
				if result.StopReason != wantStop {
					t.Fatalf("stopReason=%s, want %s; error=%s", result.StopReason, wantStop, result.ErrorMessage)
				}
				if tc.failure == "null" && result.ErrorMessage != "Cannot read properties of null (reading 'type')" {
					t.Fatalf("malformed transformation error = %q", result.ErrorMessage)
				}
				encoded, err := json.Marshal(result.Diagnostics)
				if err != nil {
					t.Fatal(err)
				}
				want := "null"
				if tc.want != "" {
					want = fmt.Sprintf(`[{"type":"anthropic_input_transformations","timestamp":%d,"details":{"transformations":%s}}]`, wantTimestamp, tc.want)
				}
				assertShapeJSON(t, encoded, want)
			})
		})
	}
}

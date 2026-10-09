//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// PR-3-5 / RP-004: Pi assigns the explicit empty signature before publishing the redacted placeholder delta (bedrock-converse-stream.ts:663-671).
func TestBedrockRedactedResetPreservesEmptySignature(t *testing.T) {
	result, events := runBedrockEvents(t,
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberSignature{Value: "signed"}}}},
		bedrockRedactedDelta(0, []byte("ciphertext")), indexedBedrockStop(0),
		&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}},
	)
	found := false
	for _, event := range events {
		if delta, ok := event.(ThinkingDeltaEvent); ok && delta.Delta == "[Reasoning redacted]" {
			found = true
			raw, err := json.Marshal(delta.Partial.Content[delta.ContentIndex])
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, raw, `{"type":"thinking","thinking":"[Reasoning redacted]","thinkingSignature":"","redacted":true}`)
		}
	}
	if !found {
		t.Fatal("missing redacted thinking_delta")
	}
	raw, err := json.Marshal(result.Content)
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, raw, `[{"type":"thinking","thinking":"[Reasoning redacted]","thinkingSignature":"Y2lwaGVydGV4dA==","redacted":true}]`)
}

// RP-004: packages/ai/src/api/bedrock-converse-stream.ts:635 creates thinkingSignature="" before publishing thinking_start; finalization does not delete it.
func TestBedrockEmptyThinkingSignaturePresence(t *testing.T) {
	for _, tc := range []struct {
		name, text, signature string
		stopBlock             bool
	}{
		{"unsigned reasoning", "reason", "", true},
		{"unfinished reasoning", "reason", "", false},
		{"empty reasoning", "", "", true},
		{"signed reasoning", "reason", "signed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := []btypes.ConverseStreamOutput{
				&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
				&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberText{Value: tc.text}}}},
			}
			if tc.signature != "" {
				input = append(input, &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberSignature{Value: tc.signature}}}})
			}
			if tc.stopBlock {
				input = append(input, indexedBedrockStop(0))
			}
			input = append(input, &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}})
			result, events := runBedrockEvents(t, input...)
			raw, err := json.Marshal(result.Content)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal([]map[string]any{{"type": "thinking", "thinking": tc.text, "thinkingSignature": tc.signature}})
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, raw, string(want))
			started := false
			for _, event := range events {
				if start, ok := event.(ThinkingStartEvent); ok {
					started = true
					block, err := json.Marshal(start.Partial.Content[start.ContentIndex])
					if err != nil {
						t.Fatal(err)
					}
					assertShapeJSON(t, block, `{"type":"thinking","thinking":"","thinkingSignature":""}`)
				}
			}
			if !started {
				t.Fatal("missing thinking_start")
			}
		})
	}
}

//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// bedrock-converse-stream.ts:1323 decodes with atob and drops invalid persisted signatures.
func TestBedrockRedactedReplayPoisonedSignature(t *testing.T) {
	for _, tc := range []struct {
		signature string
		want      []byte
	}{
		{"", nil}, {"not base64!", nil}, {"YQ", []byte("a")}, {" YQ==\n", []byte("a")}, {"A", nil}, {"YQ=", nil},
	} {
		t.Run(tc.signature, func(t *testing.T) {
			got, err := bedrockAssistantContent([]AssistantContentBlock{ThinkingContent{Redacted: true, ThinkingSignature: tc.signature}, TextContent{Text: "done"}}, "global.openai.gpt-5.6-terra", "")
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if len(got) != 1 {
					t.Fatalf("invalid signature retained: %#v", got)
				}
			} else {
				if len(got) != 2 {
					t.Fatalf("valid signature dropped: %#v", got)
				}
				reasoning := got[0].(*btypes.ContentBlockMemberReasoningContent).Value.(*btypes.ReasoningContentBlockMemberRedactedContent)
				if !bytes.Equal(reasoning.Value, tc.want) {
					t.Fatalf("decoded=%q, want %q", reasoning.Value, tc.want)
				}
			}
		})
	}
}

// bedrock-converse-stream.ts:654 appends signature deltas but never mixes them with encrypted bytes.
func TestBedrockSignatureDeltaAccumulation(t *testing.T) {
	signature := func(value string) btypes.ConverseStreamOutput {
		return &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberSignature{Value: value}}}}
	}
	stop := &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}}
	result, _ := runBedrockEvents(t, signature("first"), signature("second"), stop)
	if len(result.Content) != 1 || result.Content[0].(ThinkingContent).ThinkingSignature != "firstsecond" {
		t.Fatalf("content=%#v", result.Content)
	}
	result, _ = runBedrockEvents(t, signature("discarded"), bedrockRedactedDelta(0, []byte("encrypted")), signature("ignored"), stop)
	if len(result.Content) != 1 || result.Content[0].(ThinkingContent).ThinkingSignature != base64.StdEncoding.EncodeToString([]byte("encrypted")) {
		t.Fatalf("content=%#v", result.Content)
	}
}

// bedrock-converse-stream.ts:697 finalizes encrypted bytes on error as well as success.
func TestBedrockRedactedFinalizedOnProviderError(t *testing.T) {
	data := upstreamBedrockRedactedBytes(t)
	result, _ := runBedrockEvents(t, bedrockRedactedDelta(0, data), &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonGuardrailIntervened}})
	if result.StopReason != StopReasonError {
		t.Fatalf("result=%+v", result)
	}
	assertBedrockRedactedThinking(t, result)
}

func BenchmarkBedrockRedacted64KiB(b *testing.B) {
	data := bytes.Repeat([]byte("r"), 64*1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		builder := newAssistantStreamBuilder(context.Background(), APIBedrockConverseStream, "amazon-bedrock", "model")
		provider := &BedrockProvider{}
		blocks := map[int32]*activeBlock{}
		for offset := 0; offset < len(data); offset += 1024 {
			event := bedrockRedactedDelta(0, data[offset:offset+1024]).(*btypes.ConverseStreamOutputMemberContentBlockDelta)
			provider.handleBedrockDelta(event.Value, blocks, builder)
		}
		flushBedrockRedactedContent(blocks[0], builder)
		builder.done(StopReasonStop, nil, "")
		result := builder.stream.Result()
		if len(result.Content[0].(ThinkingContent).ThinkingSignature) != base64.StdEncoding.EncodedLen(len(data)) {
			b.Fatal("incomplete signature")
		}
	}
}

//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// The Bedrock parser must settle on any event sequence: dropped, duplicated and reordered events, wrong or missing block indexes, and
// missing members (the SDK delivers typed events, so these are the corruptions its decoder can pass on).
func TestBedrockParserSettlesOnMutatedEventSequences(t *testing.T) {
	seed := func() []btypes.ConverseStreamOutput {
		return []btypes.ConverseStreamOutput{
			&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
			&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberText{Value: "hm"}}}},
			&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
			&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(1), Delta: &btypes.ContentBlockDeltaMemberText{Value: "answer"}}},
			&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)}},
			&btypes.ConverseStreamOutputMemberContentBlockStart{Value: btypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(2), Start: &btypes.ContentBlockStartMemberToolUse{Value: btypes.ToolUseBlockStart{ToolUseId: aws.String("call-1"), Name: aws.String("read")}}}},
			&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(2), Delta: &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: aws.String(`{"path":"a"}`)}}}},
			&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(2)}},
			&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonToolUse}},
			&btypes.ConverseStreamOutputMemberMetadata{Value: btypes.ConverseStreamMetadataEvent{Usage: &btypes.TokenUsage{InputTokens: aws.Int32(10), OutputTokens: aws.Int32(5)}}},
		}
	}
	r := rand.New(rand.NewSource(6))
	for i := range 4000 {
		events := seed()
		for range 1 + r.Intn(3) {
			if len(events) == 0 {
				break
			}
			at := r.Intn(len(events))
			switch r.Intn(7) {
			case 0:
				events = append(events[:at], events[at+1:]...)
			case 1:
				events = append(events[:at+1], events[at:]...)
			case 2:
				other := r.Intn(len(events))
				events[at], events[other] = events[other], events[at]
			case 3:
				events = events[:at]
			case 4:
				switch e := events[at].(type) {
				case *btypes.ConverseStreamOutputMemberContentBlockDelta:
					e.Value.ContentBlockIndex = aws.Int32(int32(r.Intn(5) - 1))
				case *btypes.ConverseStreamOutputMemberContentBlockStop:
					e.Value.ContentBlockIndex = aws.Int32(int32(r.Intn(5) - 1))
				case *btypes.ConverseStreamOutputMemberContentBlockStart:
					e.Value.ContentBlockIndex = nil
				}
			case 5:
				switch e := events[at].(type) {
				case *btypes.ConverseStreamOutputMemberContentBlockDelta:
					e.Value.Delta = nil
				case *btypes.ConverseStreamOutputMemberContentBlockStart:
					e.Value.Start = nil
				case *btypes.ConverseStreamOutputMemberMetadata:
					e.Value.Usage = nil
				}
			case 6:
				// A user message start is the one event the parser rejects (bedrock-converse-stream.ts:300-303, the messageStart role check), so the failure path runs.
				if e, ok := events[at].(*btypes.ConverseStreamOutputMemberMessageStart); ok {
					e.Value.Role = btypes.ConversationRoleUser
				}
			}
		}
		done := make(chan string, 1)
		go func() {
			defer func() {
				if e := recover(); e != nil {
					done <- fmt.Sprint("PANIC ", e)
				}
			}()
			input := make(chan btypes.ConverseStreamOutput, len(events))
			for _, event := range events {
				input <- event
			}
			close(input)
			builder := newAssistantStreamBuilder(context.Background(), APIBedrockConverseStream, "amazon-bedrock", "model")
			// The parser runs on this goroutine so that a panic in it is recovered above and reported with its iteration; on its own goroutine a
			// panic would end the whole test binary.
			(&BedrockProvider{}).parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: input}, builder, "")
			if m := builder.stream.Result(); m.StopReason == "" || m.StopReason == StopReasonPending {
				done <- "PENDING " + string(m.StopReason)
				return
			}
			done <- ""
		}()
		select {
		case res := <-done:
			if res != "" {
				t.Fatalf("iter %d: %s (%d events)", i, res, len(events))
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("iter %d HANG (%d events)", i, len(events))
		}
	}
}

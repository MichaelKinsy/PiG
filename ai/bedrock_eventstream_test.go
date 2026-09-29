package ai

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

// golden.json is the output of testdata/bedrock-eventstream/probe.mjs: what Pi's real AWS SDK and @smithy/core event-stream stack yields or throws for each byte sequence, recorded on Node 24.19.0. Node 26.7.0 prints the same file.
type bedrockEventStreamGolden struct {
	Node  string `json:"node"`
	Cases map[string]struct {
		Chunks  []string `json:"chunks"`
		Results []struct {
			Item  map[string]any `json:"item"`
			Error *struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"results"`
	} `json:"cases"`
}

type bedrockReplayResult struct {
	item     map[string]any
	errName  string
	errMsg   string
	modeled  bool
	hasError bool
}

// replayBedrockEventStream runs bytes through PiG's framing, decoding and unmarshalling until the first thrown error, as the async generator chain stops at its first rejection.
func replayBedrockEventStream(chunks [][]byte) []bedrockReplayResult {
	var results []bedrockReplayResult
	fail := func(err error) []bedrockReplayResult {
		result := bedrockReplayResult{hasError: true, errMsg: err.Error()}
		if apiError, ok := errors.AsType[smithy.APIError](err); ok {
			result.errName, result.errMsg, result.modeled = apiError.ErrorCode(), apiError.ErrorMessage(), true
		}
		return append(results, result)
	}
	var chunker bedrockMessageChunker
	for _, chunk := range chunks {
		messages, feedErr := chunker.feed(chunk)
		for _, raw := range messages {
			message, err := decodeBedrockEventMessage(raw)
			if err != nil {
				return fail(err)
			}
			item, err := bedrockEventDeserializer(message)
			if err != nil {
				return fail(err)
			}
			if item.event != nil {
				results = append(results, bedrockReplayResult{item: projectBedrockItem(item)})
			}
		}
		if feedErr != nil {
			return fail(feedErr)
		}
	}
	if err := chunker.finish(); err != nil {
		return fail(err)
	}
	return results
}

// projectBedrockItem keeps the members Pi's loop reads (bedrock-converse-stream.ts:296-330, handleContentBlock*, handleMetadata) in the shape Node prints them.
func projectBedrockItem(item bedrockStreamItem) map[string]any {
	compact := func(fields map[string]any) map[string]any {
		for key, value := range fields {
			if value == nil {
				delete(fields, key)
			}
		}
		return fields
	}
	number := func(value *int32) any {
		if value == nil {
			return nil
		}
		return float64(*value)
	}
	switch e := item.event.(type) {
	case *btypes.ConverseStreamOutputMemberMessageStart:
		return map[string]any{"messageStart": compact(map[string]any{"role": string(e.Value.Role)})}
	case *btypes.ConverseStreamOutputMemberContentBlockStart:
		fields := map[string]any{"contentBlockIndex": number(e.Value.ContentBlockIndex)}
		if start, ok := e.Value.Start.(*btypes.ContentBlockStartMemberToolUse); ok {
			fields["start"] = map[string]any{"toolUse": compact(map[string]any{"toolUseId": aws.ToString(start.Value.ToolUseId), "name": aws.ToString(start.Value.Name)})}
		}
		return map[string]any{"contentBlockStart": compact(fields)}
	case *btypes.ConverseStreamOutputMemberContentBlockDelta:
		fields := map[string]any{"contentBlockIndex": number(e.Value.ContentBlockIndex)}
		switch delta := e.Value.Delta.(type) {
		case *btypes.ContentBlockDeltaMemberText:
			fields["delta"] = map[string]any{"text": delta.Value}
		case *btypes.ContentBlockDeltaMemberToolUse:
			fields["delta"] = map[string]any{"toolUse": map[string]any{"input": aws.ToString(delta.Value.Input)}}
		case *btypes.ContentBlockDeltaMemberReasoningContent:
			reasoning := map[string]any{}
			if item.reasoning.text != nil {
				reasoning["text"] = *item.reasoning.text
			}
			if item.reasoning.signature != nil {
				reasoning["signature"] = *item.reasoning.signature
			}
			if item.reasoning.redacted != nil {
				reasoning["redactedContent"] = slices.Collect(func(yield func(any) bool) {
					for _, b := range item.reasoning.redacted {
						if !yield(float64(b)) {
							return
						}
					}
				})
			}
			fields["delta"] = map[string]any{"reasoningContent": reasoning}
		}
		return map[string]any{"contentBlockDelta": compact(fields)}
	case *btypes.ConverseStreamOutputMemberContentBlockStop:
		return map[string]any{"contentBlockStop": compact(map[string]any{"contentBlockIndex": number(e.Value.ContentBlockIndex)})}
	case *btypes.ConverseStreamOutputMemberMessageStop:
		return map[string]any{"messageStop": compact(map[string]any{"stopReason": string(e.Value.StopReason)})}
	case *btypes.ConverseStreamOutputMemberMetadata:
		u := e.Value.Usage
		if u == nil {
			return map[string]any{"metadata": map[string]any{}}
		}
		usage := compact(map[string]any{
			"inputTokens": number(u.InputTokens), "outputTokens": number(u.OutputTokens), "totalTokens": number(u.TotalTokens),
			"cacheReadInputTokens": number(u.CacheReadInputTokens), "cacheWriteInputTokens": number(u.CacheWriteInputTokens),
		})
		if u.CacheDetails != nil {
			details := []any{}
			for _, detail := range u.CacheDetails {
				details = append(details, compact(map[string]any{"ttl": string(detail.Ttl), "inputTokens": number(detail.InputTokens)}))
			}
			usage["cacheDetails"] = details
		}
		return map[string]any{"metadata": map[string]any{"usage": usage}}
	}
	return nil
}

// projectNodeItem reduces Node's item to the same projection: it drops members Pi never reads and converts a Uint8Array's JSON object form to a number list.
func projectNodeItem(item map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range item {
		fields, _ := value.(map[string]any)
		switch key {
		case "metadata":
			projected := map[string]any{}
			if usage, ok := fields["usage"]; ok {
				projected["usage"] = usage
			}
			out[key] = projected
		case "contentBlockDelta":
			if delta, ok := fields["delta"].(map[string]any); ok {
				if reasoning, ok := delta["reasoningContent"].(map[string]any); ok {
					if redacted, ok := reasoning["redactedContent"].(map[string]any); ok {
						bytes := make([]any, len(redacted))
						for index, b := range redacted {
							var at int
							if _, err := json.Number(index).Int64(); err == nil {
								n, _ := json.Number(index).Int64()
								at = int(n)
							}
							bytes[at] = b
						}
						reasoning["redactedContent"] = bytes
					}
				}
			}
			out[key] = fields
		default:
			out[key] = fields
		}
	}
	return out
}

func TestBedrockEventStreamMatchesNode(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "bedrock-eventstream", "golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden bedrockEventStreamGolden
	if err := json.Unmarshal(content, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Cases) == 0 {
		t.Fatal("golden has no cases")
	}
	for name, tc := range golden.Cases {
		t.Run(name, func(t *testing.T) {
			var chunks [][]byte
			for _, encoded := range tc.Chunks {
				chunk, err := hex.DecodeString(encoded)
				if err != nil {
					t.Fatal(err)
				}
				chunks = append(chunks, chunk)
			}
			got := replayBedrockEventStream(chunks)
			if len(got) != len(tc.Results) {
				t.Fatalf("results: got %d want %d\n got %#v\nwant %#v", len(got), len(tc.Results), got, tc.Results)
			}
			for i, want := range tc.Results {
				switch {
				case want.Error != nil:
					if !got[i].hasError {
						t.Fatalf("result %d: got item %v, want error %s: %s", i, got[i].item, want.Error.Name, want.Error.Message)
					}
					if got[i].errMsg != want.Error.Message {
						t.Fatalf("result %d error message: got %q want %q", i, got[i].errMsg, want.Error.Message)
					}
					if got[i].modeled && got[i].errName != want.Error.Name {
						t.Fatalf("result %d error name: got %q want %q", i, got[i].errName, want.Error.Name)
					}
				default:
					if got[i].hasError {
						t.Fatalf("result %d: got error %q, want item %v", i, got[i].errMsg, want.Item)
					}
					if wantItem := projectNodeItem(want.Item); !reflect.DeepEqual(got[i].item, wantItem) {
						t.Fatalf("result %d item\n got %#v\nwant %#v", i, got[i].item, wantItem)
					}
				}
			}
		})
	}
}

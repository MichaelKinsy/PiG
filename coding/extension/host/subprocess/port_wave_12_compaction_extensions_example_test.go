//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave12CompactionExtensionsExample(t *testing.T) {
	// packages/coding-agent/test/compaction-extensions-example.test.ts:16
	t.Run("custom compaction example should type-check correctly", func(t *testing.T) {
		isolateExampleHost(t)
		ext := loadExampleWithActions(t, filepath.Join("testdata", "port-wave-12", "compaction-documentation.ts"), &HostCallbacks{})
		handler := requireExampleHandler(t, ext, "session_before_compact")
		// In addition to upstream's callable-factory check, exercise its field assertions through the host event projection.
		result, err := handler(map[string]any{
			"type": "session_before_compact",
			"preparation": map[string]any{
				"messagesToSummarize": []any{map[string]any{"role": "user", "content": "remember this", "timestamp": 1}},
				"turnPrefixMessages":  []any{}, "tokensBefore": 42, "firstKeptEntryId": "entry-1", "isSplitTurn": false,
			},
			"branchEntries": []any{},
		}, t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertExampleCompaction(t, result, "User requests:\n- remember this")
	})

	// packages/coding-agent/test/compaction-extensions-example.test.ts:57
	t.Run("custom compaction example dispatches through modelRegistry.complete", func(t *testing.T) {
		isolateExampleHost(t)
		dir := t.TempDir()
		// Preserve the actual example, replacing only the two imports upstream mocks (:8-11).
		source, err := os.ReadFile(upstreamExamplePath(t, "custom-compaction"))
		if err != nil {
			t.Fatal(err)
		}
		const original = `import { convertToLlm, serializeConversation } from "@earendil-works/pi-coding-agent";`
		if strings.Count(string(source), original) != 1 {
			t.Fatal("custom-compaction conversation import changed")
		}
		write(t, filepath.Join(dir, "custom-compaction.ts"), strings.Replace(string(source), original, `import { convertToLlm, serializeConversation } from "./compaction-conversation.mjs";`, 1))
		for _, name := range []string{"compaction-custom.mjs", "compaction-conversation.mjs"} {
			data, err := os.ReadFile(filepath.Join("testdata", "port-wave-12", name))
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(dir, name), string(data))
		}
		var calls atomic.Int64
		ext := loadExampleWithActions(t, filepath.Join(dir, "compaction-custom.mjs"), &HostCallbacks{
			StreamModel: func(_ context.Context, model, request map[string]any) (*ai.AssistantMessageEventStream, error) {
				calls.Add(1)
				if model["provider"] != "example-custom" || model["api"] != "example-custom-api" || model["id"] != "summary-model" {
					t.Errorf("model identity = %#v", model)
				}
				if request["maxTokens"] != float64(8192) {
					t.Errorf("maxTokens = %#v, want 8192", request["maxTokens"])
				}
				if _, exists := request["apiKey"]; exists {
					t.Errorf("completion unexpectedly supplies apiKey: %#v", request)
				}
				stream := ai.NewAssistantMessageEventStream()
				final := &ai.AssistantMessage{
					Content:  []ai.AssistantContentBlock{ai.TextContent{Text: "custom provider summary"}},
					Provider: "example-custom", API: "example-custom-api", Model: "summary-model", StopReason: ai.StopReasonStop,
					Usage:     ai.Usage{Input: 1, Output: 2, CacheRead: 0, CacheWrite: 0, TotalTokens: 3, Cost: ai.UsageCost{}},
					Timestamp: time.Now().UnixMilli(),
				}
				if err := stream.Push(ai.StartEvent{Partial: final}); err != nil {
					return nil, err
				}
				if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}); err != nil {
					return nil, err
				}
				return stream, nil
			},
		})
		handler := requireExampleHandler(t, ext, "session_before_compact")
		result, err := handler(map[string]any{
			"type": "session_before_compact",
			"preparation": map[string]any{
				"messagesToSummarize": []any{map[string]any{
					"role": "user", "content": []any{map[string]any{"type": "text", "text": "please remember this"}}, "timestamp": time.Now().UnixMilli(),
				}},
				"turnPrefixMessages": []any{}, "tokensBefore": 42, "firstKeptEntryId": "entry-1",
			},
			"branchEntries": []any{},
		}, t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatalf("modelRegistry.complete dispatch count = %d, want 1", calls.Load())
		}
		assertExampleCompaction(t, result, "custom provider summary")
	})

	// packages/coding-agent/test/compaction-extensions-example.test.ts:136
	t.Run("compact event should have correct fields", func(t *testing.T) {
		isolateExampleHost(t)
		ext := loadExampleWithActions(t, filepath.Join("testdata", "port-wave-12", "compaction-documentation.ts"), &HostCallbacks{})
		handler := requireExampleHandler(t, ext, "session_compact")
		if _, err := handler(map[string]any{
			"type": "session_compact", "fromExtension": true,
			"compactionEntry": map[string]any{"type": "compaction", "summary": "summary", "tokensBefore": 42},
		}, t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func assertExampleCompaction(t *testing.T, result any, summary string) {
	t.Helper()
	raw, ok := result.(json.RawMessage)
	if !ok {
		t.Fatalf("compaction result = %#v, want wire result", result)
	}
	var got struct {
		Compaction struct {
			Summary          string `json:"summary"`
			FirstKeptEntryID string `json:"firstKeptEntryId"`
			TokensBefore     int    `json:"tokensBefore"`
		} `json:"compaction"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Compaction.Summary != summary || got.Compaction.FirstKeptEntryID != "entry-1" || got.Compaction.TokensBefore != 42 {
		t.Fatalf("compaction = %s, want summary %q / entry-1 / 42", raw, summary)
	}
}

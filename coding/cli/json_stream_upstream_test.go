package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

type upstreamJSONUpdate struct {
	wire       map[string]any
	usage      ai.Usage
	bytes      int
	runtimeCWD string
}

func collectUpstreamJSONUpdates(t *testing.T, prompt string, responses []ai.FauxResponse, extraTools ...agent.AgentTool) []upstreamJSONUpdate {
	t.Helper()
	// Fixed token granularity makes the size ratio deterministic without changing either input text.
	provider := ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: new(4), Max: new(4)}})
	provider.SetResponses(fauxSteps(responses...))
	var updates []upstreamJSONUpdate
	host := printModeTestHost(t, provider, extraTools...)
	result := runPrintModeForTest(t, host, printModeOptions{
		Mode: "json", InitialMessage: prompt,
		convertEvent: func(event agent.AgentEvent) ([]any, error) {
			values, err := rpcAgentEvent(event)
			if err != nil {
				return nil, err
			}
			if update, ok := event.(agent.MessageUpdateEvent); ok {
				if update.Message.Assistant == nil || update.Message.Assistant.Usage == nil {
					return nil, fmt.Errorf("internal update lacks assistant message or usage")
				}
				raw, err := json.Marshal(update.AssistantMessageEvent)
				if err != nil {
					return nil, err
				}
				var inner map[string]any
				if err := json.Unmarshal(raw, &inner); err != nil {
					return nil, err
				}
				if _, present := inner["partial"]; !present {
					return nil, fmt.Errorf("internal update lacks partial")
				}
				if len(values) != 1 {
					return nil, fmt.Errorf("message update projected to %d events", len(values))
				}
				encoded, err := json.Marshal(values[0])
				if err != nil {
					return nil, err
				}
				var wire map[string]any
				if err := json.Unmarshal(encoded, &wire); err != nil {
					return nil, err
				}
				updates = append(updates, upstreamJSONUpdate{wire: wire, usage: *update.Message.Assistant.Usage, bytes: len(encoded), runtimeCWD: host.Services.CWD()})
			}
			return values, nil
		},
	})
	if result.err != nil || result.stderr != "" {
		t.Fatalf("JSON mode result=%+v", result)
	}
	if len(updates) == 0 {
		t.Fatal("no message updates")
	}
	return updates
}

func assertDeltaOnlyUpstream(t *testing.T, wire map[string]any) {
	t.Helper()
	if _, present := wire["message"]; present {
		t.Error("wire update includes cumulative message")
	}
	inner, ok := wire["assistantMessageEvent"].(map[string]any)
	if !ok {
		t.Fatal("assistant event missing")
	}
	if _, present := inner["partial"]; present {
		t.Error("wire assistant event includes cumulative partial")
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7290-json-stream-linear.test.ts:37
func TestJSONUpdatesEmitDeltaOnlyAndScaleLinearlyUpstream(t *testing.T) {
	measure := func(size int) int {
		total := 0
		for _, update := range collectUpstreamJSONUpdates(t, "respond", []ai.FauxResponse{fauxTextResponse(strings.Repeat("x", size))}) {
			assertDeltaOnlyUpstream(t, update.wire)
			total += update.bytes
		}
		return total
	}
	small, large := measure(2000), measure(4000)
	if large <= small || float64(large)/float64(small) >= 2.2 {
		t.Fatalf("wire size small=%d large=%d ratio=%f", small, large, float64(large)/float64(small))
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7925-toolcall-start-metadata.test.ts:13
func TestJSONToolCallStartIncludesIDAndNameWithoutSnapshotsUpstream(t *testing.T) {
	updates := collectUpstreamJSONUpdates(t, "write a file", []ai.FauxResponse{
		{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "output.txt", "content": strings.Repeat("x", 100)}, &ai.FauxToolCallOptions{ID: "call_7925"})}, StopReason: "toolUse"},
		fauxTextResponse("done"),
	}, &tools.WriteTool{})
	for _, update := range updates {
		inner, _ := update.wire["assistantMessageEvent"].(map[string]any)
		if inner["type"] != "toolcall_start" {
			continue
		}
		want := map[string]any{"type": "message_update", "usage": decodeRPCEvent(t, update.usage), "assistantMessageEvent": map[string]any{"type": "toolcall_start", "contentIndex": float64(0), "id": "call_7925", "toolName": "write"}}
		if !reflect.DeepEqual(update.wire, want) {
			t.Fatalf("wire update=%#v, want %#v", update.wire, want)
		}
		content, err := os.ReadFile(filepath.Join(update.runtimeCWD, "output.txt"))
		if err != nil || string(content) != strings.Repeat("x", 100) {
			t.Fatalf("tool output=%q error=%v", content, err)
		}
		return
	}
	t.Fatal("expected toolcall_start assistant update")
}

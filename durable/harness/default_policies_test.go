package harness

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/harness/agent.ts:19-37 DEFAULT_RETRY_POLICY, DEFAULT_COMPACTION_POLICY and DEFAULT_PROGRESS_POLICY: the built-in values, which
// ResolveSettings applies when the harness settings give none.
func TestDefaultPoliciesMatchPi(t *testing.T) {
	if want := (durable.ConversationRetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxAgentDelayMs: new(60000)}); !reflect.DeepEqual(DefaultRetryPolicy, want) {
		t.Errorf("DefaultRetryPolicy = %+v, want %+v", DefaultRetryPolicy, want)
	}
	if want := (durable.CompactionPolicy{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000, BackgroundTokens: 32768}); !reflect.DeepEqual(DefaultCompactionPolicy, want) {
		t.Errorf("DefaultCompactionPolicy = %+v, want %+v", DefaultCompactionPolicy, want)
	}
	if want := (durable.ProgressPolicy{PartialIntervalMs: 100, OutputIntervalMs: 100}); DefaultProgressPolicy != want {
		t.Errorf("DefaultProgressPolicy = %+v, want %+v", DefaultProgressPolicy, want)
	}
	resolved := ResolveSettings(nil)
	if !reflect.DeepEqual(resolved.Retry, DefaultRetryPolicy) || !reflect.DeepEqual(resolved.Compaction, DefaultCompactionPolicy) || resolved.Progress != DefaultProgressPolicy {
		t.Errorf("ResolveSettings(nil) = %+v, want the default policies", resolved)
	}
}

// TestDefineToolAndExtensionKeepTheirDefinition: defineTool and defineExtension return their argument (define.ts); the Go
// registry compares by pointer, so each call returns a distinct pointer to an equal definition.
func TestDefineToolAndExtensionKeepTheirDefinition(t *testing.T) {
	tool := durable.ToolRegistration{Replay: durable.ToolReplay("safe")}
	first, second := DefineTool(tool), DefineTool(tool)
	if first == second || first.Replay != "safe" {
		t.Errorf("DefineTool pointers %p %p, replay %q", first, second, first.Replay)
	}
	extension := durable.Extension{Name: "x", Tools: []*durable.ToolRegistration{first}}
	define := DefineExtension // a function value keeps the wrapper itself under test; go fix inlines direct calls
	got := define(extension)
	if got.Name != "x" || len(got.Tools) != 1 || got.Tools[0] != first {
		t.Errorf("DefineExtension = %+v", got)
	}
}

// TestCheckpointAndRequestWireShapes pins the JSON of CompactionCheckpoint (select has no request members; summarize and
// retry spread the SummaryRequest and retry adds until; compaction.ts:46-49), SummaryRequest, ToolTaskInput,
// ToolTaskResult and GenerationResult.
func TestCheckpointAndRequestWireShapes(t *testing.T) {
	request := &SummaryRequest{Attempt: 2, Model: durable.ModelRef{}, MaxTokens: 100, Tail: 9, FirstKept: 4}
	until := 1234.0
	control := durable.ToolControl{}
	for name, c := range map[string]struct {
		value any
		want  string
	}{
		"select":    {CompactionCheckpoint{Phase: "select"}, `{"phase":"select"}`},
		"summarize": {CompactionCheckpoint{Phase: "summarize", SummaryRequest: request}, ""},
		"retry":     {CompactionCheckpoint{Phase: "retry", SummaryRequest: request, Until: &until}, ""},
		"input":     {ToolTaskInput{Kind: "model", Assistant: 5, CallId: "call_1"}, `{"kind":"model","assistant":5,"callId":"call_1"}`},
		"result":    {ToolTaskResult{Kind: "model", EntryId: 6}, `{"kind":"model","entryId":6}`},
		"control":   {ToolTaskResult{Kind: "model", EntryId: 6, Control: &control}, ""},
		"generated": {GenerationResult{EntryId: 7}, `{"entryId":7}`},
	} {
		raw, err := json.Marshal(c.value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.want != "" && string(raw) != c.want {
			t.Errorf("%s = %s, want %s", name, raw, c.want)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		switch name {
		case "summarize", "retry":
			for _, k := range []string{"phase", "attempt", "model", "thinkingLevel", "streamOptions", "maxTokens", "tail", "firstKept"} {
				if _, ok := keys[k]; !ok {
					t.Errorf("%s lacks %q: %s", name, k, raw)
				}
			}
			if _, ok := keys["until"]; ok != (name == "retry") {
				t.Errorf("%s until presence = %v: %s", name, ok, raw)
			}
		case "control":
			if _, ok := keys["control"]; !ok {
				t.Errorf("result lost its control: %s", raw)
			}
		}
	}
}

package harness

// pi: packages/durable/src/harness/compaction.ts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// tool.ts:33,40 ToolTaskInput { assistant, callId } and ToolTaskResult { entryId, control? }: the persisted task input and result keep those member names, and control is absent until a tool requests one.
func TestToolTaskInputAndResultWireShapes(t *testing.T) {
	if got := mustJSON(t, ToolTaskInput{Assistant: 1, CallId: "call-1"}); got != `{"assistant":1,"callId":"call-1"}` {
		t.Fatalf("input = %s", got)
	}
	if got := mustJSON(t, ToolTaskResult{EntryId: 2}); got != `{"entryId":2}` {
		t.Fatalf("result without control = %s", got)
	}
	if got := mustJSON(t, ToolTaskResult{EntryId: 2, Control: &durable.ToolControl{Terminate: true}}); got != `{"entryId":2,"control":{"terminate":true}}` {
		t.Fatalf("result with control = %s", got)
	}
	var decoded ToolTaskResult
	if err := json.Unmarshal([]byte(`{"entryId":3,"control":{"handoff":"b"}}`), &decoded); err != nil || decoded.EntryId != 3 || decoded.Control == nil || decoded.Control.Handoff == nil || *decoded.Control.Handoff != "b" {
		t.Fatalf("decoded = %+v (%v)", decoded, err)
	}
}

// generation.ts:92 GenerationResult { entryId }.
func TestGenerationResultWireShape(t *testing.T) {
	if got := mustJSON(t, GenerationResult{EntryId: 9}); got != `{"entryId":9}` {
		t.Fatalf("result = %s", got)
	}
}

// compaction.ts:34-49: the select checkpoint carries only its phase; summarize adds the pinned SummaryRequest members; retry adds `until` as well. The CompactionReason values are types.ts:403 "manual" | "threshold" | "overflow".
func TestCompactionCheckpointPhasesCarryTheSummaryRequest(t *testing.T) {
	if got := mustJSON(t, CompactionCheckpoint{Phase: "select"}); got != `{"phase":"select"}` {
		t.Fatalf("select = %s", got)
	}
	request := &SummaryRequest{Attempt: 2, Model: durable.ModelRef{Provider: "p", ModelId: "m"}, ThinkingLevel: "high", MaxTokens: 512, Tail: 5, FirstKept: 3}
	summarize := mustJSON(t, CompactionCheckpoint{Phase: "summarize", SummaryRequest: request})
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(summarize), &members); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"phase", "attempt", "model", "thinkingLevel", "streamOptions", "maxTokens", "tail", "firstKept"} {
		if _, ok := members[key]; !ok {
			t.Errorf("summarize lacks %q: %s", key, summarize)
		}
	}
	if _, ok := members["until"]; ok {
		t.Errorf("summarize carries until: %s", summarize)
	}
	until := 1234.0
	retry := mustJSON(t, CompactionCheckpoint{Phase: "retry", SummaryRequest: request, Until: &until})
	var retryMembers map[string]json.RawMessage
	if err := json.Unmarshal([]byte(retry), &retryMembers); err != nil || string(retryMembers["until"]) != "1234" || string(retryMembers["tail"]) != `5` {
		t.Fatalf("retry = %s (%v)", retry, err)
	}
	if got := mustJSON(t, CompactionInput{Reason: durable.CompactionManual}); got != `{"reason":"manual"}` {
		t.Fatalf("input = %s", got)
	}
	for reason, want := range map[durable.CompactionReason]string{durable.CompactionManual: "manual", durable.CompactionThreshold: "threshold", durable.CompactionOverflow: "overflow"} {
		if string(reason) != want {
			t.Errorf("reason %q, want %q", reason, want)
		}
	}
}

// agent.ts:18-30 pins the built-in policies.
func TestBuiltInRetryAndCompactionPolicies(t *testing.T) {
	if DefaultRetryPolicy.Enabled != true || DefaultRetryPolicy.MaxRetries != 3 || DefaultRetryPolicy.BaseDelayMs != 2000 || DefaultRetryPolicy.MaxAgentDelayMs == nil || *DefaultRetryPolicy.MaxAgentDelayMs != 60000 {
		t.Fatalf("retry = %+v", DefaultRetryPolicy)
	}
	want := durable.CompactionPolicy{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000, BackgroundTokens: 32768}
	if DefaultCompactionPolicy != want {
		t.Fatalf("compaction = %+v, want %+v", DefaultCompactionPolicy, want)
	}
}

// define.ts:5-17 defineExtension and defineTool return their argument: the registration keeps every member the caller set.
func TestDefineExtensionAndDefineToolKeepTheirArgument(t *testing.T) {
	registration := durable.ToolRegistration{Replay: durable.ReplaySafe, Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{}, nil
	}}
	tool := DefineTool(registration)
	tool.Name = "t"
	if tool.Replay != durable.ReplaySafe || tool.Execute == nil || tool.Name != "t" {
		t.Fatalf("tool = %+v", tool)
	}
	if again := DefineTool(*tool); again == tool {
		t.Fatal("a defined tool must be its own registration, not a shared pointer to the argument")
	}
	extension := new(durable.Extension{Name: "ext", Tools: []*durable.ToolRegistration{tool}})
	if extension.Name != "ext" || len(extension.Tools) != 1 || extension.Tools[0] != tool {
		t.Fatalf("extension = %+v", extension)
	}
}

// types.ts:349 ConversationInit runs in the creating commit with the new conversation's id.
func TestConversationInitReceivesTheTransactionAndTheNewConversationId(t *testing.T) {
	var seen durable.ConversationId
	var init ConversationInit = func(tx durable.Tx, id durable.ConversationId) error {
		seen = id
		return nil
	}
	harness, _ := openHarness(t, newControlledStorage(), []string{"read"})
	conversation := mustRoot(t, harness, &RootOptions{Init: init})
	if seen == 0 || seen != conversation.Id() {
		t.Fatalf("init saw %d, conversation %d", seen, conversation.Id())
	}
}

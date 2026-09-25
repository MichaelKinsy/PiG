package ai

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func TestTestFauxParityOracleToolCallIDs(t *testing.T) {
	// Exercise Pi's actual extension with the installed pinned pi-ai event stream, not a source-text approximation of the counter.
	cmd := exec.CommandContext(t.Context(), "node", "--test", filepath.Join("..", "parity", "testdata", "test-faux-provider.test.mjs"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("parity oracle ID contract: %v\n%s", err, output)
	}
}

// The parity backend must allocate IDs per call, not per response. Real Pi providers preserve each server-issued ID (and fauxToolCall generates one when omitted).
func TestTestFauxToolCallIDs(t *testing.T) {
	p := &TestFauxProvider{}
	for _, tc := range []struct {
		session string
		prompt  string
		want    []string
	}{
		{"a", "What is 20+22?", nil},
		{"a", "Run: expr 20 + 22", []string{"call_test_faux_1"}},
		{"a", "Run: parallel reads", []string{"call_test_faux_2", "call_test_faux_3"}},
		{"b", "Run: expr 20 + 22", []string{"call_test_faux_1"}},
		{"a", "Run: expr 20 + 22", []string{"call_test_faux_4"}},
		{"", "Run: expr 20 + 22", []string{"call_test_faux_1"}},
		{"", "Run: expr 20 + 22", []string{"call_test_faux_2"}},
	} {
		t.Run(tc.session+"/"+tc.prompt, func(t *testing.T) {
			stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(tc.prompt)}}}), StreamOptions{SessionID: tc.session})
			if err != nil {
				t.Fatal(err)
			}
			var ended []string
			for event := range stream.Events(t.Context()) {
				if event, ok := event.(ToolCallEndEvent); ok {
					ended = append(ended, event.ToolCall.ID)
				}
			}
			result := stream.Result()
			if result.StopReason == StopReasonError {
				t.Fatal(result.ErrorMessage)
			}
			var ids []string
			for _, block := range result.Content {
				if call, ok := block.(ToolCall); ok {
					ids = append(ids, call.ID)
				}
			}
			if !slices.Equal(ids, tc.want) || !slices.Equal(ended, tc.want) {
				t.Errorf("result IDs %v, toolcall_end IDs %v; want %v", ids, ended, tc.want)
			}
		})
	}
}

func TestTestFauxToolCallIDsResume(t *testing.T) {
	// A recreated provider must not reuse IDs in resumed, errored, aborted, or orphaned history.
	for _, reason := range []StopReason{StopReasonToolUse, StopReasonError, StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			p := &TestFauxProvider{}
			messages := []Message{
				AssistantMessage{StopReason: reason, Content: []AssistantContentBlock{ToolCall{ID: "call_test_faux_7", Name: "bash", Arguments: JsonObject{}}}},
				ToolResultMessage{ToolCallID: "call_test_faux_9", ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: "orphan"}}},
				UserMessage{Content: UserText("Run: expr 20 + 22")},
			}
			stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), StreamOptions{SessionID: "resumed"})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if len(result.Content) != 1 {
				t.Fatalf("result = %#v", result)
			}
			call, ok := result.Content[0].(ToolCall)
			if !ok || call.ID != "call_test_faux_10" {
				t.Fatalf("call = %#v, want call_test_faux_10", result.Content[0])
			}
		})
	}
}

func TestTestFauxToolCallIDsResumeAssistantOnly(t *testing.T) {
	// Assistant IDs must seed the counter even without a tool result, including interrupted turns.
	for _, reason := range []StopReason{StopReasonToolUse, StopReasonError, StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			p := &TestFauxProvider{}
			messages := []Message{
				AssistantMessage{StopReason: reason, Content: []AssistantContentBlock{ToolCall{ID: "call_test_faux_7", Name: "bash", Arguments: JsonObject{}}}},
				UserMessage{Content: UserText("Run: expr 20 + 22")},
			}
			stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), StreamOptions{SessionID: "resumed"})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.StopReason != StopReasonToolUse || len(result.Content) != 1 {
				t.Fatalf("result = %#v", result)
			}
			call, ok := result.Content[0].(ToolCall)
			if !ok || call.ID != "call_test_faux_8" {
				t.Fatalf("call = %#v, want call_test_faux_8", result.Content[0])
			}
		})
	}
}

func TestTestFauxToolCallIDsConcurrent(t *testing.T) {
	p := &TestFauxProvider{}
	const requests = 32
	results := make(chan *AssistantMessage, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() {
			stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Run: parallel reads")}}}), StreamOptions{SessionID: "shared"})
			if err != nil {
				t.Error(err)
				return
			}
			results <- stream.Result()
		})
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	for result := range results {
		for _, block := range result.Content {
			call, ok := block.(ToolCall)
			if !ok {
				t.Fatalf("not a tool call: %#v", block)
			}
			if seen[call.ID] {
				t.Errorf("duplicate ID %q", call.ID)
			}
			seen[call.ID] = true
		}
	}
	for i := 1; i <= requests*2; i++ {
		if !seen[fmt.Sprintf("call_test_faux_%d", i)] {
			t.Errorf("missing ID %d", i)
		}
	}
}

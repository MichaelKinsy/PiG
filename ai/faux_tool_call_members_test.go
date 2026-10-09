package ai

import "testing"

// upstream: packages/ai/src/providers/faux.ts:fauxToolCall,streamWithDeltas (toolcall_end carries the response block, the partial message holds {type, id, name, arguments}, the final message holds the response blocks).
func TestFauxToolCallOptionalMembersReachEndEventAndFinalMessage(t *testing.T) {
	t.Parallel()
	provider := NewFauxProvider(FauxConfig{})
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	call := FauxToolCall("lookup", map[string]any{"q": "x"}, &FauxToolCallOptions{ID: "call-1"})
	call.Namespace, call.ThoughtSignature = "ns", "sig"
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{call}, StopReason: "toolUse"})})
	stream, err := provider.Stream(t.Context(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var ended *ToolCall
	for event := range stream.Events(t.Context()) {
		if end, ok := event.(ToolCallEndEvent); ok {
			ended = &end.ToolCall
			partial := end.Partial.Content[end.ContentIndex].(ToolCall)
			if partial.Namespace != "" || partial.ThoughtSignature != "" {
				t.Errorf("partial tool call = %+v, want only id, name and arguments", partial)
			}
		}
	}
	if ended == nil || ended.Namespace != "ns" || ended.ThoughtSignature != "sig" {
		t.Fatalf("toolcall_end tool call = %+v, want namespace ns and signature sig", ended)
	}
	final := stream.Result().Content[0].(ToolCall)
	if final.Namespace != "ns" || final.ThoughtSignature != "sig" || final.ID != "call-1" {
		t.Errorf("final tool call = %+v", final)
	}
}

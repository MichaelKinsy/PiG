package ai

import "testing"

// upstream: packages/ai/src/providers/faux.ts:fauxText,streamWithDeltas (the partial message holds {type: "text", text}; the final message holds the response blocks, so types.ts TextContent.textSignature survives only there).
func TestFauxTextSignatureReachesOnlyTheFinalMessage(t *testing.T) {
	t.Parallel()
	provider := NewFauxProvider(FauxConfig{})
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	signed := FauxText("signed")
	signed.TextSignature = "sig-1"
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("plain"), signed, FauxThinking("hm"), signed}, StopReason: "stop"})})
	stream, err := provider.Stream(t.Context(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for event := range stream.Events(t.Context()) {
		if end, ok := event.(TextEndEvent); ok {
			if got := end.Partial.Content[end.ContentIndex].(TextContent).TextSignature; got != "" {
				t.Errorf("partial text block %d carries signature %q, want none", end.ContentIndex, got)
			}
		}
	}
	final := stream.Result().Content
	want := []string{"", "sig-1", "", "sig-1"}
	for i, sig := range want {
		if i == 2 {
			continue
		}
		if got := final[i].(TextContent).TextSignature; got != sig {
			t.Errorf("final block %d textSignature = %q, want %q", i, got, sig)
		}
	}
}

// upstream: packages/ai/src/providers/faux.ts:fauxThinking,streamWithDeltas (the partial message holds {type: "thinking", thinking}; the final message holds the response blocks, so types.ts ThinkingContent.thinkingSignature and redacted survive only there).
func TestFauxThinkingSignatureAndRedactedReachOnlyTheFinalMessage(t *testing.T) {
	t.Parallel()
	provider := NewFauxProvider(FauxConfig{})
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	signed := FauxThinking("signed")
	signed.ThinkingSignature = "sig-1"
	redacted := FauxThinking("hidden")
	redacted.ThinkingSignature, redacted.Redacted = "opaque", true
	redactedOnly := FauxThinking("bare")
	redactedOnly.Redacted = true
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxThinking("plain"), signed, FauxText("t"), redacted, redactedOnly}, StopReason: "stop"})})
	stream, err := provider.Stream(t.Context(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ends := 0
	for event := range stream.Events(t.Context()) {
		if end, ok := event.(ThinkingEndEvent); ok {
			ends++
			if got := end.Partial.Content[end.ContentIndex].(ThinkingContent); got.ThinkingSignature != "" || got.Redacted {
				t.Errorf("partial thinking block %d = %+v, want no signature and not redacted", end.ContentIndex, got)
			}
		}
	}
	if ends != 4 {
		t.Fatalf("thinking_end events = %d, want 4", ends)
	}
	final := stream.Result().Content
	want := map[int]ThinkingContent{0: {Thinking: "plain"}, 1: {Thinking: "signed", ThinkingSignature: "sig-1"}, 3: {Thinking: "hidden", ThinkingSignature: "opaque", Redacted: true}, 4: {Thinking: "bare", Redacted: true}}
	for i, w := range want {
		got := final[i].(ThinkingContent)
		if got.Thinking != w.Thinking || got.ThinkingSignature != w.ThinkingSignature || got.Redacted != w.Redacted {
			t.Errorf("final block %d = %+v, want %+v", i, got, w)
		}
	}
	if got := final[2].(TextContent); got.Text != "t" || got.TextSignature != "" {
		t.Errorf("final block 2 = %+v, want text t without signature", got)
	}
}

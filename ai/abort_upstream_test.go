package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// The helpers in .upstream/v0.87.1/packages/ai/test/abort.test.ts:14-99 assert cancellation and follow-up behavior independently of generated wording. The paced faux provider supplies the deterministic generation side; remote transport cancellation is live-only.
func TestAbortMatrixUpstream(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	specs := []struct{ provider, model string }{
		{"google", "gemini-2.5-flash"}, {"openai", "gpt-4o-mini"}, {"openai", "gpt-5-mini"}, {"azure-openai-responses", "gpt-4o-mini"},
		{"anthropic", "claude-sonnet-4-6"}, {"mistral", "devstral-medium-latest"}, {"together", "moonshotai/Kimi-K2.6"}, {"baseten", "zai-org/GLM-5.2"},
		{"minimax", "MiniMax-M2.7"}, {"xiaomi", "mimo-v2.5-pro"}, {"xiaomi-token-plan-cn", "mimo-v2.5-pro"}, {"xiaomi-token-plan-ams", "mimo-v2.5-pro"},
		{"xiaomi-token-plan-sgp", "mimo-v2.5-pro"}, {"qwen-token-plan", "qwen3.7-max"}, {"qwen-token-plan-individual", "qwen3.8-max"}, {"qwen-token-plan-cn", "qwen3.7-max"},
		{"kimi-coding", "kimi-for-coding"}, {"vercel-ai-gateway", "google/gemini-2.5-flash"}, {"openai-codex", "gpt-5.5"}, {"amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0"},
	}
	cases := upstreamCaseSites(t, "packages/ai/test/abort.test.ts")
	if len(cases) != len(specs)*2+1 {
		t.Fatalf("upstream cases=%d, want two per model plus the Bedrock follow-up case", len(cases))
	}
	for index, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/v0.87.1/packages/ai/test/abort.test.ts:%d", tc.Line)
			spec := specs[min(index/2, len(specs)-1)]
			if _, ok := ai.LookupModelExact(spec.provider + "/" + spec.model); !ok {
				t.Fatal("missing upstream model", spec)
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: spec.provider, Model: spec.model, TokensPerSecond: 100, MinTokenSize: 1, MaxTokenSize: 1})
			provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(strings.Repeat("Alice Bob Carol David ", 10))}, StopReason: "stop"})})
			model := &ai.Model{ID: spec.model, Provider: provider}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if strings.Contains(tc.ID, "abort mid-stream") {
				request := ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("What is 15 + 27? Think step by step. Then list 50 first names.")}}}
				stream := services.ModelRuntime().Stream(ctx, model, request, ai.StreamOptions{})
				text := ""
				aborted := false
				// Upstream's iterator has no independent cancellation signal. Drain the producer's terminal event after aborting its request.
				for event := range stream.Events(t.Context()) {
					switch event := event.(type) {
					case ai.TextDeltaEvent:
						text += event.Delta
					case ai.ThinkingDeltaEvent:
						text += event.Delta
					}
					if len(text) >= 50 && !aborted {
						cancel()
						aborted = true
					}
				}
				message := stream.Result()
				if !aborted || message.StopReason != ai.StopReasonAborted || len(message.Content) == 0 {
					t.Fatalf("abortFired=%v result=%#v", aborted, message)
				}
				request.Messages = append(request.Messages, *message, ai.UserMessage{Content: ai.UserText("Please continue, but only generate 5 names.")})
				provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Alice Bob Carol David Eve")}, StopReason: "stop"})})
				followUp := services.ModelRuntime().Complete(t.Context(), model, request, ai.StreamOptions{})
				if followUp.StopReason != ai.StopReasonStop || len(followUp.Content) == 0 {
					t.Fatalf("follow-up=%#v", followUp)
				}
				return
			}
			cancel()
			prompt := "Hello"
			afterAbort := strings.Contains(tc.ID, "abort then new message")
			if afterAbort {
				prompt = "Hello, how are you?"
			} else if !strings.Contains(tc.ID, "immediate abort") {
				t.Fatal("unported helper", tc.ID)
			}
			request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(prompt)}}}
			message := services.ModelRuntime().Complete(ctx, model, request, ai.StreamOptions{})
			if message.StopReason != ai.StopReasonAborted {
				t.Fatalf("immediate abort=%#v", message)
			}
			if afterAbort {
				if len(message.Content) != 0 {
					t.Fatal("immediate abort retained content")
				}
				request.Messages = append(request.Messages, *message, ai.UserMessage{Content: ai.UserText("What is 2 + 2?")})
				provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("4")}, StopReason: "stop"})})
				followUp := services.ModelRuntime().Complete(t.Context(), model, request, ai.StreamOptions{})
				if followUp.StopReason != ai.StopReasonStop || len(followUp.Content) == 0 {
					t.Fatalf("after abort=%#v", followUp)
				}
			}
		})
	}
}

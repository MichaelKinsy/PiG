// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	harnesscompaction "github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/ai"
)

type harnessSummaryProvider struct {
	options  []ai.StreamOptions
	requests []ai.TranscriptContext
}

func (*harnessSummaryProvider) ID() string   { return "harness-summary" }
func (*harnessSummaryProvider) Close() error { return nil }
func (p *harnessSummaryProvider) Stream(_ context.Context, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.options = append(p.options, options)
	p.requests = append(p.requests, request)
	message := sessionTestMessage(p.ID(), "summary", ai.StopReasonStop, "")
	message.Usage = ai.Usage{Input: 21, Output: 5, TotalTokens: 26}
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

// The public harness helper drives Services-owned ModelRuntime, not a test-only model implementation.
func TestHarnessCompactionUsesModelRuntime(t *testing.T) {
	services := newTestServices(t)
	provider := &harnessSummaryProvider{}
	model := fakeModelWithProvider(provider)
	model.Capabilities.MaxOutputTokens = 128000
	model.Capabilities.MaxThinking = ai.ThinkingHigh
	messages := []agent.AgentMessage{queueUser("Summarize this.", 1)}
	prep := harnesscompaction.CompactionPreparation{MessagesToSummarize: messages, TurnPrefixMessages: messages, RetainedTail: messages, IsSplitTurn: true, TokensBefore: 600000, Settings: harnesscompaction.CompactionSettings{Enabled: true, ReserveTokens: 500000, KeepRecentTokens: 20000}, FileOps: harnesscompaction.CreateFileOps()}
	result, err := harnesscompaction.Compact(t.Context(), prep, services.ModelRuntime(), model, "", ai.ThinkingHigh, nil, ai.RetryCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.options) != 2 || result.Usage == nil || result.Usage.TotalTokens != 52 || len(result.RetainedTail) != 1 {
		t.Fatalf("options=%+v result=%+v", provider.options, result)
	}
	for _, options := range provider.options {
		if options.MaxTokens != 128000 || options.CacheRetention != "none" || options.Thinking != ai.ThinkingHigh || options.Transport != "" {
			t.Fatalf("options=%+v", options)
		}
	}
	if provider.options[0].SessionID == provider.options[1].SessionID {
		t.Fatal("summary requests share a routing identity")
	}
	for _, request := range provider.requests {
		if ai.GetCurrentSystemPrompt(request.Messages()) != harnesscompaction.SummarizationSystemPrompt || len(ai.GetCurrentTools(request.Messages())) != 0 {
			t.Fatal("summary inherited agent instructions/tools")
		}
	}
}

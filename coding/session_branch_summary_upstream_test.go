// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/agent"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// packages/coding-agent/test/suite/regressions/6324-branch-summary-ambient-auth.test.ts:16
func TestBranchSummaryAmbientAuthUpstream(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true})
	streamCalls := 0
	h.session.Agent().SetStreamFunction(func(_ context.Context, model *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		streamCalls++
		if options.APIKey != "" {
			t.Errorf("API key = %q, want absent", options.APIKey)
		}
		message := &ai.AssistantMessage{
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "branch summary text"}},
			API:     model.ProviderMeta.API, Provider: providerID(model), Model: model.ID,
			Usage:      ai.Usage{Input: 1, Output: 1, TotalTokens: 2, Cost: ai.UsageCost{Total: 0.25}},
			StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli(),
		}
		return newSessionTestStream(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
	})
	target := appendTreeUser(t, h.session, "first branch")
	appendTreeAssistant(t, h.session, "first reply")
	appendTreeUser(t, h.session, "abandoned branch work")
	appendTreeAssistant(t, h.session, "abandoned reply")

	result, err := h.session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Cancelled || streamCalls != 1 {
		t.Fatalf("cancelled=%v streamCalls=%d, want false/1", result.Cancelled, streamCalls)
	}
	entry := result.SummaryEntry
	if entry == nil || entry.Type != "branch_summary" || !strings.Contains(entry.Summary, "branch summary text") {
		t.Fatalf("summary entry = %+v", entry)
	}
	if entry.Usage == nil || entry.Usage.Cost.Total != 0.25 {
		t.Fatalf("summary usage = %+v, want total cost 0.25", entry.Usage)
	}
}

type branchAuthProvider struct {
	upstreamBranchProvider
	auth ai.ProviderAuth
}

func (p *branchAuthProvider) Auth() ai.ProviderAuth { return p.auth }

// agent-session.ts:_getSummarizationRequestAuth supplies resolved key/headers/env/baseUrl to stock and custom streams alike. Only custom streams may continue when registry auth is absent.
// Branch summaries (agent-session.ts:4045-4046) and bug report summaries (agent-session.ts:4312-4313) both take it. A header the auth deletes is
// dropped from the summary request (agent-session.ts:241 withoutDeletedHeaders, 589).
func TestBranchSummaryRequestAuth(t *testing.T) {
	for _, operation := range []string{"branch", "bug-report"} {
		for _, kind := range []string{"api-key", "ambient-headers", "deleted-header", "oauth"} {
			for _, custom := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/custom=%v", operation, kind, custom), func(t *testing.T) {
					h := newRecoveryHarness(t, harnessOptions{})
					wantAuth := ai.ModelAuth{APIKey: "resolved-key", BaseURL: "https://auth.example.test/v1"}
					if kind == "ambient-headers" || kind == "deleted-header" {
						wantAuth.APIKey = ""
						wantAuth.Headers = ai.ProviderHeadersFromStrings(map[string]string{"Authorization": "Bearer ambient-token"})
					}
					resolvedAuth := wantAuth
					if kind == "deleted-header" {
						resolvedAuth.Headers = ai.ProviderHeaders{"Authorization": new("Bearer ambient-token"), "X-Deleted": nil}
					}
					resolved := 0
					p := &branchAuthProvider{upstreamBranchProvider: upstreamBranchProvider{response: ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "summary"}}, StopReason: ai.StopReasonStop}}}
					if kind == "oauth" {
						if err := h.session.services.Auth().Set(p.ID(), ai.Credential{Type: ai.CredentialOAuth, Access: "oauth-token", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
							t.Fatal(err)
						}
						wantAuth.APIKey = "oauth-token"
						p.auth.OAuth = &ai.OAuthAuth{Name: "Test OAuth", ToAuth: func(c ai.Credential) (ai.ModelAuth, error) {
							resolved++
							if c.Access != wantAuth.APIKey {
								t.Errorf("OAuth access = %q", c.Access)
							}
							return wantAuth, nil
						}}
					} else {
						p.auth.APIKey = &ai.APIKeyAuth{Name: "Test auth", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
							resolved++
							return &ai.AuthResult{Auth: resolvedAuth, Env: map[string]string{"REQUEST_AUTH": "ambient"}}, nil
						}}
					}
					installCompactionModel(h.session, p, 200000, 8192)
					h.session.Model().ProviderMeta.BaseURL = "https://catalog.example.test/v1"
					if custom {
						h.session.Agent().SetStreamFunction(func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
							if model.ProviderMeta.BaseURL != wantAuth.BaseURL {
								t.Errorf("request endpoint = %q", model.ProviderMeta.BaseURL)
							}
							return p.Stream(ctx, transcript, options)
						})
					}
					target := appendTreeUser(t, h.session, "first branch")
					appendTreeAssistant(t, h.session, "abandoned reply")
					if operation == "branch" {
						if _, err := h.session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true}); err != nil {
							t.Fatal(err)
						}
					} else if _, err := h.session.SummarizeForBugReport(t.Context(), ""); err != nil {
						t.Fatal(err)
					}
					if resolved != 1 || len(p.options) != 1 {
						t.Fatalf("auth resolutions=%d stream calls=%d, want 1/1", resolved, len(p.options))
					}
					options := p.options[0]
					if options.APIKey != wantAuth.APIKey || !reflect.DeepEqual(options.Headers, wantAuth.Headers) || options.CacheRetention != ai.CacheRetentionNone {
						t.Fatalf("request options = %+v", options)
					}
					if kind != "oauth" && options.Env["REQUEST_AUTH"] != "ambient" {
						t.Fatalf("auth environment = %v", options.Env)
					}
					if h.session.Model().ProviderMeta.BaseURL != "https://catalog.example.test/v1" {
						t.Fatal("request auth mutated the selected model")
					}
				})
			}
		}
	}
}

// The same Agent stream override must reach compaction and branch-summary callers, not just ordinary turns.
func TestSummarizationUsesAgentStreamOverride(t *testing.T) {
	for _, operation := range []string{"branch", "compaction"} {
		t.Run(operation, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{})
			model, err := BuildModel("faux/faux-1", h.session.services)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAUX_API_KEY", "")
			h.session.Agent().SetModel(model)
			suiteCompactionSeed(t, h.session)
			calls := 0
			h.session.Agent().SetStreamFunction(func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				return nil, errors.New("custom summary failure")
			})
			if operation == "branch" {
				_, err = h.session.NavigateTree(t.Context(), h.session.inner.GetEntries()[0].Base().ID, NavigateTreeOptions{Summarize: true})
			} else {
				_, err = h.session.Compact(t.Context(), "")
			}
			if calls != 1 || err == nil || !strings.Contains(err.Error(), "custom summary failure") {
				t.Fatalf("calls=%d error=%v, want custom failure after one call despite absent registry auth", calls, err)
			}
		})
	}
}

// Pi: packages/coding-agent/src/core/agent-session.ts:1383 (Session.abortBranchSummary); packages/coding-agent/src/core/agent-session.ts:1452 (Session.isCompacting).
func TestBranchSummaryAgentStreamCancellation(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	target := appendTreeUser(t, h.session, "first branch")
	appendTreeAssistant(t, h.session, "abandoned reply")
	leaf := treeLeaf(h.session)
	started := make(chan struct{})
	h.session.Agent().SetStreamFunction(func(ctx context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	type completion struct {
		result NavigateTreeResult
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		result, err := h.session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true})
		done <- completion{result, err}
	}()
	<-started
	if !h.session.IsCompacting() {
		t.Error("navigation did not retain ownership while awaiting its stream")
	}
	h.session.AbortBranchSummary()
	got := <-done
	if got.err != nil || !got.result.Cancelled || !got.result.Aborted || got.result.SummaryEntry != nil || treeLeaf(h.session) != leaf || h.session.IsCompacting() {
		t.Fatalf("navigation=%+v error=%v leaf=%s", got.result, got.err, treeLeaf(h.session))
	}
}

// The pure collector preserves exactly the stream's text and usage, and rejects tools/length/errors before any Session persistence.
func TestModelCompleterAgentStreamOverride(t *testing.T) {
	model := fakeModel()
	for _, reason := range []ai.StopReason{ai.StopReasonStop, ai.StopReasonLength, ai.StopReasonError, ai.StopReasonToolUse} {
		t.Run(string(reason), func(t *testing.T) {
			message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "summary"}}, StopReason: reason, Usage: ai.Usage{Cost: ai.UsageCost{Total: 0.25}}}
			if reason == ai.StopReasonError {
				message.ErrorMessage = "failed summary"
			}
			if reason == ai.StopReasonToolUse {
				message.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "read", Arguments: ai.JsonObject{}}}
			}
			calls := 0
			c := compaction.ProviderCompleter{StreamFn: func(_ context.Context, gotModel *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				if gotModel != model || ai.GetCurrentSystemPrompt(transcript.Messages()) != "summarize" || options.APIKey != "" {
					t.Fatal("collector changed request model, system prompt or absent auth")
				}
				if reason == ai.StopReasonError {
					return newSessionTestStream(ai.ErrorEvent{Reason: reason, Error: message}), nil
				}
				return newSessionTestStream(ai.DoneEvent{Reason: reason, Message: message}), nil
			}}
			text, usage, err := c.CompleteSimple(t.Context(), model, "summarize", []agent.AgentMessage{queueUser("branch", 1)}, ai.StreamOptions{})
			if calls != 1 {
				t.Fatalf("stream calls=%d", calls)
			}
			if reason == ai.StopReasonStop {
				if err != nil || text != "summary" || usage == nil || usage.Cost.Total != 0.25 {
					t.Fatalf("text=%q usage=%+v error=%v", text, usage, err)
				}
			} else if err == nil {
				t.Fatalf("accepted invalid summary with stop reason %s", reason)
			}
		})
	}
}

type upstreamBranchProvider struct {
	response ai.AssistantMessage
	options  []ai.StreamOptions
}

func (*upstreamBranchProvider) ID() string   { return "anthropic" }
func (*upstreamBranchProvider) Close() error { return nil }
func (p *upstreamBranchProvider) Stream(_ context.Context, _ ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.options = append(p.options, opts)
	return newSessionTestStream(ai.StartEvent{Partial: &p.response}, ai.DoneEvent{Reason: p.response.StopReason, Message: &p.response}), nil
}

func TestBranchSummarizationUpstream(t *testing.T) {
	cases := []struct {
		name      string
		max       int
		response  ai.AssistantMessage
		wantMax   int
		wantError string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/branch-summarization.test.ts:47
		{"does not override tool choice for branch summaries", 8192, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "summary"}}, StopReason: ai.StopReasonStop}, 4096, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/branch-summarization.test.ts:68
		{"clamps the branch summary output cap to the model limit", 1024, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "summary"}}, StopReason: ai.StopReasonStop}, 1024, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/branch-summarization.test.ts:88
		{"rejects tool calls from branch summaries", 8192, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "tool-call-1", Name: "read", Arguments: ai.JsonObject{"path": "README.md"}}}, StopReason: ai.StopReasonToolUse}, 4096, "Branch summarization attempted to call a tool"},
		// .upstream/v0.87.1/packages/coding-agent/test/branch-summarization.test.ts:112
		{"rejects length-limited branch summaries", 8192, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}, StopReason: ai.StopReasonLength}, 4096, "Branch summarization failed: generation hit the token cap and the summary is incomplete"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.response.API = "anthropic-messages"
			tc.response.Provider = "anthropic"
			tc.response.Model = "test-model"
			provider := &upstreamBranchProvider{response: tc.response}
			model := fakeModelWithProvider(provider)
			model.ID = "test-model"
			model.DisplayName = "Test Model"
			model.Capabilities.ContextWindow = 200000
			model.Capabilities.MaxOutputTokens = tc.max
			entry := sessionentry.DecodeSessionEntry([]byte(`{"type":"message","id":"branch-user","parentId":null,"timestamp":"1970-01-01T00:00:00.001Z","message":{"role":"user","content":"Abandoned request","timestamp":1}}`))
			result := compaction.GenerateBranchSummary(t.Context(), []codingagent.SessionEntry{entry}, compaction.GenerateBranchSummaryOptions{Model: model, Completer: compaction.ProviderCompleter{}})
			if result.Error != tc.wantError {
				t.Fatalf("error = %q, want %q", result.Error, tc.wantError)
			}
			if len(provider.options) != 1 || provider.options[0].MaxTokens != tc.wantMax || provider.options[0].ToolChoice != nil {
				t.Fatalf("options = %+v", provider.options)
			}
			fmt.Printf("BRANCH_SUMMARY %d max=%d error=%q\n", i, provider.options[0].MaxTokens, result.Error)
		})
	}
}

package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// packages/agent/src/agent.ts:155-191 declares convertToLlm, onResponse, onProviderStreamEvent, sessionId, thinkingBudgets, transport,
// maxRetryDelayMs and toolExecution as public properties, and createLoopConfig (agent.ts:~470) reads each when a run starts, so an
// assignment between two runs changes the second one. The Go setters and getters are those properties; each case sets a value
// between two Sends and checks what the provider (or the tool batch) saw in the second run.
func TestAgent_PropertiesAssignedBetweenRunsApplyToTheNextRun(t *testing.T) {
	ten := 10
	budgets := &ai.ThinkingBudgets{Low: 111}
	for _, tc := range []struct {
		name  string
		set   func(*Agent, *[]string)
		check func(t *testing.T, a *Agent, second scriptedRequest, seen *[]string)
	}{
		{"transport", func(a *Agent, _ *[]string) { a.SetTransport(ai.TransportWebSocket) }, func(t *testing.T, a *Agent, r scriptedRequest, _ *[]string) {
			if r.opts.Transport != ai.TransportWebSocket || a.Transport() != ai.TransportWebSocket {
				t.Fatalf("transport = %q / %q", r.opts.Transport, a.Transport())
			}
		}},
		{"sessionId", func(a *Agent, _ *[]string) { a.SetSessionID("session-2") }, func(t *testing.T, a *Agent, r scriptedRequest, _ *[]string) {
			if r.opts.SessionID != "session-2" || a.SessionID() != "session-2" {
				t.Fatalf("session = %q / %q", r.opts.SessionID, a.SessionID())
			}
		}},
		{"thinkingBudgets", func(a *Agent, _ *[]string) { a.SetThinkingBudgets(budgets) }, func(t *testing.T, a *Agent, r scriptedRequest, _ *[]string) {
			if r.opts.ThinkingBudgets != budgets || a.ThinkingBudgets() != budgets {
				t.Fatalf("budgets = %v / %v", r.opts.ThinkingBudgets, a.ThinkingBudgets())
			}
		}},
		{"maxRetryDelayMs", func(a *Agent, _ *[]string) { a.SetMaxRetryDelayMs(&ten) }, func(t *testing.T, a *Agent, r scriptedRequest, _ *[]string) {
			if r.opts.MaxRetryDelayMs != &ten || a.MaxRetryDelayMs() != &ten {
				t.Fatalf("maxRetryDelayMs = %v / %v", r.opts.MaxRetryDelayMs, a.MaxRetryDelayMs())
			}
		}},
		{"onResponse", func(a *Agent, seen *[]string) {
			a.SetOnResponse(func(context.Context, ai.ProviderResponse, *ai.Model) error {
				*seen = append(*seen, "response")
				return nil
			})
		}, func(t *testing.T, a *Agent, r scriptedRequest, seen *[]string) {
			if r.opts.OnResponse == nil || a.OnResponse() == nil {
				t.Fatal("the second request carries no OnResponse")
			}
			if err := r.opts.OnResponse(context.Background(), ai.ProviderResponse{Status: 200}, nil); err != nil || len(*seen) != 1 {
				t.Fatalf("OnResponse = %v, seen %v", err, *seen)
			}
		}},
		{"onProviderStreamEvent", func(a *Agent, seen *[]string) {
			a.SetOnProviderStreamEvent(func(context.Context, any, *ai.Model) error { *seen = append(*seen, "event"); return nil })
		}, func(t *testing.T, a *Agent, r scriptedRequest, seen *[]string) {
			if r.opts.OnProviderStreamEvent == nil || a.OnProviderStreamEvent() == nil {
				t.Fatal("the second request carries no OnProviderStreamEvent")
			}
			if err := r.opts.OnProviderStreamEvent(context.Background(), nil, nil); err != nil || len(*seen) != 1 {
				t.Fatalf("OnProviderStreamEvent = %v, seen %v", err, *seen)
			}
		}},
		{"convertToLlm", func(a *Agent, seen *[]string) {
			a.SetConvertToLlm(func([]AgentMessage) ([]ai.Message, error) {
				*seen = append(*seen, "convert")
				return []ai.Message{ai.UserMessage{Content: ai.UserText("converted"), Timestamp: 1}}, nil
			})
		}, func(t *testing.T, a *Agent, r scriptedRequest, seen *[]string) {
			messages := r.transcript.Messages()
			if len(*seen) != 1 || a.ConvertToLlm() == nil || len(messages) != 1 {
				t.Fatalf("converter ran %d times, messages = %d", len(*seen), len(messages))
			}
			if user, ok := messages[0].(ai.UserMessage); !ok || user.Content != ai.UserText("converted") {
				t.Fatalf("message = %#v, want the converter's message", messages[0])
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedProvider{respond: replyText("ok")}
			a := mustNewAgent(AgentOptions{Model: scriptedModel(provider)})
			var seen []string
			mustSend(t, a, "first")
			first := provider.request(1)
			tc.set(a, &seen)
			mustSend(t, a, "second")
			second := provider.request(2)
			tc.check(t, a, second, &seen)
			// The first run is unchanged by the later assignment.
			if first.opts.Transport == ai.TransportWebSocket || first.opts.SessionID == "session-2" || first.opts.ThinkingBudgets == budgets || first.opts.MaxRetryDelayMs == &ten {
				t.Fatalf("the earlier request changed: %+v", first.opts)
			}
		})
	}
}

// agent.ts:191 toolExecution: "sequential" runs the calls of one assistant message one at a time, "parallel" runs them concurrently.
func TestAgent_SetToolExecutionChangesHowTheNextBatchRuns(t *testing.T) {
	for _, tc := range []struct {
		mode        ToolExecutionMode
		wantOverlap bool
	}{{ToolModeParallel, true}, {ToolModeSequential, false}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			var running, peak atomic.Int32
			tool := &scriptTool{name: "slow", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
				now := running.Add(1)
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}
				time.Sleep(60 * time.Millisecond)
				running.Add(-1)
				return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, nil
			}}
			provider := &scriptedProvider{respond: toolCallsThenText(toolCall("a", "slow", nil), toolCall("b", "slow", nil))}
			a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}, ToolExecution: ToolModeParallel})
			other := ToolModeSequential
			if tc.mode == ToolModeSequential {
				other = ToolModeParallel
			}
			a.SetToolExecution(other)
			a.SetToolExecution(tc.mode)
			mustSend(t, a, "go")
			if a.ToolExecutionMode() != tc.mode {
				t.Fatalf("ToolExecutionMode = %q", a.ToolExecutionMode())
			}
			if overlap := peak.Load() > 1; overlap != tc.wantOverlap {
				t.Fatalf("peak concurrency = %d, want overlap %v", peak.Load(), tc.wantOverlap)
			}
		})
	}
}

// Pi assigns these properties on one event loop; Go callers may assign them while a run starts on another goroutine, so each setter and the run's capture of the properties take the same lock. Run with -race.
func TestAgent_PropertySettersRaceFreeWithRunStart(t *testing.T) {
	a := newTestAgentForNextTurn(t)
	ten := 10
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			a.SetTransport(ai.TransportWebSocket)
			a.SetSessionID("session")
			a.SetThinkingBudgets(&ai.ThinkingBudgets{Low: 1})
			a.SetMaxRetryDelayMs(&ten)
			a.SetToolExecution(ToolModeSequential)
			a.SetConvertToLlm(nil)
			a.SetOnResponse(nil)
			a.SetOnProviderStreamEvent(nil)
			a.SetFinishTurn(nil)
			a.SetPrepareRequest(nil)
			a.SetPrepareNextTurn(nil)
			a.SetPrepareNextTurnWithContext(nil)
		}
	}()
	for range 200 {
		_ = a.createLoopConfig(false)
		_, _, _ = a.Transport(), a.SessionID(), a.ToolExecutionMode()
		_, _, _, _ = a.FinishTurnHook(), a.PrepareRequestHook(), a.PrepareNextTurnHook(), a.PrepareNextTurnWithContextHook()
	}
	<-done
}

// packages/agent/src/agent.ts:211-213 declares prepareNextTurn as a public mutable property, and createLoopConfig (agent.ts:484-491)
// runs `prepareNextTurnWithContext || prepareNextTurn` when a run starts. A legacy hook assigned between two runs therefore runs in the
// second one, and runs there only while no with-context hook is set. Each run is one tool-call turn and one text turn, so the hook
// runs once per run.
func TestAgent_SetPrepareNextTurnAppliesToTheNextRun(t *testing.T) {
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call%2 == 1 {
			return doneStream(toolUseMessage(toolCall("tool-1", "noop", nil)))
		}
		return doneStream(textMessage("done"))
	}}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{noopTool()}})
	mustSend(t, a, "first")
	var calls []string
	a.SetPrepareNextTurn(func(context.Context) (*AgentLoopTurnUpdate, error) {
		calls = append(calls, "legacy")
		return nil, nil
	})
	if a.PrepareNextTurnHook() == nil {
		t.Fatal("PrepareNextTurnHook returned nil after SetPrepareNextTurn")
	}
	mustSend(t, a, "second")
	if len(calls) != 1 {
		t.Fatalf("legacy hook ran %d times in the second run, want 1", len(calls))
	}
	a.SetPrepareNextTurnWithContext(func(context.Context, PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
		calls = append(calls, "withContext")
		return nil, nil
	})
	mustSend(t, a, "third")
	if want := []string{"legacy", "withContext"}; len(calls) != 2 || calls[1] != want[1] {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	a.SetPrepareNextTurn(nil)
	a.SetPrepareNextTurnWithContext(nil)
	mustSend(t, a, "fourth")
	if len(calls) != 2 {
		t.Fatalf("a cleared hook still ran: %v", calls)
	}
}

package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_HandedBackEventObjectsKeepTheirMemberOrder pins that an object an event hands an extension, and the extension hands back unchanged, is written
// with the member order it was sent in, as Pi's JavaScript object keeps it: the draft entries another extension proposed at a turn boundary (their data, content blocks and details), the context's
// messages with their tool-call arguments, the provider request's payload, a tool result's details and a message_end message. Each row is the bytes the host
// ends up holding after the round trip through the extension, and must equal the bytes the host sent (the run with no extension): every member below is
// written in an order that is not byte order, so an SDK that writes an object from a map it decoded fails it. The Go, Node, Rust, Python and Lisp extensions
// each run in their own process and in a shared runtime cell, and the fused Go extension runs in the host's process; the in-process reference has no wire and
// hands the host's own objects back.
func TestConformance_HandedBackEventObjectsKeepTheirMemberOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const draftData = `{"z":1,"a":2,"m":{"y":1,"b":[{"q":1,"c":2},{"d":3,"a":4}]}}`
	// The drafts another extension proposes: a custom entry, and a custom message with content blocks and details.
	drafts := `[{"type":"custom","customType":"other","data":` + draftData + `},` +
		`{"type":"custom_message","customType":"msg","content":[{"type":"text","text":"hello"},{"type":"image","mimeType":"image/png","data":"aGk="}],"display":true,"details":{"z":1,"a":2}}]`
	const arguments = `{"path":"a.txt","offset":1,"limit":2,"extra":{"z":1,"a":2}}`
	const user = `{"role":"user","content":"hello","timestamp":1}`
	assistant := `{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"read","arguments":` + arguments + `}],"api":"openai-completions","provider":"faux","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2}`
	const toolResult = `{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":3}`
	const payload = `{"model":"m","messages":[{"role":"user","content":"x"}],"stream":true,"zeta":{"z":1,"a":2}}`
	const details = `{"z":1,"a":{"y":1,"b":2}}`
	custom := `{"role":"custom","customType":"x","content":"c","display":true,"details":{"z":1,"a":2},"timestamp":1}`
	marshal := func(t *testing.T, value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx := context.Background()
			check := func(t *testing.T, got, want string) {
				t.Helper()
				if got != want {
					t.Errorf("after the round trip through the extension\n got %s\nwant %s", got, want)
				}
			}

			// A handler reads an object's keys in the order Object.keys gives them: the order the host wrote them, which no sorted read produces.
			t.Run("object_keys", func(t *testing.T) {
				runConformanceCommandArgs(t, h, "event_keys_probe", "before_provider_request")
				if _, err := h.runner.EmitBeforeProviderRequest(ctx, json.RawMessage(payload)); err != nil {
					t.Fatal(err)
				}
				const prefix = "event_keys:before_provider_request:"
				report := func() (string, bool) {
					for _, n := range *h.notify {
						if rest, ok := strings.CutPrefix(n, prefix); ok {
							return strings.TrimSuffix(rest, ":info"), true
						}
					}
					return "", false
				}
				pollUntilConformance(t, "the keys probe never reported", func() bool { _, ok := report(); return ok })
				got, _ := report()
				check(t, got, `[["model","messages","stream","zeta"],["z","a"]]`)
			})

			t.Run("turn_end", func(t *testing.T) {
				boundary := func() string {
					t.Helper()
					base := extension.TurnEndEvent{Type: "turn_end", TurnIndex: 7, MessageEntryID: "order-assistant", ToolResultEntryIds: []string{},
						Message:     wireAgentMessage(map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}, "stopReason": "stop"}),
						ToolResults: []extension.ToolResultMessage{}, BoundaryState: &extension.BoundaryState{Outcome: extension.AgentActivityCompleted}}
					build := func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
						return extension.BoundaryContextPreview{ContextEntries: []extension.ProjectedSessionEntry{}, ContextMessages: []extension.AgentMessage{}, LLMMessages: []ai.Message{},
							PendingMessages: []extension.AgentMessage{}, CanContinue: true}, nil
					}
					result, err := h.runner.EmitBoundary(ctx, base, build)
					if err != nil {
						t.Fatal(err)
					}
					if !result.Valid {
						t.Fatalf("the boundary is not valid: %+v", result)
					}
					return marshal(t, result.Entries)
				}
				// Another extension proposes the drafts; the next handler leaves them in the event, and the extension's own copy of them is echoed back with the event's entries.
				runConformanceCommandArgs(t, h, "event_ordered_result_probe", `turn_end {"entries":`+drafts+`}`)
				runConformanceCommandArgs(t, h, "event_probe_on", "turn_end")
				check(t, boundary(), drafts)
				// A handler that returns the entries it was sent.
				runConformanceCommandArgs(t, h, "event_echo_probe", "turn_end")
				check(t, boundary(), drafts)
			})

			t.Run("context", func(t *testing.T) {
				runConformanceCommandArgs(t, h, "event_echo_probe", "context")
				messages := []extension.AgentMessage{agentMessageFromJSON(t, user), agentMessageFromJSON(t, assistant), agentMessageFromJSON(t, toolResult)}
				got, err := h.runner.EmitContext(ctx, messages)
				if err != nil {
					t.Fatal(err)
				}
				want := marshal(t, messages[1:])
				if !bytes.Contains([]byte(want), []byte(`"arguments":`+arguments)) {
					t.Fatalf("the host does not write the tool call's arguments as it read them: %s", want)
				}
				check(t, marshal(t, got), want)
			})

			t.Run("before_provider_request", func(t *testing.T) {
				runConformanceCommandArgs(t, h, "event_echo_probe", "before_provider_request")
				got, err := h.runner.EmitBeforeProviderRequest(ctx, json.RawMessage(payload))
				if err != nil {
					t.Fatal(err)
				}
				check(t, marshal(t, got), payload)
			})

			t.Run("tool_result", func(t *testing.T) {
				runConformanceCommandArgs(t, h, "event_echo_probe", "tool_result")
				got, err := h.runner.EmitToolResult(ctx, extension.CustomToolResultEvent{
					ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "c1", Input: map[string]any{}, Content: []any{map[string]any{"type": "text", "text": "ok"}}},
					ToolName:            "probe", Details: json.RawMessage(details),
				})
				if err != nil {
					t.Fatal(err)
				}
				if got == nil {
					t.Fatal("the dispatch returned no result")
				}
				check(t, marshal(t, got.Details), details)
			})

			t.Run("message_end", func(t *testing.T) {
				runConformanceCommandArgs(t, h, "event_echo_probe", "message_end")
				message := agentMessageFromJSON(t, custom)
				got, err := h.runner.EmitMessageEnd(ctx, extension.MessageEndEvent{Type: "message_end", Message: message})
				if err != nil {
					t.Fatal(err)
				}
				if got == nil {
					t.Fatal("the dispatch returned no replacement")
				}
				check(t, marshal(t, *got), marshal(t, message))
			})
		})
	}
}

// agentMessageFromJSON decodes the message the JSON text holds, keeping the member order of its objects.
func agentMessageFromJSON(t *testing.T, text string) extension.AgentMessage {
	t.Helper()
	var message extension.AgentMessage
	if err := json.Unmarshal([]byte(text), &message); err != nil {
		t.Fatal(err)
	}
	return message
}

package extensionconformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// isolationEvent is one event the isolation row sends: the event, the marker every handler must report for its container (the first context entry's sourceEntry id
// of a boundary event, the length of messages of a conversation event) and the text of the three messages the probe reads.
type isolationEvent struct {
	name     string
	event    func() extension.ExtensionEvent
	marker   string
	messages string
}

// TestConformance_EventHandlersDoNotShareTheirPayload is the isolation row of lever A (handoffs/fused-direct-design.md section 5 item 2, review FD-8b), extended to the
// message lists of the payload and to the context events (handoffs/next/specs/event-wire-perf.md).
//
// This is PiG's isolation, which is stricter than Pi's. Pi hands every handler of an emit the same event object (runner.ts emit, emitToolCall), a fresh shallow
// event whose context and message are the session's live objects at a boundary (emitBoundary: a write to contextMessages[i] reaches later handlers and the session),
// and one structuredClone of the messages per request that all handlers of a context dispatch share (emitContext): in Pi a handler's write to nested event data
// can reach another handler. A socket SDK (Node, Rust, Python, Lisp, isolated or packed) cannot do that, its handler holds a decoded copy of a frame, so there
// isolation is forced by the transport. The fused Go realization (and the in-process Go handlers) is a choice: the SDK clones the decoded payload per handler
// (eventArgsMemo), so all realizations agree. Whether that is a numbered divergence is the owner's decision (open); this row states the behavior, it is not a
// Pi parity row.
//
// In one dispatch the first handler writes a sentinel into the payload's container (a boundary event's context.contextEntries[0].sourceEntry, a conversation event's
// messages list: a message appended), writes sentinels into the three messages the probe reads, and where the language allows (not Rust, whose handler holds
// the data only for the call) keeps them and writes again from another thread after it returned; the second handler of the same dispatch and the first handler's
// next dispatch of the same event (the same payload bytes again, for a context event the kept encodings of the messages) report what they received. Every
// realization must report the originals everywhere.
func TestConformance_EventHandlersDoNotShareTheirPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	user := func(content string) extension.AgentMessage {
		return wireAgentMessage(map[string]any{"role": "user", "content": content, "timestamp": float64(1700000000000)})
	}
	conversation := func() []extension.AgentMessage {
		return []extension.AgentMessage{user("orig-0"), user("orig-1"), user("orig-2")}
	}
	events := []isolationEvent{
		{name: "turn_end", marker: "original-marker", messages: "orig-msg|orig-llm|orig-msg", event: func() extension.ExtensionEvent {
			state := boundaryStateFixture()
			state.Context.ContextEntries = []extension.ProjectedSessionEntry{{SourceEntry: map[string]any{"id": "original-marker", "type": "message"}, Messages: []extension.AgentMessage{}}}
			// The text of the first message is in contextMessages, in the first context entry's messages and, converted, in llmMessages.
			original := user("orig-msg")
			state.Context.ContextEntries[0].Messages = []extension.AgentMessage{original}
			state.Context.ContextMessages = []extension.AgentMessage{original}
			state.Context.LLMMessages = []ai.Message{ai.UserMessage{Content: ai.UserText("orig-llm"), Timestamp: 1700000000000}}
			return extension.TurnEndEvent{BoundaryState: state, Type: "turn_end", TurnIndex: 1, Message: user("hi"), ToolResults: []extension.ToolResultMessage{}}
		}},
		{name: "context", marker: "3", messages: "orig-0|orig-1|orig-2", event: func() extension.ExtensionEvent {
			return extension.ContextEvent{Type: "context", Messages: conversation()}
		}},
		{name: "context_with_system", marker: "3", messages: "orig-0|orig-1|orig-2", event: func() extension.ExtensionEvent {
			return extension.ContextWithSystemEvent{Type: "context_with_system", Messages: conversation()}
		}},
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
			for _, probe := range events {
				t.Run(probe.name, func(t *testing.T) {
					*h.notify = (*h.notify)[:0]
					runConformanceCommandArgs(t, h, "event_isolation_probe", probe.name)
					reports := func(prefix string) []string {
						var out []string
						for _, n := range *h.notify {
							if rest, ok := strings.CutPrefix(n, prefix); ok {
								out = append(out, strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end"))
							}
						}
						return out
					}
					// A conversation event carries the session's encodings, as the Session's context dispatch does, so the second dispatch is the one that reads the kept bytes.
					ctx := boundarywire.WithContextEncodings(context.Background(), &boundarywire.Encodings{})
					event := probe.event()
					for emit := 1; emit <= 2; emit++ {
						if _, err := h.runner.Emit(ctx, event); err != nil {
							t.Fatalf("emit %d: %v", emit, err)
						}
						pollUntilConformance(t, "both handlers report", func() bool { return len(reports("event_isolation:")) >= 2*emit })
						time.Sleep(300 * time.Millisecond) // the Go late writer runs 100 ms after the second handler started (after the first handler returned and was answered), before the next dispatch
					}
					messageReports := reports("event_isolation_msgs:")
					for _, r := range messageReports {
						if !strings.HasSuffix(r, ":"+probe.messages) {
							t.Errorf("a handler received messages %q, want %q whatever another handler wrote into its own copy (reports: %v)", r, probe.messages, messageReports)
						}
					}
					if len(messageReports) != 4 {
						t.Errorf("message reports = %v, want A and B for each of two dispatches", messageReports)
					}
					got := reports("event_isolation:")
					for _, r := range got {
						if strings.Contains(r, "SENTINEL") || !strings.HasSuffix(r, ":"+probe.marker) {
							t.Errorf("a handler received %q, want the original marker %q (reports: %v)", r, probe.marker, got)
						}
					}
					if len(got) != 4 {
						t.Errorf("reports = %v, want A and B for each of two dispatches", got)
					}
				})
			}
		})
	}
}

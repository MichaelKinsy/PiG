package extensionconformance

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
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
// can reach another handler. A socket SDK (Node, Rust, Python, Lisp, isolated or packed) cannot do that across a process boundary, and each handler's request carries its own
// decoded copy of the payload. Within one extension the copy is also the SDKs' choice (the streamed path clones its replica, the fused Go realization clones a shared master:
// eventArgsMemo), so every SDK realization agrees. The Go function handlers that inproc.Runner calls in the host's process are not in this row and share one event as Pi does.
// This is the numbered divergence D657; this row states the behavior, it is not a Pi parity row.
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
					h.ui.ClearRecorded()
					runConformanceCommandArgs(t, h, "event_isolation_probe", probe.name)
					reports := func(prefix string) []string {
						var out []string
						for _, n := range h.ui.Recorded() {
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

	// Two extensions of one packed cell: the first extension's handlers write their sentinels, and the handler of the second extension (the cell's peer), which the
	// runner calls after them, reads the original of the same dispatch and of the next.
	for _, peer := range isolationPeerCases() {
		t.Run(peer.name, func(t *testing.T) {
			h := peer.make(t)
			t.Cleanup(func() {
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for _, probe := range events {
				t.Run(probe.name, func(t *testing.T) {
					h.ui.ClearRecorded()
					runConformanceCommandArgs(t, h, "event_isolation_probe", probe.name)
					reports := func(prefix string) []string {
						var out []string
						for _, n := range h.ui.Recorded() {
							if rest, ok := strings.CutPrefix(n, prefix); ok {
								out = append(out, strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end"))
							}
						}
						return out
					}
					ctx := boundarywire.WithContextEncodings(context.Background(), &boundarywire.Encodings{})
					event := probe.event()
					for emit := 1; emit <= 2; emit++ {
						if _, err := h.runner.Emit(ctx, event); err != nil {
							t.Fatalf("emit %d: %v", emit, err)
						}
						pollUntilConformance(t, "the first extension's handlers and the peer report", func() bool {
							return len(reports("event_isolation:")) >= 2*emit && len(reports("event_isolation_peer:")) >= emit
						})
						time.Sleep(300 * time.Millisecond) // the first extension's late writer runs 100 ms after its first handler returned
					}
					got := reports("event_isolation_peer:")
					for _, r := range got {
						if r != probe.marker {
							t.Errorf("the peer extension's handler received %q, want the original marker %q whatever the first extension's handlers wrote into their copies (reports: %v)", r, probe.marker, got)
						}
					}
					if len(got) != 2 {
						t.Errorf("peer reports = %v, want one for each of two dispatches", got)
					}
				})
			}
		})
	}
}

// isolationPeerSource is the source of the cell's second extension in each language: one handler for each event of the row that reports the marker it received
// (the length of event.messages of a conversation event, the first context entry's sourceEntry id of a boundary event) under its own prefix.
const (
	isolationPeerNode = `export default function (pi) {
  for (const name of ["turn_end", "context", "context_with_system"]) {
    const conversation = name !== "turn_end";
    pi.on(name, async (event, ctx) => {
      const marker = conversation ? (event.messages ?? []).length : event.context?.contextEntries?.[0]?.sourceEntry?.id;
      ctx.ui.notify(` + "`event_isolation_peer:${marker}:end`" + `, "info");
    });
  }
}
`
	isolationPeerPython = `import pig_sdk


def new_extension():
    ext = pig_sdk.Extension("ui-peer")

    def reader(conversation):
        def handler(hctx, data):
            if conversation:
                marker = len(data.get("messages") or [])
            else:
                marker = ((data.get("context") or {}).get("contextEntries") or [{}])[0]["sourceEntry"]["id"]
            hctx.notify("event_isolation_peer:%s:end" % marker, "info")

        return handler

    for name in ("turn_end", "context", "context_with_system"):
        ext.on_event(name, reader(name != "turn_end"))
    return ext
`
)

// isolationPeerCases are the Node and the Python packed cells whose second extension handles the events the row sends.
func isolationPeerCases() []harnessCase {
	return []harnessCase{
		{name: "two-extensions-node-packed", extName: "node-sdk-fixture", make: func(t *testing.T) *harness { return makeSubprocessNodePackedHarnessWithPeer(t, isolationPeerNode) }},
		{name: "two-extensions-python-packed", extName: "python-sdk-fixture", make: func(t *testing.T) *harness {
			configs, err := pythonIsolationPeerConfigs()
			if err != nil {
				t.Fatal(err)
			}
			return makePackedUIHarnessOf(t, "python", configs)
		}},
	}
}

// pythonIsolationPeerConfigs are the Python packed cell's configs with the peer replaced by one that handles the row's events.
var pythonIsolationPeerConfigs = sync.OnceValues(func() ([]subprocess.ExtConfig, error) {
	configs, err := packedUIConfigs["python"]()
	if err != nil {
		return nil, err
	}
	configs = slices.Clone(configs)
	i := slices.IndexFunc(configs, func(c subprocess.ExtConfig) bool { return c.Name == "ui-peer" })
	dir := filepath.Join(sharedSources, "packed-isolation-peer-python", "ui-peer")
	if err := writeSharedSource(filepath.Join(dir, "ui_peer.py"), isolationPeerPython); err != nil {
		return nil, err
	}
	configs[i].Source = dir
	return configs, nil
})

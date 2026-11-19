package extensionconformance

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// contextEdit is what the second handler of a session's context dispatch received after the first handler edited the conversation in place.
type contextEdit struct {
	// message is whether the second handler read the first's in-place write into the content of a message of the list.
	message bool
	// list is whether it read an element the first handler appended to the list in place.
	list bool
}

// contextEditsOf runs the session's dispatch of phase ("context": Runner.EmitContext, "context_with_system": Runner.EmitContextWithSystem) once over three user messages
// and reads what the handlers of the event_isolation_probe reported. The first handler appends a message to event.messages and writes a sentinel into the content of the
// first three messages; the second handler reports the length of the list and the content of the first three messages.
func contextEditsOf(t *testing.T, h *harness, phase string) (edits contextEdit) {
	t.Helper()
	h.ui.ClearRecorded()
	runConformanceCommandArgs(t, h, "event_isolation_probe", phase)
	user := func(content string) extension.AgentMessage {
		return wireAgentMessage(map[string]any{"role": "user", "content": content, "timestamp": float64(1700000000000)})
	}
	messages := []extension.AgentMessage{user("orig-0"), user("orig-1"), user("orig-2")}
	ctx := boundarywire.WithContextEncodings(context.Background(), &boundarywire.Encodings{})
	var err error
	if phase == "context" {
		_, err = h.runner.EmitContext(ctx, messages)
	} else {
		_, err = h.runner.EmitContextWithSystem(ctx, messages)
	}
	if err != nil {
		t.Fatalf("%s: %v", phase, err)
	}
	reports := func(prefix string) []string {
		var out []string
		for _, n := range h.ui.Recorded() {
			if rest, ok := strings.CutPrefix(n, prefix); ok {
				out = append(out, strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end"))
			}
		}
		return out
	}
	pollUntilConformance(t, "both handlers report", func() bool {
		return len(reports("event_isolation:")) >= 2 && len(reports("event_isolation_msgs:")) >= 2
	})
	for _, r := range reports("event_isolation_msgs:") {
		if rest, ok := strings.CutPrefix(r, "B:"); ok {
			edits.message = strings.Contains(rest, "SENTINEL")
		}
	}
	for _, r := range reports("event_isolation:") {
		if rest, ok := strings.CutPrefix(r, "B:"); ok {
			edits.list = rest != "3"
		}
	}
	return edits
}

// contextEditsReached is what the second handler reads: every cell the session's dispatch runs reads both edits. The Lisp probe can only assign event.messages a new vector
// (the SDK decodes arrays as simple vectors, which cannot grow in place); the native SDKs carry an assigned event.messages of context (TestAssignedEventMembersFollowPi), where
// Pi carries only an in-place edit of its one list.
func contextEditsReached() contextEdit {
	return contextEdit{message: true, list: true}
}

// TestConformance_ContextEditsInASessionReachTheNextHandler pins what a session's context dispatch (Runner.EmitContext and EmitContextWithSystem, the calls the Session makes; not
// Runner.Emit, which the isolation row uses) hands the second handler of one extension after the first edited the conversation in place. In Pi the handlers of one dispatch hold
// the same message objects and, for a context event, the same list (runner.ts emitContext), so the second handler reads both edits.
func TestConformance_ContextEditsInASessionReachTheNextHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
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
			for _, phase := range []string{"context", "context_with_system"} {
				t.Run(phase, func(t *testing.T) {
					if got, want := contextEditsOf(t, h, phase), contextEditsReached(); got != want {
						t.Errorf("second handler of one extension read %+v, want %+v", got, want)
					}
				})
			}
		})
	}
}

// TestConformance_ContextListEditsInASessionReachTheNextExtension is the same dispatch across two extensions of one packed cell: the first extension's handler appends a message
// to event.messages in place and the peer extension's handler, called after it, reads the list with it (3 messages sent, 4 read), as in Pi's one list.
func TestConformance_ContextListEditsInASessionReachTheNextExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, peer := range isolationPeerCases() {
		t.Run(peer.name, func(t *testing.T) {
			h := peer.make(t)
			t.Cleanup(func() {
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for _, phase := range []string{"context", "context_with_system"} {
				t.Run(phase, func(t *testing.T) {
					h.ui.ClearRecorded()
					runConformanceCommandArgs(t, h, "event_isolation_probe", phase)
					messages := []extension.AgentMessage{wireAgentMessage(map[string]any{"role": "user", "content": "orig", "timestamp": float64(1700000000000)})}
					ctx := boundarywire.WithContextEncodings(context.Background(), &boundarywire.Encodings{})
					var err error
					if phase == "context" {
						_, err = h.runner.EmitContext(ctx, messages)
					} else {
						_, err = h.runner.EmitContextWithSystem(ctx, messages)
					}
					if err != nil {
						t.Fatal(err)
					}
					var peerRead string
					pollUntilConformance(t, "the peer reports", func() bool {
						for _, n := range h.ui.Recorded() {
							if rest, ok := strings.CutPrefix(n, "event_isolation_peer:"); ok {
								peerRead = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
								return true
							}
						}
						return false
					})
					if peerRead != "2" {
						t.Errorf("the peer extension read %s messages, want 2 (the one sent and the one the first extension's handler appended in place)", peerRead)
					}
				})
			}
		})
	}
}

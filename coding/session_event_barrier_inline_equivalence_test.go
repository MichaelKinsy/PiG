//go:build !race

package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// volatileEventFields are the members a rerun changes: clocks, generated entry and call identifiers and the faux provider's per-instance API name.
//
//portlint:allow regexfold test-only normalizer of Go-encoded event JSON with ASCII member names; no Pi counterpart
var volatileEventFields = regexp.MustCompile(`"((?i:timestamp|durationMs)|[A-Za-z]*(?:id|Id|ID)|cwd|path|api)":("[^"]*"|-?[0-9.]+)`)

// sessionEntryID is a generated session entry ID wherever it appears, such as in turn_end's toolResultEntryIds.
var sessionEntryID = regexp.MustCompile(`"[0-9a-f]{8}"`)

// deliveryLog records, in order, what one observer saw: the JSON of each value at the point it was delivered.
type deliveryLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *deliveryLog) add(source string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(fmt.Sprintf("%q", err.Error()))
	}
	l.mu.Lock()
	normalized := sessionEntryID.ReplaceAllString(volatileEventFields.ReplaceAllString(string(encoded), `"$1":<v>`), `"<entry>"`)
	l.entries = append(l.entries, fmt.Sprintf("%s %T ", source, value)+normalized)
	l.mu.Unlock()
}

// recordedDelivery runs one scripted prompt (a streamed write tool call, then a streamed text) with an in-process extension and a Session listener. hooks is the interleaving of every Session listener call and extension handler call; events is what Events delivered, when drained. executor forces every event onto the Session executor goroutine, the delivery of 4cfaf24153; otherwise producers execute their events inline whenever nothing is queued ahead. drain reads Events as RPC mode does; otherwise the Session discards Events as print mode does.
func recordedDelivery(t *testing.T, executor, drain bool) (hooks, events []string) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	log, wire := &deliveryLog{}, &deliveryLog{}
	size := 4
	faux := ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: &size, Max: &size}, Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 1_000_000, MaxTokens: 100_000}}})
	stamp := int64(1)
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxThinking("plan the file"), ai.FauxToolCall("write", map[string]any{"path": "out.txt", "content": strings.Repeat("abcdefgh", 40)}, &ai.FauxToolCallOptions{ID: "call_1"})}, ai.FauxAssistantMessageOptions{StopReason: string(ai.StopReasonToolUse), Timestamp: &stamp}),
		ai.FauxAssistantMessage(ai.FauxContentString(strings.Repeat("done ", 30)), ai.FauxAssistantMessageOptions{Timestamp: &stamp}),
	})
	handler := func(name string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			if len(args) > 0 {
				log.add("extension "+name, args[0])
			}
			return nil, nil
		}
	}
	handlers := map[string][]extension.HandlerFn{}
	for _, name := range []string{"agent_start", "turn_start", "message_start", "message_update", "message_end", "tool_execution_start", "tool_execution_end", "turn_end", "agent_end"} {
		handlers[name] = []extension.HandlerFn{handler(name)}
	}
	session, err := NewSession(services, SessionOptions{Model: faux.GetModel(), NoSession: true, ResourceLoader: NoResources, SystemPrompt: "Test",
		Runner: inproc.NewRunner([]extension.Extension{{Path: "/delivery", Handlers: handlers}}, services.CWD())})
	if err != nil {
		t.Fatal(err)
	}
	if executor {
		// No producer ever finds the executor idle: every event goes through rawEvents.
		session.queued.Add(1 << 40)
	}
	session.Subscribe(func(event agent.AgentEvent) { log.add("listener", event) })
	drained := make(chan struct{})
	if drain {
		go func() {
			defer close(drained)
			for event := range session.Events() {
				if !AcknowledgeEvent(event) {
					// The consumer runs on its own goroutine and a streamed message observes the live stream when it is encoded, so only the sequence is deterministic here, with either executor.
					wire.add("events", fmt.Sprintf("%T", event))
				}
			}
		}()
	} else {
		session.DiscardEvents()
		close(drained)
	}
	ctx := context.Background()
	if _, err := session.Send(ctx, "write the file"); err != nil {
		t.Fatal(err)
	}
	if err := session.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	<-drained
	return slices.Clone(log.entries), slices.Clone(wire.entries)
}

// Executing Session events on the producer goroutine changes no observable delivery: the Session listener and the extension handlers see the same events, with the same message state, in the same interleaving as when the executor goroutine runs every event (4cfaf24153), and Events delivers the same sequence, for print mode (Events discarded) and for an Events consumer (RPC mode).
func TestSessionInlineDeliveryMatchesExecutorDelivery(t *testing.T) {
	for _, drain := range []bool{false, true} {
		t.Run(fmt.Sprintf("drain=%v", drain), func(t *testing.T) {
			want, wantEvents := recordedDelivery(t, true, drain)
			got, gotEvents := recordedDelivery(t, false, drain)
			updates := 0
			for _, entry := range want {
				if strings.HasPrefix(entry, "listener agent.MessageUpdateEvent ") {
					updates++
				}
			}
			if updates < 20 || drain && len(wantEvents) < updates {
				t.Fatalf("the reference run delivered %d streamed updates to the listener and %d events, want a streamed response", updates, len(wantEvents))
			}
			t.Logf("%d hook calls, %d streamed updates, %d Events", len(want), updates, len(wantEvents))
			compareDelivery(t, "hook", got, want)
			compareDelivery(t, "Events", gotEvents, wantEvents)
		})
	}
}

func compareDelivery(t *testing.T, what string, got, want []string) {
	t.Helper()
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			at := 0
			for at < min(len(got[i]), len(want[i])) && got[i][at] == want[i][at] {
				at++
			}
			from := max(0, at-200)
			t.Fatalf("%s delivery %d of %d/%d differs at byte %d (%.80s):\ninline:   …%.400s\nexecutor: …%.400s", what, i, len(got), len(want), at, want[i], got[i][from:], want[i][from:])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("inline %s delivery has %d entries, executor delivery %d", what, len(got), len(want))
	}
}

package codingagent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi finalizes the Responses output slot in place. A repeated or absent identity
// on output_item.done must not strand the streaming card and add a second card.
func TestInteractiveResponsesToolUpdatesOneCard(t *testing.T) {
	for _, fields := range []struct{ name, json string }{
		{"repeated", `"id":"fc_original","call_id":"call_original",`},
		{"omitted", ""},
		{"different", `"id":"fc_final","call_id":"call_final",`},
	} {
		t.Run(fields.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if requests.Add(1) == 1 {
					_, _ = fmt.Fprintf(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_original","call_id":"call_original","name":"read","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"row-probe.txt\"}"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call",%s"name":"read","arguments":"{\"path\":\"row-probe.txt\"}"}}

`, fields.json)
				}
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			}))
			defer server.Close()
			provider := ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "unused", ProviderID: "probe", Model: "probe"})
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			var mu sync.Mutex
			var events []agent.AgentEvent
			a := agent.NewAgent(agent.AgentOptions{
				Model: &ai.Model{ID: "probe", Provider: provider},
				Tools: []agent.AgentTool{&stubTool{name: "read"}},
				OnEvent: func(event agent.AgentEvent) {
					mu.Lock()
					defer mu.Unlock()
					events = append(events, event)
				},
			})
			if _, err := a.Send(t.Context(), "Read the probe"); err != nil {
				t.Fatal(err)
			}
			m, _ := newTickRenderProbe(t, "regular")
			var calls int
			for _, event := range events {
				if _, ok := event.(agent.ToolExecutionStartEvent); ok {
					calls++
				}
				m.handleAgentEvent(event)
			}
			if calls != 1 {
				t.Fatalf("fixture executed %d tools, want the single scripted read", calls)
			}
			if len(m.toolOrder) != calls {
				t.Fatalf("transcript has %d cards for %d executed calls", len(m.toolOrder), calls)
			}
			if m.toolOrder[0].State != tui.ToolStateDone {
				t.Fatalf("streaming card left pending: state=%d", m.toolOrder[0].State)
			}
			chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
			if got := strings.Count(chat, "read row-probe.txt"); got != calls {
				t.Fatalf("rendered %d tool headers for %d calls:\n%s", got, calls, chat)
			}
		})
	}
}

package agent

// pi: packages/agent/src/agent.ts:259 (`this.transport = runtimeOptions.transport ?? "auto"`) and :485 (createLoopConfig forwards `this.transport`)

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's Agent never has an unset transport: the constructor defaults it to "auto" and every run forwards it, so a provider whose behaviour depends on the transport (the Codex cached context) sees "auto" for an agent
// built without one. A named transport stays as given.
func TestNewAgentDefaultsTheTransportToAuto(t *testing.T) {
	for _, tc := range []struct{ name, given, want string }{
		{"unset", "", "auto"},
		{"auto", "auto", "auto"},
		{"sse", "sse", "sse"},
		{"websocket", "websocket", "websocket"},
		{"websocket-cached", "websocket-cached", "websocket-cached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedProvider{respond: replyText("ok")}
			a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Transport: ai.Transport(tc.given)})
			if got := a.Transport(); got != ai.Transport(tc.want) {
				t.Errorf("Transport() = %q, want %q", got, tc.want)
			}
			mustSend(t, a, "hi")
			if got := provider.request(1).opts.Transport; got != ai.Transport(tc.want) {
				t.Errorf("the first request carries transport %q, want %q", got, tc.want)
			}
		})
	}
}

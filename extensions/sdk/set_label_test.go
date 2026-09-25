package sdk

import (
	"strings"
	"testing"
)

// Upstream setLabel throws when the session cannot record the label; the SDK
// returns the host's failure instead of discarding it.
func TestSetLabelReturnsHostError(t *testing.T) {
	host := newMockHost(t)
	defer host.close()
	ext := New("label-test")
	ext.Command("label", "label an entry", func(ctx Context, _ string) error {
		return ctx.SetLabel("missing-entry", "tag")
	})
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-label", Request: &requestMsg{Method: "command", Tool: "label"}})

	var response envelope
	for {
		env := host.readEnvelopeRaw(t)
		if env.Type == msgCall && env.Call != nil && env.Call.Method == "setLabel" {
			host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Error: &errorInfo{Message: "Entry missing-entry not found"}}})
			continue
		}
		if env.Type == msgResponse {
			response = env
			break
		}
	}
	if response.Response == nil || response.Response.Error == nil || !strings.Contains(response.Response.Error.Message, "Entry missing-entry not found") {
		t.Fatalf("command response = %+v, want the host's setLabel error", response.Response)
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

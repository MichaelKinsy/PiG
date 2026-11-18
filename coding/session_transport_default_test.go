package coding

// pi: packages/coding-agent/src/core/sdk.ts:435 (`transport: settingsManager.getTransport()`), settings-manager.ts:811 (`this.settings.transport ?? SETTINGS_DEFAULTS.transport`), settings-defaults.ts:5 (`transport: "auto"`), agent.ts:259 and :485

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// transportCapture is a provider that records the transport of every request the Session sends it, then answers from the wrapped scripted provider.
type transportCapture struct {
	ai.Provider
	mu   sync.Mutex
	seen []ai.Transport
}

func (c *transportCapture) Stream(ctx context.Context, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	c.mu.Lock()
	c.seen = append(c.seen, options.Transport)
	c.mu.Unlock()
	return c.Provider.Stream(ctx, request, options)
}

// Pi's createAgentSession hands the Agent the settings' transport, "auto" when the settings name none, so no request of a Pi session has an unset transport. The caller's SessionOptions.Transport (PiG's own option)
// takes the settings' place.
func TestSessionSendsTheSettingsTransportWithAutoAsTheDefault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
		option   ai.Transport
		want     ai.Transport
	}{
		{"no transport in the settings", `{}`, "", ai.TransportAuto},
		{"auto", `{"transport":"auto"}`, "", ai.TransportAuto},
		{"sse", `{"transport":"sse"}`, "", ai.TransportSSE},
		{"websocket", `{"transport":"websocket"}`, "", ai.TransportWebSocket},
		{"websocket-cached", `{"transport":"websocket-cached"}`, "", ai.TransportWebSocketCached},
		{"the caller's option outranks the settings'", `{"transport":"sse"}`, ai.TransportWebSocket, ai.TransportWebSocket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &transportCapture{Provider: &scriptedProvider{responses: []scriptedResponse{boundaryReply("ok", ai.StopReasonStop, 0)}, wrap: true}}
			h := newRecoveryHarness(t, harnessOptions{provider: capture, settings: tc.settings, transport: tc.option})
			if err := h.session.Prompt(t.Context(), "hi", nil); err != nil {
				t.Fatal(err)
			}
			h.settle(t)
			capture.mu.Lock()
			defer capture.mu.Unlock()
			if len(capture.seen) != 1 || capture.seen[0] != tc.want {
				t.Errorf("the provider saw transports %q, want [%q]", capture.seen, tc.want)
			}
		})
	}
}

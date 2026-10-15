package mcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts:418 arms a request's timeout from `options.timeoutMs ?? this.options.requestTimeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS` (38: 30_000):
// the per-request value wins, then the client's, then 30 s. The Go mechanic for "unset" is the zero value (client.go RequestTimeoutMs).
// Pi: packages/mcp/src/client.ts:418, :38
func TestClientRequestTimeoutPrecedenceIsRequestThenClientThenDefault(t *testing.T) {
	for _, tc := range []struct {
		name            string
		client, request int
		want            time.Duration
	}{
		{"request beats client", 2000, 500, 500 * time.Millisecond},
		{"client beats default", 2000, 0, 2000 * time.Millisecond},
		{"default when neither is set", 0, 0, 30 * time.Second},
		{"request beats default", 0, 700, 700 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{}
			clientTransport, server := createServer(t)
			client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "c", Version: "1"}, RequestTimeoutMs: tc.client})
			mcp.SetClientAfterFuncForTest(client, clock.AfterFunc)
			if _, err := client.Connect(t.Context(), clientTransport); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			server.setHandler("tools/list", func(mcp.JSONRPCMessage) (any, error) { return blockUntilTestEnds(t) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() { _, _ = client.ListTools(ctx, mcp.RequestOptions{TimeoutMs: tc.request}) }()
			waitForTimers(t, clock, 1)
			clock.mu.Lock()
			defer clock.mu.Unlock()
			var armed []time.Duration
			for _, timer := range clock.timers {
				if !timer.stopped && !timer.fired {
					armed = append(armed, timer.at)
				}
			}
			if len(armed) != 1 || armed[0] != tc.want {
				t.Fatalf("armed timeouts = %v, want [%v]", armed, tc.want)
			}
		})
	}
}

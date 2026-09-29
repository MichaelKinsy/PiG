package sdk

import (
	"encoding/json"
	"net"
	"testing"
	"testing/synctest"
)

// Pi 0.87.1 packages/ai/src/utils/event-stream.ts:43-89: cancellation alone does not end the producer's stream. The stable dispatcher waits only for events, not a separate result.
func TestProviderStreamShutdownDrainsUnsettledResult(t *testing.T) {
	for _, method := range []string{"stream", "streamSimple", "fetchDeferred"} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, server := net.Pipe()
				ext := New("unsettled-provider")
				stream := CreateAssistantMessageEventStream()
				produce := func(map[string]any, map[string]any, ProviderStreamOptions) (*ModelEventStream, error) {
					return stream, nil
				}
				if err := ext.RegisterNativeProvider(&Provider{ID: "unsettled", Name: "Unsettled", Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Key", Resolve: func(APIKeyAuthInput) (*AuthResult, error) { return nil, nil }}}, GetModels: func() ([]map[string]any, error) { return nil, nil }, Stream: produce, StreamSimple: produce, FetchDeferred: produce}); err != nil {
					t.Fatal(err)
				}
				returned := make(chan error, 1)
				go func() { returned <- ext.RunWithConn(client) }()
				host := newConn(server)
				host.start()
				defer func() { _ = server.Close(); _ = client.Close(); <-host.done }()
				registration := <-host.incoming
				if err := host.send(envelope{Type: msgReady, Ready: &readyMsg{}}); err != nil {
					t.Fatal(err)
				}
				args, err := json.Marshal(map[string]any{"method": method, "params": map[string]any{"model": map[string]any{}, "context": map[string]any{}, "handle": map[string]any{}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := host.send(envelope{Type: msgRequest, ID: "stream", Request: &requestMsg{Method: "provider_stream", Tool: registration.Register.Providers[0].Native.Key, Args: args}}); err != nil {
					t.Fatal(err)
				}
				for {
					frame := <-host.incoming
					if frame.Notify == nil || frame.Notify.Method != "tool_update" {
						continue
					}
					break
				}
				synctest.Wait()
				if err := host.send(envelope{Type: msgShutdown}); err != nil {
					t.Fatal(err)
				}
				if err := <-returned; err != nil {
					t.Errorf("SDK-owned waiter prevented graceful shutdown: %v", err)
				}
				select {
				case <-stream.done:
					t.Error("transport settled the caller-owned result")
				default:
				}
				stream.End(map[string]any{"late": true})
				ext.requestWG.Wait()
				if stream.Result()["late"] != true {
					t.Error("transport replaced the caller's late result")
				}
			})
		})
	}
}

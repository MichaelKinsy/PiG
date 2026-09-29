package sdk

import (
	"encoding/json"
	"net"
	"testing"
)

func TestContextScopedModelsWireResults(t *testing.T) {
	for _, tc := range []struct{ name, payload, failure string }{
		{"empty", `[]`, ""},
		{"ordered", `[{"model":{"id":"second"},"thinkingLevel":"high"},{"model":{"id":"first"}}]`, ""},
		{"host-error", "", "scope_failed: scope unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
			connection := newConn(client)
			connection.start()
			var models []ScopedModel
			done := make(chan error, 1)
			go func() {
				var err error
				models, err = (Context{ext: &Extension{conn: connection}}).ScopedModels()
				done <- err
			}()
			host := &mockHost{nc: server}
			call := host.readEnvelope(t)
			if call.Call == nil || call.Call.Method != "getScopedModels" {
				t.Fatalf("call=%+v", call)
			}
			response := &callResultMsg{Result: json.RawMessage(tc.payload)}
			if tc.failure != "" {
				response.Error = &errorInfo{Code: "scope_failed", Message: "scope unavailable"}
			}
			host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: response})
			err := <-done
			if tc.failure != "" {
				if err == nil || err.Error() != tc.failure {
					t.Fatalf("error=%v; want %q", err, tc.failure)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(models)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.payload {
				t.Fatalf("scope=%s; want %s", encoded, tc.payload)
			}
		})
	}
}

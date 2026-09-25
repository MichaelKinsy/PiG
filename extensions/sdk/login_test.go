package sdk

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
)

func TestContextSetLoginSendsCanonicalDefinitionAsCallArguments(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })

	connection := newConn(client)
	connection.start()
	t.Cleanup(func() { _ = client.Close() })

	definition := LoginDefinition{
		Brand:       []string{"brand"},
		Hero:        []string{"hero"},
		Mascot:      []string{"mascot"},
		Palette:     map[string]string{"X": "#123ABC"},
		Name:        "Example Bot",
		Description: "Custom agent",
		Tagline:     "Reason in public.",
	}
	result := make(chan error, 1)
	go func() {
		result <- (Context{ext: &Extension{conn: connection}}).SetLogin(definition)
	}()

	host := &mockHost{nc: server}
	call := host.readEnvelope(t)
	if call.Type != msgCall || call.Call == nil || call.Call.Method != "ui.setLogin" {
		t.Fatalf("call = %+v", call)
	}
	var got map[string]any
	if err := json.Unmarshal(call.Call.Args, &got); err != nil {
		t.Fatalf("decode call args: %v", err)
	}
	want := map[string]any{
		"brand":       []any{"brand"},
		"hero":        []any{"hero"},
		"mascot":      []any{"mascot"},
		"palette":     map[string]any{"X": "#123ABC"},
		"name":        "Example Bot",
		"description": "Custom agent",
		"tagline":     "Reason in public.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	if err := <-result; err != nil {
		t.Fatalf("SetLogin: %v", err)
	}
}

func TestContextSetLoginSurfacesHostError(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })

	connection := newConn(client)
	connection.start()
	t.Cleanup(func() { _ = client.Close() })

	result := make(chan error, 1)
	go func() {
		result <- (Context{ext: &Extension{conn: connection}}).SetLogin(LoginDefinition{})
	}()

	host := &mockHost{nc: server}
	call := host.readEnvelope(t)
	host.writeEnvelope(t, envelope{
		Type: msgCallResult,
		ID:   call.ID,
		CallResult: &callResultMsg{Error: &errorInfo{
			Code:    "invalid_arguments",
			Message: "invalid login definition brand",
		}},
	})
	if err := <-result; err == nil || err.Error() != "invalid_arguments: invalid login definition brand" {
		t.Fatalf("SetLogin error = %v", err)
	}
}

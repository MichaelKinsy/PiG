package sdk

import (
	"encoding/json"
	"net"
	"testing"
)

// Pi runner.ts:809-886 keeps a captured Context live until runtime invalidation. Completing its originating command does not invalidate later host getters.
func TestRetainedContextCallsHostAfterCommandResponse(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	connection, host := newConn(client), newConn(server)
	connection.start()
	host.start()
	ext := New("retained-context")
	ext.conn = connection
	captured := make(chan Context, 1)
	ext.Command("capture", "Capture the original context", func(ctx Context, _ string) error {
		captured <- ctx
		return nil
	})
	done := make(chan struct{})
	go func() {
		ext.handleRequest("origin", &requestMsg{Method: "command", Tool: "capture"})
		close(done)
	}()
	next := func(kind string) envelope {
		for env := range host.incoming {
			if env.Type == kind {
				return env
			}
		}
		t.Fatalf("connection closed before %s", kind)
		return envelope{}
	}
	next(msgResponse)
	ctx := <-captured
	for _, want := range []string{"nondefault retained editor", "", "replacement editor"} {
		result := make(chan string, 1)
		go func() {
			text, err := ctx.GetEditorText()
			if err != nil {
				text = "error: " + err.Error()
			}
			result <- text
		}()
		call := next(msgCall)
		if call.Call == nil || call.Call.Method != "ui.getEditorText" {
			t.Fatalf("call = %+v", call)
		}
		if call.Call.ParentRequestID != "" {
			t.Errorf("retained context sent completed parent %q", call.Call.ParentRequestID)
		}
		payload, err := json.Marshal(map[string]string{"text": want})
		if err != nil {
			t.Fatal(err)
		}
		if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: payload}}); err != nil {
			t.Fatal(err)
		}
		got := <-result
		if got != want {
			t.Fatalf("getter = %q; want %q", got, want)
		}
	}
	<-done
}

package sdk

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// Source: runner.ts emitContext uses `handlerResult?.messages` as the
// replacement list whenever it is not nullish, and restoreSystemMessages keeps
// the current list only when sameMessages finds the same message objects in the
// same order. Pi has no typed-slice distinction: an array of message objects is
// a replacement whatever the author called its element type. A Go handler that
// returns []map[string]any must therefore replace the context, keep identity
// when it holds the same message objects, and a value that is not a message
// list must reach the host decoder (which reports a handler error) instead of
// being reported as an unchanged context.
func TestContextEventResultHonorsTypedMessageSlices(t *testing.T) {
	dir, err := os.MkdirTemp("", "pigctx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "h.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host := &mockHost{listener: listener, sockPath: sockPath}
	defer host.close()

	ext := New("context-typed")
	// Handler 1: drop the first message through a []map[string]any list.
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		messages := data["messages"].([]any)
		kept := []map[string]any{messages[1].(map[string]any)}
		return map[string]any{"messages": kept}, nil
	})
	// Handler 2: the same message objects in the same order, in a new typed list.
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		messages := data["messages"].([]any)
		same := []map[string]any{messages[0].(map[string]any), messages[1].(map[string]any)}
		return map[string]any{"messages": same}, nil
	})
	// Handler 3: a value that is not a message list.
	ext.OnEvent(EventContext, func(Context, map[string]any) (any, error) {
		return map[string]any{"messages": "not-a-list"}, nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: dir, Width: 80}})

	args, err := json.Marshal(map[string]any{
		"type": "context",
		"messages": []any{
			map[string]any{"role": "user", "content": "first", "timestamp": 1},
			map[string]any{"role": "user", "content": "second", "timestamp": 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := func(handlerID int) *responseMsg {
		t.Helper()
		id := "ctx-" + strconv.Itoa(handlerID)
		host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "event", Event: EventContext, HandlerID: handlerID, Args: args}})
		resp := host.readEnvelope(t)
		if resp.Type != msgResponse || resp.ID != id || resp.Response == nil {
			t.Fatalf("handler %d response = %+v", handlerID, resp)
		}
		return resp.Response
	}
	type contextReport struct {
		Messages  []map[string]any `json:"messages"`
		Unchanged *bool            `json:"_pigContextUnchanged"`
	}

	dropped := dispatch(1)
	if dropped.Error != nil {
		t.Fatalf("dropped handler error = %+v", dropped.Error)
	}
	var droppedReport contextReport
	if err := json.Unmarshal(dropped.Result, &droppedReport); err != nil {
		t.Fatalf("dropped result %s: %v", dropped.Result, err)
	}
	if droppedReport.Unchanged == nil || *droppedReport.Unchanged {
		t.Fatalf("typed replacement reported unchanged: %s", dropped.Result)
	}
	if len(droppedReport.Messages) != 1 || droppedReport.Messages[0]["content"] != "second" {
		t.Fatalf("typed replacement messages = %s", dropped.Result)
	}

	same := dispatch(2)
	if same.Error != nil {
		t.Fatalf("same handler error = %+v", same.Error)
	}
	var sameReport contextReport
	if err := json.Unmarshal(same.Result, &sameReport); err != nil {
		t.Fatalf("same result %s: %v", same.Result, err)
	}
	if sameReport.Unchanged == nil || !*sameReport.Unchanged {
		t.Fatalf("same message objects reported as a replacement: %s", same.Result)
	}
	if len(sameReport.Messages) != 2 || sameReport.Messages[0]["content"] != "first" || sameReport.Messages[1]["content"] != "second" {
		t.Fatalf("same messages = %s", same.Result)
	}

	malformed := dispatch(3)
	if malformed.Error != nil {
		t.Fatalf("malformed handler error = %+v", malformed.Error)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(malformed.Result, &forwarded); err != nil {
		t.Fatalf("malformed result %s: %v", malformed.Result, err)
	}
	if !reflect.DeepEqual(forwarded, map[string]any{"messages": "not-a-list"}) {
		t.Fatalf("malformed result was not forwarded to the host decoder: %s", malformed.Result)
	}
	// The host decodes context results as extension.ContextEventResult; this
	// value must fail that decode so the host reports a handler error.
	var hostShape struct {
		Messages []any `json:"messages"`
	}
	if json.Unmarshal(malformed.Result, &hostShape) == nil {
		t.Fatalf("malformed result decodes as a message list: %s", malformed.Result)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

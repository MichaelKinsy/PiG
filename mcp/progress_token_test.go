package mcp_test

import (
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts:403-407 sends progressToken (the request id) in params._meta only when the call has onProgress, keeping the caller's own _meta members;
// client.ts:531-542 routes a notifications/progress to the request whose id the token names (a string "N" is not the number N: isJsonRpcId and a
// Map keyed by the value), ignores a notification whose progress is not a number, and passes the notification's params to onProgress; client.ts:575
// forgets the token when the request ends.
func TestProgressTokenRoutesNotificationsToTheRequestThatCarriesIt(t *testing.T) {
	clientTransport, server := createServer(t)
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "2.0.0"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	var mu sync.Mutex
	var sentParams []map[string]any
	var lateToken any
	server.setHandler("tools/call", func(request mcp.JSONRPCMessage) (any, error) {
		params := paramsOf(t, request)
		mu.Lock()
		sentParams = append(sentParams, params)
		mu.Unlock()
		meta, _ := params["_meta"].(map[string]any)
		token := meta["progressToken"]
		if token == nil {
			return map[string]any{"content": []any{}}, nil
		}
		lateToken = token
		send := func(body map[string]any) {
			raw, _ := json.Marshal(body)
			_ = server.transport.Send(mcp.NewNotification("notifications/progress", raw))
		}
		send(map[string]any{"progressToken": strconv.Itoa(int(token.(float64))), "progress": 1}) // a string token: not this request's number
		send(map[string]any{"progressToken": token, "progress": "1"})                            // progress must be a number
		send(map[string]any{"progressToken": 12345, "progress": 1})                              // a token no request carries
		send(map[string]any{"progressToken": token, "progress": 3, "total": 4, "message": "m"})  // delivered
		return map[string]any{"content": []any{}}, nil
	})

	delivered := make(chan mcp.ProgressNotification, 8)
	if _, err := client.CallTool(t.Context(), "tool", map[string]any{"x": 1}, mcp.RequestOptions{OnProgress: func(p mcp.ProgressNotification) { delivered <- p }}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-delivered:
		wantToken, _ := lateToken.(float64)
		if p.Progress != 3 || p.ProgressToken.String() != mcp.NumberID(wantToken).String() {
			t.Errorf("delivered %+v, want progress 3 for token %v", p, lateToken)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the matching progress notification was not delivered")
	}
	select {
	case extra := <-delivered:
		t.Errorf("a notification the client must ignore reached onProgress: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	// After the request ended the token is forgotten (client.ts:575): a late notification reaches nobody.
	raw, _ := json.Marshal(map[string]any{"progressToken": lateToken, "progress": 9})
	_ = server.transport.Send(mcp.NewNotification("notifications/progress", raw))
	select {
	case late := <-delivered:
		t.Errorf("a progress notification after the request ended reached onProgress: %+v", late)
	case <-time.After(100 * time.Millisecond):
	}

	// Without onProgress no progressToken is sent, and params pass through untouched (client.ts:403-407).
	if _, err := client.CallTool(t.Context(), "tool", map[string]any{"y": 2}, mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	first, second := sentParams[0], sentParams[len(sentParams)-1]
	if meta, _ := first["_meta"].(map[string]any); meta == nil || meta["progressToken"] == nil {
		t.Errorf("a call with onProgress sent no progressToken: %v", first)
	}
	if _, present := second["_meta"]; present {
		t.Errorf("a call without onProgress sent _meta: %v", second)
	}
}

package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/client.ts cancelRequest: the notification params are { requestId: id, ...(reason ? { reason } : {}) }, so requestId precedes reason on the wire.
func TestClientCancelledNotificationParamsKeepUpstreamMemberOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		cause error
		want  string
	}{
		"reason": {cause: errors.New("stop"), want: `{"requestId":2,"reason":"stop"}`},
	} {
		t.Run(name, func(t *testing.T) {
			client, server := connect(t)
			server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) { select {} })
			ctx, cancel := context.WithCancelCause(t.Context())
			errs := make(chan error, 1)
			go func() {
				_, err := client.CallTool(ctx, "wait", map[string]any{}, mcp.RequestOptions{})
				errs <- err
			}()
			waitFor(t, func() bool { return recordedMethod(server, "tools/call") })
			cancel(tc.cause)
			<-errs
			waitFor(t, func() bool { return recordedMethod(server, "notifications/cancelled") })
			for _, m := range server.recorded() {
				if m.Method == "notifications/cancelled" && string(m.Params) != tc.want {
					t.Fatalf("params = %s, want %s", m.Params, tc.want)
				}
			}
		})
	}
}

// The CancelledNotification type is the notification's params: it encodes requestId first and omits an empty reason, and decodes both id kinds.
func TestCancelledNotificationEncodesAndDecodesParams(t *testing.T) {
	data, err := json.Marshal(mcp.CancelledNotification{RequestID: mcp.NumberID(7), Reason: "why"})
	if err != nil || string(data) != `{"requestId":7,"reason":"why"}` {
		t.Fatalf("encoded = %s, err = %v", data, err)
	}
	data, err = json.Marshal(mcp.CancelledNotification{RequestID: mcp.StringID("r-1")})
	if err != nil || string(data) != `{"requestId":"r-1"}` {
		t.Fatalf("encoded without reason = %s, err = %v", data, err)
	}
	var decoded mcp.CancelledNotification
	if err := json.Unmarshal([]byte(`{"requestId":"abc","reason":"done"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RequestID.String() != "abc" || !decoded.RequestID.IsString() || decoded.Reason != "done" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

// Ports packages/mcp/src/protocol/content.ts ContentAnnotations: audience, priority and lastModified survive a content block round trip.
func TestContentAnnotationsDecodeAndEncodeOnBlocks(t *testing.T) {
	var blocks []mcp.ContentBlock
	if err := json.Unmarshal([]byte(`[{"type":"text","text":"a","annotations":{"audience":["user","assistant"],"priority":0.5,"lastModified":"2025-01-01T00:00:00Z"}},{"type":"text","text":"b"}]`), &blocks); err != nil {
		t.Fatal(err)
	}
	got := blocks[0].Annotations
	if got == nil || len(got.Audience) != 2 || got.Audience[1] != "assistant" || got.Priority == nil || *got.Priority != 0.5 || got.LastModified != "2025-01-01T00:00:00Z" {
		t.Fatalf("annotations = %+v", got)
	}
	if blocks[1].Annotations != nil {
		t.Fatalf("absent annotations = %+v", blocks[1].Annotations)
	}
	out, err := json.Marshal(blocks[0])
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]json.RawMessage
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatal(err)
	}
	if string(round["annotations"]) != `{"audience":["user","assistant"],"priority":0.5,"lastModified":"2025-01-01T00:00:00Z"}` {
		t.Fatalf("encoded annotations = %s", round["annotations"])
	}
	// Every member is optional upstream, so an empty annotations object stays empty.
	if empty, err := json.Marshal(mcp.ContentAnnotations{}); err != nil || string(empty) != `{}` {
		t.Fatalf("empty annotations = %s, err = %v", empty, err)
	}
}

// Ports packages/mcp/src/protocol/types.ts CallToolResult._meta: the server's _meta object is kept verbatim and re-encoded.
// Pi declares packages/mcp/src/protocol/content.ts:65-70 (CallToolResult): _meta is kept as given.
func TestCallToolResultKeepsMetaVerbatim(t *testing.T) {
	var result mcp.CallToolResult
	if err := json.Unmarshal([]byte(`{"content":[],"_meta":{"progressToken":"p","n":1}}`), &result); err != nil {
		t.Fatal(err)
	}
	if string(result.Meta) != `{"progressToken":"p","n":1}` {
		t.Fatalf("Meta = %s", result.Meta)
	}
	out, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var round struct {
		Meta json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(out, &round); err != nil || string(round.Meta) != `{"progressToken":"p","n":1}` {
		t.Fatalf("re-encoded = %s, err = %v", out, err)
	}
	var absent mcp.CallToolResult
	if err := json.Unmarshal([]byte(`{"content":[]}`), &absent); err != nil || absent.Meta != nil {
		t.Fatalf("absent Meta = %s, err = %v", absent.Meta, err)
	}
}

func recordedMethod(server *testServer, method string) bool {
	for _, m := range server.recorded() {
		if m.Method == method {
			return true
		}
	}
	return false
}

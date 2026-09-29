package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexLongSessionRetainsLogicalCacheIdentity(t *testing.T) {
	peer := newCodexWSPeer(t, func(int, int, map[string]any) []string { return []string{codexWSTerminal("resp_long")} })
	session := strings.Repeat("x", 67)
	_, _ = codexWSResult(t, peer.provider(t, "acc_test"), codexUpstreamContext(), StreamOptions{Transport: TransportAuto, SessionID: session})
	headers, _, _ := peer.snapshot()
	if len(headers) != 1 || headers[0].Get("session-id") != strings.Repeat("x", 64) || GetOpenAICodexWebSocketDebugStats(session) == nil || GetOpenAICodexWebSocketDebugStats(strings.Repeat("x", 64)) != nil {
		t.Fatalf("headers=%v logical=%#v", headers, GetOpenAICodexWebSocketDebugStats(session))
	}
	CloseOpenAICodexWebSocketSessions(session)
	<-peer.closed
	codexWebSocketSessions.mu.Lock()
	_, retained := codexWebSocketSessions.sessions[session]
	codexWebSocketSessions.mu.Unlock()
	if retained {
		t.Fatal("logical session cleanup retained socket")
	}
}

func TestToolSchemaConstrainedSamplingFalseWire(t *testing.T) {
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"optional","parameters":{"type":"object","properties":{"value":{"type":"string"}}},"constrainedSampling":false}`), &tool); err != nil {
		t.Fatal(err)
	}
	if tool.ConstrainedSampling != nil {
		t.Fatalf("explicit false=%#v", tool.ConstrainedSampling)
	}
	if err := json.Unmarshal([]byte(`{"name":"optional","constrainedSampling":true}`), &tool); err == nil {
		t.Fatal("true is not a constrained-sampling union member")
	}
}

package mcp

import (
	"encoding/json"
	"testing"
)

// jsonrpc.ts:1 JsonRpcId = string | number and jsonrpc.ts:89-91 isJsonRpcId accepts a string or a finite number. The parser (jsonrpc.ts:94-105) refuses any
// other id with invalidRequest, and the client reports an unmatched response with the id as a template literal renders it
// (client.ts: `Received response for unknown MCP request ${message.id}`): JavaScript's String(number).
func TestJSONRPCIDAcceptsOnlyAStringOrAFiniteNumber(t *testing.T) {
	for _, id := range []string{`"a"`, `""`, `7`, `1.5`, `-3`, `0`} {
		raw := `{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}`
		message, err := ParseJSONRPCMessage([]byte(raw))
		if err != nil || message.ID == nil || !message.ID.IsSet() {
			t.Errorf("id %s: err=%v message=%+v, want an accepted request id", id, err, message)
		}
	}
	for _, id := range []string{`null`, `true`, `{}`, `[]`} {
		raw := `{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}`
		message, err := ParseJSONRPCMessage([]byte(raw))
		if err == nil && message.IsRequest() {
			t.Errorf("id %s: accepted as a request, want it refused (isJsonRpcId)", id)
		}
	}
}

func TestJSONRPCIDKeepsStringAndNumberApart(t *testing.T) {
	if StringID("1").key() == NumberID(1).key() {
		t.Error("string id \"1\" and number id 1 share a pending-request key: a response with id \"1\" would resolve request 1")
	}
	if !StringID("x").IsString() || NumberID(1).IsString() {
		t.Error("IsString does not tell a string id from a number id")
	}
	for _, id := range []JSONRPCID{StringID("a\"b"), StringID(""), NumberID(0), NumberID(-2.5), NumberID(1e21)} {
		wire, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		var back JSONRPCID
		if err := json.Unmarshal(wire, &back); err != nil || back.key() != id.key() {
			t.Errorf("%s round-trips to %+v (err %v), want %+v", wire, back, err, id)
		}
	}
}

// String is JavaScript's String(id) (an id interpolated into the unknown-response message): exponent form from 1e21 and below 1e-6, -0 as "0".
func TestJSONRPCIDStringIsJavaScriptStringOfTheNumber(t *testing.T) {
	negativeZero := NumberID(0)
	negativeZero.num = -negativeZero.num
	for _, tc := range []struct {
		id   JSONRPCID
		want string
	}{
		{StringID("abc"), "abc"},
		{NumberID(1), "1"},
		{NumberID(-3), "-3"},
		{NumberID(1.5), "1.5"},
		{negativeZero, "0"},
		{NumberID(0.000001), "0.000001"},
		{NumberID(1e-7), "1e-7"},
		{NumberID(123456789012345680000), "123456789012345680000"},
		{NumberID(1e21), "1e+21"},
		{NumberID(-1.5e300), "-1.5e+300"},
	} {
		if got := tc.id.String(); got != tc.want {
			t.Errorf("String(%v) = %q, want %q (JS String)", tc.id, got, tc.want)
		}
	}
}

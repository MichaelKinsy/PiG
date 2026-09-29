package main

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

// Pi rpc-mode.ts:64-80 copies command.id unchanged. JSON.stringify omits undefined, but retains a present empty string on every success/error variant.
func TestRPCOptionalCommandIDRoundTrip(t *testing.T) {
	for _, factory := range []struct {
		name       string
		newCommand func() any
	}{
		{"envelope", func() any { return &RPCCommandEnvelope{} }},
		{"prompt", func() any { return &RPCPromptCommand{} }},
		{"steer", func() any { return &RPCSteerCommand{} }},
		{"follow_up", func() any { return &RPCFollowUpCommand{} }},
		{"bash", func() any { return &RPCBashCommand{} }},
		{"get_state", func() any { return &RPCGetStateCommand{} }},
		{"set_model", func() any { return &RPCSetModelCommand{} }},
	} {
		t.Run(factory.name, func(t *testing.T) {
			for _, id := range []*string{nil, new(""), new("request-1")} {
				input, err := json.Marshal(struct {
					ID   *string `json:"id,omitempty"`
					Type string  `json:"type"`
				}{id, factory.name})
				if err != nil {
					t.Fatal(err)
				}
				command := factory.newCommand()
				if err := json.Unmarshal(input, command); err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(command)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(encoded, &fields); err != nil {
					t.Fatal(err)
				}
				value, present := fields["id"]
				if present != (id != nil) || (id != nil && value != *id) {
					t.Fatalf("input=%s lost ID in %s", input, encoded)
				}
			}
		})
	}
}

func BenchmarkRPCCorrelationRoundTrip(b *testing.B) {
	for _, tc := range []struct{ name, request string }{
		{"omitted", `{"type":"get_state"}`},
		{"empty", `{"type":"get_state","id":""}`},
		{"nonempty", `{"type":"get_state","id":"request-1"}`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			input := []byte(tc.request)
			b.ReportAllocs()
			for b.Loop() {
				env, err := parseRPCCommand(input)
				if err != nil {
					b.Fatal(err)
				}
				writeJSONLine(io.Discard, rpcSuccess(env.ID, env.Type, nil))
			}
		})
	}
}

func TestRPCResponsePreservesRequestIDPresence(t *testing.T) {
	for _, tc := range []struct {
		name, request, prefix string
	}{
		{"omitted", `{"type":"probe"}`, ""},
		{"empty", `{"type":"probe","id":""}`, `"id":"",`},
		{"nonempty", `{"type":"probe","id":"request-1"}`, `"id":"request-1",`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := parseRPCCommand([]byte(tc.request))
			if err != nil {
				t.Fatal(err)
			}
			for _, response := range []struct {
				name  string
				value any
				body  string
			}{
				{"ack", rpcSuccess(env.ID, "probe", nil), `"type":"response","command":"probe","success":true`},
				{"data", rpcSuccess(env.ID, "probe", map[string]string{"value": "ok"}), `"type":"response","command":"probe","success":true,"data":{"value":"ok"}`},
				{"null", rpcSuccessNull(env.ID, "probe"), `"type":"response","command":"probe","success":true,"data":null`},
				{"error", rpcError(env.ID, "probe", "failed"), `"type":"response","command":"probe","success":false,"error":"failed"`},
			} {
				t.Run(response.name, func(t *testing.T) {
					var out bytes.Buffer
					writeJSONLine(&out, response.value)
					want := "{" + tc.prefix + response.body + "}\n"
					if out.String() != want {
						t.Fatalf("response=%s want=%s", out.String(), want)
					}
				})
			}
		})
	}
}

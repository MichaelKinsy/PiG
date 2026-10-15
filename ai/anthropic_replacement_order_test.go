package ai

import (
	"encoding/json"
	"testing"
)

// anthropic-messages.ts:640-650 replaces the params with `{ ...nextParams, stream: true }`: the replacement keeps its own key order, and
// stream keeps its position when the replacement has one and is added last when it has none. The SDK then removes betas from the body
// (anthropic-beta header). An extension's replacement reaches the provider as its JSON text (subprocess onPayload returns json.RawMessage).
func TestAnthropicReplacementPayloadKeepsItsKeyOrder(t *testing.T) {
	for _, tc := range []struct {
		name, payload, body string
		betas               *string
	}{
		{"stream added last", `{"model":"m","max_tokens":5,"messages":[],"system":"s"}`, `{"model":"m","max_tokens":5,"messages":[],"system":"s","stream":true}`, nil},
		{"stream keeps its position", `{"stream":false,"model":"m","max_tokens":5}`, `{"stream":true,"model":"m","max_tokens":5}`, nil},
		{"betas leave the body and become the header", `{"model":"m","betas":["a","b"],"stream":false,"max_tokens":5}`, `{"model":"m","stream":true,"max_tokens":5}`, new("a,b")},
		{"nested objects keep their order", `{"zeta":{"b":1,"a":{"y":1,"x":2}},"alpha":[{"d":1,"c":2}]}`, `{"zeta":{"b":1,"a":{"y":1,"x":2}},"alpha":[{"d":1,"c":2}],"stream":true}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, betas, err := anthropicWireBody(json.RawMessage(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != tc.body {
				t.Errorf("body = %s\nwant   %s", body, tc.body)
			}
			if (betas == nil) != (tc.betas == nil) || betas != nil && *betas != *tc.betas {
				t.Errorf("betas = %v, want %v", betas, tc.betas)
			}
		})
	}
}
